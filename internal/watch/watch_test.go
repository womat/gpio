package watch

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/womat/gpio"
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
	for range sent {
		w.Deliver(rising())
	}

	if got, want := w.Dropped(), uint64(sent-capacity); got != want {
		t.Errorf("dropped count is %d, want %d", got, want)
	}
	if got := len(drain(ch)); got != capacity {
		t.Errorf("%d events are readable, want %d", got, capacity)
	}
}

func TestDeliverReportsLossesOnTheNextEvent(t *testing.T) {
	const capacity = 2

	var w Watcher

	ch, err := w.Start(gpio.RisingEdge, capacity)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	const sent = 5
	for range sent {
		w.Deliver(rising())
	}

	// The buffered events arrived before the gap and report none.
	for i := range capacity {
		if evt := <-ch; evt.Missed != 0 {
			t.Errorf("buffered event %d has Missed = %d, want 0", i, evt.Missed)
		}
	}

	w.Deliver(rising())
	if got, want := (<-ch).Missed, uint64(sent-capacity); got != want {
		t.Errorf("the first event after the gap has Missed = %d, want %d", got, want)
	}

	w.Deliver(rising())
	if got := (<-ch).Missed; got != 0 {
		t.Errorf("the gap was reported twice: the next event has Missed = %d", got)
	}
}

func TestDeliverPassesBackendLossesThrough(t *testing.T) {
	var w Watcher

	ch, err := w.Start(gpio.RisingEdge, bufferSize)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	evt := rising()
	evt.Missed = 3
	w.Deliver(evt)

	if got := (<-ch).Missed; got != 3 {
		t.Errorf("Missed = %d, want the 3 the backend reported", got)
	}
	if got := w.Dropped(); got != 3 {
		t.Errorf("dropped count is %d, want 3: losses the backend reports are losses too", got)
	}
}

func TestDeliverAddsBackendLossesOfADroppedEvent(t *testing.T) {
	var w Watcher

	ch, err := w.Start(gpio.RisingEdge, 1)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	w.Deliver(rising()) // fills the buffer

	evt := rising()
	evt.Missed = 2
	w.Deliver(evt) // dropped, together with the two before it

	<-ch
	w.Deliver(rising())

	if got := (<-ch).Missed; got != 3 {
		t.Errorf("Missed = %d, want 3 (two lost in the backend, one dropped here)", got)
	}
	if got := w.Dropped(); got != 3 {
		t.Errorf("dropped count is %d, want 3", got)
	}
}

