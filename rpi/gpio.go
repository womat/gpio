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
//  func main() {
//      // Create a GPIO pin
//      gpioPin, err := rpi.NewPin(17)
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
//
//		// Create a context to control watching lifetime
//		ctx, cancel := context.WithCancel(context.Background())
//		defer cancel()
//
//		// Watch for rising and falling edges
//      events, err := gpioPin.Watch(ctx.gpio.RisingEdge | gpio.FallingEdge)
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

package rpi

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	gpiod "github.com/warthog618/go-gpiocdev"
	"github.com/womat/golib/gpio"
)

// Compile-time check
var _ gpio.Pin = (*pin)(nil)

// Chip defines the default GPIO chip device used to request Pins.
const Chip = "gpiochip0"

// Pin represents a single requested GPIO Pin (Line).
//
// A Pin provides access to configuring the line direction,
// reading and writing logical levels, configuring pull resistors,
// and receiving edge events.
//
// Internally, it wraps a gpiod.Line object from the Linux GPIO character device API.
type pin struct {
	sync.Mutex
	gpioLine  *gpiod.Line        // underlying gpiod line
	events    chan gpio.Event    // // channel to deliver GPIO events
	dropCount atomic.Uint64      // atomic counter for dropped events
	watching  atomic.Bool        // atomic flag: true if Watch() is active
	cancel    context.CancelFunc // cancel is used to stop the event watching goroutine
}

// NewPin requests a GPIO line and returns a pin.
func NewPin(n int) (gpio.Pin, error) {

	p := &pin{
		Mutex:    sync.Mutex{},
		watching: atomic.Bool{},
	}

	line, err := gpiod.RequestLine(
		Chip,
		n,
		gpiod.WithEventHandler(p.handler),
		gpiod.WithoutEdges,
		gpiod.AsInput)

	p.watching.Store(false)
	p.gpioLine = line
	return p, err
}

// Close releases the underlying Line resource and disables event detection.
func (p *pin) Close() error {
	var errs []error

	if err := p.StopWatching(); err != nil {
		errs = append(errs, err)
	}

	if err := p.gpioLine.Reconfigure(gpiod.WithoutEdges, gpiod.AsInput); err != nil {
		errs = append(errs, err)
	}

	if err := p.gpioLine.Close(); err != nil {
		errs = append(errs, err)
	}

	if len(errs) == 0 {
		return nil
	}

	return errors.Join(errs...)
}

// SetValue sets the logical output level of the GPIO Pin.
// The Pin must currently be set as an output.
func (p *pin) SetValue(n gpio.Level) error {
	return p.gpioLine.SetValue(int(n))
}

// Value returns the current logical level of the GPIO Pin.
func (p *pin) Value() (gpio.Level, error) {
	l, err := p.gpioLine.Value()
	return gpio.Level(l), err
}

// SetMode configures the line as Input or Output.
func (p *pin) SetMode(mode gpio.Mode) error {
	switch mode {
	case gpio.Input:
		return p.gpioLine.Reconfigure(gpiod.WithoutEdges, gpiod.AsInput)
	case gpio.Output:
		return p.gpioLine.Reconfigure(gpiod.WithoutEdges, gpiod.AsOutput())
	default:
		return gpio.ErrInvalidMode
	}
}

// SetPullMode configures the internal pull resistor of the GPIO Pin.
func (p *pin) SetPullMode(mode gpio.PullMode) error {
	switch mode {
	case gpio.PullUp:
		return p.gpioLine.Reconfigure(gpiod.WithPullUp)
	case gpio.PullDown:
		return p.gpioLine.Reconfigure(gpiod.WithPullDown)
	case gpio.PullNone:
		return nil
	default:
		return gpio.ErrInvalidPullMode
	}
}

// SetDebounce configures hardware debounce for the GPIO Pin.
func (p *pin) SetDebounce(t time.Duration) error {
	return p.gpioLine.Reconfigure(gpiod.WithDebounce(t))
}

// Number returns the GPIO line offset (BCM number).
func (p *pin) Number() int {
	return p.gpioLine.Offset()
}

// Info returns diagnostic information about the GPIO Pin.
func (p *pin) Info() string {
	return fmt.Sprint(p.gpioLine.Info())
}

// Watch enables edge detection and returns a channel for events.
// The edges parameter can be a combination of gpio.RisingEdge and gpio.FallingEdge.
func (p *pin) Watch(ctx context.Context, edges gpio.Edge) (<-chan gpio.Event, error) {
	if !p.watching.CompareAndSwap(false, true) {
		return nil, gpio.ErrAlreadyWatching
	}

	p.Lock()
	defer p.Unlock()

	p.dropCount.Store(0)
	gpiodEdge := gpiod.WithoutEdges

	switch {
	case edges == (gpio.RisingEdge | gpio.FallingEdge):
		gpiodEdge = gpiod.WithBothEdges
	case edges == gpio.RisingEdge:
		gpiodEdge = gpiod.WithRisingEdge
	case edges == gpio.FallingEdge:
		gpiodEdge = gpiod.WithFallingEdge
	default:
		p.watching.Store(false)
		return nil, gpio.ErrInvalidEdgeConfig
	}

	if err := p.gpioLine.Reconfigure(gpiodEdge); err != nil {
		p.watching.Store(false)
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	ch := make(chan gpio.Event, 8)
	p.events = ch

	go p.waitForContext(ctx)
	return ch, nil
}

// DroppedEvents returns how many events were dropped due to a full buffer.
func (p *pin) DroppedEvents() uint64 {
	return p.dropCount.Load()
}

// StopWatching disables edge detection and stops event delivery.
func (p *pin) StopWatching() error {
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

// Handler is called by gpiod on edge events.
// Very hot path: lock-free, only atomic operations.
func (p *pin) handler(evt gpiod.LineEvent) {
	ch := p.events // atomic load of the channel reference, no lock needed

	if ch == nil || (evt.Type != gpiod.LineEventRisingEdge && evt.Type != gpiod.LineEventFallingEdge) {
		return
	}

	event := gpio.Event{
		Time: time.Now(),
		Edge: mapEdge(evt.Type),
	}

	select {
	case ch <- event:
		// successfully delivered
	default:
		// channel full → drop event
		p.dropCount.Add(1)
	}
}

// waitForContext waits for the context to be done and then stops watching for events.
func (p *pin) waitForContext(ctx context.Context) {
	<-ctx.Done()
	_ = p.stopWatchingInternal()
}

// mapEdge converts a gpiod edge type to gpio.Edge.
func mapEdge(event gpiod.LineEventType) gpio.Edge {
	if event == gpiod.LineEventRisingEdge {
		return gpio.RisingEdge
	}
	return gpio.FallingEdge
}

// stopWatchingInternal is called to clean up resources when stopping watching for events.
func (p *pin) stopWatchingInternal() error {

	if !p.watching.Load() {
		return nil
	}

	p.Lock()
	defer p.Unlock()

	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}

	ch := p.events
	p.events = nil
	p.watching.Store(false)

	if ch != nil {
		close(ch)
	}

	return p.gpioLine.Reconfigure(gpiod.WithoutEdges)
}
