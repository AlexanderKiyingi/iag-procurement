package repo

import (
	"context"

	procevents "iag-procurement/backend/internal/events"
)

// ListPOEventLines returns SKU/qty/uom rows for a PO to enrich procurement.grn.posted.
func (p *Procurement) ListPOEventLines(ctx context.Context, poID string) ([]procevents.GrnPostedLine, error) {
	if poID == "" {
		return nil, nil
	}
	rows, err := p.pool.Query(ctx, `
		SELECT i.sku, pl.qty, i.uom
		FROM po_lines pl
		JOIN items i ON i.id = pl.item_id
		WHERE pl.po_id = $1
		ORDER BY pl.id`, poID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []procevents.GrnPostedLine
	for rows.Next() {
		var line procevents.GrnPostedLine
		if err := rows.Scan(&line.SKU, &line.Qty, &line.UOM); err != nil {
			return nil, err
		}
		out = append(out, line)
	}
	return out, rows.Err()
}
