package repo

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestSyncSCMParty_skipsFarmers(t *testing.T) {
	p := &Procurement{}
	if err := p.SyncSCMParty(context.Background(), uuid.NewString(), "FRM-1", "farmer", "Test"); err != nil {
		t.Fatalf("farmer skip should not error: %v", err)
	}
}

func TestSyncSCMParty_requiresIDs(t *testing.T) {
	p := &Procurement{}
	if err := p.SyncSCMParty(context.Background(), "", "VND-1", "vendor", "Acme"); err != nil {
		t.Fatalf("empty party id should no-op: %v", err)
	}
}
