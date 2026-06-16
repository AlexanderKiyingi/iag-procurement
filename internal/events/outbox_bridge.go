package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"

	"iag-procurement/backend/internal/outbox"
)

// BuildRequisitionOutcome constructs the CloudEvents envelope for a requisition
// approval/rejection and returns the Kafka partition key plus the marshaled
// payload. The repo uses it to enqueue the event transactionally with the
// status change, so the wire shape stays identical to the legacy direct emit.
func BuildRequisitionOutcome(eventType, requisitionID, pmRequisitionID, workspaceOwnerUserID, actor, budgetID string) (key string, payload []byte, err error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	data := map[string]any{
		"requisitionId": requisitionID, // procurement-side ID
		"budgetId":      budgetID,
	}
	// PM keys off its own local int, so surface it as requisitionId and keep
	// the procurement id under procurementRequisitionId for traceability.
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
		return "", nil, err
	}
	key = requisitionID
	if pmRequisitionID != "" {
		key = pmRequisitionID
	}
	return key, body, nil
}

// BuildInvoiceReceived constructs the CloudEvents envelope for a vendor invoice
// (consumed by iag-finance to open an AP item) and returns the partition key
// plus marshaled payload. The repo enqueues it in the invoice-insert tx, so the
// AP notification can never be lost to a post-commit crash. The wire shape is
// identical to the legacy direct emit.
func BuildInvoiceReceived(documentRef, vendorRef, amount, currency, poRef string, dueDate *time.Time) (key string, payload []byte, err error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	data := map[string]any{
		"documentRef": documentRef,
		"vendorRef":   vendorRef,
		"amount":      amount,
		"currency":    currency,
		"description": "Procurement vendor invoice",
	}
	if dueDate != nil {
		data["dueDate"] = dueDate.Format("2006-01-02")
	}
	// poRef lets iag-finance clear any goods-received (GR/IR) accrual booked for
	// this PO when the invoice arrives, instead of double-booking the expense.
	if poRef != "" {
		data["poRef"] = poRef
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
		return "", nil, err
	}
	return documentRef, body, nil
}

// BuildGrnPosted constructs the CloudEvents envelope for a posted goods receipt
// (consumed by iag-warehouse to draft an intake) and returns the partition key
// plus marshaled payload. The repo enqueues it in the GRN-write tx. The wire
// shape is identical to the legacy direct emit.
func BuildGrnPosted(grnID, poID, vendorID, receivedBy, receivedValue string, lines []GrnPostedLine) (key string, payload []byte, err error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	data := map[string]any{
		"grn_id":      grnID,
		"po_id":       poID,
		"vendor_id":   vendorID,
		"received_by": receivedBy,
	}
	// amount is the monetary value of the received lines (qty × unit price), so
	// iag-finance can book the GR/IR accrual (Dr expense / Cr GR-IR clearing) at
	// goods receipt rather than waiting for the invoice.
	if receivedValue != "" {
		data["amount"] = receivedValue
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
		return "", nil, err
	}
	return grnID, body, nil
}

// DispatchOutbox writes a persisted outbox row to Kafka. It implements
// outbox.Dispatcher for the background publisher. A disabled publisher returns
// nil (treated as delivered) so rows aren't retried forever in local/test runs.
func (p *Publisher) DispatchOutbox(ctx context.Context, row outbox.Row) error {
	if !p.Enabled() {
		return nil
	}
	return p.writer.WriteMessages(ctx, kafka.Message{
		Topic: TopicCommercial,
		Key:   []byte(row.EventKey),
		Value: row.Payload,
		Headers: []kafka.Header{
			{Key: "ce-type", Value: []byte(row.EventType)},
			{Key: "ce-source", Value: []byte(Source)},
		},
	})
}
