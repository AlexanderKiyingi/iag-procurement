package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"iag-procurement/backend/internal/cache"
	"iag-procurement/backend/internal/email"
	"iag-procurement/backend/internal/events"
	"iag-procurement/backend/internal/signals"
)

// Service coordinates in-app rows, email jobs, the Redis queue, and signal handlers.
type Service struct {
	store    *Store
	cache    *cache.Client
	queueKey string
	mailer   email.Mailer
}

func NewService(store *Store, c *cache.Client, queueKey string, mailer email.Mailer) *Service {
	return &Service{store: store, cache: c, queueKey: queueKey, mailer: mailer}
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

// EnqueueAlertEmail persists an in-app notification, records an email job, and pushes the job id to Redis.
func (s *Service) EnqueueAlertEmail(ctx context.Context, p AlertJobPayload) error {
	if len(p.To) == 0 {
		return fmt.Errorf("notifications: missing recipients")
	}
	body := p.Message
	if p.Detail != "" {
		body = p.Message + "\n\n" + p.Detail
	}
	if _, err := s.store.InsertInApp(ctx, events.ProcurementAlert, p.Title, body, "info"); err != nil {
		return err
	}
	payload := map[string]any{
		"to":      p.To,
		"title":   p.Title,
		"message": p.Message,
		"detail":  p.Detail,
	}
	id, err := s.store.InsertEmailJob(ctx, "alert.html", p.Title, payload)
	if err != nil {
		return err
	}
	return s.cache.QueueLPush(ctx, s.queueKey, strconv.FormatInt(id, 10))
}

// StartEmailConsumers runs n blocking workers that dequeue job ids and send mail.
func StartEmailConsumers(ctx context.Context, n int, c *cache.Client, queueKey string, store *Store, mailer email.Mailer) {
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		go func(workerID int) {
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				raw, err := c.QueueBRPop(ctx, 30*time.Second, queueKey)
				if err != nil {
					if errors.Is(err, redis.Nil) {
						continue
					}
					time.Sleep(time.Second)
					continue
				}
				id, err := strconv.ParseInt(raw, 10, 64)
				if err != nil {
					continue
				}
				processEmailJob(ctx, store, mailer, id)
			}
		}(i)
	}
}

func processEmailJob(ctx context.Context, store *Store, mailer email.Mailer, id int64) {
	tpl, subject, payloadBytes, ok, err := store.ClaimEmailJob(ctx, id)
	if err != nil || !ok {
		return
	}
	var p AlertJobPayload
	if err := json.Unmarshal(payloadBytes, &p); err != nil {
		_ = store.MarkEmailJobFailed(ctx, id, err.Error())
		return
	}
	data := email.AlertData{Title: p.Title, Message: p.Message, Detail: p.Detail}
	html, err := email.RenderHTML(tpl, data)
	if err != nil {
		_ = store.MarkEmailJobFailed(ctx, id, err.Error())
		return
	}
	if err := mailer.SendHTML(ctx, p.To, subject, html); err != nil {
		_ = store.MarkEmailJobFailed(ctx, id, err.Error())
		return
	}
	_ = store.MarkEmailJobSent(ctx, id)
}
