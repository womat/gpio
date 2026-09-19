package rpiemu

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/womat/golib/gpio"
)

// collector gathers the edges delivered on a watch channel. The events arrive
// on a goroutine of their own, so every access goes through the mutex.
type collector struct {
	mu    sync.Mutex
	edges []gpio.Edge
}

// collect starts draining ch until it is closed.
func collect(ch <-chan gpio.Event) *collector {
	c := &collector{}

	go func() {
		for evt := range ch {
			c.mu.Lock()
			c.edges = append(c.edges, evt.Edge)
			c.mu.Unlock()
		}
	}()

	return c
}

// snapshot returns the edges collected so far.
func (c *collector) snapshot() []gpio.Edge {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]gpio.Edge(nil), c.edges...)
}

// assertEdges compares the collected edges against want.
func assertEdges(t *testing.T, c *collector, want ...gpio.Edge) {
	t.Helper()

	got := c.snapshot()
	if len(got) != len(want) {
		t.Fatalf("collected %d events (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d is %v, want %v (sequence: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSetValueAndValue(t *testing.T) {
	p, _ := NewPin(17, WithMode(gpio.Output),
		WithPullup(gpio.PullUp))

	// Standardwert sollte Low sein
	val, _ := p.Value()
	if val != gpio.Low {
		t.Errorf("expected initial Low, got %v", val)
	}

	// Setze High
	if err := p.SetValue(gpio.High); err != nil {
		t.Fatalf("SetValue failed: %v", err)
	}

	val, _ = p.Value()
	if val != gpio.High {
		t.Errorf("expected High, got %v", val)
	}

	// Setze Low
	if err := p.SetValue(gpio.Low); err != nil {
		t.Fatalf("SetValue failed: %v", err)
	}

	val, _ = p.Value()
	if val != gpio.Low {
		t.Errorf("expected Low, got %v", val)
	}
}

// TestEdgeCallback watches an input pin, which is the case the emulator
// exists for: an input cannot be written with SetValue, so the level change
// comes from Drive, standing in for the outside world.
func TestEdgeCallback(t *testing.T) {
	p, err := NewPin(18, WithMode(gpio.Input),
		WithPullup(gpio.PullUp))
	if err != nil {
		t.Fatalf("NewPin failed: %v", err)
	}

	ch, err := p.WatchCh(gpio.RisingEdge | gpio.FallingEdge)
	if err != nil {
		t.Fatalf("WatchCh failed: %v", err)
	}
	events := collect(ch)

	if err := p.Drive(gpio.High); err != nil {
		t.Fatalf("Drive high failed: %v", err)
	}
	if err := p.Drive(gpio.Low); err != nil {
		t.Fatalf("Drive low failed: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	assertEdges(t, events, gpio.RisingEdge, gpio.FallingEdge)
}

// TestSetValueRejectsInputPins is the counterpart: SetValue keeps behaving
// like real hardware, so writing to an input stays an error.
func TestSetValueRejectsInputPins(t *testing.T) {
	p, err := NewPin(18, WithMode(gpio.Input))
	if err != nil {
		t.Fatalf("NewPin failed: %v", err)
	}

	if err := p.SetValue(gpio.High); !errors.Is(err, gpio.ErrInvalidMode) {
		t.Errorf("SetValue on an input returned %v, want ErrInvalidMode", err)
	}
}

// TestDriveReportsInvalidLevels covers the one thing Drive does validate.
func TestDriveReportsInvalidLevels(t *testing.T) {
	p, _ := NewPin(18, WithMode(gpio.Input))

	if err := p.Drive(gpio.Level(42)); !errors.Is(err, gpio.ErrInvalidLevel) {
		t.Errorf("Drive with an invalid level returned %v, want ErrInvalidLevel", err)
	}
}

// TestWatchChDeliversOnlyRequestedEdges is the regression test for an ignored
// edge mask: the emulator used to deliver both directions no matter what the
// watcher asked for.
func TestWatchChDeliversOnlyRequestedEdges(t *testing.T) {
	tests := []struct {
		name  string
		edges gpio.Edge
		want  []gpio.Edge
	}{
		{"rising only", gpio.RisingEdge, []gpio.Edge{gpio.RisingEdge}},
		{"falling only", gpio.FallingEdge, []gpio.Edge{gpio.FallingEdge}},
		{"both", gpio.RisingEdge | gpio.FallingEdge, []gpio.Edge{gpio.RisingEdge, gpio.FallingEdge}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := NewPin(21, WithMode(gpio.Input))

			ch, err := p.WatchCh(tt.edges)
			if err != nil {
				t.Fatalf("WatchCh failed: %v", err)
			}
			events := collect(ch)

			_ = p.Drive(gpio.High) // rising
			_ = p.Drive(gpio.Low)  // falling
			time.Sleep(20 * time.Millisecond)

			assertEdges(t, events, tt.want...)
		})
	}
}

// TestWatchChRejectsEmptyEdgeMask is the regression test for a leaked watch
// flag: the invalid configuration was reported, but the pin stayed marked as
// watching and could never be watched again.
func TestWatchChRejectsEmptyEdgeMask(t *testing.T) {
	p, _ := NewPin(22)

	if _, err := p.WatchCh(0); !errors.Is(err, gpio.ErrInvalidEdgeConfig) {
		t.Fatalf("WatchCh(0) returned %v, want ErrInvalidEdgeConfig", err)
	}

	if _, err := p.WatchCh(gpio.RisingEdge); err != nil {
		t.Errorf("the pin stayed blocked after a rejected WatchCh: %v", err)
	}
}

// TestOnlyOneWatcher covers the documented restriction.
func TestOnlyOneWatcher(t *testing.T) {
	p, _ := NewPin(23)

	if _, err := p.WatchCh(gpio.RisingEdge); err != nil {
		t.Fatalf("WatchCh failed: %v", err)
	}

	if _, err := p.WatchCh(gpio.RisingEdge); !errors.Is(err, gpio.ErrAlreadyWatching) {
		t.Errorf("a second WatchCh returned %v, want ErrAlreadyWatching", err)
	}
	if err := p.WatchFunc(gpio.RisingEdge, func(gpio.Event) {}); !errors.Is(err, gpio.ErrAlreadyWatching) {
		t.Errorf("WatchFunc alongside an active watcher returned %v, want ErrAlreadyWatching", err)
	}

	// After stopping, watching is possible again.
	if err := p.StopWatching(); err != nil {
		t.Fatalf("StopWatching failed: %v", err)
	}
	if _, err := p.WatchCh(gpio.RisingEdge); err != nil {
		t.Errorf("WatchCh after StopWatching failed: %v", err)
	}
}

// TestStopWatchingClosesTheChannel is the regression test for a consumer that
// hung forever: neither StopWatching nor Close used to close the channel,
// although the gpio.Pin contract says they do.
func TestStopWatchingClosesTheChannel(t *testing.T) {
	tests := []struct {
		name string
		stop func(Pin) error
	}{
		{"StopWatching", func(p Pin) error { return p.StopWatching() }},
		{"Close", func(p Pin) error { return p.Close() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := NewPin(24, WithMode(gpio.Input))

			ch, err := p.WatchCh(gpio.RisingEdge | gpio.FallingEdge)
			if err != nil {
				t.Fatalf("WatchCh failed: %v", err)
			}

			done := make(chan struct{})
			go func() {
				defer close(done)
				for range ch { // must terminate once the channel is closed
				}
			}()

			if err := tt.stop(p); err != nil {
				t.Fatalf("%s failed: %v", tt.name, err)
			}

			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("the consumer is stuck: the event channel was never closed")
			}
		})
	}
}

// TestWatchFunc covers the callback variant.
func TestWatchFunc(t *testing.T) {
	p, _ := NewPin(25, WithMode(gpio.Input))

	var mu sync.Mutex
	var got []gpio.Edge

	err := p.WatchFunc(gpio.RisingEdge|gpio.FallingEdge, func(evt gpio.Event) {
		mu.Lock()
		got = append(got, evt.Edge)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("WatchFunc failed: %v", err)
	}

	_ = p.Drive(gpio.High)
	_ = p.Drive(gpio.Low)
	time.Sleep(50 * time.Millisecond) // callbacks are invoked asynchronously

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != gpio.RisingEdge || got[1] != gpio.FallingEdge {
		t.Errorf("callback received %v, want [Rising Falling]", got)
	}
}

func TestModeAndPull(t *testing.T) {
	_, err := NewPin(19,
		WithMode(gpio.Output),
		WithPullup(gpio.PullUp))

	if err != nil {
		t.Fatalf("SetMode failed: %v", err)
	}
}

func TestDebounce(t *testing.T) {
	p, err := NewPin(20,
		WithMode(gpio.Output),
		WithPullup(gpio.PullUp),
		WithDebounce(50*time.Millisecond))
	if err != nil {
		t.Fatalf("SetMode failed: %v", err)
	}
	ch, err := p.WatchCh(gpio.RisingEdge | gpio.FallingEdge)
	if err != nil {
		t.Fatalf("WatchCh failed: %v", err)
	}
	events := collect(ch)

	// Flanken simulieren
	p.SetValue(gpio.High) // erstes Event
	time.Sleep(10 * time.Millisecond)
	p.SetValue(gpio.Low) // wird gefiltert (10ms < 50ms)
	time.Sleep(60 * time.Millisecond)
	p.SetValue(gpio.High) // neues Event (60ms > 50ms)
	time.Sleep(60 * time.Millisecond)
	p.SetValue(gpio.Low) // neues Event (60ms > 50ms)

	// Warten, damit alle goroutines durchlaufen
	time.Sleep(20 * time.Millisecond)

	// Erwartete Events: RisingEdge, RisingEdge, FallingEdge
	assertEdges(t, events, gpio.RisingEdge, gpio.RisingEdge, gpio.FallingEdge)

}
