package notifications

import (
	"encoding/json"
	"fmt"

	"iag-procurement/backend/internal/events"
)

// ValidateEmitPayload checks JSON shapes before signal handlers run so clients get 400 instead of opaque 500s.
func ValidateEmitPayload(event string, payload json.RawMessage) error {
	switch event {
	case events.ProcurementAlert:
		var p AlertJobPayload
		if len(payload) == 0 || string(payload) == "{}" || string(payload) == "null" {
			return fmt.Errorf(`procurement.alert: payload must include "to" (non-empty array), "title", and "message"`)
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("procurement.alert: invalid JSON: %w", err)
		}
		if len(p.To) == 0 {
			return fmt.Errorf(`procurement.alert: "to" must be a non-empty array of email addresses`)
		}
		for _, addr := range p.To {
			if addr == "" {
				return fmt.Errorf(`procurement.alert: "to" entries must be non-empty strings`)
			}
		}
		if p.Title == "" || p.Message == "" {
			return fmt.Errorf(`procurement.alert: "title" and "message" are required`)
		}
		return nil

	case events.RequisitionPending:
		var p struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		if len(payload) == 0 || string(payload) == "{}" {
			return fmt.Errorf(`requisition.pending: payload must include "id" and "title"`)
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("requisition.pending: invalid JSON: %w", err)
		}
		if p.ID == "" || p.Title == "" {
			return fmt.Errorf(`requisition.pending: "id" and "title" are required`)
		}
		return nil

	default:
		return fmt.Errorf("unknown event")
	}
}
