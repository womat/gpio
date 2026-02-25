// Package rpiemu provides an in-memory emulator for gpio.Pin.
//
// This package simulates GPIO behavior without requiring hardware, making it
// useful for testing and development. Each Pin simulates a single GPIO line,
// including its mode (input/output), pull-up/down resistors, logical state,
// edge events, and optional debounce timing.
//
// # Concurrency
//
// A Pin is safe for concurrent use. Event callbacks are executed asynchronously
// in separate goroutines.
//
// # Lifecycle
//
// A Pin must be closed after use by calling Close(). This disables event callbacks.
//
// # Example
//
//	p, _ := rpiemu.NewPin(17)
//	p.SetMode(gpio.Output)
//	p.SetValue(gpio.High)
//	p.WatchingEvents(func(evt gpio.Event) {
//	    fmt.Println("Event:", evt.Edge, "at", evt.Time)
//	})
package rpiemu

import (
	"fmt"
	"sync"
	"time"

	"github.com/womat/golib/gpio"
)

var ErrInvalidMode = fmt.Errorf("invalid mode")
var ErrInvalidLevel = fmt.Errorf("invalid level")
var ErrInvalidPullMode = fmt.Errorf("invalid pull mode")
var ErrGPIONotOutput = fmt.Errorf("cannot set value: gpio is not in output mode")

// Pin simulates a GPIO pin.
type Pin struct {
	sync.RWMutex
	pin      int // pin is the GPIO number
	mode     gpio.Mode
	pull     gpio.PullMode
	debounce time.Duration
	state    gpio.Level // state represents the current logical level of the emulated GPIO Pin.

	callback  func(event gpio.Event) // callback is the event handler function.
	lastEvent time.Time              // lastEvent stores the time of the last edge event, used for debouncing.
}

// Compile-time check
var _ gpio.Pin = (*Pin)(nil)

// NewPin creates a new emulated GPIO pin.
func NewPin(n int) (*Pin, error) {
	return &Pin{
		pin:   n,
		mode:  gpio.Input,
		pull:  gpio.PullNone,
		state: gpio.Low,
	}, nil
}

// Close releases the gpio pin.
func (p *Pin) Close() error {
	p.Lock()
	defer p.Unlock()
	p.callback = nil
	return nil
}

// SetValue sets the logical level of the Pin. Only allowed if the Pin is in Output mode.
// If a callback is registered, it is triggered on level change, respecting debouncing.
func (p *Pin) SetValue(level gpio.Level) error {
	p.Lock()
	defer p.Unlock()

	if p.mode != gpio.Output {
		return ErrGPIONotOutput
	}

	if level != gpio.High && level != gpio.Low {
		return ErrInvalidLevel
	}

	old := p.state
	p.state = level
	if old != level && p.callback != nil {
		now := time.Now()
		if p.debounce > 0 && now.Sub(p.lastEvent) < p.debounce {
			return nil
		}
		p.lastEvent = now

		edge := gpio.RisingEdge
		if level == gpio.Low {
			edge = gpio.FallingEdge
		}

		go p.callback(gpio.Event{
			Time: now,
			Edge: edge,
		})
	}

	return nil
}

// Value returns the current logical level of the Pin.
func (p *Pin) Value() (gpio.Level, error) {
	p.RLock()
	defer p.RUnlock()
	return p.state, nil
}

// SetMode sets the Pin direction to Input or Output.
func (p *Pin) SetMode(mode gpio.Mode) error {
	p.Lock()
	defer p.Unlock()

	if mode != gpio.Input && mode != gpio.Output {
		return ErrInvalidMode
	}

	p.mode = mode
	return nil
}

// SetPullMode sets the internal pull resistor of the Pin (Up, Down, None).
func (p *Pin) SetPullMode(mode gpio.PullMode) error {
	if mode != gpio.PullNone && mode != gpio.PullUp && mode != gpio.PullDown {
		return ErrInvalidPullMode
	}

	p.Lock()
	defer p.Unlock()
	p.pull = mode
	return nil
}

// SetDebounce sets the debounced duration for edge events on this Pin.
func (p *Pin) SetDebounce(d time.Duration) error {
	p.Lock()
	defer p.Unlock()
	p.debounce = d
	return nil
}

// Number returns the GPIO pin.
func (p *Pin) Number() int {
	return p.pin
}

// Info returns diagnostic information about the emulated Pin.
func (p *Pin) Info() string {
	p.RLock()
	defer p.RUnlock()
	return fmt.Sprintf("gpioemu pin=%d mode=%s level=%s pull=%s debounce=%s",
		p.pin, p.mode, p.state, p.pull, p.debounce)
}

// WatchingEvents registers a callback for edge events.
func (p *Pin) WatchingEvents(handler func(gpio.Event)) error {
	p.Lock()
	defer p.Unlock()
	p.callback = handler
	return nil
}

// StopWatching unregisters the callback.
func (p *Pin) StopWatching() error {
	p.Lock()
	defer p.Unlock()
	p.callback = nil
	return nil
}
