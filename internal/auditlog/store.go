package auditlog

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

type Entry struct {
	UserID        *int64
	ActorEmail    string
	Action        string
	ResourceType  string
	ResourceID    string
	Method        string
	Path          string
	StatusCode    int
	IP            string
	UserAgent     string
	Details       map[string]any
}

func (s *Store) Insert(ctx context.Context, e Entry) error {
	var details []byte
	var err error
	if e.Details == nil {
		details = []byte("{}")
	} else {
		details, err = json.Marshal(e.Details)
		if err != nil {
			return err
		}
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO api_audit_logs (
			user_id, actor_email, action, resource_type, resource_id,
			method, path, status_code, ip, user_agent, details
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb)`,
		e.UserID, e.ActorEmail, e.Action, e.ResourceType, e.ResourceID,
		e.Method, e.Path, e.StatusCode, e.IP, e.UserAgent, string(details))
	return err
}

type APIAuditRow struct {
	ID           int64          `json:"id"`
	CreatedAt    string         `json:"createdAt"`
	UserID       *int64         `json:"userId,omitempty"`
	ActorEmail   string         `json:"actorEmail"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resourceType"`
	ResourceID   string         `json:"resourceId"`
	Method       string         `json:"method"`
	Path         string         `json:"path"`
	StatusCode   int            `json:"statusCode"`
	IP           string         `json:"ip"`
	UserAgent    string         `json:"userAgent"`
	Details      map[string]any `json:"details,omitempty"`
}

func (s *Store) List(ctx context.Context, limit int) ([]APIAuditRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,
		       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       user_id, actor_email, action, resource_type, resource_id,
		       method, path, status_code, ip, user_agent,
		       coalesce(details::text, '{}')
		FROM api_audit_logs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIAuditRow
	for rows.Next() {
		var r APIAuditRow
		var det string
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.UserID, &r.ActorEmail, &r.Action, &r.ResourceType, &r.ResourceID,
			&r.Method, &r.Path, &r.StatusCode, &r.IP, &r.UserAgent, &det); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(det), &r.Details)
		out = append(out, r)
	}
	return out, rows.Err()
}
