package rpi

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/womat/gpio"
)

// loopPairs returns the output:input pairs named by RPI_TEST_LOOP and skips
// the test when it is unset: these tests drive real lines, which CI does not
// have. Each output must be wired to its input, directly or through an
// optocoupler; whether the path inverts is measured, not configured. The burst
// and rewatch tests use the first pair only, so list the direct connection
// first - an optocoupler is good for 1 kHz only.
//
//	RPI_TEST_LOOP=18:25              direct connection, 18 as output
//	RPI_TEST_LOOP=18:25,13:6,12:26   plus both optocouplers for TestLoopbackEdges
func loopPairs(t *testing.T) [][2]int {
	t.Helper()
	s := os.Getenv("RPI_TEST_LOOP")
	if s == "" {
		t.Skip("needs wired GPIO pairs: set RPI_TEST_LOOP, e.g. 18:25")
	}
	var pairs [][2]int
	for f := range strings.SplitSeq(s, ",") {
		out, in, ok := strings.Cut(strings.TrimSpace(f), ":")
		o, err1 := strconv.Atoi(out)
		i, err2 := strconv.Atoi(in)
		if !ok || err1 != nil || err2 != nil {
			t.Fatalf("RPI_TEST_LOOP: %q is not output:input", f)
		}
		pairs = append(pairs, [2]int{o, i})
	}
	return pairs
}

// settle is how long a level change takes to reach the input, with room to
// spare for the slower optocoupler.
const settle = 10 * time.Millisecond

type loopback struct {
	out, in  gpio.Pin
	inverted bool // the input reads the opposite of the output
}

