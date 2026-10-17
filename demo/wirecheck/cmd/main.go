// Command wirecheck checks the wiring of a GPIO test bench: output pins looped
// back to input pins, directly or through an optocoupler, plus input-only
// sensors.
//
// For every pair it drives the output low and high three times and reads the
// input (with pull-up), reporting whether the input follows and whether the
// path inverts. A cross check then raises one output at a time and flags any
// other input that changes with it. Finally it prints the level of each sensor
// and, for -watch, records their edges while you trigger them by hand.
//
// The defaults describe the bench this module is tested on:
//
//	18 → 25   direct, through 1 kΩ
//	13 →  6   EL817 optocoupler (inverting)
//	12 → 26   4N35 optocoupler (inverting)
//	21, 19    reed contact, motion sensor (PIR)
//
// Usage:
//
//	wirecheck [-pairs 18:25,13:6,12:26] [-sensors 21,19] [-watch 30s]
//
// The exit code is 1 if a pair does not follow or two pairs cross-talk.
// Hardware PWM or SPI on one of the pins must be switched off first, otherwise
// the line is busy.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/womat/gpio"
	"github.com/womat/gpio/rpi"
)

type pair struct {
	out, in int
	o, i    gpio.Pin
}

const settle = 50 * time.Millisecond

func main() {
	pairsFlag := flag.String("pairs", "18:25,13:6,12:26", "output:input pairs, comma separated (empty = none)")
	sensorsFlag := flag.String("sensors", "21,19", "input-only pins, comma separated (empty = none)")
	watch := flag.Duration("watch", 30*time.Second, "how long to record sensor edges (0 = not at all)")
	flag.Parse()

	pairs, err := parsePairs(*pairsFlag)
	if err != nil {
		fail(err)
	}
	sensorNums, err := parseInts(*sensorsFlag)
	if err != nil {
		fail(fmt.Errorf("-sensors: %w", err))
	}

	var pins []gpio.Pin
	defer func() {
		for _, p := range pins {
			_ = p.SetValue(gpio.Low) // inputs refuse, that's fine
			_ = p.Close()
		}
	}()
	open := func(n int, opts ...rpi.Option) gpio.Pin {
		p, err := rpi.NewPin(n, opts...)
		if err != nil {
			fail(fmt.Errorf("GPIO %d: %w", n, err))
		}
		pins = append(pins, p)
		return p
	}
	for _, p := range pairs {
		p.o = open(p.out, rpi.WithMode(gpio.Output))
		p.i = open(p.in, rpi.WithMode(gpio.Input), rpi.WithPullup(gpio.PullUp))
	}
	sensors := make([]gpio.Pin, len(sensorNums))
	for k, n := range sensorNums {
		sensors[k] = open(n, rpi.WithMode(gpio.Input), rpi.WithPullup(gpio.PullUp))
	}

	read := func(p gpio.Pin) gpio.Level {
		l, err := p.Value()
		if err != nil {
			fail(fmt.Errorf("reading GPIO %d: %w", p.Number(), err))
		}
		return l
	}
	set := func(p gpio.Pin, l gpio.Level) {
		if err := p.SetValue(l); err != nil {
			fail(fmt.Errorf("setting GPIO %d: %w", p.Number(), err))
		}
	}
	allLow := func() {
		for _, p := range pairs {
			set(p.o, gpio.Low)
		}
		time.Sleep(settle)
	}

	fmt.Println("== Pairs (input with pull-up) ==")
	allLow()
	failed := false
	for _, p := range pairs {
		var atLow, atHigh []gpio.Level
		for range 3 {
			set(p.o, gpio.Low)
			time.Sleep(settle)
			atLow = append(atLow, read(p.i))
			set(p.o, gpio.High)
			time.Sleep(settle)
			atHigh = append(atHigh, read(p.i))
		}
		set(p.o, gpio.Low)
		verdict := judge(atLow, atHigh)
		if !strings.HasPrefix(verdict, "ok") {
			failed = true
		}
		fmt.Printf("%2d → %2d  out low→in %v  out high→in %v  %s\n",
			p.out, p.in, atLow, atHigh, verdict)
	}

	fmt.Println("\n== Cross check (one output high, read all inputs) ==")
	allLow()
	base := map[int]gpio.Level{}
	for _, q := range pairs {
		base[q.in] = read(q.i)
	}
	for _, p := range pairs {
		allLow()
		set(p.o, gpio.High)
		time.Sleep(settle)
		var b strings.Builder
		for _, q := range pairs {
			l := read(q.i)
			changed := l != base[q.in]
			mark := ""
			switch {
			case q == p && !changed:
				mark = " (does not follow!)"
				failed = true
			case q != p && changed:
				mark = " (CROSS-TALK!)"
				failed = true
			}
			fmt.Fprintf(&b, "  in %2d=%v%s", q.in, l, mark)
		}
		fmt.Printf("out %2d high:%s\n", p.out, b.String())
	}
	allLow()

	if len(sensors) > 0 {
		fmt.Println("\n== Sensors ==")
		for _, s := range sensors {
			fmt.Printf("GPIO %2d: %v\n", s.Number(), read(s))
		}
		if *watch > 0 {
			watchSensors(sensors, *watch)
		}
	}

	if failed {
		fmt.Println("\nRESULT: wiring error (see above)")
		os.Exit(1)
	}
	fmt.Println("\nRESULT: pairs ok")
}

