// Package rpi provides a Linux implementation of the gpio.Pin interface
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
// A Pin is safe for concurrent use.
// Event delivery occurs asynchronously via the internal gpiod event handler.
//
// # Lifecycle
//
// A Pin must be closed after use by calling Close(). Closing the Pin
// releases the underlying Line and disables event delivery.
// Close is idempotent and may be called multiple times.
//
// # Example usage
//
//	func main() {
//	    gpioPin, err := rpi.NewPin(17)
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    defer gpioPin.Close()
//
//	    events, err := gpioPin.WatchCh(gpio.RisingEdge|gpio.FallingEdge)
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//
//	    for evt := range events {
//	        fmt.Println("GPIO Event:", evt.Edge, "at", evt.Time.Format("15:04:05.000"))
//	    }
//	}
package rpi

import (
	"errors"
	"fmt"
	"time"

	gpiod "github.com/warthog618/go-gpiocdev"
	"github.com/womat/golib/gpio"
	"github.com/womat/golib/gpio/internal/watch"
)

// Compile-time check
var _ gpio.Pin = (*pin)(nil)

type Option func(*pin, *[]gpiod.LineReqOption)

// defaultBufferSize defines the size of the buffered channel for GPIO events.
const defaultBufferSize = 32

// Chip defines the default GPIO chip device used to request Pins.
const Chip = "gpiochip0"

// pin represents a single GPIO pin.
//
// It allows reading/writing the pin, configuring mode/pull/debounce,
// and watching for edge events via a channel or callback.
type pin struct {
	gpioLine *gpiod.Line   // underlying gpiod line
	watcher  watch.Watcher // event delivery, shared with the emulator backend
}

// NewPin requests a GPIO line from the default chip and returns a gpio.Pin.
//
// The line is initially configured as input with edge detection disabled.
// On failure it returns a nil Pin, so the error must be checked before the
// result is used or closed.
func NewPin(n int, opts ...Option) (gpio.Pin, error) {

	p := &pin{}

	gpioOpts := []gpiod.LineReqOption{
		gpiod.WithEventHandler(p.handler), // install the internal edge handler
		gpiod.WithoutEdges,                // start without edge detection
	}

	for _, opt := range opts {
		opt(p, &gpioOpts)
	}

	line, err := gpiod.RequestLine(Chip, n, gpioOpts...)
	if err != nil {
		return nil, err
	}
	p.gpioLine = line

	return p, nil
}

func WithMode(m gpio.Mode) Option {
	return func(p *pin, opts *[]gpiod.LineReqOption) {
		switch m {
		case gpio.Input:
			*opts = append(*opts, gpiod.AsInput)
		case gpio.Output:
			*opts = append(*opts, gpiod.AsOutput())
		}
	}
}

func WithPullup(pull gpio.PullMode) Option {
	return func(p *pin, opts *[]gpiod.LineReqOption) {
		switch pull {
		case gpio.PullUp:
			*opts = append(*opts, gpiod.WithPullUp)
		case gpio.PullDown:
			*opts = append(*opts, gpiod.WithPullDown)
		}
	}
}

// WithDebounce configures hardware debounce for the GPIO Pin during line request.
func WithDebounce(d time.Duration) Option {
	return func(p *pin, opts *[]gpiod.LineReqOption) {
		if d > 0 {
			*opts = append(*opts, gpiod.WithDebounce(d))
		}
	}
}

// Close stops any active watcher, disables edge detection, and releases the line.
// Close is idempotent and can be called multiple times safely.
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

// Number returns the GPIO line offset (BCM number).
func (p *pin) Number() int {
	return p.gpioLine.Offset()
}

// Info returns diagnostic information about the GPIO Pin.
func (p *pin) Info() string {
	info, err := p.gpioLine.Info()
	if err != nil {
		return fmt.Sprintf("Error retrieving line info: %v", err)
	}
	s := fmt.Sprintf("Offset: %v, Name: %q, Consumer: %q, Used: %v, Config: (ActiveLow: %v, Direction: %v, Drive: %v, Bias: %v, EdgeDetection: %v, Debounced: %v, DebouncePeriod: %v, EventClock: %v)",
		info.Offset, info.Name, info.Consumer, info.Used,
		info.Config.ActiveLow, info.Config.Direction, info.Config.Drive,
		info.Config.Bias, info.Config.EdgeDetection, info.Config.Debounced,
		info.Config.DebouncePeriod, info.Config.EventClock)

	return s
}

