package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"

	"iag-procurement/backend/internal/events"
	"iag-procurement/backend/internal/repo"
	"iag-procurement/backend/internal/signals"
)

const pmRequisitionSubmitted = "pm.requisition.submitted"

type Config struct {
	Brokers []string
	GroupID string
	Topic   string
}

type PlatformEvent struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Time          string          `json:"time"`
	Source        string          `json:"source"`
	SpecVersion   string          `json:"specversion"`
	CorrelationID string          `json:"correlationId"`
	Data          json.RawMessage `json:"data"`
}

type pmRequisitionData struct {
	RequisitionID        string `json:"requisitionId"`
	WorkspaceOwnerUserID string `json:"workspaceOwnerUserId"`
	Title                string `json:"title"`
	Amount               string `json:"amount"`
	Currency             string `json:"currency"`
	Status               string `json:"status"`
	RequestedBy          string `json:"requestedBy"`
	ForDept              string `json:"forDept"`
	Urgency              string `json:"urgency"`
	Payee                string `json:"payee"`
	Justification        string `json:"justification"`
}

type Commercial struct {
	reader      *kafka.Reader
	procurement *repo.Procurement
	bus         *signals.Bus
}

func NewCommercial(cfg Config, procurement *repo.Procurement, bus *signals.Bus) *Commercial {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  cfg.Brokers,
		GroupID:  cfg.GroupID,
		Topic:    cfg.Topic,
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &Commercial{reader: reader, procurement: procurement, bus: bus}
}

func (c *Commercial) Run(ctx context.Context) error {
	log.Printf("procurement commercial consumer started topic=%s group=%s", c.reader.Config().Topic, c.reader.Config().GroupID)
	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("procurement consumer fetch: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if err := c.handleMessage(ctx, msg.Value); err != nil {
			log.Printf("procurement consumer handle: %v", err)
		} else if err := c.reader.CommitMessages(ctx, msg); err != nil {
			log.Printf("procurement consumer commit: %v", err)
		}
	}
}

func (c *Commercial) Close() error {
	if c.reader == nil {
		return nil
	}
	return c.reader.Close()
}

func (c *Commercial) handleMessage(ctx context.Context, payload []byte) error {
	var evt PlatformEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return err
	}
	if evt.Type != pmRequisitionSubmitted {
		return nil
	}
	return c.handlePMRequisition(ctx, evt)
}

func (c *Commercial) handlePMRequisition(ctx context.Context, evt PlatformEvent) error {
	var data pmRequisitionData
	if err := json.Unmarshal(evt.Data, &data); err != nil {
		return err
	}
	pmID := strings.TrimSpace(data.RequisitionID)
	title := strings.TrimSpace(data.Title)
	if pmID == "" || title == "" {
		return nil
	}

	total, _ := strconv.ParseFloat(strings.TrimSpace(data.Amount), 64)
	if total <= 0 {
		return nil
	}

	dept := strings.TrimSpace(data.ForDept)
	budgetID, err := c.procurement.ResolveBudgetForDept(ctx, dept)
	if err != nil {
		return err
	}

	row, err := c.procurement.ImportPMRequisition(
		ctx,
		pmID,
		strings.TrimSpace(data.WorkspaceOwnerUserID),
		title,
		dept,
		strings.TrimSpace(data.RequestedBy),
		mapPMUrgency(data.Urgency),
		mapPMStatus(data.Status),
		total,
		strings.TrimSpace(data.Currency),
		budgetID,
		evt.ID,
	)
	if errors.Is(err, repo.ErrPMRequisitionExists) {
		return nil
	}
	if err != nil {
		return err
	}

	if c.bus != nil && row != nil {
		body, _ := json.Marshal(map[string]string{"id": row.ID, "title": row.Title, "pmId": pmID})
		_ = c.bus.Emit(ctx, signals.Event{Name: events.RequisitionPending, Payload: body})
	}
	log.Printf("procurement: imported PM requisition pmId=%s -> %s", pmID, row.ID)
	return nil
}

func mapPMUrgency(u string) string {
	switch strings.ToLower(strings.TrimSpace(u)) {
	case "high":
		return "High"
	case "low":
		return "Low"
	default:
		return "Medium"
	}
}

func mapPMStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "approved":
		return "Approved"
	case "draft":
		return "Draft"
	default:
		return "Pending Approval"
	}
}

func ParseBrokers(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