func TestStartForgetsTheGapOfAPreviousWatch(t *testing.T) {
	var w Watcher

	if _, err := w.Start(gpio.RisingEdge, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.Deliver(rising())
	w.Deliver(rising()) // dropped
	w.Stop()

	ch, err := w.Start(gpio.RisingEdge, 1)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	w.Deliver(rising())

	if got := (<-ch).Missed; got != 0 {
		t.Errorf("the first event of a new watch has Missed = %d, want 0", got)
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
// extracted for: rpi read the event channel without synchronisation and
// closed it from another goroutine, so a kernel edge arriving during shutdown
// panicked with "send on closed channel".
//
// Run with -race; without the lock in Deliver this fails within a few
// iterations, either on the race detector or on the panic itself.
func TestDeliverRacingStop(t *testing.T) {
	for range 200 {
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
		for range 2 {
			go func() {
				defer wg.Done()
				for range 50 {
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
	for range 200 {
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

// TestCloseEndsTheWatcherForGood checks that Close closes the channel of an
// active watch like Stop, and that no watch can be started afterwards.
func TestCloseEndsTheWatcherForGood(t *testing.T) {
	var w Watcher

	ch, err := w.Start(gpio.RisingEdge, bufferSize)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	w.Close()
	if _, ok := <-ch; ok {
		t.Error("the channel is still open after Close")
	}

	if _, err := w.Start(gpio.RisingEdge, bufferSize); !errors.Is(err, gpio.ErrClosed) {
		t.Errorf("Start after Close returned %v, want ErrClosed", err)
	}
	if err := w.StartFunc(gpio.RisingEdge, bufferSize, func(gpio.Event) {}); !errors.Is(err, gpio.ErrClosed) {
		t.Errorf("StartFunc after Close returned %v, want ErrClosed", err)
	}

	w.Close() // a second Close is harmless
}

// TestStartRacingClose is the regression test for a channel that was never
// closed: a backend's Close found no active watch, a concurrent Start then
// claimed the watcher, and nothing was left to close its channel - a consumer
// ranging over it hung forever.
func TestStartRacingClose(t *testing.T) {
	for range 200 {
		var w Watcher

		var ch <-chan gpio.Event
		var startErr error
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			ch, startErr = w.Start(gpio.RisingEdge, bufferSize)
		}()
		go func() {
			defer wg.Done()
			w.Close()
		}()

		wg.Wait()

		switch {
		case errors.Is(startErr, gpio.ErrClosed):
			// Close ran first.
		case startErr != nil:
			t.Fatalf("Start returned an unexpected error: %v", startErr)
		default:
			// A select, not a plain receive: an open channel would hang
			// the test until go test's timeout instead of failing it.
			select {
			case _, ok := <-ch:
				if ok {
					t.Fatal("Start won the race, but Close left its channel open")
				}
			case <-time.After(time.Second):
				t.Fatal("Start won the race, but Close left its channel open")
			}
		}
	}
}

// TestStartFuncDeliversInOrder covers the callback variant of Start.
func TestStartFuncDeliversInOrder(t *testing.T) {
	var w Watcher
	got := make(chan gpio.Edge, bufferSize)

	if err := w.StartFunc(gpio.RisingEdge|gpio.FallingEdge, bufferSize, func(evt gpio.Event) {
		got <- evt.Edge
	}); err != nil {
		t.Fatalf("StartFunc: %v", err)
	}
	defer w.Stop()

	w.Deliver(rising())
	w.Deliver(falling())

	for _, want := range []gpio.Edge{gpio.RisingEdge, gpio.FallingEdge} {
		select {
		case edge := <-got:
			if edge != want {
				t.Errorf("callback got %v, want %v", edge, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("callback never received %v", want)
		}
	}
}

// TestStartFuncRejects checks the errors of StartFunc, and that none of them
// claims the watcher.
func TestStartFuncRejects(t *testing.T) {
	var w Watcher
	f := func(gpio.Event) {}

	if err := w.StartFunc(gpio.RisingEdge, bufferSize, nil); !errors.Is(err, gpio.ErrNilCallback) {
		t.Errorf("nil callback: %v, want ErrNilCallback", err)
	}
	if err := w.StartFunc(0, bufferSize, f); !errors.Is(err, gpio.ErrInvalidEdgeConfig) {
		t.Errorf("empty edge mask: %v, want ErrInvalidEdgeConfig", err)
	}
	if w.Active() {
		t.Fatal("a rejected StartFunc claimed the watcher")
	}

	if _, err := w.Start(gpio.RisingEdge, bufferSize); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()
	if err := w.StartFunc(gpio.RisingEdge, bufferSize, f); !errors.Is(err, gpio.ErrAlreadyWatching) {
		t.Errorf("second watcher: %v, want ErrAlreadyWatching", err)
	}
}

// TestStartFuncSkipsBufferedEventsAfterStop is the regression test for
// callbacks that kept arriving after StopWatching or Close had returned: the
// goroutine drained the whole buffer first. Only the call that already holds
// its event may still run.
func TestStartFuncSkipsBufferedEventsAfterStop(t *testing.T) {
	var w Watcher
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})

	if err := w.StartFunc(gpio.RisingEdge, bufferSize, func(gpio.Event) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
	}); err != nil {
		t.Fatalf("StartFunc: %v", err)
	}

	w.Deliver(rising())
	<-entered // the first call is under way and holds the goroutine
	for range 5 {
		w.Deliver(rising()) // these wait in the buffer
	}

	w.Stop()
	close(release)
	time.Sleep(50 * time.Millisecond) // give a buggy goroutine time to drain

	if n := calls.Load(); n != 1 {
		t.Errorf("callback ran %d times, want 1: buffered events were delivered after Stop", n)
	}
}

// TestStopFromInsideTheCallback guards against the deadlock that waiting for
// the callback in Stop would cause: a callback that ends its own watch.
func TestStopFromInsideTheCallback(t *testing.T) {
	var w Watcher
	stopped := make(chan struct{})

	if err := w.StartFunc(gpio.RisingEdge, bufferSize, func(gpio.Event) {
		w.Stop()
		close(stopped)
	}); err != nil {
		t.Fatalf("StartFunc: %v", err)
	}

	w.Deliver(rising())

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop called from the callback did not return")
	}
}
