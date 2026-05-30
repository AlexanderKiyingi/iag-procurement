package notifications

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

type Row struct {
	ID        int64   `json:"id"`
	EventType string  `json:"eventType"`
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	Severity  string  `json:"severity"`
	ReadAt    *string `json:"readAt,omitempty"`
	CreatedAt string  `json:"createdAt"`
}

func (s *Store) InsertInApp(ctx context.Context, eventType, title, body, severity string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO notifications (event_type, title, body, severity)
		VALUES ($1, $2, $3, $4) RETURNING id`, eventType, title, body, severity).Scan(&id)
	return id, err
}

func (s *Store) List(ctx context.Context, limit int) ([]Row, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, event_type, title, body, severity,
		       to_char(read_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM notifications ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var readAt sql.NullString
		if err := rows.Scan(&r.ID, &r.EventType, &r.Title, &r.Body, &r.Severity, &readAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		if readAt.Valid {
			s := readAt.String
			r.ReadAt = &s
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MarkRead(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE notifications SET read_at = NOW() WHERE id = $1 AND read_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("notification %d not found or already read", id)
	}
	return nil
}
