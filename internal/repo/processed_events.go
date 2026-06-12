package repo

import "context"

// IsEventProcessed reports whether an inbound event id has already been handled,
// so a redelivery (rebalance, retry, no-DLQ replay) is a safe no-op.
func (p *Procurement) IsEventProcessed(ctx context.Context, eventID string) (bool, error) {
	if eventID == "" {
		return false, nil
	}
	var exists bool
	err := p.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1)`, eventID,
	).Scan(&exists)
	return exists, err
}

// MarkEventProcessed records that an inbound event id has been handled.
func (p *Procurement) MarkEventProcessed(ctx context.Context, eventID string) error {
	if eventID == "" {
		return nil
	}
	_, err := p.pool.Exec(ctx,
		`INSERT INTO processed_events (event_id) VALUES ($1) ON CONFLICT DO NOTHING`, eventID,
	)
	return err
}
