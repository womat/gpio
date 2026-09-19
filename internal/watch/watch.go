// Package watch implements the edge event delivery shared by the gpio backends.
//
// A Watcher owns the channel the consumer ranges over, the edge mask that was
// asked for, and the number of events that had to be dropped. Delivery and
// shutdown are synchronised with each other, which is what keeps a backend
// from closing the channel while an event is being delivered into it - a race
// that ends in "send on closed channel" and takes the process down.
//
// The backends differ in how they learn about an edge (a kernel callback for
// real hardware, a simulated level change for the emulator) but not in what
// happens afterwards, so that part lives here and is tested without hardware.
package watch

import (
	"sync"
	"sync/atomic"

	"github.com/womat/golib/gpio"
)

// Watcher delivers edge events to a single consumer.
//
// The zero value is an inactive Watcher, ready for use. A Watcher is safe for
// concurrent use.
type Watcher struct {
	mu     sync.RWMutex
	events chan gpio.Event
	edges  gpio.Edge
	active bool

	dropped atomic.Uint64
}

// Start claims the Watcher and returns the channel events are delivered on.
//
// It reports gpio.ErrInvalidEdgeConfig if edges selects no edge at all, and
// gpio.ErrAlreadyWatching if a watcher is already active.
func (w *Watcher) Start(edges gpio.Edge, bufferSize int) (<-chan gpio.Event, error) {
	if edges&(gpio.RisingEdge|gpio.FallingEdge) == 0 {
		return nil, gpio.ErrInvalidEdgeConfig
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.active {
		return nil, gpio.ErrAlreadyWatching
	}

	w.edges = edges
	w.events = make(chan gpio.Event, bufferSize)
	w.active = true

	return w.events, nil
}

// Stop releases the Watcher and closes the channel handed out by Start, so a
// consumer ranging over it terminates.
//
// It reports whether a watcher was active, and is safe to call when none was.
func (w *Watcher) Stop() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.active {
		return false
	}

	close(w.events)
	w.events = nil
	w.active = false

	return true
}

// Active reports whether a watcher is currently running.
func (w *Watcher) Active() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.active
}

// Wants reports whether an active watcher asked for this edge.
func (w *Watcher) Wants(edge gpio.Edge) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.active && w.edges&edge != 0
}

// Deliver hands an event to the consumer.
//
// It never blocks: if the consumer does not keep up, the event is dropped and
// counted. Events of an edge the watcher did not ask for are ignored, as are
// all events while no watcher is active.
//
// The read lock is held for the whole delivery, which is what makes this safe
// against a concurrent Stop - the channel cannot be closed while a send is in
// flight. Taking it is cheap and never blocks on another delivery.
func (w *Watcher) Deliver(event gpio.Event) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if !w.active || w.edges&event.Edge == 0 {
		return
	}

	select {
	case w.events <- event:
		// delivered
	default:
		w.dropped.Add(1)
	}
}

// Dropped returns how many events were dropped because the consumer did not
// keep up. The count is cumulative over the lifetime of the Watcher.
func (w *Watcher) Dropped() uint64 {
	return w.dropped.Load()
}
