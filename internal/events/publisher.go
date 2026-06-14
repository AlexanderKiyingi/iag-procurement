package events

import (
	"github.com/segmentio/kafka-go"
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

// Publisher is the Kafka-facing side of the procurement event pipeline: it
// drains the transactional outbox to iag.commercial via DispatchOutbox (see
// outbox_bridge.go). Domain events are built by the Build* helpers and enqueued
// in the writing transaction by the repo, so there is no direct, non-atomic
// emit path. A disabled publisher is a safe no-op.
type Publisher struct {
	writer  *kafka.Writer
	enabled bool
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
// All procurement domain events (requisition approval/rejection, invoice
// received, GRN posted) are built by the Build* helpers in outbox_bridge.go and
// enqueued transactionally by the repo, then drained here by DispatchOutbox.
// The legacy direct-publish methods were removed so no event can be lost to a
// post-commit crash or a broker outage.
