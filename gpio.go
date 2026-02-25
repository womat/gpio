// Package gpio defines a generic interface for GPIO event handling on Raspberry Pi.
//
// This package provides a standard Pin interface that can be implemented by different backends.
// It defines event types for rising and falling edges, an Event struct with timestamps,
// and a common interface (Pin) interacting with GPIO lines (Pins).
//
// # Example Usage
//
//	 package main
//
//	 import (
//	     "fmt"
//	     "log"
//	     "time"
//
//	     "github.com/womat/golib/gpio"
//	     "github.com/womat/golib/rpi"
//	 )
//
//	 func main() {
//		    // Create a GPIO pin using an emulated backend
//		    gpioDevice, err := gpioemu.NewPort(17)
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
//
//	     // Watch for rising and falling edges
//	     events, err := gpioPin.Watch(gpio.RisingEdge | gpio.FallingEdge)
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
//	     // Stop watching
//	     if err := gpioPin.StopWatching(); err != nil {
//	         log.Fatal(err)
//	     }
//	 }
//
// Note: This package does not interact with hardware directly but defines
// the interface for GPIO event handling, allowing multiple implementations.
package gpio

import (
	"fmt"
	"time"
)

// Level represents the logical signal level of a GPIO Pin.
// Typically, High or Low.
type Level int

// Edge represents a signal transition detected on a GPIO Pin.
// Low to High (RisingEdge) or from High to Low (FallingEdge).
type Edge uint8

// PullMode defines the internal resistor configuration for an input Pin.
type PullMode int

// Mode represents the direction configuration of a GPIO Pin.
type Mode int

const (
	// FallingEdge indicates a transition from High to Low.
	FallingEdge Edge = 1 << iota
	// RisingEdge indicates a transition from Low to High.
	RisingEdge

	// High represents a logical high signal level.
	High Level = 1
	// Low represents a logical low signal level.
	Low Level = 0
)

const (
	// PullNone disables internal pull resistors.
	PullNone PullMode = iota
	// PullUp enables an internal pull-up resistor.
	PullUp
	// PullDown enables an internal pull-down resistor.
	PullDown
)

const (
	// Input configures the GPIO Pin as input.
	Input Mode = iota
	// Output configures the GPIO Pin as output.
	Output
)

// Event represents a state change event (RisingEdge or FallingEdge) on a GPIO Pin.
type Event struct {
	Time time.Time // Time when the edge was detected.
	Edge Edge      // Type of edge (RisingEdge or FallingEdge).
}

// Pin defines the interface for GPIO Pin operations.
type Pin interface {
	// Close releases the GPIO Pin.
	Close() error

	// Value access
	SetValue(level Level) error
	Value() (Level, error)

	// Configuration
	SetMode(mode Mode) error
	SetPullMode(mode PullMode) error
	SetDebounce(d time.Duration) error

	// Metadata
	Number() int
	Info() string

	// Events
	Watch(edges Edge) (<-chan Event, error)
	StopWatching() error
	DroppedEvents() uint64
}

// Stringer implementations

func (m Mode) String() string {
	switch m {
	case Input:
		return "Input"
	case Output:
		return "Output"
	default:
		return "Unknown"
	}
}

func (m Level) String() string {
	switch m {
	case High:
		return "High"
	case Low:
		return "Low"
	default:
		return "Unknown"
	}
}

func (m Edge) String() string {
	switch m {
	case FallingEdge:
		return "Falling"
	case RisingEdge:
		return "Rising"
	default:
		return "Unknown"
	}
}

func (p PullMode) String() string {
	switch p {
	case PullUp:
		return "Up"
	case PullDown:
		return "Down"
	case PullNone:
		return "None"
	default:
		return "Unknown"
	}
}

// String formats the Event for logging/debugging.
func (e Event) String() string {
	return fmt.Sprintf("%s at %s", e.Edge, e.Time.Format(time.RFC3339Nano))
}

// Convenience methods on Event

// IsRising returns true if the event is a RisingEdge.
func (e Event) IsRising() bool { return e.Edge == RisingEdge }

// IsFalling returns true if the event is a FallingEdge.
func (e Event) IsFalling() bool { return e.Edge == FallingEdge }
