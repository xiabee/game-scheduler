// Package events provides a tiny in-process publish/subscribe bus used to push
// "something changed" signals to live dashboard streams (SSE). It carries no
// payload — subscribers recompute the current state when notified — so it stays
// trivially decoupled from what changed.
package events

import "sync"

// Bus fans out change notifications to subscribers.
type Bus struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

// New creates an empty Bus.
func New() *Bus { return &Bus{subs: map[chan struct{}]struct{}{}} }

// Subscribe registers a subscriber and returns its signal channel plus a cancel
// func that unregisters and closes it. The channel is buffered (size 1) so a
// burst of notifications coalesces into a single pending signal.
func (b *Bus) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
			close(ch)
		})
	}
	return ch, cancel
}

// Notify signals all subscribers without blocking. If a subscriber already has
// a pending signal, the extra notification is dropped (coalesced).
func (b *Bus) Notify() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
