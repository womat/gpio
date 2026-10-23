package rpi

import (
	"testing"

	gpiod "github.com/warthog618/go-gpiocdev"
	"github.com/womat/gpio"
)

// TestHandler covers the one piece of rpi with logic of its own that needs no
// line: the kernel event handler. It maps the event type to an edge, turns the
// gap in the line's sequence numbers into Missed, and passes nothing on that
// is not an edge. A zero pin with a started watcher is all it touches, so this
// runs in CI.
func TestHandler(t *testing.T) {
	p := &pin{}
	ch, err := p.watcher.Start(gpio.RisingEdge|gpio.FallingEdge, defaultBufferSize)
	if err != nil {
		t.Fatal(err)
	}

	p.handler(gpiod.LineEvent{}) // the zero type is no edge
	p.handler(gpiod.LineEvent{Type: gpiod.LineEventRisingEdge, LineSeqno: 5})
	p.handler(gpiod.LineEvent{Type: gpiod.LineEventFallingEdge, LineSeqno: 8})

	for _, want := range []gpio.Event{
		{Edge: gpio.RisingEdge, Missed: 0},  // first edge of the watch: no predecessor
		{Edge: gpio.FallingEdge, Missed: 2}, // 6 and 7 were lost in the kernel
	} {
		select {
		case got := <-ch:
			if got.Edge != want.Edge || got.Missed != want.Missed {
				t.Errorf("got %v with Missed = %d, want %v with Missed = %d", got.Edge, got.Missed, want.Edge, want.Missed)
			}
		default:
			t.Fatalf("no event delivered, want %v", want.Edge)
		}
	}

	select {
	case got := <-ch:
		t.Errorf("unexpected event %v: the event that is no edge was delivered", got)
	default:
	}
}
