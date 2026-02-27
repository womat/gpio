package rpi_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/womat/golib/gpio"
	"github.com/womat/golib/gpio/rpi"
)

func ExamplePort_usage() {
	// Configure as output and set high
	out, err := rpi.NewPin(17, rpi.WithMode(gpio.Output), rpi.WithPullup(gpio.PullUp))
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	out.SetValue(gpio.High)

	// Configure as input with pull-up
	in, err := rpi.NewPin(18, rpi.WithMode(gpio.Input), rpi.WithPullup(gpio.PullUp), rpi.WithDebounce(10*time.Millisecond))
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	// Create a context to control watching lifetime
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Watching events
	ch, err := in.WatchCh(ctx, gpio.RisingEdge|gpio.FallingEdge)
	if err != nil {
		log.Fatal(err)
	}

	// Consume events
	go func() {
		for evt := range ch {
			fmt.Println("GPIO Event:", evt.Edge, "at", evt.Time.Format("15:04:05.000"))
		}
	}()

	// optional: cancel() if you want to stop watching immediately
	// cancel()

	// Simulate some waiting for a demonstration
	time.Sleep(100 * time.Millisecond)

	// Output:
	// (No fixed output expected; this is just a usage example)
}
