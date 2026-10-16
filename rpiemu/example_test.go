package rpiemu_test

import (
	"fmt"
	"log"

	"github.com/womat/gpio"
	"github.com/womat/gpio/rpiemu"
)

// Example_usage watches an input pin and simulates the outside world with
// Drive, the way a test exercises receiving code without hardware.
func Example_usage() {
	pin, err := rpiemu.NewPin(17,
		rpiemu.WithMode(gpio.Input),
		rpiemu.WithPullup(gpio.PullDown),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer pin.Close()

	events, err := pin.WatchCh(gpio.RisingEdge | gpio.FallingEdge)
	if err != nil {
		log.Fatal(err)
	}

	// SetValue refuses an input pin; Drive stands in for the signal source.
	fmt.Println("SetValue:", pin.SetValue(gpio.High))

	for _, level := range []gpio.Level{gpio.High, gpio.Low} {
		if err := pin.Drive(level); err != nil {
			log.Fatal(err)
		}
		evt := <-events
		fmt.Println(evt.Edge, "missed:", evt.Missed)
	}

	// Output:
	// SetValue: gpio: invalid mode
	// Rising missed: 0
	// Falling missed: 0
}
