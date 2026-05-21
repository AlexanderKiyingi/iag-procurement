package events

// Signal / notification event names (used with signals.Bus and Redis broadcasts).
const (
	ProcurementAlert   = "procurement.alert"
	RequisitionPending = "requisition.pending"
)

// Kafka event types consumed from iag.commercial.
const PMRequisitionSubmitted = "pm.requisition.submitted"
