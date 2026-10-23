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
// # Event time
//
// gpio.Event.Time is the moment the kernel detected the edge, not the moment
// the event reached Go, so delivery latency does not distort the interval
// between two events: it is the difference of the kernel's timestamps, to the
// nanosecond except for a few hundred nanoseconds once a second, when the
// reference between both clocks is renewed. The time carries Go's monotonic
// clock reading, which keeps such intervals correct across wall-clock steps
// (NTP, boards without RTC); the wall time follows such a step within a
// second. With debounce, the kernel stamps the edge when its debounce timer
// runs. The Pi has no debounce in hardware, and the kernel's timer counts
// whole ticks (4 ms at CONFIG_HZ=250): the period is rounded up to a tick.
// Every period up to 4 ms gives the same delay; above that, the delay rises
// in 4 ms steps. Shortest delay after the edge, measured on Pi 3B+, Linux
// 6.18.50+rpt-rpi-v8, CONFIG_HZ=250: 4 ms for 100 µs, 1 ms and 4 ms asked
// for, 8 ms for 5 ms, 12 ms for 10 ms, 20 ms for 20 ms.
//
// gpio.Event.Missed also counts edges the kernel lost because its own event
// buffer overflowed, detected through the per-line sequence number the kernel
// assigns (uAPI v2). On a kernel without it, only losses on the Go side are
// reported.
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
	"sync/atomic"
	"time"

	gpiod "github.com/warthog618/go-gpiocdev"
	"github.com/womat/gpio"
	"github.com/womat/gpio/internal/watch"
	"golang.org/x/sys/unix"
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
	closed   atomic.Bool   // set by the first Close; later calls return nil

	// output is whether the line was requested as an output. Set by the
	// options in NewPin and read-only afterwards, so it needs no lock.
	output bool

	// lastSeqno is the kernel's sequence number of the previous edge, 0 before
	// the first one of a watch. Written by the handler and reset by startWatch,
	// which can overlap with a late event of the previous watch - hence atomic.
	lastSeqno atomic.Uint32
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
			p.output = false
		case gpio.Output:
			*opts = append(*opts, gpiod.AsOutput())
			p.output = true
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

// WithDebounce configures debounce for the GPIO Pin during line request. On a
// Pi the kernel does it in whole ticks; see Event time in the package doc.
func WithDebounce(d time.Duration) Option {
	return func(p *pin, opts *[]gpiod.LineReqOption) {
		if d > 0 {
			*opts = append(*opts, gpiod.WithDebounce(d))
			p.output = false // go-gpiocdev's debounce option requests an input
		}
	}
}

// Close stops any active watcher, disables edge detection, and releases the line.
// Close is idempotent and can be called multiple times safely. A watch
// started afterwards fails with gpio.ErrClosed.
func (p *pin) Close() error {
	// go-gpiocdev reports "already closed" for a released line, so only the
	// first call may touch it.
	if p.closed.Swap(true) {
		return nil
	}

	// Closing the watcher rather than stopping it also covers a WatchCh
	// running concurrently: its channel is either closed here or never
	// handed out. Edge detection is switched off right below.
	p.watcher.Close()

	var errs []error

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
// gpio.ErrAlreadyWatching is returned. An output pin cannot be watched -
// edge detection would switch the line to an input - so that returns
// gpio.ErrInvalidMode.
//
// The returned channel is closed when:
//   - StopWatching is called
//   - the pin is closed
//
// edges can be a combination of gpio.RisingEdge and gpio.FallingEdge.
// Use the returned channel to consume events - unread events may be dropped
// if the internal buffer (size 32) is full.
func (p *pin) WatchCh(edges gpio.Edge) (<-chan gpio.Event, error) {
	var ch <-chan gpio.Event
	err := p.startWatch(edges, func() (err error) {
		ch, err = p.watcher.Start(edges, defaultBufferSize)
		return err
	})
	if err != nil {
		return nil, err
	}
	return ch, nil
}

// WatchFunc starts monitoring the GPIO pin for edges and calls f for each one.
//
// Only one watcher is allowed at a time. If a watcher is already active,
// gpio.ErrAlreadyWatching is returned; on an output pin, gpio.ErrInvalidMode.
//
// Events are delivered by a single goroutine, so f is never called
// concurrently with itself and sees the edges in the order they occurred.
// A callback that blocks never stalls the kernel event handler; it stalls
// delivery instead, and events are eventually dropped, which DroppedEvents
// reports.
//
// The goroutine ends when StopWatching is called or the pin is closed.
// Events still buffered then are not passed to f; a call already under way
// can still be running when StopWatching or Close returns.
//
// edges can be a combination of gpio.RisingEdge and gpio.FallingEdge.
func (p *pin) WatchFunc(edges gpio.Edge, f func(event gpio.Event)) error {
	if f == nil {
		return gpio.ErrNilCallback
	}

	return p.startWatch(edges, func() error {
		return p.watcher.StartFunc(edges, defaultBufferSize, f)
	})
}

// startWatch is the shared implementation for WatchCh and WatchFunc; claim
// starts the watcher in the way the caller needs.
//
// It claims the watcher first and enables edge detection afterwards, so the
// first event the kernel reports already has somewhere to go.
func (p *pin) startWatch(edges gpio.Edge, claim func() error) error {
	if p.output {
		return gpio.ErrInvalidMode
	}

	// The zero value of gpiod.LineEdge is LineEdgeNone, which is what
	// gpiod.WithoutEdges is - and the default branch returns, so every path
	// that reaches the call below has set this explicitly.
	var gpiodEdge gpiod.LineEdge
	switch edges {
	case gpio.RisingEdge | gpio.FallingEdge:
		gpiodEdge = gpiod.WithBothEdges
	case gpio.RisingEdge:
		gpiodEdge = gpiod.WithRisingEdge
	case gpio.FallingEdge:
		gpiodEdge = gpiod.WithFallingEdge
	default:
		return gpio.ErrInvalidEdgeConfig
	}

	if err := claim(); err != nil {
		return err
	}

	// The first edge of this watch has no predecessor to measure a gap against.
	p.lastSeqno.Store(0)

	// Enable edge detection only once the watcher is ready to receive.
	if err := p.gpioLine.Reconfigure(gpiodEdge); err != nil {
		p.watcher.Stop()
		return err
	}

	return nil
}

// DroppedEvents returns how many events were lost, to a full buffer here or
// to an overflow in the kernel.
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
		Time:   eventTime(evt.Timestamp),
		Edge:   mapEdge(evt.Type),
		Missed: seqGap(p.lastSeqno.Swap(evt.LineSeqno), evt.LineSeqno),
	})
}

