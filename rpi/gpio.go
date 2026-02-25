// Package gpio provides a Linux implementation of the gpio.Pin interface
// using the go-gpiocdev library.
//
// This package interacts with GPIO lines via the Linux character device
// interface (gpiod). It allows configuring Pins as input or output,
// reading and writing logical levels, configuring pull resistors,
// enabling edge detection, and setting hardware debounce.
//
// A Pin represents a single GPIO line on the Raspberry Pi. Internally,
// this is a gpiod Line object, but the interface abstracts it as a Pin.
//
// # Concurrency
//
// A Pin is safe for concurrent use unless otherwise stated.
// Event handlers are executed asynchronously in separate goroutines.
//
// # Lifecycle
//
// A Pin must be closed after use by calling Close(). Closing the Pin
// releases the underlying Line and disables event delivery.
//
// # Requirements
//
// This package requires a Linux system with GPIO character device support
// and the gpiod subsystem enabled.
//
// # Example Usage
//
//  gpioPin, err := gpio.NewPin(17)
//  if err != nil {
//      log.Fatal(err)
//  }
//  defer gpioPin.Close()
//
//  // Configure as output and set high
//  gpioPin.SetMode(gpio.Output)
//  gpioPin.SetValue(gpio.High)
//
//  // Configure as input with pull-up
//  gpioPin.SetMode(gpio.Input)
//  gpioPin.SetPullMode(gpio.PullUp)
//
//  // Watching events
//  gpioPin.WatchingEvents(func(evt gpio.Event) {
//      fmt.Println("GPIO Pin Event:", evt.Edge, "at", evt.Time)
//  })

package rpi

import (
	"errors"
	"fmt"
	"sync"
	"time"

	gpiod "github.com/warthog618/go-gpiocdev"
	"github.com/womat/golib/gpio"
)

// Compile-time check
var _ gpio.Pin = (*Pin)(nil)

var ErrUnknownPullMode = errors.New("unknown pull mode")
var ErrUnknownMode = errors.New("unknown mode")

// Chip defines the default GPIO chip device used to request Pins.
const Chip = "gpiochip0"

// Pin represents a single requested GPIO Pin (Line).
//
// A Pin provides access to configuring the line direction,
// reading and writing logical levels, configuring pull resistors,
// and receiving edge events.
//
// Internally, it wraps a gpiod.Line object from the Linux GPIO character device API.
type Pin struct {
	sync.RWMutex
	gpioLine     *gpiod.Line            // gpioLine is the underlying gpiod Line handler
	eventHandler func(event gpio.Event) // eventHandler is the registered callback function
}

// NewPin requests control of a single GPIO Pin (Line) from the configured chip.
// The Pin is initially configured as input with edge detection disabled.
func NewPin(pin int) (*Pin, error) {

	var err error

	p := &Pin{RWMutex: sync.RWMutex{}}
	p.gpioLine, err = gpiod.RequestLine(
		Chip,
		pin,
		gpiod.WithEventHandler(p.handler),
		gpiod.WithoutEdges,
		gpiod.AsInput)

	return p, err
}

// Close releases the underlying Line resource and disables event detection.
func (p *Pin) Close() error {
	_ = p.gpioLine.Reconfigure(gpiod.WithoutEdges, gpiod.AsInput)
	return p.gpioLine.Close()
}

// SetValue sets the logical output level of the GPIO Pin.
// The Pin must currently be set as an output.
func (p *Pin) SetValue(n gpio.Level) error {
	return p.gpioLine.SetValue(int(n))
}

// Value returns the current logical level of the GPIO Pin.
func (p *Pin) Value() (gpio.Level, error) {
	l, err := p.gpioLine.Value()
	return gpio.Level(l), err
}

// SetMode configures the line as Input or Output.
func (p *Pin) SetMode(mode gpio.Mode) error {
	switch mode {
	case gpio.Input:
		return p.gpioLine.Reconfigure(gpiod.WithoutEdges, gpiod.AsInput)
	case gpio.Output:
		return p.gpioLine.Reconfigure(gpiod.WithoutEdges, gpiod.AsOutput())
	default:
		return ErrUnknownMode
	}
}

// SetPullMode configures the internal pull resistor of the GPIO Pin.
func (p *Pin) SetPullMode(mode gpio.PullMode) error {
	switch mode {
	case gpio.PullUp:
		return p.gpioLine.Reconfigure(gpiod.WithPullUp)
	case gpio.PullDown:
		return p.gpioLine.Reconfigure(gpiod.WithPullDown)
	case gpio.PullNone:
		return nil
	default:
		return ErrUnknownPullMode
	}
}

// SetDebounce configures hardware debounce for the GPIO Pin.
func (p *Pin) SetDebounce(t time.Duration) error {
	return p.gpioLine.Reconfigure(gpiod.WithDebounce(t))
}

// Number returns the GPIO line offset (BCM number).
func (p *Pin) Number() int {
	return p.gpioLine.Offset()
}

// Info returns diagnostic information about the GPIO Pin.
func (p *Pin) Info() string {
	return fmt.Sprint(p.gpioLine.Info())
}

// WatchingEvents enables edge detection and registers an event handler.
// The handler is executed asynchronously.
func (p *Pin) WatchingEvents(handler func(gpio.Event)) error {
	p.Lock()
	defer p.Unlock()
	p.eventHandler = handler
	return p.gpioLine.Reconfigure(gpiod.WithBothEdges)
}

// StopWatching disables edge detection and removes the registered handler.
func (p *Pin) StopWatching() error {
	p.Lock()
	defer p.Unlock()
	p.eventHandler = nil
	return p.gpioLine.Reconfigure(gpiod.WithoutEdges)
}

// handler forwards gpiod events to the registered event handler.
func (p *Pin) handler(evt gpiod.LineEvent) {

	p.RLock()
	handler := p.eventHandler
	p.RUnlock()

	if handler == nil ||
		(evt.Type != gpiod.LineEventFallingEdge && evt.Type != gpiod.LineEventRisingEdge) {
		return
	}

	go handler(gpio.Event{
		Time: time.Now(),
		Edge: mapEdge(evt.Type),
	})
}

// mapEdge converts a gpiod edge type to gpio.Edge.
func mapEdge(event gpiod.LineEventType) gpio.Edge {
	if event == gpiod.LineEventRisingEdge {
		return gpio.RisingEdge
	}
	return gpio.FallingEdge
}
