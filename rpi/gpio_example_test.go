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
	gpioPort, err := rpi.NewPin(17)
	if err != nil {
		log.Fatal(err)
	}
	defer gpioPort.Close()

	// Configure as output and set high
	gpioPort.SetMode(gpio.Output)
	gpioPort.SetValue(gpio.High)

	// Configure as input with pull-up
	gpioPort.SetMode(gpio.Input)
	gpioPort.SetPullMode(gpio.PullUp)

	// Create a context to control watching lifetime
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Watching events
	ch, err := gpioPort.Watch(ctx, gpio.RisingEdge|gpio.FallingEdge)
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
