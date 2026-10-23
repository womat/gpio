// Package rpiemu provides an in-memory emulator for gpio.Pin.
//
// This package simulates GPIO behavior without requiring hardware, making it
// useful for testing and development. Each pin simulates a single GPIO line,
// including its mode (input/output), pull-up/down resistors, logical state,
// edge events, and optional debounce timing.
//
// # Driving the line
//
// SetValue behaves like real hardware and only writes to output pins. To
// exercise code that reacts to incoming edges, use Drive: it stands in for the
// outside world and works on input pins as well. Drive is what makes the
// emulator useful for testing receivers, such as a line decoder.
//
// # Debounce
//
// WithDebounce does not emulate the kernel. The emulator locks out edges for
// the debounce period after each reported edge: an edge within that time
// changes the level but is never reported, even if the new level then holds
// for longer than the period. The kernel instead reports the level that has
// settled. With a 5 ms debounce, Drive(High), Low 1 ms later and High again
// 10 ms after that give Rising, Rising here, but Rising, Falling, Rising on a
// Pi. A test that depends on the difference does not carry over to hardware.
//
// # Concurrency
//
// A pin is safe for concurrent use. Events are delivered by a single
// goroutine, so a watcher observes the edges in the order they occurred.
//
// # Lifecycle
//
// A pin must be closed after use by calling Close(), which also stops an
// active watcher and closes its event channel. After Close, SetValue, Value,
// Drive, WatchCh and WatchFunc return gpio.ErrClosed, as they do on real
// hardware.
//
// # Example usage
//
//	pin, err := rpiemu.NewPin(17,
//	    rpiemu.WithMode(gpio.Input),
//	    rpiemu.WithPullup(gpio.PullUp),
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer pin.Close()
//
//	events, err := pin.WatchCh(gpio.RisingEdge | gpio.FallingEdge)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Simulate an external signal and read the edge each change causes.
//	for _, level := range []gpio.Level{gpio.High, gpio.Low} {
//	    if err := pin.Drive(level); err != nil {
//	        log.Fatal(err)
//	    }
//	    evt := <-events
//	    fmt.Println(evt.Edge, "missed:", evt.Missed)
//	}
package rpiemu

import (
	"fmt"
	"sync"
	"time"

	"github.com/womat/gpio"
	"github.com/womat/gpio/internal/watch"
)

// pin simulates a GPIO pin.
type pin struct {
	sync.Mutex
	pin       int           // GPIO pin number
	mode      gpio.Mode     // input or output
	pull      gpio.PullMode // pull resistor configuration
	debounce  time.Duration // debounce duration for edge events
	state     gpio.Level    // current logical level
	lastEvent time.Time     // last event timestamp for debounce
	closed    bool          // set by Close; SetValue, Value and Drive fail from then on

	watcher watch.Watcher // event delivery, shared with the hardware backend
}

// Option configures a Pin in NewPin. An unknown value is ignored; see Not
// addressed in the README.
type Option func(*pin)

// Pin is an emulated GPIO pin.
//
// It is a gpio.Pin plus Drive, which has no counterpart on real hardware:
// it simulates the outside world changing the line, and is the only way to
// produce edge events on an input pin.
type Pin interface {
	gpio.Pin

	// Drive simulates an external level change on the line.
	//
	// Unlike SetValue it works regardless of the configured mode, so code
	// that reacts to input edges can be tested without hardware.
	// Debouncing and edge filtering apply.
	Drive(level gpio.Level) error
}

// Compile-time checks
var (
	_ gpio.Pin = (*pin)(nil)
	_ Pin      = (*pin)(nil)
)

// defaultBufferSize defines the size of the buffered channel for GPIO events.
const defaultBufferSize = 32

// NewPin creates a new emulated GPIO pin with default state (input, low, no pull).
//
// The returned Pin satisfies gpio.Pin and additionally offers Drive to
// simulate external level changes.
func NewPin(n int, opts ...Option) (Pin, error) {
	p := &pin{
		pin:   n,
		mode:  gpio.Input,
		pull:  gpio.PullNone,
		state: gpio.Low,
	}

	for _, opt := range opts {
		opt(p)
	}

	return p, nil
}

// Close disables any active watchers and resets the pin state. SetValue,
// Value, Drive and a watch started afterwards fail with gpio.ErrClosed, as
// on real hardware.
func (p *pin) Close() error {
	p.watcher.Close()

	p.Lock()
	defer p.Unlock()
	p.closed = true
	p.mode = gpio.Input
	p.pull = gpio.PullNone
	p.state = gpio.Low
	p.debounce = 0

	return nil
}

// SetValue sets the level of an output pin.
func (p *pin) SetValue(level gpio.Level) error {
	p.Lock()
	defer p.Unlock()

	if p.closed {
		return gpio.ErrClosed
	}
	if p.mode != gpio.Output {
		return gpio.ErrInvalidMode
	}

	return p.setLevel(level)
}

