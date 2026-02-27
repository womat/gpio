package gpio

import (
	"context"
	"testing"
	"time"
)

// mockPin exists solely to ensure that the Pin interface
// is satisfied at compile time.
type mockPin struct{}

func (m *mockPin) Close() error                                        { return nil }
func (m *mockPin) SetValue(Level) error                                { return nil }
func (m *mockPin) Value() (Level, error)                               { return Low, nil }
func (m *mockPin) Number() int                                         { return 0 }
func (m *mockPin) Info() string                                        { return "mock" }
func (m *mockPin) WatchCh(context.Context, Edge) (<-chan Event, error) { return make(chan Event), nil }
func (m *mockPin) WatchFunc(context.Context, Edge, func(Event)) error  { return nil }
func (m *mockPin) StopWatching() error                                 { return nil }
func (m *mockPin) DroppedEvents() uint64                               { return 0 }

// Compile-time check:
var _ Pin = (*mockPin)(nil)

func TestModeString(t *testing.T) {
	if Input.String() != "Input" {
		t.Errorf("expected Input, got %s", Input.String())
	}
	if Output.String() != "Output" {
		t.Errorf("expected Output, got %s", Output.String())
	}
}

func TestLevelString(t *testing.T) {
	if High.String() != "High" {
		t.Errorf("expected High, got %s", High.String())
	}
	if Low.String() != "Low" {
		t.Errorf("expected Low, got %s", Low.String())
	}
}

func TestEdgeString(t *testing.T) {
	if RisingEdge.String() != "Rising" {
		t.Errorf("expected Rising, got %s", RisingEdge.String())
	}
	if FallingEdge.String() != "Falling" {
		t.Errorf("expected Falling, got %s", FallingEdge.String())
	}
}

func TestPullModeString(t *testing.T) {
	if PullUp.String() != "Up" {
		t.Errorf("expected Up, got %s", PullUp.String())
	}
	if PullDown.String() != "Down" {
		t.Errorf("expected Down, got %s", PullDown.String())
	}
	if PullNone.String() != "None" {
		t.Errorf("expected None, got %s", PullNone.String())
	}
}

func TestEventHelpers(t *testing.T) {
	rising := Event{Time: time.Now(), Edge: RisingEdge}
	if !rising.IsRising() {
		t.Error("expected IsRising() to be true")
	}
	if rising.IsFalling() {
		t.Error("expected IsFalling() to be false")
	}

	falling := Event{Time: time.Now(), Edge: FallingEdge}
	if !falling.IsFalling() {
		t.Error("expected IsFalling() to be true")
	}
	if falling.IsRising() {
		t.Error("expected IsRising() to be false")
	}
}

func TestEventString(t *testing.T) {
	e := Event{
		Time: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		Edge: RisingEdge,
	}

	expected := "Rising at 2024-01-01T12:00:00Z"
	if e.String() != expected {
		t.Errorf("expected %s, got %s", expected, e.String())
	}
}
