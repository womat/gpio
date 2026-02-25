package rpiemu

import (
	"context"
	"fmt"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/womat/golib/gpio"
)

func TestSetValueAndValue(t *testing.T) {
	p, _ := NewPin(17)

	// Standardwert sollte Low sein
	val, _ := p.Value()
	if val != gpio.Low {
		t.Errorf("expected initial Low, got %v", val)
	}

	// Setze Output-Mode
	if err := p.SetMode(gpio.Output); err != nil {
		t.Fatalf("SetMode failed: %v", err)
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

func TestEdgeCallback(t *testing.T) {
	p, _ := NewPin(18)
	if err := p.SetMode(gpio.Output); err != nil {
		t.Fatalf("SetMode failed: %v", err)
	}

	// Create a context to control watching lifetime
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var events []gpio.Edge
	ch, err := p.Watch(ctx, gpio.RisingEdge|gpio.FallingEdge)
	if err != nil {
		log.Fatal(err)
	}

	// Consume events
	var mu sync.Mutex
	go func() {
		for evt := range ch {
			mu.Lock()
			events = append(events, evt.Edge)
			mu.Unlock()
			fmt.Println("GPIO Event:", evt.Edge, "at", evt.Time.Format("15:04:05.000"))
		}
	}()

	// Trigger Rising
	p.SetValue(gpio.High)
	time.Sleep(10 * time.Millisecond)

	// Trigger Falling
	p.SetValue(gpio.Low)
	time.Sleep(10 * time.Millisecond)

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0] != gpio.RisingEdge || events[1] != gpio.FallingEdge {
		t.Errorf("unexpected edge sequence: %v", events)
	}

	// optional: cancel() if you want to stop watching immediately
	// cancel()
}

func TestModeAndPull(t *testing.T) {
	p, _ := NewPin(19)

	if err := p.SetMode(gpio.Input); err != nil {
		t.Fatalf("SetMode failed: %v", err)
	}

	if err := p.SetPullMode(gpio.PullUp); err != nil {
		t.Fatalf("SetPullMode PullUp failed: %v", err)
	}

	if err := p.SetPullMode(gpio.PullDown); err != nil {
		t.Fatalf("SetPullMode PullDown failed: %v", err)
	}

	if err := p.SetPullMode(gpio.PullNone); err != nil {
		t.Fatalf("SetPullMode PullNone failed: %v", err)
	}
}

func TestDebounce(t *testing.T) {
	p, _ := NewPin(20)
	if err := p.SetMode(gpio.Output); err != nil {
		t.Fatalf("SetMode failed: %v", err)
	}

	// Setze Debounce auf 50ms
	p.SetDebounce(50 * time.Millisecond)

	// Create a context to control watching lifetime
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var events []gpio.Edge
	ch, err := p.Watch(ctx, gpio.RisingEdge|gpio.FallingEdge)
	if err != nil {
		log.Fatal(err)
	}

	// Consume events
	var mu sync.Mutex
	go func() {
		for evt := range ch {
			mu.Lock()
			events = append(events, evt.Edge)
			mu.Unlock()
			fmt.Println("GPIO Event:", evt.Edge, "at", evt.Time.Format("15:04:05.000"))
		}
	}()

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
	expected := []gpio.Edge{gpio.RisingEdge, gpio.RisingEdge, gpio.FallingEdge}

	if len(events) != len(expected) {
		t.Fatalf("expected %d events, got %d", len(expected), len(events))
	}

	for i := range events {
		if events[i] != expected[i] {
			t.Errorf("event %d: expected %v, got %v", i, expected[i], events[i])
		}
	}

	// optional: cancel() if you want to stop watching immediately
	// cancel()
}
