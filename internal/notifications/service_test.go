package notifications

import (
	"testing"

	"iag-procurement/backend/internal/events"
	"iag-procurement/backend/internal/signals"
)

// signalNames is every signal this service emits. Bus.Emit runs zero handlers
// and returns nil for a name nobody subscribed to, so an unwired signal looks
// exactly like a delivered one — no error, no log, nothing in the inbox.
//
// requisition.decided was emitted by the tiered (amount-band) approval path
// with no subscriber at all: approving or rejecting a requisition notified the
// requester through no channel, while the desk-chain path notified on the same
// transitions. Keep every name here wired.
var signalNames = []string{
	events.ProcurementAlert,
	events.RequisitionPending,
	events.RequisitionDecided,
}

func TestRegisterSubscribesEverySignal(t *testing.T) {
	bus := signals.NewBus()
	svc := NewService(nil, nil)
	svc.Register(bus)

	for _, name := range signalNames {
		if !bus.HasSubscribers(name) {
			t.Errorf("signal %q is emitted but has no subscriber: Emit will run zero "+
				"handlers and return nil, so the notification is dropped silently", name)
		}
	}
}
