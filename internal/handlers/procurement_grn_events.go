package handlers

import (
	"context"
	"strings"

	procevents "iag-procurement/backend/internal/events"
	"iag-procurement/backend/internal/models"
)

func (a *API) emitGrnPostedIfNeeded(ctx context.Context, row *models.Grn) {
	if a.publisher == nil || row == nil || strings.TrimSpace(row.Status) != "Posted" {
		return
	}
	poID := ""
	if row.PoID != nil {
		poID = strings.TrimSpace(*row.PoID)
	}
	var lines []procevents.GrnPostedLine
	if poID != "" {
		if poLines, err := a.procurement.ListPOEventLines(ctx, poID); err == nil {
			lines = poLines
		}
	}
	a.publisher.PublishGrnPosted(ctx, row.ID, poID, row.VendorID, row.ReceivedBy, lines)
}
