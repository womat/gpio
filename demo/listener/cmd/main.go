// Command gpio-watch is a CLI tool for monitoring GPIO input events on a Raspberry Pi.
//
// It allows selecting a GPIO pin, configuring rising and/or falling edge detection,
// and optionally setting a debounce time in milliseconds. The program initializes
// the pin as input with an internal pull-up resistor and logs each detected edge
// event with a precise timestamp.
//
// The watcher runs until interrupted (e.g., via Ctrl+C).
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/womat/golib/gpio"
	"github.com/womat/golib/gpio/rpi"
)

func main() {
	gpioLine := flag.Int("gpioline", 17, "Number of GPIO-Pin")

	edgeRising := flag.Bool("rising", false, "Listen to rising edges")
	edgeFalling := flag.Bool("falling", false, "Listen to falling edges")
	debounce := flag.Int("debounce", 0, "Debounce in milliseconds")
	flag.Parse()

	gpioPin, err := rpi.NewPin(*gpioLine)
	if err != nil {
		log.Fatal(err)
	}
	defer gpioPin.Close()

	if err = gpioPin.SetMode(gpio.Input); err != nil {
		log.Fatal(err)
	}
	if err = gpioPin.SetPullMode(gpio.PullUp); err != nil {
		log.Fatal(err)
	}
	if *debounce > 0 {
		if err = gpioPin.SetDebounce(time.Duration(*debounce) * time.Millisecond); err != nil {
			log.Fatal(err)
		}
	}

	var edge gpio.Edge
	switch {
	case *edgeFalling && *edgeRising:
		edge = gpio.RisingEdge | gpio.FallingEdge
	case *edgeFalling:
		edge = gpio.FallingEdge
	case *edgeRising:
		edge = gpio.RisingEdge
	default:
		log.Fatalf("No edge selection: use -rising and/or -falling")
	}

	// Create a context to control watching lifetime
	// --- Kontext für sauberes Stoppen ---
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Watch for rising and falling edges
	events, err := gpioPin.Watch(ctx, edge)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("GPIO Pin %d: %s", gpioPin.Number(), gpioPin.Info())
	log.Printf("Listening on GPIO Pin %d, edge: %v, debounce: %dms", gpioPin.Number(), edge, *debounce)
	//  Consume events
	for evt := range events {
		log.Printf("GPIO Event on pin %d: %s\tat %s", gpioPin.Number(), evt.Edge, evt.Time.Format("15:04:05.000000"))
	}
}