// Drive simulates an external level change on the line, regardless of the
// configured mode. See the Pin interface.
func (p *pin) Drive(level gpio.Level) error {
	p.Lock()
	defer p.Unlock()

	if p.closed {
		return gpio.ErrClosed
	}
	return p.setLevel(level)
}

// setLevel applies a new level and emits the resulting edge event, if any.
//
// The caller must hold the mutex, which guards the level and the debounce
// bookkeeping. Delivery is safe against a concurrent StopWatching through the
// lock in watch.Watcher, not through this mutex.
func (p *pin) setLevel(level gpio.Level) error {
	if level != gpio.High && level != gpio.Low {
		return gpio.ErrInvalidLevel
	}

	old := p.state
	p.state = level

	if old == level {
		return nil
	}

	edge := gpio.RisingEdge
	if level == gpio.Low {
		edge = gpio.FallingEdge
	}

	// Skip the debounce bookkeeping for edges nobody is waiting for.
	if !p.watcher.Wants(edge) {
		return nil
	}

	now := time.Now()
	if p.debounce > 0 && now.Sub(p.lastEvent) < p.debounce {
		return nil
	}
	p.lastEvent = now

	p.watcher.Deliver(gpio.Event{Time: now, Edge: edge})

	return nil
}

// Value returns the current logical level of the pin.
func (p *pin) Value() (gpio.Level, error) {
	p.Lock()
	defer p.Unlock()

	if p.closed {
		return gpio.Low, gpio.ErrClosed
	}
	return p.state, nil
}

// WithMode configures the pin as an input or output; an unknown mode is
// ignored.
func WithMode(mode gpio.Mode) Option {
	return func(p *pin) {
		if mode != gpio.Input && mode != gpio.Output {
			return
		}

		p.Lock()
		p.mode = mode
		p.Unlock()
	}
}

// WithPullup records the pull resistor (none, up or down); an unknown value
// is ignored.
func WithPullup(pull gpio.PullMode) Option {
	return func(p *pin) {
		if pull != gpio.PullNone && pull != gpio.PullUp && pull != gpio.PullDown {
			return
		}

		p.Lock()
		p.pull = pull
		p.Unlock()
	}
}

// WithDebounce suppresses every edge within d after a reported edge; d <= 0
// leaves debounce off. This is a lockout, not the kernel's debounce; see
// Debounce in the package doc.
func WithDebounce(d time.Duration) Option {
	return func(p *pin) {
		if d > 0 {
			p.Lock()
			p.debounce = d
			p.Unlock()
		}
	}
}

// Number returns the GPIO pin number.
func (p *pin) Number() int {
	return p.pin
}

// Info returns a string with the current pin configuration and state.
func (p *pin) Info() string {
	p.Lock()
	defer p.Unlock()
	return fmt.Sprintf("gpioemu pin=%d mode=%s level=%s pull=%s debounce=%s drops=%d",
		p.pin, p.mode, p.state, p.pull, p.debounce, p.watcher.Dropped())
}

// WatchCh enables edge detection and returns a channel for events.
//
// The channel is closed by StopWatching and by Close. An output pin cannot be
// watched and returns gpio.ErrInvalidMode, because on real hardware edge
// detection would turn it into an input.
func (p *pin) WatchCh(edges gpio.Edge) (<-chan gpio.Event, error) {
	if err := p.checkWatchable(); err != nil {
		return nil, err
	}
	return p.watcher.Start(edges, defaultBufferSize)
}

// WatchFunc enables edge detection and calls f for every event.
//
// Events are delivered by a single goroutine, so f is never called
// concurrently with itself and sees the edges in the order they occurred.
// A callback that blocks stalls delivery and eventually causes events to be
// dropped, which DroppedEvents reports.
//
// The goroutine ends when StopWatching is called or the pin is closed.
// Events still buffered then are not passed to f; a call already under way
// can still be running when StopWatching or Close returns. An output pin
// returns gpio.ErrInvalidMode, as for WatchCh.
func (p *pin) WatchFunc(edges gpio.Edge, f func(gpio.Event)) error {
	if f == nil {
		return gpio.ErrNilCallback
	}
	if err := p.checkWatchable(); err != nil {
		return err
	}

	return p.watcher.StartFunc(edges, defaultBufferSize, f)
}

// checkWatchable rejects a watch on an output pin, like the rpi backend.
func (p *pin) checkWatchable() error {
	p.Lock()
	defer p.Unlock()

	if p.mode == gpio.Output {
		return gpio.ErrInvalidMode
	}
	return nil
}

// StopWatching disables any active watcher (channel or callback).
//
// A channel handed out by WatchCh is closed, so a consumer ranging over it
// terminates. It is safe to call even if no watcher is active.
func (p *pin) StopWatching() error {
	p.watcher.Stop()

	return nil
}

// DroppedEvents returns the number of events dropped due to full buffer.
func (p *pin) DroppedEvents() uint64 {
	return p.watcher.Dropped()
}