// watchSensors prints every edge on the sensors for d. No debounce: a reed
// contact bounces for a few hundred microseconds, and seeing that is the point.
func watchSensors(sensors []gpio.Pin, d time.Duration) {
	fmt.Printf("Recording for %v — trigger the sensors now.\n", d)
	start := time.Now()
	var wg sync.WaitGroup
	for _, s := range sensors {
		ch, err := s.WatchCh(gpio.RisingEdge | gpio.FallingEdge)
		if err != nil {
			fail(fmt.Errorf("GPIO %d WatchCh: %w", s.Number(), err))
		}
		wg.Go(func() {
			n := 0
			for ev := range ch {
				n++
				fmt.Printf("  %6.2fs  GPIO %2d %v\n", ev.Time.Sub(start).Seconds(), s.Number(), ev)
			}
			fmt.Printf("GPIO %2d: %d edges, dropped %d\n", s.Number(), n, s.DroppedEvents())
		})
	}
	time.Sleep(d)
	for _, s := range sensors {
		_ = s.StopWatching()
	}
	wg.Wait()
}

func judge(atLow, atHigh []gpio.Level) string {
	same := func(ls []gpio.Level) (gpio.Level, bool) {
		for _, l := range ls[1:] {
			if l != ls[0] {
				return 0, false
			}
		}
		return ls[0], true
	}
	l, okL := same(atLow)
	h, okH := same(atHigh)
	switch {
	case !okL || !okH:
		return "ERROR: input flickers"
	case l == h:
		return fmt.Sprintf("ERROR: input does not follow (always %v)", l)
	case l == gpio.Low:
		return "ok, not inverted"
	default:
		return "ok, inverted"
	}
}

func parsePairs(s string) ([]*pair, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil // sensors only, no pin is driven
	}
	var pairs []*pair
	for f := range strings.SplitSeq(s, ",") {
		out, in, ok := strings.Cut(strings.TrimSpace(f), ":")
		if !ok {
			return nil, fmt.Errorf("-pairs: %q is not output:input", f)
		}
		o, err := strconv.Atoi(out)
		if err != nil {
			return nil, fmt.Errorf("-pairs: %w", err)
		}
		i, err := strconv.Atoi(in)
		if err != nil {
			return nil, fmt.Errorf("-pairs: %w", err)
		}
		pairs = append(pairs, &pair{out: o, in: i})
	}
	return pairs, nil
}

func parseInts(s string) ([]int, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var ns []int
	for f := range strings.SplitSeq(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return nil, err
		}
		ns = append(ns, n)
	}
	return ns, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
