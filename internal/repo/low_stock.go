package repo

import (
	"context"
	"fmt"
)

// RecordLowStockSignal appends an audit entry when warehouse reports below-minimum stock.
func (p *Procurement) RecordLowStockSignal(ctx context.Context, data map[string]any) error {
	sku, _ := data["sku"].(string)
	qty, _ := data["qty"].(float64)
	minQty, _ := data["min_qty"].(float64)
	detail := fmt.Sprintf("Low stock signal sku=%s qty=%.2f min=%.2f", sku, qty, minQty)
	_, err := p.pool.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ('iag-warehouse', 'warehouse.stock.below_minimum', $1, $2)`,
		sku, detail)
	return err
}
