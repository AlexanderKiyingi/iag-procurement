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

// govRequisitionApproved is emitted by iag-contract-management when a governance
// requisition completes its approval chain — procurement imports it for sourcing.
const govRequisitionApproved = "contracts.requisition.approved"

// maxHandleAttempts bounds in-process retries before a message is parked in the
// DLQ so a poison message can't block the partition forever.
const maxHandleAttempts = 5

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
	dlq         *kafka.Writer
	dlqTopic    string
}

func NewCommercial(cfg Config, procurement *repo.Procurement, bus *signals.Bus) *Commercial {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  cfg.Brokers,
		GroupID:  cfg.GroupID,
		Topic:    cfg.Topic,
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	c := &Commercial{reader: reader, procurement: procurement, bus: bus}
	// Dead-letter topic for messages that fail every retry, so a poison message
	// is parked for inspection instead of silently dropped or blocking the
	// partition.
	if len(cfg.Brokers) > 0 {
		c.dlqTopic = cfg.Topic + ".dlq"
		c.dlq = &kafka.Writer{
			Addr:         kafka.TCP(cfg.Brokers...),
			Topic:        c.dlqTopic,
			Balancer:     &kafka.LeastBytes{},
			RequiredAcks: kafka.RequireAll,
		}
	}
	return c
}

func (c *Commercial) Run(ctx context.Context) error {
	log.Printf("procurement commercial consumer started topic=%s group=%s dlq=%s", c.reader.Config().Topic, c.reader.Config().GroupID, c.dlqTopic)
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

		// Bounded in-process retry. On exhaustion, route to the DLQ and commit
		// so the partition advances; if the DLQ write itself fails, do NOT
		// commit — prefer reprocessing on restart over losing the message.
		var handleErr error
		for attempt := 1; attempt <= maxHandleAttempts; attempt++ {
			if handleErr = c.handleMessage(ctx, msg.Value); handleErr == nil {
				break
			}
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("procurement consumer handle attempt %d/%d: %v", attempt, maxHandleAttempts, handleErr)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		if handleErr != nil {
			if !c.deadLetter(ctx, msg, handleErr) {
				// DLQ unavailable — leave uncommitted so we retry after restart.
				continue
			}
		}
		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			log.Printf("procurement consumer commit: %v", err)
		}
	}
}

// deadLetter publishes a failed message to the DLQ topic with the failure
// reason in a header. Returns false if the DLQ write failed (or no DLQ wired).
func (c *Commercial) deadLetter(ctx context.Context, msg kafka.Message, cause error) bool {
	if c.dlq == nil {
		log.Printf("procurement consumer: no DLQ configured, dropping message after %d attempts: %v", maxHandleAttempts, cause)
		return true // commit anyway — matches prior best-effort behavior
	}
	err := c.dlq.WriteMessages(ctx, kafka.Message{
		Key:   msg.Key,
		Value: msg.Value,
		Headers: append(msg.Headers,
			kafka.Header{Key: "x-dlq-reason", Value: []byte(cause.Error())},
			kafka.Header{Key: "x-dlq-source-topic", Value: []byte(c.reader.Config().Topic)},
		),
	})
	if err != nil {
		log.Printf("procurement consumer DLQ publish failed: %v", err)
		return false
	}
	log.Printf("procurement consumer: message routed to DLQ %s after %d attempts: %v", c.dlqTopic, maxHandleAttempts, cause)
	return true
}

func (c *Commercial) Close() error {
	var firstErr error
	if c.dlq != nil {
		if err := c.dlq.Close(); err != nil {
			firstErr = err
		}
	}
	if c.reader != nil {
		if err := c.reader.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (c *Commercial) handleMessage(ctx context.Context, payload []byte) error {
	var evt PlatformEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return err
	}
	switch evt.Type {
	case pmRequisitionSubmitted:
		return c.handlePMRequisition(ctx, evt)
	case govRequisitionApproved:
		return c.handleGovRequisitionApproved(ctx, evt)
	default:
		return nil
	}
}

type govRequisitionData struct {
	RequisitionID     string  `json:"requisitionId"`
	No                string  `json:"no"`
	Title             string  `json:"title"`
	Estimate          float64 `json:"estimate"`
	Supplier          string  `json:"supplier"`
	ProcurementMethod string  `json:"procurementMethod"`
	BudgetCode        string  `json:"budgetCode"`
	Department        string  `json:"department"`
}

// handleGovRequisitionApproved imports an approved contract-management governance
// requisition into procurement (idempotent on its requisition number).
func (c *Commercial) handleGovRequisitionApproved(ctx context.Context, evt PlatformEvent) error {
	if evt.ID != "" {
		if done, err := c.procurement.IsEventProcessed(ctx, evt.ID); err != nil {
			return err
		} else if done {
			return nil
		}
	}
	var data govRequisitionData
	if err := json.Unmarshal(evt.Data, &data); err != nil {
		return err
	}
	key := strings.TrimSpace(data.No)
	if key == "" {
		key = strings.TrimSpace(data.RequisitionID)
	}
	title := strings.TrimSpace(data.Title)
	if key == "" || title == "" || data.Estimate <= 0 {
		return c.procurement.MarkEventProcessed(ctx, evt.ID) // malformed but not retryable
	}
	dept := strings.TrimSpace(data.Department)
	budgetID, err := c.procurement.ResolveBudgetForDept(ctx, dept)
	if err != nil {
		return err
	}
	row, err := c.procurement.ImportPMRequisition(
		ctx, key, "", title, dept, "", "Medium", "Approved",
		data.Estimate, "UGX", budgetID, strings.TrimSpace(data.Supplier), "", evt.ID,
	)
	if errors.Is(err, repo.ErrPMRequisitionExists) {
		return c.procurement.MarkEventProcessed(ctx, evt.ID)
	}
	if err != nil {
		return err
	}
	log.Printf("procurement: imported governance requisition %s -> %s", key, row.ID)
	return c.procurement.MarkEventProcessed(ctx, evt.ID)
}

func (c *Commercial) handlePMRequisition(ctx context.Context, evt PlatformEvent) error {
	// Idempotency: skip events already handled (redelivery after rebalance or
	// no-DLQ retry). Belt-and-suspenders alongside the unique pm_requisition_id.
	if evt.ID != "" {
		if done, err := c.procurement.IsEventProcessed(ctx, evt.ID); err != nil {
			return err
		} else if done {
			return nil
		}
	}

	var data pmRequisitionData
	if err := json.Unmarshal(evt.Data, &data); err != nil {
		return err
	}
	pmID := strings.TrimSpace(data.RequisitionID)
	title := strings.TrimSpace(data.Title)
	if pmID == "" || title == "" {
		return c.procurement.MarkEventProcessed(ctx, evt.ID) // malformed but not retryable
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
		strings.TrimSpace(data.Payee),
		strings.TrimSpace(data.Justification),
		evt.ID,
	)
	if errors.Is(err, repo.ErrPMRequisitionExists) {
		return c.procurement.MarkEventProcessed(ctx, evt.ID)
	}
	if err != nil {
		return err
	}

	if c.bus != nil && row != nil {
		body, _ := json.Marshal(map[string]string{"id": row.ID, "title": row.Title, "pmId": pmID})
		_ = c.bus.Emit(ctx, signals.Event{Name: events.RequisitionPending, Payload: body})
	}
	log.Printf("procurement: imported PM requisition pmId=%s -> %s", pmID, row.ID)
	return c.procurement.MarkEventProcessed(ctx, evt.ID)
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
