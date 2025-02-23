// Package gpio provides an interface for controlling GPIO pins on a Raspberry Pi.
//
// This package allows configuring GPIO pins as inputs or outputs, reading and setting values,
// and handling edge events. It uses the gpiod library to interact with GPIO devices.
//
// Features:
// - Request and release GPIO lines
// - Set input and output modes
// - Read and write GPIO values
// - Enable pull-up and pull-down resistors
// - Watch for rising and falling edge events
// - Debounce support for signal stability
//
// Example Usage:
//
//	p, err := gpio.NewPort(17)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer p.Close()
//
//	p.SetOutputMode()
//	p.SetValue(1)
//	val, _ := p.GetValue()
//	fmt.Println("GPIO state:", val)
//
//	p.StartWatchingEvents(func(evt rpi.Event) {
//	    fmt.Println("Event detected:", evt)
//	})
//
// Note: This package requires a Linux system with the gpiod library installed.
package gpio

import (
	"fmt"
	gpiod "github.com/warthog618/go-gpiocdev"
	"github.com/womat/golib/rpi"
	"sync"
	"time"
)

// Chip represents a single GPIO chip that controls a set of lines.
const Chip = "gpiochip0"

// Port represents a single requested line.
type Port struct {
	sync.RWMutex
	gpioLine     *gpiod.Line           // gpioLine is the GPIO line handler
	eventHandler func(event rpi.Event) // eventHandler is the event handler function.
}

// NewPort requests control of a single line on a chip.
func NewPort(gpio int) (*Port, error) {

	var err error

	p := &Port{RWMutex: sync.RWMutex{}}
	p.gpioLine, err = gpiod.RequestLine(
		Chip,
		gpio,
		gpiod.WithEventHandler(p.handler),
		gpiod.WithoutEdges,
		gpiod.AsInput)

	return p, err
}

// handler sends the event to the eventHandler.
// The eventHandler is called when an event is detected.
// The eventHandler is called with an Event struct that contains the timestamp and the type of event.
// The eventHandler is called in a separate goroutine.
// The eventHandler is set by calling StartWatchingEvents.
// The eventHandler is removed by calling StopWatchingEvents.
func (p *Port) handler(evt gpiod.LineEvent) {

	p.RLock()
	handler := p.eventHandler
	p.RUnlock()

	if handler == nil ||
		(evt.Type != gpiod.LineEventFallingEdge && evt.Type != gpiod.LineEventRisingEdge) {
		return
	}

	go handler(rpi.Event{
		Time: time.Now(),
		Edge: mapEdge(evt.Type),
	})
}

// Close closes the port and releases the resources.
// The port is set to input mode. Sending events is disabled.
func (p *Port) Close() error {
	_ = p.gpioLine.Reconfigure(gpiod.WithoutEdges, gpiod.AsInput)
	return p.gpioLine.Close()
}

// SetInputMode sets the port to input mode.
func (p *Port) SetInputMode() error {
	return p.gpioLine.Reconfigure(gpiod.WithoutEdges, gpiod.AsInput)
}

// SetOutputMode sets the port to output mode.
func (p *Port) SetOutputMode() error {
	return p.gpioLine.Reconfigure(gpiod.WithoutEdges, gpiod.AsOutput())
}

// SetValue sets the value of the port.
// The value can be 0 or 1. 0 is low, 1 is high.
// The port must be in output mode. If the port is in input mode, an error is returned.
func (p *Port) SetValue(n int) error {
	return p.gpioLine.SetValue(n)
}

// GetValue returns the value of the port. The value can be 0 or 1. 0 is low, 1 is high.
// The port must be in input mode. If the port is in output mode, an error is returned.
func (p *Port) GetValue() (int, error) {
	return p.gpioLine.Value()
}

// SetPullUp sets the port to pull up mode. This is used to prevent floating values.
// The port will be pulled up to high.
// This is useful for buttons or switches that are connected to ground.
// The pull-up resistor will pull the port to high when the button is not pressed.
// The button will pull the port to low when pressed.
func (p *Port) SetPullUp() error {
	return p.gpioLine.Reconfigure(gpiod.WithPullUp)
}

// SetPullDown sets the port to pull down mode. This is used to prevent floating values.
// The port will be pulled down to low.
// This is useful for buttons or switches that are connected to VCC.
// The pull-down resistor will pull the port to low when the button is not pressed.
// The button will pull the port to high when pressed.
func (p *Port) SetPullDown() error {
	return p.gpioLine.Reconfigure(gpiod.WithPullDown)
}

// Info returns the information of the port. This is useful for debugging.
// The information includes the chip name, the offset, the direction, the pull mode, and the value.
// The information is returned as a string.
func (p *Port) Info() string {
	return fmt.Sprint(p.gpioLine.Info())
}

// Port returns the offset of the port. The offset is the GPIO number.
func (p *Port) Port() int {
	return p.gpioLine.Offset()
}

// WatchingEvents starts watching the port for events. The handler is called when an event is detected.
// The handler is called with an Event struct that contains the timestamp and the type of event.
// The handler is called in a separate goroutine.
func (p *Port) WatchingEvents(handler func(rpi.Event)) error {
	p.eventHandler = handler
	return p.gpioLine.Reconfigure(gpiod.WithBothEdges)
}

// StopWatching stops watching the port for events.
// The handler is removed and no more events are detected.
func (p *Port) StopWatching() error {
	p.eventHandler = nil
	return p.gpioLine.Reconfigure(gpiod.WithoutEdges)
}

// SetDebounceTime sets the debounced time of the port. This is used to prevent bouncing values.
// The debounced time is the time that the port is disabled after an event is detected.
// The debounced time is useful for buttons or switches that are connected to ground or VCC.
// The debounced time is used to prevent multiple events when the button is pressed or released.
func (p *Port) SetDebounceTime(t time.Duration) error {
	return p.gpioLine.Reconfigure(gpiod.WithDebounce(t))
}

// mapEdge maps the gpiod.LineEventType to the rpi.LineEventType.
func mapEdge(event gpiod.LineEventType) int {
	if event == gpiod.LineEventRisingEdge {
		return rpi.RisingEdge
	}
	return rpi.FallingEdge
}
