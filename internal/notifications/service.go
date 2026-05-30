package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"iag-procurement/backend/internal/events"
	"iag-procurement/backend/internal/notifyclient"
	"iag-procurement/backend/internal/signals"
)

// Service writes in-app notification rows and forwards email dispatch
// to the central iag-notifications service via notifyclient. The
// previous Redis-queue + SMTP path was removed in favour of central
// dispatch, which owns retries, idempotency, provider plumbing, and
// audit. Procurement is still authoritative for in-app rows because
// they are served back to its own frontend.
type Service struct {
	store  *Store
	notify notifyclient.Dispatcher
}

func NewService(store *Store, notify notifyclient.Dispatcher) *Service {
	if notify == nil {
		notify = notifyclient.Noop{}
	}
	return &Service{store: store, notify: notify}
}

// List returns recent in-app notifications.
func (s *Service) List(ctx context.Context, limit int) ([]Row, error) {
	return s.store.List(ctx, limit)
}

// MarkRead marks a single notification as read.
func (s *Service) MarkRead(ctx context.Context, id int64) error {
	return s.store.MarkRead(ctx, id)
}

// Register wires default signal handlers onto the bus.
func (s *Service) Register(bus *signals.Bus) {
	bus.On(events.ProcurementAlert, s.onProcurementAlert)
	bus.On(events.RequisitionPending, s.onRequisitionPending)
}

func (s *Service) onProcurementAlert(ctx context.Context, e signals.Event) error {
	var p AlertJobPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("procurement.alert payload: %w", err)
	}
	return s.EnqueueAlertEmail(ctx, p)
}

func (s *Service) onRequisitionPending(ctx context.Context, e signals.Event) error {
	var p struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("requisition.pending payload: %w", err)
	}
	title := "Requisition pending approval"
	body := fmt.Sprintf("%s (%s) needs approval.", p.Title, p.ID)
	_, err := s.store.InsertInApp(ctx, events.RequisitionPending, title, body, "warning")
	return err
}

// EnqueueAlertEmail records an in-app notification and dispatches an
// email per recipient via the central notifications service. A failed
// central dispatch is logged but does not fail the operation — the
// in-app row is the source of truth for the operator-facing alert.
// Notifications dedups by (eventId, channel, recipient), so the
// per-recipient EventID derived from the in-app row id makes retries
// safe.
func (s *Service) EnqueueAlertEmail(ctx context.Context, p AlertJobPayload) error {
	if len(p.To) == 0 {
		return fmt.Errorf("notifications: missing recipients")
	}
	body := p.Message
	if p.Detail != "" {
		body = p.Message + "\n\n" + p.Detail
	}
	inAppID, err := s.store.InsertInApp(ctx, events.ProcurementAlert, p.Title, body, "info")
	if err != nil {
		return err
	}
	eventID := "procurement.alert." + strconv.FormatInt(inAppID, 10)
	variables := map[string]string{
		"Title": p.Title,
		"Body":  body,
	}
	for _, to := range p.To {
		_, err := s.notify.Dispatch(ctx, notifyclient.DispatchRequest{
			Channel:    "email",
			Recipient:  to,
			TemplateID: "procurement.alert",
			Variables:  variables,
			EventID:    eventID,
		})
		if err != nil {
			slog.WarnContext(ctx, "procurement.alert dispatch failed",
				"recipient", to,
				"eventId", eventID,
				"err", err,
			)
		}
	}
	return nil
}
