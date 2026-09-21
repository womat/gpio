// Command blinker periodically toggles a GPIO output pin on a Raspberry Pi.
//
// Flags select the GPIO line and the toggle interval in milliseconds. The pin
// is configured as an output and its level alternates between HIGH and LOW at
// that interval, each change logged with a precise timestamp.
//
// It runs until interrupted (e.g., via Ctrl+C).
package main

import (
	"flag"
	"log"
	"time"

	"github.com/womat/golib/gpio"
	"github.com/womat/golib/gpio/rpi"
)

func main() {
	gpioLine := flag.Int("gpioline", 21, "Number of GPIO-Pin")
	interval := flag.Int("interval", 1000, "Interval in milliseconds")
	flag.Parse()

	gpioPin, err := rpi.NewPin(*gpioLine, rpi.WithMode(gpio.Output))
	if err != nil {
		log.Fatal(err)
	}
	defer gpioPin.Close()

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
