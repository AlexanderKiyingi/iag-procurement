package repo

import (
	"context"
	"strings"
)

// LinkPortalUser binds an authentication user to a procurement vendor by party or SCM business id.
func (p *Procurement) LinkPortalUser(ctx context.Context, partyID, businessID, platformUserID string) error {
	partyID = strings.TrimSpace(partyID)
	businessID = strings.TrimSpace(businessID)
	platformUserID = strings.TrimSpace(platformUserID)
	if partyID == "" || platformUserID == "" {
		return nil
	}
	_, err := p.pool.Exec(ctx, `
		UPDATE vendors SET platform_user_id = $3::uuid
		WHERE party_id = $1::uuid
		   OR scm_business_id = $2
		   OR id = $2`, partyID, businessID, platformUserID)
	return err
}
