package rpi_test

import (
	"fmt"
	"log"
	"time"

	"github.com/womat/golib/gpio"
	"github.com/womat/golib/gpio/rpi"
)

// Example_usage shows how to drive one line and watch another.
//
// It deliberately carries no "Output:" comment: the example needs a real GPIO
// chip, so it is compiled as documentation but never executed by go test.
func Example_usage() {
	// Configure as output and set high
	out, err := rpi.NewPin(17, rpi.WithMode(gpio.Output), rpi.WithPullup(gpio.PullUp))
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	if err := out.SetValue(gpio.High); err != nil {
		log.Fatal(err)
	}

	// Configure as input with pull-up
	in, err := rpi.NewPin(18, rpi.WithMode(gpio.Input), rpi.WithPullup(gpio.PullUp), rpi.WithDebounce(10*time.Millisecond))
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	// Watch for edges. The channel is closed by StopWatching or Close.
	ch, err := in.WatchCh(gpio.RisingEdge | gpio.FallingEdge)
	if err != nil {
		log.Fatal(err)
	}

	// Consume events
	go func() {
		for evt := range ch {
			fmt.Println("GPIO Event:", evt.Edge, "at", evt.Time.Format("15:04:05.000"))
		}
	}()

	// Simulate some waiting for a demonstration
	time.Sleep(100 * time.Millisecond)
}
