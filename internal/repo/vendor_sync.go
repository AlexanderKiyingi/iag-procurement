package repo

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"iag-procurement/backend/internal/events"
)

// enqueueVendorUpsertedTx writes party.vendor.upserted into the outbox in the
// same tx as the vendor mutation, so the master change and its cross-service
// notification commit atomically (or not at all). No-op unless the outbox is
// configured and vendor sync is enabled.
func (p *Procurement) enqueueVendorUpsertedTx(ctx context.Context, tx pgx.Tx, v events.VendorUpsert) error {
	if p.outbox == nil || !p.vendorSyncEnabled {
		return nil
	}
	if strings.TrimSpace(v.PartyID) == "" {
		return nil
	}
	key, payload, err := events.BuildVendorUpserted(v)
	if err != nil {
		return err
	}
	return p.outbox.EnqueueTx(ctx, tx, events.TypeVendorUpserted, key, payload)
}

// UpsertVendorByParty applies an inbound party.vendor.upserted (from finance or
// SCM) to the local vendor master, keyed on the shared party_id. It matches an
// existing row by party_id first, then by scm_business_id/id, updating in place;
// otherwise it inserts a new synced vendor. It NEVER enqueues an outgoing event
// — that is the mesh's loop-prevention rule (emit only on local API mutations).
func (p *Procurement) UpsertVendorByParty(ctx context.Context, partyID, code, name, category, email, phone, country, status string) error {
	partyID = strings.TrimSpace(partyID)
	if partyID == "" {
		return nil
	}
	name = strings.TrimSpace(name)
	if status == "" {
		status = "Active"
	}
	if category == "" {
		category = "Vendor Sync"
	}

	// Try to update an existing row identified by party_id or by the natural key
	// (scm_business_id / procurement id) carried in code.
	tag, err := p.pool.Exec(ctx, `
		UPDATE vendors SET
			party_id = $1::uuid,
			name = CASE WHEN $3 <> '' THEN $3 ELSE name END,
			email = CASE WHEN $4 <> '' THEN $4 ELSE email END,
			phone = CASE WHEN $5 <> '' THEN $5 ELSE phone END,
			country = CASE WHEN $6 <> '' THEN $6 ELSE country END,
			status = CASE WHEN $7 <> '' THEN $7 ELSE status END
		WHERE party_id = $1::uuid OR scm_business_id = $2 OR id = $2`,
		partyID, strings.TrimSpace(code), name, strings.TrimSpace(email),
		strings.TrimSpace(phone), strings.TrimSpace(country), strings.TrimSpace(status))
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	// No local row yet — insert a synced vendor. Use a fresh procurement id so
	// the local PK stays in the "V-..." shape; scm_business_id records the source
	// natural key when present.
	id := newProcurementID("V")
	scmBiz := strings.TrimSpace(code)
	_, err = p.pool.Exec(ctx, `
		INSERT INTO vendors (
			id, name, logo, category, contact, email, phone, country, terms,
			rating, status, total_spend, open_pos, party_id, scm_business_id
		) VALUES (
			$1, $2, '', $3, '', $4, $5, $6, '',
			0, $7, 0, 0, $8::uuid, NULLIF($9,'')
		)
		ON CONFLICT (id) DO NOTHING`,
		id, name, category, strings.TrimSpace(email), strings.TrimSpace(phone),
		strings.TrimSpace(country), strings.TrimSpace(status), partyID, scmBiz)
	return err
}
