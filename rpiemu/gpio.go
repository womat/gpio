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
//  func main() {
//      // Create a GPIO pin
//      gpioPin, err := rpiemu.NewPin(17)
//      if err != nil {
//          log.Fatal(err)
//      }
//      defer gpioPin.Close()
//
//      // Configure as output and set high
//      if err := gpioPin.SetMode(gpio.Output); err != nil {
//          log.Fatal(err)
//      }
//      if err := gpioPin.SetValue(gpio.High); err != nil {
//          log.Fatal(err)
//      }
//
//      // Configure as input with pull-up
//      if err := gpioPin.SetMode(gpio.Input); err != nil {
//          log.Fatal(err)
//      }
//      if err := gpioPin.SetPullMode(gpio.PullUp); err != nil {
//          log.Fatal(err)
//      }
//		// Create a context to control watching lifetime
//		ctx, cancel := context.WithCancel(context.Background())
//		defer cancel()
//
//      // Watch for rising and falling edges
//      events, err := gpioPin.Watch(ctx, gpio.RisingEdge | gpio.FallingEdge)
//      if err != nil {
//          log.Fatal(err)
//      }
//
//      // Consume events
//      go func() {
//          for evt := range events {
//              fmt.Println("GPIO Event:", evt.Edge, "at", evt.Time.Format("15:04:05.000"))
//          }
//      }()
//
//      // Keep running for a while to catch events
//      time.Sleep(5 * time.Second)
//
//      // Stop watching (optional)
//   	// cancel() // if you want to stop watching immediately
//  }

package rpiemu

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/womat/golib/gpio"
)

// pin simulates a GPIO pin.
type pin struct {
	sync.Mutex
	pin       int // pin is the GPIO number
	mode      gpio.Mode
	pull      gpio.PullMode
	debounce  time.Duration
	state     gpio.Level      // state represents the current logical level of the emulated GPIO pin.
	dropCount atomic.Uint64   // atomic counter for dropped events
	watching  atomic.Bool     // atomic flag: true if Watch() is active
	events    chan gpio.Event //  channel to deliver GPIO events
	edge      gpio.Edge       // edge represents the configured edge detection for this pin.
	lastEvent time.Time       // lastEvent stores the time of the last edge event, used for debouncing.
}

// Compile-time check
var _ gpio.Pin = (*pin)(nil)

// NewPin creates a new emulated GPIO pin.
func NewPin(n int) (gpio.Pin, error) {
	p := pin{
		pin:      n,
		mode:     gpio.Input,
		pull:     gpio.PullNone,
		state:    gpio.Low,
		debounce: 0,
		edge:     0,
	}

	p.dropCount.Store(0)
	p.watching.Store(false)
	return &p, nil
}

// Close releases the gpio pin.
func (p *pin) Close() error {

	err := p.StopWatching()

	p.mode = gpio.Input
	p.pull = gpio.PullNone
	p.state = gpio.Low
	p.debounce = 0
	return err
}

// SetValue sets the logical level of the pin. Only allowed if the pin is in Output mode.
// If a watcher is registered, it is triggered on level change, respecting debouncing.
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

		select {
		case p.events <- gpio.Event{Time: now, Edge: edge}:
			// successfully delivered
		default:
			// channel full → drop event
			p.dropCount.Add(1)
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

// SetMode sets the pin direction to Input or Output.
func (p *pin) SetMode(mode gpio.Mode) error {
	p.Lock()
	defer p.Unlock()

	if mode != gpio.Input && mode != gpio.Output {
		return gpio.ErrInvalidMode
	}

	p.mode = mode
	return nil
}

// SetPullMode sets the internal pull resistor of the pin (Up, Down, None).
func (p *pin) SetPullMode(mode gpio.PullMode) error {
	if mode != gpio.PullNone && mode != gpio.PullUp && mode != gpio.PullDown {
		return gpio.ErrInvalidPullMode
	}

	p.Lock()
	defer p.Unlock()
	p.pull = mode
	return nil
}

// SetDebounce sets the debounced duration for edge events on this pin.
func (p *pin) SetDebounce(d time.Duration) error {
	p.Lock()
	defer p.Unlock()
	p.debounce = d
	return nil
}

// Number returns the GPIO pin.
func (p *pin) Number() int {
	return p.pin
}

// Info returns diagnostic information about the emulated pin.
func (p *pin) Info() string {
	p.Lock()
	defer p.Unlock()
	return fmt.Sprintf("gpioemu pin=%d mode=%s level=%s pull=%s debounce=%s",
		p.pin, p.mode, p.state, p.pull, p.debounce)
}

// Watch enables edge detection and returns a channel for events.
// The edges parameter can be a combination of gpio.RisingEdge and gpio.FallingEdge.
func (p *pin) Watch(ctx context.Context, edges gpio.Edge) (<-chan gpio.Event, error) {
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
	p.events = make(chan gpio.Event, 8)
	return p.events, nil
}

// StopWatching unregisters the callback.
func (p *pin) StopWatching() error {
	p.Lock()
	defer p.Unlock()

	p.events = nil
	p.watching.Store(false)
	return nil
}

// DroppedEvents returns how many events were dropped due to a full buffer.
func (p *pin) DroppedEvents() uint64 {
	return p.dropCount.Load()
}