// WatchCh starts monitoring the GPIO pin for edges and returns a read-only event channel.
//
// Only one watcher is allowed at a time. If a watcher is already active,
// gpio.ErrAlreadyWatching is returned.
//
// The returned channel is closed when:
//   - StopWatching is called
//   - the pin is closed
//
// edges can be a combination of gpio.RisingEdge and gpio.FallingEdge.
// Use the returned channel to consume events - unread events may be dropped
// if the internal buffer (size 32) is full.
func (p *pin) WatchCh(edges gpio.Edge) (<-chan gpio.Event, error) {
	return p.startWatch(edges)
}

// WatchFunc starts monitoring the GPIO pin for edges and calls f for each one.
//
// Only one watcher is allowed at a time. If a watcher is already active,
// gpio.ErrAlreadyWatching is returned.
//
// Events are delivered by a single goroutine, so f is never called
// concurrently with itself and sees the edges in the order they occurred.
// A callback that blocks never stalls the kernel event handler; it stalls
// delivery instead, and events are eventually dropped, which DroppedEvents
// reports.
//
// The goroutine ends when StopWatching is called or the pin is closed.
//
// edges can be a combination of gpio.RisingEdge and gpio.FallingEdge.
func (p *pin) WatchFunc(edges gpio.Edge, f func(event gpio.Event)) error {
	if f == nil {
		return errors.New("rpi: callback must not be nil")
	}

	ch, err := p.startWatch(edges)
	if err != nil {
		return err
	}

	go func() {
		for event := range ch {
			f(event)
		}
	}()

	return nil
}

// startWatch is the shared implementation for WatchCh and WatchFunc.
//
// It claims the watcher first and enables edge detection afterwards, so the
// first event the kernel reports already has somewhere to go.
func (p *pin) startWatch(edges gpio.Edge) (<-chan gpio.Event, error) {
	gpiodEdge := gpiod.WithoutEdges
	switch {
	case edges == (gpio.RisingEdge | gpio.FallingEdge):
		gpiodEdge = gpiod.WithBothEdges
	case edges == gpio.RisingEdge:
		gpiodEdge = gpiod.WithRisingEdge
	case edges == gpio.FallingEdge:
		gpiodEdge = gpiod.WithFallingEdge
	default:
		return nil, gpio.ErrInvalidEdgeConfig
	}

	ch, err := p.watcher.Start(edges, defaultBufferSize)
	if err != nil {
		return nil, err
	}

	// Enable edge detection only once the watcher is ready to receive.
	if err := p.gpioLine.Reconfigure(gpiodEdge); err != nil {
		p.watcher.Stop()
		return nil, err
	}

	return ch, nil
}

// DroppedEvents returns how many events were dropped due to a full buffer.
func (p *pin) DroppedEvents() uint64 {
	return p.watcher.Dropped()
}

// StopWatching stops the active watcher and disables edge detection.
// Safe to call even if no watcher is active.
func (p *pin) StopWatching() error {
	return p.stopWatchingInternal()
}

// handler is called by gpiod when an edge occurs.
//
// It is a hot path and must remain lock-free to avoid blocking the kernel event handler.
// It delivers the event to the channel or callback, counting dropped events.
func (p *pin) handler(evt gpiod.LineEvent) {
	if evt.Type != gpiod.LineEventRisingEdge && evt.Type != gpiod.LineEventFallingEdge {
		return
	}

	p.watcher.Deliver(gpio.Event{
		Time: time.Now(),
		Edge: mapEdge(evt.Type),
	})
}

// mapEdge converts a gpiod.LineEventType to gpio.Edge.
func mapEdge(event gpiod.LineEventType) gpio.Edge {
	if event == gpiod.LineEventRisingEdge {
		return gpio.RisingEdge
	}
	return gpio.FallingEdge
}

// stopWatchingInternal cleans up watcher resources and disables edge detection.
func (p *pin) stopWatchingInternal() error {
	if !p.watcher.Active() {
		return nil // nothing to stop, and no reason to touch the line
	}

	// Disable edge detection before releasing the watcher, so the kernel stops
	// feeding events that nobody will read any more.
	err := p.gpioLine.Reconfigure(gpiod.WithoutEdges)
	p.watcher.Stop()

	return err
}