// seqGap returns how many edges the kernel lost between two events of the
// same line, given their sequence numbers.
//
// The kernel numbers the edges of a line consecutively, so any step larger
// than one means the edges in between were lost - typically because its
// event buffer overflowed. A zero on either side means there is nothing to
// compare: last is 0 before the first edge of a watch, and cur is 0 on a
// kernel without sequence numbers (uAPI v1). The subtraction is done in
// uint32, so a wrap-around of the counter is not mistaken for a gap.
func seqGap(last, cur uint32) uint64 {
	if last == 0 || cur == 0 {
		return 0
	}
	return uint64(cur - last - 1)
}

// clockAnchor pairs a time.Now() reading with the CLOCK_MONOTONIC reading
// taken right after it.
type clockAnchor struct {
	now  time.Time
	mono time.Duration
}

// anchor is shared by all pins; it is replaced, never modified.
var anchor atomic.Pointer[clockAnchor]

// anchorAge is how long an anchor is used before it is read again, so the
// wall time of events follows a clock step (NTP after boot) within a second.
const anchorAge = time.Second

// eventTime converts the kernel's edge timestamp into a time.Time.
//
// The kernel stamps the edge with CLOCK_MONOTONIC (Linux 5.7 and later, the
// default event clock, which this package never changes); the handler runs
// later. The timestamp is placed relative to an anchor, a pair of time.Now()
// and CLOCK_MONOTONIC read together and renewed after anchorAge, so the
// interval between two events is exactly the kernel's unless an anchor
// renewal falls between them, and the result keeps Go's monotonic reading.
// Reading both clocks per event instead put the time between the two reads
// into every event — up to ~50 µs when the handler was preempted there.
//
// If the timestamp is missing, the clock cannot be read, or the timestamp
// lies in the future (as a CLOCK_REALTIME stamp from an older kernel would),
// the result is time.Now().
func eventTime(ts time.Duration) time.Time {
	if ts <= 0 {
		return time.Now()
	}

	a := anchor.Load()
	if a == nil || ts-a.mono > anchorAge {
		a = newAnchor()
		if a == nil {
			return time.Now()
		}
		anchor.Store(a)
	}

	// time.Now() is read before the monotonic clock, so an anchor can only
	// place events early, never in the future: a later result is not a
	// CLOCK_MONOTONIC stamp.
	t := a.now.Add(ts - a.mono)
	if now := time.Now(); t.After(now) {
		return now
	}
	return t
}

// newAnchor reads both clocks, or returns nil if CLOCK_MONOTONIC cannot be read.
func newAnchor() *clockAnchor {
	now := time.Now()
	var mono unix.Timespec
	if unix.ClockGettime(unix.CLOCK_MONOTONIC, &mono) != nil {
		return nil
	}
	return &clockAnchor{now: now, mono: time.Duration(mono.Nano())}
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
