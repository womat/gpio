// Command gpio-toggle is a CLI tool that periodically toggles a GPIO output pin
// on a Raspberry Pi.
//
// It allows selecting a GPIO pin and defining a toggle interval in milliseconds.
// The program initializes the pin as an output and alternates its level between
// HIGH and LOW at the specified interval, logging each state change with a
// precise timestamp.
//
// The watcher runs until interrupted (e.g., via Ctrl+C).
package main

import (
	"flag"
	"log"
	"time"

	"github.com/womat/golib/gpio"
	"github.com/womat/golib/gpio/rpi"
)

func main() {
	gpioLine := flag.Int("gpioline", 17, "Number of GPIO-Pin")
	interval := flag.Int("interval", 1000, "Interval in milliseconds")
	flag.Parse()

	gpioPin, err := rpi.NewPin(*gpioLine)
	if err != nil {
		log.Fatal(err)
	}
	defer gpioPin.Close()

	if err = gpioPin.SetMode(gpio.Output); err != nil {
		log.Fatal(err)
	}

	log.Printf("GPIO Pin %d: %s", gpioPin.Number(), gpioPin.Info())

	level := gpio.High
	ticker := time.NewTicker(time.Duration(*interval) * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		log.Printf("GPIO-Pin %d: %s\tat %s", gpioPin.Number(), level, time.Now().Format("15:04:05.000000"))
		if err = gpioPin.SetValue(level); err != nil {
			log.Fatal(err)
		}
		level = togglePin(level)
	}
}

func togglePin(l gpio.Level) gpio.Level {
	if l == gpio.High {
		return gpio.Low
	}

	return gpio.High
}
