package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/segmentio/kafka-go"

	"iag-procurement/backend/internal/repo"
)

// Event types procurement consumes on iag.fleet. Fleet writes ONLY to its own
// domain topic post-cutover, so — like finance and notifications — procurement
// subscribes to iag.fleet and translates the events it cares about. Today that
// is just the approved fuel request, which becomes a sourcing requisition.
const fuelRequestApproved = "fleet.fuel.request_approved"

type FleetConfig struct {
	Brokers []string
	GroupID string
	Topic   string
}

type Fleet struct {
	cfg  FleetConfig
	repo *repo.Procurement
}

func NewFleet(cfg FleetConfig, p *repo.Procurement) *Fleet {
	return &Fleet{cfg: cfg, repo: p}
}

func (c *Fleet) Run(ctx context.Context) error {
	if len(c.cfg.Brokers) == 0 {
		return nil
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  c.cfg.Brokers,
		GroupID:  c.cfg.GroupID,
		Topic:    c.cfg.Topic,
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	defer r.Close()

	log.Printf("procurement fleet consumer started topic=%s group=%s", c.cfg.Topic, c.cfg.GroupID)
	for {
		msg, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("procurement fleet fetch: %v", err)
			continue
		}
		if err := c.handle(ctx, msg.Value); err != nil {
			log.Printf("procurement fleet handle: %v", err)
			continue
		}
		if err := r.CommitMessages(ctx, msg); err != nil {
			log.Printf("procurement fleet commit: %v", err)
		}
	}
}

type fleetEnvelope struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Source string          `json:"source"`
	Data   json.RawMessage `json:"data"`
}

// fuelRequestApprovedData mirrors the enriched payload fleet publishes when a
// fuel request is approved (handlers/fuel_requests.go emitApproved). Numbers
// arrive as strings, matching the fleet.fuel.recorded shape.
type fuelRequestApprovedData struct {
	RequestID  string `json:"requestId"`
	VehicleID  string `json:"vehicleId"`
	Status     string `json:"status"`
	ApprovedBy string `json:"approvedBy"`
	Litres     string `json:"litres"`
	EstTotal   string `json:"estTotal"`
	Currency   string `json:"currency"`
	Station    string `json:"station"`
	Requester  string `json:"requester"`
	Dept       string `json:"dept"`
	Purpose    string `json:"purpose"`
}

func (c *Fleet) handle(ctx context.Context, raw []byte) error {
	var env fleetEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	switch env.Type {
	case fuelRequestApproved:
		return c.handleFuelRequestApproved(ctx, env.ID, env.Data)
	default:
		return nil
	}
}

// handleFuelRequestApproved imports an approved fleet fuel request as a
// "Pending Approval" requisition, idempotent on (origin_system=fleet,
// origin_ref=requestId). Rejected requests carry status="rejected" on the same
// event type and are ignored — only an approved request should be sourced.
func (c *Fleet) handleFuelRequestApproved(ctx context.Context, eventID string, raw json.RawMessage) error {
	var d fuelRequestApprovedData
	if err := json.Unmarshal(raw, &d); err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(d.Status), "approved") {
		return nil // reject / other transitions are not sourcing events
	}
	originRef := strings.TrimSpace(d.RequestID)
	if originRef == "" {
		log.Printf("procurement fleet: dropping fuel.request_approved with no requestId (event=%s)", eventID)
		return nil
	}

	total, _ := strconv.ParseFloat(strings.TrimSpace(d.EstTotal), 64)
	dept := strings.TrimSpace(d.Dept)
	budgetID, err := c.repo.ResolveBudgetForDept(ctx, dept)
	if err != nil {
		return err
	}

	title := fuelRequisitionTitle(d)
	justification := strings.TrimSpace(d.Purpose)
	if justification == "" {
		justification = fmt.Sprintf("Fleet fuel request %s approved by %s", originRef, strings.TrimSpace(d.ApprovedBy))
	}

	row, err := c.repo.ImportProcurementRequest(ctx,
		"fleet", originRef, title, dept, strings.TrimSpace(d.Requester),
		"Medium", total, strings.TrimSpace(d.Currency), budgetID, justification, eventID)
	if errors.Is(err, repo.ErrProcurementRequestExists) {
		return nil // already imported — idempotent no-op
	}
	if err != nil {
		return err
	}
	log.Printf("procurement: imported fleet fuel request ref=%s -> %s", originRef, row.ID)
	return nil
}

// fuelRequisitionTitle builds a human requisition title from the fuel-request
// payload, e.g. "Fuel — 120.00L (VH-001)".
func fuelRequisitionTitle(d fuelRequestApprovedData) string {
	litres := strings.TrimSpace(d.Litres)
	veh := strings.TrimSpace(d.VehicleID)
	switch {
	case litres != "" && veh != "":
		return fmt.Sprintf("Fuel — %sL (%s)", litres, veh)
	case veh != "":
		return fmt.Sprintf("Fuel — %s", veh)
	default:
		return "Fuel request " + strings.TrimSpace(d.RequestID)
	}
}
