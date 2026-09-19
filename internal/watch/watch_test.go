package watch

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/womat/golib/gpio"
)

const bufferSize = 8

func rising() gpio.Event  { return gpio.Event{Time: time.Now(), Edge: gpio.RisingEdge} }
func falling() gpio.Event { return gpio.Event{Time: time.Now(), Edge: gpio.FallingEdge} }

// drain reads everything that is currently buffered.
func drain(ch <-chan gpio.Event) []gpio.Edge {
	var out []gpio.Edge
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, evt.Edge)
		default:
			return out
		}
	}
}

func TestStartRejectsAnEmptyEdgeMask(t *testing.T) {
	var w Watcher

	if _, err := w.Start(0, bufferSize); !errors.Is(err, gpio.ErrInvalidEdgeConfig) {
		t.Fatalf("Start(0) returned %v, want ErrInvalidEdgeConfig", err)
	}

	// The rejected call must not have claimed the watcher.
	if _, err := w.Start(gpio.RisingEdge, bufferSize); err != nil {
		t.Errorf("the watcher stayed claimed after a rejected Start: %v", err)
	}
}

func TestOnlyOneWatcherAtATime(t *testing.T) {
	var w Watcher

	if _, err := w.Start(gpio.RisingEdge, bufferSize); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := w.Start(gpio.RisingEdge, bufferSize); !errors.Is(err, gpio.ErrAlreadyWatching) {
		t.Errorf("a second Start returned %v, want ErrAlreadyWatching", err)
	}

	if !w.Stop() {
		t.Error("Stop reported no active watcher")
	}
	if _, err := w.Start(gpio.RisingEdge, bufferSize); err != nil {
		t.Errorf("Start after Stop failed: %v", err)
	}
}

func TestStopIsSafeWithoutAWatcher(t *testing.T) {
	var w Watcher

	if w.Stop() {
		t.Error("Stop reported an active watcher although none was started")
	}
	if w.Stop() {
		t.Error("a repeated Stop reported an active watcher")
	}
}

func TestStopClosesTheChannel(t *testing.T) {
	var w Watcher

	ch, err := w.Start(gpio.RisingEdge, bufferSize)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range ch { //nolint:revive // draining until closed is the point
		}
	}()

	w.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the consumer is stuck: Stop did not close the channel")
	}
}

func TestDeliverHonoursTheEdgeMask(t *testing.T) {
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
			var w Watcher

			ch, err := w.Start(tt.edges, bufferSize)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}

			w.Deliver(rising())
			w.Deliver(falling())

			got := drain(ch)
			if len(got) != len(tt.want) {
				t.Fatalf("delivered %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("event %d is %v, want %v (sequence %v)", i, got[i], tt.want[i], got)
				}
			}
		})
	}
}

func TestDeliverWithoutAWatcherIsIgnored(t *testing.T) {
	var w Watcher

	w.Deliver(rising()) // must not panic

	if got := w.Dropped(); got != 0 {
		t.Errorf("dropped count is %d, want 0: an event nobody asked for is not a loss", got)
	}
}

func TestDeliverCountsDroppedEvents(t *testing.T) {
	const capacity = 2

	var w Watcher

	ch, err := w.Start(gpio.RisingEdge, capacity)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	const sent = 5
	for i := 0; i < sent; i++ {
		w.Deliver(rising())
	}

	if got, want := w.Dropped(), uint64(sent-capacity); got != want {
		t.Errorf("dropped count is %d, want %d", got, want)
	}
	if got := len(drain(ch)); got != capacity {
		t.Errorf("%d events are readable, want %d", got, capacity)
	}
}

func TestWants(t *testing.T) {
	var w Watcher

	if w.Wants(gpio.RisingEdge) {
		t.Error("an inactive watcher wants events")
	}

	if _, err := w.Start(gpio.RisingEdge, bufferSize); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !w.Wants(gpio.RisingEdge) {
		t.Error("Wants(RisingEdge) is false although it was requested")
	}
	if w.Wants(gpio.FallingEdge) {
		t.Error("Wants(FallingEdge) is true although only rising edges were requested")
	}
	if !w.Active() {
		t.Error("Active() is false although a watcher is running")
	}

	w.Stop()

	if w.Wants(gpio.RisingEdge) {
		t.Error("Wants is true after Stop")
	}
	if w.Active() {
		t.Error("Active() is true after Stop")
	}
}

// TestDeliverRacingStop is the regression test for the defect this package was
// extracted for: gpio/rpi read the event channel without synchronisation and
// closed it from another goroutine, so a kernel edge arriving during shutdown
// panicked with "send on closed channel".
//
// Run with -race; without the lock in Deliver this fails within a few
// iterations, either on the race detector or on the panic itself.
func TestDeliverRacingStop(t *testing.T) {
	for i := 0; i < 200; i++ {
		var w Watcher

		// A buffer of one, so deliveries keep hitting a full channel and the
		// send path is exercised rather than short-circuited.
		if _, err := w.Start(gpio.RisingEdge|gpio.FallingEdge, 1); err != nil {
			t.Fatalf("Start: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(3)

		// Two producers, as a real backend may deliver from more than one
		// goroutine (the emulator does: any caller driving the line).
		for p := 0; p < 2; p++ {
			go func() {
				defer wg.Done()
				for n := 0; n < 50; n++ {
					w.Deliver(rising())
				}
			}()
		}

		go func() {
			defer wg.Done()
			w.Stop()
		}()

		wg.Wait()
	}
}

// TestStartRacingStop covers the other order: a watcher being restarted while
// another goroutine is shutting it down.
func TestStartRacingStop(t *testing.T) {
	for i := 0; i < 200; i++ {
		var w Watcher

		if _, err := w.Start(gpio.RisingEdge, bufferSize); err != nil {
			t.Fatalf("Start: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			w.Stop()
		}()
		go func() {
			defer wg.Done()
			// Either outcome is fine; neither may corrupt the watcher.
			if _, err := w.Start(gpio.FallingEdge, bufferSize); err != nil && !errors.Is(err, gpio.ErrAlreadyWatching) {
				t.Errorf("Start returned an unexpected error: %v", err)
			}
		}()

		wg.Wait()
		w.Stop()
	}
}
