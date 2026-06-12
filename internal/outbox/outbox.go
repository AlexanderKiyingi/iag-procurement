// Package outbox implements the transactional outbox for procurement's
// outbound iag.commercial events.
//
// Requisition approval/rejection events used to be published with a direct
// Kafka write that only logged on failure: if the broker was down when a buyer
// approved, the event was lost and project-management never heard, so the AP
// was never booked. With the outbox the approval handler enqueues the event in
// the SAME transaction as the status change, and a background Publisher drains
// the table to Kafka with retry/backoff. Worst case is duplicate delivery,
// which idempotent consumers already tolerate.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Row is a pending or completed outbox entry.
type Row struct {
	ID        int64
	EventType string
	EventKey  string
	Payload   json.RawMessage
	Attempts  int
}

// Store wraps the procurement_event_outbox table.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// execer is satisfied by both *pgxpool.Pool and pgx.Tx.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// EnqueueTx writes a pending row inside the caller's transaction so the event
// and the domain change commit or roll back together.
func (s *Store) EnqueueTx(ctx context.Context, tx pgx.Tx, eventType, key string, payload json.RawMessage) error {
	return enqueue(ctx, tx, eventType, key, payload)
}

// Enqueue writes a pending row using the pool (for emits not already in a tx).
func (s *Store) Enqueue(ctx context.Context, eventType, key string, payload json.RawMessage) error {
	return enqueue(ctx, s.pool, eventType, key, payload)
}

func enqueue(ctx context.Context, ex execer, eventType, key string, payload json.RawMessage) error {
	_, err := ex.Exec(ctx, `
		INSERT INTO procurement_event_outbox (event_type, event_key, payload)
		VALUES ($1, $2, $3::jsonb)
	`, eventType, nullable(key), []byte(payload))
	return err
}

// ClaimBatch reserves up to limit due rows, bumping attempts and pushing
// available_at out so concurrent publishers don't double-deliver.
func (s *Store) ClaimBatch(ctx context.Context, limit int, backoff time.Duration) ([]Row, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := s.pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM procurement_event_outbox
			WHERE dispatched_at IS NULL AND available_at <= NOW()
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE procurement_event_outbox o
		SET attempts = o.attempts + 1, available_at = NOW() + $2::interval
		FROM due
		WHERE o.id = due.id
		RETURNING o.id, o.event_type, COALESCE(o.event_key, ''), o.payload, o.attempts
	`, limit, fmt.Sprintf("%d milliseconds", backoff.Milliseconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ID, &r.EventType, &r.EventKey, &r.Payload, &r.Attempts); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkDispatched records a successful delivery.
func (s *Store) MarkDispatched(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE procurement_event_outbox SET dispatched_at = NOW(), last_error = NULL WHERE id = $1
	`, id)
	return err
}

// MarkFailed records the failure and schedules the next retry.
func (s *Store) MarkFailed(ctx context.Context, id int64, errMsg string, retryDelay time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE procurement_event_outbox
		SET last_error = $1, available_at = NOW() + $2::interval
		WHERE id = $3
	`, errMsg, fmt.Sprintf("%d milliseconds", retryDelay.Milliseconds()), id)
	return err
}

// Dispatcher is the Kafka-facing side, implemented by events.Publisher.
type Dispatcher interface {
	DispatchOutbox(ctx context.Context, row Row) error
}

// Publisher periodically drains the outbox.
type Publisher struct {
	store      *Store
	dispatcher Dispatcher
	tick       time.Duration
	batch      int
	maxBackoff time.Duration
}

func NewPublisher(store *Store, d Dispatcher) *Publisher {
	return &Publisher{store: store, dispatcher: d, tick: 2 * time.Second, batch: 32, maxBackoff: 5 * time.Minute}
}

// Run drains the outbox until ctx is canceled.
func (p *Publisher) Run(ctx context.Context) {
	if p == nil || p.store == nil || p.dispatcher == nil {
		return
	}
	ticker := time.NewTicker(p.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := p.drainOnce(ctx)
			if err != nil {
				slog.Warn("procurement outbox drain", "err", err)
				continue
			}
			if n >= p.batch {
				if _, err := p.drainOnce(ctx); err != nil {
					slog.Warn("procurement outbox follow-up drain", "err", err)
				}
			}
		}
	}
}

func (p *Publisher) drainOnce(ctx context.Context) (int, error) {
	rows, err := p.store.ClaimBatch(ctx, p.batch, time.Second)
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		if err := p.dispatcher.DispatchOutbox(ctx, r); err != nil {
			delay := backoffFor(r.Attempts, p.maxBackoff)
			if mErr := p.store.MarkFailed(ctx, r.ID, err.Error(), delay); mErr != nil {
				slog.Warn("procurement outbox mark-failed", "id", r.ID, "err", mErr)
			}
			slog.Warn("procurement outbox dispatch failed", "id", r.ID, "type", r.EventType, "attempts", r.Attempts, "err", err)
			continue
		}
		if mErr := p.store.MarkDispatched(ctx, r.ID); mErr != nil {
			slog.Warn("procurement outbox mark-dispatched", "id", r.ID, "err", mErr)
		}
	}
	return len(rows), nil
}

func backoffFor(attempts int, max time.Duration) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := time.Duration(math.Pow(2, float64(attempts))) * time.Second
	if d > max {
		return max
	}
	return d
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
