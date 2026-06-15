package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"

	"github.com/segmentio/kafka-go"

	"iag-procurement/backend/internal/repo"
)

// Event types procurement consumes on iag.operations.
const (
	warehouseStockBelowMinimum = "warehouse.stock.below_minimum"
	procurementRequested       = "procurement.requested"
)

type OperationsConfig struct {
	Brokers []string
	GroupID string
	Topic   string
}

type Operations struct {
	cfg  OperationsConfig
	repo *repo.Procurement
}

func NewOperations(cfg OperationsConfig, p *repo.Procurement) *Operations {
	return &Operations{cfg: cfg, repo: p}
}

func (c *Operations) Run(ctx context.Context) error {
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

	log.Printf("procurement operations consumer started topic=%s group=%s", c.cfg.Topic, c.cfg.GroupID)
	for {
		msg, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("procurement operations fetch: %v", err)
			continue
		}
		if err := c.handle(ctx, msg.Value); err != nil {
			log.Printf("procurement operations handle: %v", err)
			continue
		}
		if err := r.CommitMessages(ctx, msg); err != nil {
			log.Printf("procurement operations commit: %v", err)
		}
	}
}

type opsEnvelope struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Source string          `json:"source"`
	Data   json.RawMessage `json:"data"`
}

// procurementRequestData is the payload of a `procurement.requested` event any
// service emits to ask procurement to source something (e.g. stores replacing
// stock, fleet requesting parts). RequestID makes the import idempotent.
type procurementRequestData struct {
	SourceService string `json:"sourceService"`
	RequestID     string `json:"requestId"`
	Title         string `json:"title"`
	Department    string `json:"department"`
	RequestedBy   string `json:"requestedBy"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
	Urgency       string `json:"urgency"`
	Justification string `json:"justification"`
}

func (c *Operations) handle(ctx context.Context, raw []byte) error {
	var env opsEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	switch env.Type {
	case warehouseStockBelowMinimum:
		var data map[string]any
		if len(env.Data) > 0 {
			if err := json.Unmarshal(env.Data, &data); err != nil {
				return err
			}
		}
		return c.repo.RecordLowStockSignal(ctx, data)
	case procurementRequested:
		return c.handleProcurementRequest(ctx, env.ID, env.Source, env.Data)
	default:
		return nil
	}
}

// handleProcurementRequest turns an inbound procurement request into a
// "Pending Approval" requisition, idempotent on (origin system, request id).
func (c *Operations) handleProcurementRequest(ctx context.Context, eventID, source string, raw json.RawMessage) error {
	var d procurementRequestData
	if err := json.Unmarshal(raw, &d); err != nil {
		return err
	}
	title := strings.TrimSpace(d.Title)
	originSystem := strings.TrimSpace(d.SourceService)
	if originSystem == "" {
		originSystem = strings.TrimSpace(source)
	}
	originRef := strings.TrimSpace(d.RequestID)
	if originRef == "" {
		originRef = strings.TrimSpace(eventID) // fall back to the event id for idempotency
	}
	if title == "" || originSystem == "" || originRef == "" {
		log.Printf("procurement operations: dropping malformed procurement.requested (origin=%q ref=%q title=%q)", originSystem, originRef, title)
		return nil
	}

	total, _ := strconv.ParseFloat(strings.TrimSpace(d.Amount), 64)
	dept := strings.TrimSpace(d.Department)
	budgetID, err := c.repo.ResolveBudgetForDept(ctx, dept)
	if err != nil {
		return err
	}

	row, err := c.repo.ImportProcurementRequest(ctx,
		originSystem, originRef, title, dept, strings.TrimSpace(d.RequestedBy),
		mapPMUrgency(d.Urgency), total, strings.TrimSpace(d.Currency), budgetID,
		strings.TrimSpace(d.Justification), eventID)
	if errors.Is(err, repo.ErrProcurementRequestExists) {
		return nil // already imported — idempotent no-op
	}
	if err != nil {
		return err
	}
	log.Printf("procurement: imported request from %s ref=%s -> %s", originSystem, originRef, row.ID)
	return nil
}
