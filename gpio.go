// Package gpio defines a generic interface for GPIO pin configuration
// and event handling (developed primary for Raspberry Pi).
//
// It provides a standard Pin interface that can be implemented by
// different backends (e.g., hardware drivers, emulators, or mocks).
//
// The package defines signal levels, edge types, pull configurations,
// and a unified event model for GPIO state changes.
//
// Note: This package does not interact with hardware directly.
// It only defines the abstraction layer for GPIO implementations.
//
// # Example Usage
//
//	func main() {
//	    // Create pin
//	    pin, err := gpioemu.NewPin(17)
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    defer pin.Close()
//
//	    // Configure as output
//	    if err := pin.SetMode(gpio.Output); err != nil {
//	        log.Fatal(err)
//	    }
//	    if err := pin.SetValue(gpio.High); err != nil {
//	        log.Fatal(err)
//	    }
//
//	    // Switch to input with pull-up
//	    if err := pin.SetMode(gpio.Input); err != nil {
//	        log.Fatal(err)
//	    }
//	    if err := pin.SetPullMode(gpio.PullUp); err != nil {
//	        log.Fatal(err)
//	    }
//
//	    ctx, cancel := context.WithCancel(context.Background())
//	    defer cancel()
//
//	    events, err := pin.WatchCh(ctx, gpio.RisingEdge|gpio.FallingEdge)
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//
//	    go func() {
//	        for evt := range events {
//	            fmt.Println(evt)
//	        }
//	    }()
//
//	    time.Sleep(5 * time.Second)
//	}
//
// Note: This package does not interact with hardware directly but defines
// the interface for GPIO event handling, allowing multiple implementations.
package gpio

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidLevel = fmt.Errorf("gpio: invalid level")
var ErrInvalidPullMode = errors.New("gpio: invalid pull mode")
var ErrInvalidMode = errors.New("gpio: invalid mode")
var ErrInvalidEdgeConfig = errors.New("gpio: invalid edge configuration")
var ErrAlreadyWatching = errors.New("gpio: already watching")

// Level represents the logical signal level of a GPIO pin.
type Level int

// Edge represents one or more signal transitions detected on a GPIO pin.
// It may contain RisingEdge, FallingEdge, or both.
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
	// Input configures the GPIO pin as input.
	Input Mode = iota
	// Output configures the GPIO pin as output.
	Output
)

// Event represents a detected edge transition on a GPIO pin.
type Event struct {
	Time time.Time // Time when the edge was detected.
	Edge Edge      // Type of edge (RisingEdge or FallingEdge).
}

// Pin defines the interface for GPIO pin operations.
type Pin interface {
	// Close releases the GPIO pin.
	Close() error

	// Value access
	SetValue(level Level) error
	Value() (Level, error)

	// Metadata
	Number() int
	Info() string

	// WatchCh starts monitoring the pin for the specified edge transitions.
	//
	// The returned channel delivers Event values until:
	//   - the provided context is canceled, or
	//   - StopWatching is called, or
	//   - an internal error occurs.
	//
	// Only one active watcher is allowed at a time.
	// If watching is already active, ErrAlreadyWatching is returned.
	WatchCh(edges Edge) (<-chan Event, error)
	WatchFunc(edges Edge, f func(event Event)) error
	// StopWatching stops an active Watch operation.
	// It is safe to call even if no watcher is active.
	StopWatching() error
	// DroppedEvents returns the number of events that were dropped
	// due to internal buffering limits.
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

func (l Level) String() string {
	switch l {
	case High:
		return "High"
	case Low:
		return "Low"
	default:
		return "Unknown"
	}
}

func (e Edge) String() string {
	switch e {
	case 0:
		return "NoEdge"
	}

	var parts []string
	if e&FallingEdge != 0 {
		parts = append(parts, "Falling")
	}
	if e&RisingEdge != 0 {
		parts = append(parts, "Rising")
	}

	if len(parts) == 0 {
		return "Unknown"
	}

	return strings.Join(parts, "|")
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
func (e Event) IsRising() bool { return e.Edge&RisingEdge != 0 }

// IsFalling returns true if the event is a FallingEdge.
func (e Event) IsFalling() bool { return e.Edge&FallingEdge != 0 }
