package repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// RecordVendorPayment reflects a settlement that finance already executed
// (finance.payment.made) back into procurement's read model: it inserts a
// payments row, marks the invoice Paid, and flips the linked PO's payment_status
// to "Issued". Correlated on invoiceRef, which is the procurement invoice id
// carried as the finance AP document_ref.
//
// Idempotent two ways: the caller dedupes on the platform event id, and the
// paymentRef unique index makes a duplicate insert a no-op. Unknown invoiceRefs
// (a settlement for a non-procurement AP item) are ignored, not errored.
func (p *Procurement) RecordVendorPayment(ctx context.Context, invoiceRef, paymentRef string, amount float64, currency, method string, payDate *time.Time) error {
	invoiceRef = strings.TrimSpace(invoiceRef)
	if invoiceRef == "" {
		return nil
	}
	if method == "" {
		method = "Bank Transfer"
	}
	if payDate == nil {
		t := time.Now().UTC()
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		payDate = &d
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Resolve the procurement invoice by id, then by invoice_no, so finance's
	// document_ref matches whichever the emitter used.
	var invoiceID, vendorID, currentStatus string
	var poID *string
	err = tx.QueryRow(ctx, `
		SELECT id, vendor_id, po_id, status FROM invoices WHERE id = $1 OR invoice_no = $1
		ORDER BY (id = $1) DESC LIMIT 1`, invoiceRef,
	).Scan(&invoiceID, &vendorID, &poID, &currentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // not a procurement invoice — nothing to reconcile
	}
	if err != nil {
		return err
	}
	if currency == "" {
		currency = "USD"
	}

	// payments.reference is the human-facing key and carries the finance
	// document ref; the row key is the column's own gen_random_uuid().
	if paymentRef == "" {
		paymentRef = newDocNo("PMT")
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO payments (invoice_id, vendor_id, amount, currency, pay_date, method, reference, status, initiated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'Cleared','finance')
		ON CONFLICT (reference) WHERE reference <> '' DO NOTHING`,
		invoiceID, vendorID, amount, currency, payDate, method, paymentRef,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // already recorded this payment ref
	}

	if _, err := tx.Exec(ctx, `
		UPDATE invoices SET status = 'Paid', payment_date = $2, payment_method = $3 WHERE id = $1`,
		invoiceID, payDate, method,
	); err != nil {
		return err
	}
	if poID != nil && strings.TrimSpace(*poID) != "" {
		if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET payment_status = 'Issued' WHERE id = $1`, *poID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_entries (username, action, target, detail)
		VALUES ('finance','payment',$1,$2)`,
		invoiceID, "vendor payment "+paymentRef+" settled in finance",
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
