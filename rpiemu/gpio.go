// Package rpiemu provides an in-memory emulator for gpio.Pin.
//
// This package simulates GPIO behavior without requiring hardware, making it
// useful for testing and development. Each pin simulates a single GPIO line,
// including its mode (input/output), pull-up/down resistors, logical state,
// edge events, and optional debounce timing.
//
// # Concurrency
//
// A pin is safe for concurrent use. Event callbacks are executed asynchronously
// in separate goroutines.
//
// # Lifecycle
//
// A pin must be closed after use by calling Close(). This disables event callbacks.
//
// # Example Usage
//
//	 func main() {
//	     // Create a GPIO pin
//	     gpioPin, err := rpiemu.NewPin(17)
//	     if err != nil {
//	         log.Fatal(err)
//	     }
//	     defer gpioPin.Close()
//
//	     // Configure as output and set high
//	     if err := gpioPin.SetMode(gpio.Output); err != nil {
//	         log.Fatal(err)
//	     }
//	     if err := gpioPin.SetValue(gpio.High); err != nil {
//	         log.Fatal(err)
//	     }
//
//	     // Configure as input with pull-up
//	     if err := gpioPin.SetMode(gpio.Input); err != nil {
//	         log.Fatal(err)
//	     }
//	     if err := gpioPin.SetPullMode(gpio.PullUp); err != nil {
//	         log.Fatal(err)
//	     }
//		 // Create a context to control watching lifetime
//			ctx, cancel := context.WithCancel(context.Background())
//			defer cancel()
//
//	     // Watch for rising and falling edges
//	     events, err := gpioPin.WatchCh(ctx, gpio.RisingEdge | gpio.FallingEdge)
//	     if err != nil {
//	         log.Fatal(err)
//	     }
//
//	     // Consume events
//	     go func() {
//	         for evt := range events {
//	             fmt.Println("GPIO Event:", evt.Edge, "at", evt.Time.Format("15:04:05.000"))
//	         }
//	     }()
//
//	     // Keep running for a while to catch events
//	     time.Sleep(5 * time.Second)
//
//	     // Stop watching (optional)
//	     // cancel()
//	 }
package rpiemu

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/womat/golib/gpio"
)

// pin simulates a GPIO pin.
type pin struct {
	sync.Mutex
	pin       int              // GPIO pin number
	mode      gpio.Mode        // input or output
	pull      gpio.PullMode    // pull resistor configuration
	debounce  time.Duration    // debounce duration for edge events
	state     gpio.Level       // current logical level
	dropCount atomic.Uint64    // number of events dropped due to full channel
	watching  atomic.Bool      // true if WatchCh or WatchFunc is active
	events    chan gpio.Event  // channel to deliver events (WatchCh only)
	edge      gpio.Edge        // configured edge detection
	lastEvent time.Time        // last event timestamp for debounce
	callback  func(gpio.Event) // optional callback for edge events
}

type Option func(*pin)

// Compile-time check
var _ gpio.Pin = (*pin)(nil)

// defaultBufferSize defines the size of the buffered channel for GPIO events.
const defaultBufferSize = 32

// NewPin creates a new emulated GPIO pin with default state (input, low, no pull).
func NewPin(n int, opts ...Option) (gpio.Pin, error) {
	p := &pin{
		pin:      n,
		mode:     gpio.Input,
		pull:     gpio.PullNone,
		state:    gpio.Low,
		debounce: 0,
		edge:     0,
	}

	for _, opt := range opts {
		opt(p)
	}

	p.dropCount.Store(0)
	p.watching.Store(false)
	return p, nil
}

// Close disables any active watchers and resets the pin state.
func (p *pin) Close() error {

	err := p.StopWatching()

	p.Lock()
	defer p.Unlock()
	p.mode = gpio.Input
	p.pull = gpio.PullNone
	p.state = gpio.Low
	p.debounce = 0
	p.callback = nil
	return err
}

// SetValue sets the pin's logical level (output only) and triggers edge events if watching.
// Debouncing is respected.
func (p *pin) SetValue(level gpio.Level) error {
	p.Lock()
	defer p.Unlock()

	if p.mode != gpio.Output {
		return gpio.ErrInvalidMode
	}

	if level != gpio.High && level != gpio.Low {
		return gpio.ErrInvalidLevel
	}

	old := p.state
	p.state = level
	if old != level && p.watching.Load() {
		now := time.Now()
		if p.debounce > 0 && now.Sub(p.lastEvent) < p.debounce {
			return nil
		}
		p.lastEvent = now

		edge := gpio.RisingEdge
		if level == gpio.Low {
			edge = gpio.FallingEdge
		}

		event := gpio.Event{Time: now, Edge: edge}

		// atomic load of the channel reference, no lock needed
		if ch := p.events; ch != nil {
			select {
			case ch <- event:
				// successfully delivered
			default:
				// channel full → drop event
				p.dropCount.Add(1)
			}
		}

		// call callback if present
		if f := p.callback; f != nil {
			go f(event) // async to avoid blocking
		}
	}

	return nil
}

// Value returns the current logical level of the pin.
func (p *pin) Value() (gpio.Level, error) {
	p.Lock()
	defer p.Unlock()
	return p.state, nil
}

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

// WithDebounce configures hardware debounce for the GPIO Pin during line request.
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
		p.pin, p.mode, p.state, p.pull, p.debounce, p.dropCount.Load())
}

// WatchCh enables edge detection and returns a channel for events.
func (p *pin) WatchCh(edges gpio.Edge) (<-chan gpio.Event, error) {
	if !p.watching.CompareAndSwap(false, true) {
		return nil, gpio.ErrAlreadyWatching
	}

	if edges&(gpio.RisingEdge|gpio.FallingEdge) == 0 {
		return nil, gpio.ErrInvalidEdgeConfig
	}

	p.Lock()
	defer p.Unlock()

	p.dropCount.Add(0)
	p.edge = edges
	p.callback = nil
	p.events = make(chan gpio.Event, defaultBufferSize)
	return p.events, nil
}

// WatchFunc enables edge detection and registers a callback for events.
func (p *pin) WatchFunc(edges gpio.Edge, f func(gpio.Event)) error {
	if !p.watching.CompareAndSwap(false, true) {
		return gpio.ErrAlreadyWatching
	}

	if edges&(gpio.RisingEdge|gpio.FallingEdge) == 0 {
		return gpio.ErrInvalidEdgeConfig
	}

	p.Lock()
	defer p.Unlock()

	p.dropCount.Add(0)
	p.edge = edges
	p.events = nil
	p.callback = f
	return nil
}

// StopWatching disables any active watcher (channel or callback).
func (p *pin) StopWatching() error {
	p.Lock()
	defer p.Unlock()

	p.events = nil
	p.callback = nil
	p.watching.Store(false)
	return nil
}

// DroppedEvents returns the number of events dropped due to full buffer.
func (p *pin) DroppedEvents() uint64 {
	return p.dropCount.Load()
}
