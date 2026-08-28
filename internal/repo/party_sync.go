package repo

import (
	"context"
	"strings"
)

// SyncSCMParty links a procurement vendor row to an SCM party_id from scm.party.* events.
func (p *Procurement) SyncSCMParty(ctx context.Context, partyID, businessID, supplierType, name string) error {
	switch strings.ToLower(strings.TrimSpace(supplierType)) {
	case "vendor", "cooperative":
	default:
		return nil
	}
	partyID = strings.TrimSpace(partyID)
	businessID = strings.TrimSpace(businessID)
	if partyID == "" || businessID == "" {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = businessID
	}

	tag, err := p.pool.Exec(ctx, `
		UPDATE vendors SET
			party_id = $1::uuid,
			scm_business_id = COALESCE(scm_business_id, $2),
			name = CASE WHEN $3 <> '' THEN $3 ELSE name END
		WHERE scm_business_id = $2 OR id = $2`, partyID, businessID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	// vendors.id became uuid in migration 027_uuid_entity_ids, after this was
	// written. It used to insert the SCM business id ("COOP-001") straight into
	// id, which now fails on type - so a party sync would error at runtime even
	// with a healthy broker. The id is derived from the business id instead:
	// deterministic, so the same party always resolves to the same vendor row
	// and a replayed event updates rather than duplicating. The business id
	// itself is still carried in scm_business_id, which is where it belongs.
	_, err = p.pool.Exec(ctx, `
		INSERT INTO vendors (
			id, name, logo, category, contact, email, phone, country, terms,
			rating, status, total_spend, open_pos, party_id, scm_business_id
		) VALUES (
			uuid_in(overlay(overlay(md5('iag:scm:party:' || $2)
			  placing '3' from 13) placing '8' from 17)::cstring),
			$3, '', 'SCM Sync', '', '', '', '', '',
			0, 'Active', 0, 0, $1::uuid, $2
		)
		ON CONFLICT (id) DO UPDATE SET
			party_id = EXCLUDED.party_id,
			scm_business_id = EXCLUDED.scm_business_id,
			name = EXCLUDED.name`, partyID, businessID, name)
	return err
}

// MarkKafkaEventProcessed returns false when the event was already seen.
func (p *Procurement) MarkKafkaEventProcessed(ctx context.Context, eventID, topic string) (bool, error) {
	if eventID == "" {
		return true, nil
	}
	tag, err := p.pool.Exec(ctx, `
		INSERT INTO kafka_dedupe (event_id, topic) VALUES ($1, $2)
		ON CONFLICT (event_id) DO NOTHING`, eventID, topic)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
