package rpi_test

import (
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

	// Watching events
	gpioPort.WatchingEvents(func(evt gpio.Event) {
		fmt.Println("GPIO Event:", evt.Edge, "at", evt.Time.Format(time.RFC3339Nano))
	})

	// Simulate some waiting for a demonstration
	time.Sleep(100 * time.Millisecond)

	// Output:
	// (No fixed output expected; this is just a usage example)
}
