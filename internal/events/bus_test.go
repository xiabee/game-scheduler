package events

import (
	"testing"
	"time"
)

func TestBusNotifyDelivers(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe()
	defer cancel()

	b.Notify()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("expected notification")
	}
}

func TestBusCoalesces(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe()
	defer cancel()

	// Many notifications with no reader in between collapse to one pending.
	for i := 0; i < 10; i++ {
		b.Notify()
	}
	<-ch // first
	select {
	case <-ch:
		t.Fatal("notifications should have coalesced into one")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBusCancelUnsubscribes(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe()
	cancel()
	// Closed channel: a receive returns immediately with !ok.
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after cancel")
	}
	// Notify must not panic with no subscribers.
	b.Notify()
	// Double cancel must be safe.
	cancel()
}

func TestNilBusNotifyNoPanic(t *testing.T) {
	var b *Bus
	b.Notify() // must be a no-op, not a panic
}
