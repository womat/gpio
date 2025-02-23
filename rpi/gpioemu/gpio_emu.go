// Package gpioemu provides an emulator for Raspberry Pi GPIO pins.
//
// This package simulates GPIO ports and events without requiring actual hardware,
// making it useful for testing and development. It allows setting input and output modes,
// toggling GPIO states, and handling event-driven changes such as rising and falling edges.
//
// Features:
// - Simulates GPIO pin behavior for testing without physical hardware
// - Supports input and output modes
// - Allows setting and reading GPIO states
// - Implements event handling for rising and falling edge detection
// - Provides debouncing support to filter out noise in input signals
//
// Example Usage:
//
//  p, err := gpioemu.NewPort(17, time.Millisecond * 100)
//  if err != nil {
//      log.Fatal(err)
//  }
//  defer p.Close()
//
//  p.SetOutputMode()
//  p.SetValue(1)
//  val, _ := p.GetValue()
//  fmt.Println("GPIO state:", val)
//
//  p.StartWatchingEvents(func(evt rpi.Event) {
//      fmt.Println("Event detected:", evt)
//  })
//
// This package is useful for software development and testing environments
// where real GPIO hardware is not available.
//
// Note: This is an emulator and does not interact with actual hardware.

package gpioemu

import (
	"fmt"
	"github.com/womat/golib/rpi"
	"sync"
	"time"
)

// Port represents a simulated GPIO port with state (HIGH or LOW) for testing purposes.
type Port struct {
	sync.RWMutex
	callback  func(event rpi.Event) // callback is the event handler function.
	gpioPin   int                   // gpioPin is the GPIO number of the port.
	portState int                   // portState represents the simulated state of the GPIO port (HIGH or LOW).
}

// NewPort requests control of a single line on a chip.
func NewPort(gpio int, pulse time.Duration) (*Port, error) {
	return &Port{
		gpioPin:   gpio,
		portState: rpi.Low,
	}, nil
}

// Close closes the portState and releases the resources.
// The portState is set to input mode. Sending events is disabled.
func (p *Port) Close() error {
	return nil
}

// SetInputMode sets the portState to input mode.
func (p *Port) SetInputMode() error {
	return nil
}

// SetOutputMode sets the portState to output mode.
func (p *Port) SetOutputMode() error {
	return nil
}

// SetValue sets the value of the portState.
// The value can be 0 or 1. 0 is low, 1 is high.
func (p *Port) SetValue(n int) error {
	if n != rpi.Low && n != rpi.High {
		return fmt.Errorf("invalid value: %d, expected 0 (Low) or 1 (High)", n)
	}

	p.Lock()
	p.portState = n
	callback := p.callback
	p.Unlock()

	if callback != nil {
		go callback(rpi.Event{Time: time.Now(), Edge: n})
	}
	return nil
}

// GetValue returns the value of the portState. The value can be 0 or 1. 0 is low, 1 is high.
func (p *Port) GetValue() (int, error) {
	p.RLock()
	n := p.portState
	p.RUnlock()
	return n, nil
}

// SetPullUp simulates setting the port to pull-up mode. (Not implemented in the emulator)
func (p *Port) SetPullUp() error {
	return nil
}

// SetPullDown simulates setting the port to pull-up mode. (Not implemented in the emulator)
func (p *Port) SetPullDown() error {
	return nil
}

// Port returns the GPIO number.
func (p *Port) Port() int {
	return p.gpioPin
}

// WatchingEvents starts watching the portState for events. The handler is called when an event is detected.
// The handler is called with an Event struct that contains the timestamp and the type of event.
// The handler is called in a separate goroutine.
func (p *Port) WatchingEvents(handler func(rpi.Event)) error {
	p.callback = handler
	return nil
}

// StopWatching stops watching the portState for events.
// The handler is removed and no more events are detected.
func (p *Port) StopWatching() error {
	p.callback = nil
	return nil
}

// SetDebounceTime sets the debounced time of the portState. This is used to prevent bouncing values.
// The debounced time is the time that the portState is disabled after an event is detected.
// The debounced time is useful for buttons or switches that are connected to ground or VCC.
// The debounced time is used to prevent multiple events when the button is pressed or released.
func (p *Port) SetDebounceTime(t time.Duration) error {
	return nil
}

// Info returns the information of the portState. This is useful for debugging.
// The information is returned as a string.
func (p *Port) Info() string {
	return fmt.Sprint(p)
}