// openLoop requests both lines of a pair, leaves the output low, and measures
// whether the path inverts. It fails the test if the input does not follow.
func openLoop(t *testing.T, pair [2]int) *loopback {
	t.Helper()
	out, err := NewPin(pair[0], WithMode(gpio.Output))
	if err != nil {
		t.Fatalf("output %d: %v", pair[0], err)
	}
	t.Cleanup(func() {
		out.SetValue(gpio.Low)
		out.Close()
	})
	in, err := NewPin(pair[1], WithMode(gpio.Input), WithPullup(gpio.PullUp))
	if err != nil {
		t.Fatalf("input %d: %v", pair[1], err)
	}
	t.Cleanup(func() { in.Close() })

	read := func(l gpio.Level) gpio.Level {
		if err := out.SetValue(l); err != nil {
			t.Fatal(err)
		}
		time.Sleep(settle)
		v, err := in.Value()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	atHigh, atLow := read(gpio.High), read(gpio.Low)
	if atHigh == atLow {
		t.Fatalf("input %d does not follow output %d (always %v)", pair[1], pair[0], atLow)
	}
	return &loopback{out: out, in: in, inverted: atLow == gpio.High}
}

// record starts watching both edges on the input and collects the events in
// the background, so the 32-event buffer does not limit how many a test sees.
// The returned function stops watching and returns what was delivered.
func record(t *testing.T, p gpio.Pin) func() []gpio.Event {
	t.Helper()
	ch, err := p.WatchCh(gpio.RisingEdge | gpio.FallingEdge)
	if err != nil {
		t.Fatal(err)
	}
	var events []gpio.Event
	done := make(chan struct{})
	go func() {
		for ev := range ch {
			events = append(events, ev)
		}
		close(done)
	}()
	return func() []gpio.Event {
		time.Sleep(settle) // let the last edges arrive
		if err := p.StopWatching(); err != nil {
			t.Errorf("StopWatching: %v", err)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("event channel not closed by StopWatching")
		}
		return events
	}
}

// toggle drives the output through n level changes, starting with high, and
// returns the time read right before each change. A pause below a millisecond
// is busy-waited from that time on, because time.Sleep is far coarser.
func toggle(t *testing.T, out gpio.Pin, n int, pause time.Duration) []time.Time {
	t.Helper()
	at := make([]time.Time, n)
	level := gpio.High
	for i := range n {
		at[i] = time.Now()
		if err := out.SetValue(level); err != nil {
			t.Fatal(err)
		}
		level = gpio.High - level
		if pause >= time.Millisecond {
			time.Sleep(pause)
		} else {
			for time.Since(at[i]) < pause {
			}
		}
	}
	return at
}

func flip(e gpio.Edge) gpio.Edge { return (gpio.RisingEdge | gpio.FallingEdge) ^ e }

// earlySlack is how far before the SetValue call an event may be placed. The
// clock anchor in eventTime is read as time.Now() first and CLOCK_MONOTONIC
// second, so a pause between the two reads places every event of that second
// early by the length of the pause.
const earlySlack = 200 * time.Microsecond

// TestLoopbackEdges drives each pair slowly enough for every path to follow
// and checks that every edge arrives once, in the right direction, inside the
// window of the SetValue call that caused it, and that intervals between
// events are positive - the hardware side of the regression fixed by the
// shared clock anchor.
func TestLoopbackEdges(t *testing.T) {
	for _, pair := range loopPairs(t) {
		t.Run(fmt.Sprintf("%d-%d", pair[0], pair[1]), func(t *testing.T) {
			lb := openLoop(t, pair)
			const n = 100
			const pause = 2 * time.Millisecond

			stop := record(t, lb.in)
			at := toggle(t, lb.out, n, pause)
			events := stop()

			if len(events) != n {
				t.Fatalf("got %d events for %d edges, dropped %d", len(events), n, lb.in.DroppedEvents())
			}
			want := gpio.RisingEdge
			if lb.inverted {
				want = gpio.FallingEdge
			}
			var minLat, maxLat, maxDev time.Duration
			minLat = time.Hour
			for i, ev := range events {
				if ev.Edge != want {
					t.Errorf("event %d: edge %v, want %v", i, ev.Edge, want)
				}
				want = flip(want)
				if ev.Missed != 0 {
					t.Errorf("event %d: Missed = %d", i, ev.Missed)
				}

				lat := ev.Time.Sub(at[i])
				if lat < -earlySlack || lat > pause {
					t.Errorf("event %d: %v after SetValue, want within [-%v, %v]", i, lat, earlySlack, pause)
				}
				minLat, maxLat = min(minLat, lat), max(maxLat, lat)

				if i == 0 {
					continue
				}
				d := ev.Time.Sub(events[i-1].Time)
				if d <= 0 {
					t.Errorf("event %d: interval %v, want > 0", i, d)
				}
				dev := d - at[i].Sub(at[i-1])
				maxDev = max(maxDev, dev, -dev)
			}
			if got := lb.in.DroppedEvents(); got != 0 {
				t.Errorf("DroppedEvents = %d, want 0", got)
			}
			t.Logf("inverted=%v  latency after SetValue %v..%v  max interval deviation %v",
				lb.inverted, minLat, maxLat, maxDev)
		})
	}
}

// TestLoopbackBurst toggles the first pair as fast as SetValue allows - about
// 400 kHz on a Pi 3B+, beyond the 10-100 kHz at which the old per-event clock
// reads produced negative intervals - and checks what holds even when edges
// are lost: time never runs backwards, and nothing is counted twice.
//
// Exact accounting cannot be asserted at this rate. Above roughly 50 kHz the
// Pi's interrupt handling merges edges before the kernel numbers them: on a
// Pi 3B+ (Linux 6.18) only about 800 of 4000 edges at 400 kHz arrived or were
// counted as lost, and consecutive events had the same direction with no gap
// in the sequence numbers. TestLoopbackPaced covers the accounting.
func TestLoopbackBurst(t *testing.T) {
	lb := openLoop(t, loopPairs(t)[0])
	const n = 20000

	stop := record(t, lb.in)
	start := time.Now()
	toggle(t, lb.out, n, 0)
	elapsed := time.Since(start)
	events := stop()
	dropped := lb.in.DroppedEvents()

	if got := uint64(len(events)) + dropped; got > n {
		t.Errorf("delivered %d + dropped %d = %d, more than the %d edges driven", len(events), dropped, got, n)
	}

	var missed uint64
	var backwards int
	minInterval := time.Hour
	for i, ev := range events {
		missed += ev.Missed
		if i == 0 {
			continue
		}
		d := ev.Time.Sub(events[i-1].Time)
		if d < 0 {
			backwards++
		}
		minInterval = min(minInterval, d)
	}
	if backwards > 0 {
		t.Errorf("%d intervals negative, smallest %v", backwards, minInterval)
	}
	if missed > dropped {
		t.Errorf("sum of Missed %d exceeds DroppedEvents %d", missed, dropped)
	}
	t.Logf("%d edges in %v (%.0f kHz): delivered %d, dropped %d, reported as Missed %d, smallest interval %v",
		n, elapsed, float64(n)/elapsed.Seconds()/1000, len(events), dropped, missed, minInterval)
}

// TestLoopbackPaced toggles the first pair at 10 kHz, where the kernel sees
// every edge (measured on a Pi 3B+: none lost at 10 kHz, now and then one in
// 2000 at 20 kHz), and checks the
// accounting exactly: every edge is either delivered or counted as dropped,
// and each event's Missed agrees with its direction.
func TestLoopbackPaced(t *testing.T) {
	lb := openLoop(t, loopPairs(t)[0])
	const n = 2000

	stop := record(t, lb.in)
	toggle(t, lb.out, n, 100*time.Microsecond)
	events := stop()
	dropped := lb.in.DroppedEvents()

	if got := uint64(len(events)) + dropped; got != n {
		t.Errorf("delivered %d + dropped %d = %d, want %d edges", len(events), dropped, got, n)
	}
	var missed uint64
	for i, ev := range events {
		missed += ev.Missed
		if i == 0 {
			continue
		}
		prev := events[i-1]
		// An even number of lost edges in between leaves the direction
		// alternating; an odd number repeats it.
		if alternates := ev.Edge != prev.Edge; alternates != (ev.Missed%2 == 0) {
			t.Errorf("event %d: edge %v after %v with Missed = %d", i, ev.Edge, prev.Edge, ev.Missed)
		}
		if d := ev.Time.Sub(prev.Time); d <= 0 {
			t.Errorf("event %d: interval %v, want > 0", i, d)
		}
	}
	if missed > dropped {
		t.Errorf("sum of Missed %d exceeds DroppedEvents %d", missed, dropped)
	}
	t.Logf("delivered %d, dropped %d, reported as Missed %d", len(events), dropped, missed)
}

// TestLoopbackRewatch checks that edges while nobody watches are neither
// delivered later nor reported as missed by the next watch.
func TestLoopbackRewatch(t *testing.T) {
	lb := openLoop(t, loopPairs(t)[0])
	const n = 10
	const pause = time.Millisecond

	stop := record(t, lb.in)
	toggle(t, lb.out, n, pause)
	if got := len(stop()); got != n {
		t.Fatalf("first watch: %d events, want %d", got, n)
	}

	toggle(t, lb.out, n, pause) // unwatched
	time.Sleep(settle)

	stop = record(t, lb.in)
	toggle(t, lb.out, n, pause)
	events := stop()
	if len(events) != n {
		t.Fatalf("second watch: %d events, want %d", len(events), n)
	}
	if events[0].Missed != 0 {
		t.Errorf("first event of second watch: Missed = %d, want 0", events[0].Missed)
	}
	if got := lb.in.DroppedEvents(); got != 0 {
		t.Errorf("DroppedEvents = %d, want 0", got)
	}
}

// TestLoopbackCloseEndsWatch checks that closing a pin that is being watched
// closes the event channel, so a consumer ranging over it terminates.
func TestLoopbackCloseEndsWatch(t *testing.T) {
	lb := openLoop(t, loopPairs(t)[0])

	ch, err := lb.in.WatchCh(gpio.RisingEdge | gpio.FallingEdge)
	if err != nil {
		t.Fatal(err)
	}
	toggle(t, lb.out, 4, time.Millisecond)
	time.Sleep(settle)

	if err := lb.in.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	deadline := time.After(time.Second)
	for n := 0; ; n++ {
		select {
		case _, ok := <-ch:
			if !ok {
				if n != 4 {
					t.Errorf("%d events before the channel closed, want 4", n)
				}
				return
			}
		case <-deadline:
			t.Fatal("event channel not closed by Close")
		}
	}
}
