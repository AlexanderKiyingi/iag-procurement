package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"

	"iag-procurement/backend/internal/outbox"
)

const (
	// SpecVersion mirrors the IAG event envelope version used across services.
	SpecVersion = "1.0"
	// Source is the CloudEvents source identifier for procurement-emitted events.
	Source = "iag.procurement"

	// TopicCommercial is the cross-domain bus topic procurement publishes to.
	TopicCommercial = "iag.commercial"

	// Event types procurement emits on iag.commercial.
	TypeRequisitionApproved = "procurement.requisition.approved"
	TypeRequisitionRejected = "procurement.requisition.rejected"
	TypeInvoiceReceived     = "procurement.invoice.received"
	TypeGrnPosted           = "procurement.grn.posted"
)

// platformEvent is the canonical CloudEvents-compatible envelope used by IAG.
// It matches what consumers across the platform deserialize.
type platformEvent struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Time          string         `json:"time"`
	Source        string         `json:"source"`
	SpecVersion   string         `json:"specversion"`
	CorrelationID string         `json:"correlationId,omitempty"`
	Data          map[string]any `json:"data"`
}

// Publisher emits procurement domain events to iag.commercial. Disabled
// publishers are safe no-ops so callers don't need to guard every emit.
type Publisher struct {
	writer  *kafka.Writer
	enabled bool
	// outbox, when set, makes every emit durable: the event is persisted and
	// drained to Kafka by the background publisher instead of written inline.
	outbox *outbox.Store
}

// SetOutbox routes all emits through the durable outbox. The same store is
// drained by an outbox.Publisher whose Dispatcher is this Publisher
// (DispatchOutbox). Nil keeps the legacy direct-write path.
func (p *Publisher) SetOutbox(store *outbox.Store) {
	if p != nil {
		p.outbox = store
	}
}

// emit delivers an already-marshaled event envelope: via the durable outbox
// when configured, else a direct Kafka write. eventType sets the ce-type
// header; key is the partition key.
func (p *Publisher) emit(ctx context.Context, eventType, key string, body []byte) error {
	if p.outbox != nil {
		return p.outbox.Enqueue(ctx, eventType, key, body)
	}
	return p.writer.WriteMessages(ctx, kafka.Message{
		Topic: TopicCommercial,
		Key:   []byte(key),
		Value: body,
		Headers: []kafka.Header{
			{Key: "ce-type", Value: []byte(eventType)},
			{Key: "ce-source", Value: []byte(Source)},
		},
	})
}

// PublisherConfig configures a Publisher.
type PublisherConfig struct {
	Brokers []string
	Enabled bool
}

// NewPublisher constructs a Publisher; disabled config returns a no-op.
func NewPublisher(cfg PublisherConfig) *Publisher {
	if !cfg.Enabled || len(cfg.Brokers) == 0 {
		return &Publisher{enabled: false}
	}
	return &Publisher{
		enabled: true,
		writer: &kafka.Writer{
			Addr:         kafka.TCP(cfg.Brokers...),
			Topic:        TopicCommercial,
			Balancer:     &kafka.Hash{},
			RequiredAcks: kafka.RequireAll,
			Transport:    &kafka.Transport{ClientID: Source},
		},
	}
}

// Enabled reports whether publishing is wired.
func (p *Publisher) Enabled() bool { return p != nil && p.enabled }

// Close flushes and closes the underlying writer.
func (p *Publisher) Close() error {
	if p == nil || !p.enabled {
		return nil
	}
	return p.writer.Close()
}

// GrnPostedLine is one PO line included in procurement.grn.posted for warehouse intake.
type GrnPostedLine struct {
	SKU string  `json:"sku"`
	Qty float64 `json:"qty"`
	UOM string  `json:"uom"`
}

// PublishGrnPosted notifies iag-warehouse to create a draft goods receipt.
func (p *Publisher) PublishGrnPosted(ctx context.Context, grnID, poID, vendorID, receivedBy string, lines []GrnPostedLine) {
	if !p.Enabled() || grnID == "" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	data := map[string]any{
		"grn_id":      grnID,
		"po_id":       poID,
		"vendor_id":   vendorID,
		"received_by": receivedBy,
	}
	if len(lines) > 0 {
		raw := make([]map[string]any, 0, len(lines))
		for _, l := range lines {
			uom := l.UOM
			if uom == "" {
				uom = "ea"
			}
			raw = append(raw, map[string]any{"sku": l.SKU, "qty": l.Qty, "uom": uom})
		}
		data["lines"] = raw
	}
	evt := platformEvent{
		ID:          uuid.NewString(),
		Type:        TypeGrnPosted,
		Time:        now,
		Source:      Source,
		SpecVersion: SpecVersion,
		Data:        data,
	}
	body, err := json.Marshal(evt)
	if err != nil {
		slog.Warn("procurement grn event marshal", "err", err)
		return
	}
	if err := p.emit(ctx, TypeGrnPosted, grnID, body); err != nil {
		slog.Warn("procurement grn event publish", "err", err)
	}
}

// PublishInvoiceReceived notifies iag-finance to create an AP open item.
func (p *Publisher) PublishInvoiceReceived(ctx context.Context, invoiceNo, vendorRef, amount, currency string, dueDate *time.Time) {
	if !p.Enabled() || invoiceNo == "" || amount == "" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	data := map[string]any{
		"documentRef": invoiceNo,
		"vendorRef":   vendorRef,
		"amount":      amount,
		"currency":    currency,
		"description": "Procurement vendor invoice",
	}
	if dueDate != nil {
		data["dueDate"] = dueDate.Format("2006-01-02")
	}
	evt := platformEvent{
		ID:          uuid.NewString(),
		Type:        TypeInvoiceReceived,
		Time:        now,
		Source:      Source,
		SpecVersion: SpecVersion,
		Data:        data,
	}
	body, err := json.Marshal(evt)
	if err != nil {
		slog.Warn("procurement invoice event marshal", "err", err)
		return
	}
	if err := p.emit(ctx, TypeInvoiceReceived, invoiceNo, body); err != nil {
		slog.Warn("procurement invoice event publish", "err", err)
	}
}

// Requisition approval/rejection events are now built by BuildRequisitionOutcome
// and enqueued transactionally by the repo (see internal/repo/requisition_outcome.go);
// the legacy direct-publish methods were removed.
