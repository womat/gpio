package rpi

import (
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// tolerance absorbs scheduling between the clock reads in the test and in eventTime.
const tolerance = 5 * time.Millisecond

// monoNow returns the current CLOCK_MONOTONIC reading, the clock the kernel stamps edges with.
func monoNow(t *testing.T) time.Duration {
	t.Helper()
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		t.Fatalf("clock_gettime: %v", err)
	}
	return time.Duration(ts.Nano())
}

func near(t *testing.T, name string, got, want time.Duration) {
	t.Helper()
	if d := got - want; d < -tolerance || d > tolerance {
		t.Errorf("%s = %v, want %v ± %v", name, got, want, tolerance)
	}
}

func TestEventTimeSubtractsAge(t *testing.T) {
	const age = 50 * time.Millisecond

	got := eventTime(monoNow(t) - age)

	near(t, "time.Since(eventTime)", time.Since(got), age)
}

func TestEventTimeFallsBackToNow(t *testing.T) {
	for name, ts := range map[string]time.Duration{
		"zero":     0,
		"negative": -time.Second,
		"future":   monoNow(t) + time.Hour,
	} {
		t.Run(name, func(t *testing.T) {
			got := eventTime(ts)
			if got.After(time.Now()) {
				t.Errorf("eventTime(%v) = %v lies in the future", ts, got)
			}
			near(t, "time.Since(eventTime)", time.Since(got), 0)
		})
	}
}

func TestEventTimePreservesIntervals(t *testing.T) {
	const interval = 330 * time.Millisecond // one 1 Wh pulse at 11 kW

	now := monoNow(t)
	first := eventTime(now - time.Second)
	second := eventTime(now - time.Second + interval)

	near(t, "interval", second.Sub(first), interval)
}

// TestEventTimeIntervalIsExact is the regression test for intervals that were
// off by the time between two clock reads in eventTime: a handler preempted
// between them shifted an event by up to ~50 µs on a Pi 400 at 10–100 kHz,
// enough for negative intervals between consecutive edges.
func TestEventTimeIntervalIsExact(t *testing.T) {
	now := monoNow(t)
	ts := []time.Duration{now - 3*time.Millisecond, now - 2*time.Millisecond, now - 1*time.Millisecond}

	for i := range 1000 {
		first := eventTime(ts[0])
		second := eventTime(ts[1])
		third := eventTime(ts[2])
		if d := second.Sub(first); d != time.Millisecond {
			t.Fatalf("round %d: interval = %v, want exactly 1ms", i, d)
		}
		if d := third.Sub(first); d != 2*time.Millisecond {
			t.Fatalf("round %d: interval = %v, want exactly 2ms", i, d)
		}
	}
}

// TestEventTimeFollowsWallClockStep covers a service started before NTP set
// the clock, common on boards without RTC: the wall time of later events must
// follow the step instead of staying at the anchor's old offset.
func TestEventTimeFollowsWallClockStep(t *testing.T) {
	t.Cleanup(func() { anchor.Store(nil) })

	// An anchor taken two seconds ago, before the wall clock stepped one hour
	// forward. Round(0) drops the monotonic reading so only the wall time is off.
	mono := monoNow(t)
	anchor.Store(&clockAnchor{
		now:  time.Now().Round(0).Add(-2*time.Second - time.Hour),
		mono: mono - 2*time.Second,
	})

	got := eventTime(mono - time.Millisecond)

	near(t, "wall time behind now", time.Now().Round(0).Sub(got.Round(0)), time.Millisecond)
}

func TestEventTimeKeepsMonotonicReading(t *testing.T) {
	got := eventTime(monoNow(t) - time.Millisecond)

	// Round(0) strips the monotonic reading; if there was one, the result differs in String().
	if got.String() == got.Round(0).String() {
		t.Errorf("eventTime result %v carries no monotonic clock reading", got)
	}
}
