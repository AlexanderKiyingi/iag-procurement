package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
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

// Publisher emits procurement domain events to iag.commercial. Disabled
// publishers are safe no-ops so callers don't need to guard every emit.
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

// PublishRequisitionApproved emits procurement.requisition.approved on
// iag.commercial. workspaceOwnerUserID, when present, lets PM route the event
// back to the originating workspace.
func (p *Publisher) PublishRequisitionApproved(ctx context.Context, requisitionID, pmRequisitionID, workspaceOwnerUserID, approvedBy, budgetID string) {
	p.publishRequisitionOutcome(ctx, TypeRequisitionApproved, requisitionID, pmRequisitionID, workspaceOwnerUserID, approvedBy, budgetID)
}

// PublishRequisitionRejected emits procurement.requisition.rejected.
func (p *Publisher) PublishRequisitionRejected(ctx context.Context, requisitionID, pmRequisitionID, workspaceOwnerUserID, rejectedBy, budgetID string) {
	p.publishRequisitionOutcome(ctx, TypeRequisitionRejected, requisitionID, pmRequisitionID, workspaceOwnerUserID, rejectedBy, budgetID)
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
	if err := p.writer.WriteMessages(ctx, kafka.Message{
		Topic: TopicCommercial,
		Key:   []byte(grnID),
		Value: body,
		Headers: []kafka.Header{
			{Key: "ce-type", Value: []byte(TypeGrnPosted)},
			{Key: "ce-source", Value: []byte(Source)},
		},
	}); err != nil {
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
	if err := p.writer.WriteMessages(ctx, kafka.Message{
		Topic: TopicCommercial,
		Key:   []byte(invoiceNo),
		Value: body,
		Headers: []kafka.Header{
			{Key: "ce-type", Value: []byte(TypeInvoiceReceived)},
			{Key: "ce-source", Value: []byte(Source)},
		},
	}); err != nil {
		slog.Warn("procurement invoice event publish", "err", err)
	}
}

func (p *Publisher) publishRequisitionOutcome(ctx context.Context, eventType, requisitionID, pmRequisitionID, workspaceOwnerUserID, actor, budgetID string) {
	if !p.Enabled() {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	data := map[string]any{
		"requisitionId": requisitionID, // procurement-side ID
		"budgetId":      budgetID,
	}
	// pmRequisitionId is what PM cares about — it's PM's local int. Send it as
	// requisitionId for the PM consumer's convenience, and keep the procurement
	// id under procurementRequisitionId for traceability.
	if pmRequisitionID != "" {
		data["requisitionId"] = pmRequisitionID
		data["procurementRequisitionId"] = requisitionID
	}
	if workspaceOwnerUserID != "" {
		data["workspaceOwnerUserId"] = workspaceOwnerUserID
	}
	switch eventType {
	case TypeRequisitionApproved:
		data["approvedBy"] = actor
		data["approvedAt"] = now
	case TypeRequisitionRejected:
		data["rejectedBy"] = actor
		data["rejectedAt"] = now
	}
	evt := platformEvent{
		ID:          uuid.NewString(),
		Type:        eventType,
		Time:        now,
		Source:      Source,
		SpecVersion: SpecVersion,
		Data:        data,
	}
	body, err := json.Marshal(evt)
	if err != nil {
		slog.Warn("procurement event marshal", "type", eventType, "err", err)
		return
	}
	key := requisitionID
	if pmRequisitionID != "" {
		key = pmRequisitionID
	}
	if err := p.writer.WriteMessages(ctx, kafka.Message{
		Topic: TopicCommercial,
		Key:   []byte(key),
		Value: body,
		Headers: []kafka.Header{
			{Key: "ce-type", Value: []byte(eventType)},
			{Key: "ce-source", Value: []byte(Source)},
		},
	}); err != nil {
		slog.Warn("procurement event publish", "type", eventType, "err", fmt.Errorf("write: %w", err))
	}
}
