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
