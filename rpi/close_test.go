package rpi

import (
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/womat/gpio"
)

// testLine returns the GPIO line named by RPI_TEST_LINE and skips the test
// when it is unset: these tests request a real line, which CI does not have.
// Run them on a Pi with a free line, e.g. RPI_TEST_LINE=20.
func testLine(t *testing.T) int {
	t.Helper()
	s := os.Getenv("RPI_TEST_LINE")
	if s == "" {
		t.Skip("needs a GPIO line: set RPI_TEST_LINE")
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("RPI_TEST_LINE=%q: %v", s, err)
	}
	return n
}

// TestNewPinFailureReturnsNil is the regression test for e4daf43: NewPin
// returned a Pin alongside its error, so the usual NewPin/defer Close pattern
// dereferenced a nil line. It needs no line - there is no line 9999, and CI
// has no GPIO chip at all - so it runs in CI.
func TestNewPinFailureReturnsNil(t *testing.T) {
	p, err := NewPin(9999)
	if err == nil {
		t.Fatal("NewPin(9999) succeeded, want an error")
	}
	if p != nil {
		t.Errorf("NewPin returned a non-nil Pin alongside %q, want nil", err)
	}
}

// TestCloseIsIdempotent is the regression test for a second Close that
// returned "already closed" from go-gpiocdev, although the package documents
// Close as safe to call multiple times.
func TestCloseIsIdempotent(t *testing.T) {
	p, err := NewPin(testLine(t), WithMode(gpio.Input))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.WatchCh(gpio.RisingEdge); err != nil {
		t.Fatal(err)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestWatchFuncRejectsNilCallback checks the sentinel, and that the failed
// call leaves the pin free for a watcher. It lives here because it needs a
// real line, like the Close test above.
func TestWatchFuncRejectsNilCallback(t *testing.T) {
	p, err := NewPin(testLine(t), WithMode(gpio.Input))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.WatchFunc(gpio.RisingEdge, nil); !errors.Is(err, gpio.ErrNilCallback) {
		t.Fatalf("WatchFunc(nil) returned %v, want ErrNilCallback", err)
	}
	if _, err := p.WatchCh(gpio.RisingEdge); err != nil {
		t.Errorf("WatchCh after a rejected WatchFunc: %v", err)
	}
}

// TestWatchRejectsOutputPin is the regression test for a watch on an output:
// enabling edge detection made go-gpiocdev request the line as an input, so
// the output silently stopped driving.
func TestWatchRejectsOutputPin(t *testing.T) {
	p, err := NewPin(testLine(t), WithMode(gpio.Output))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if _, err := p.WatchCh(gpio.RisingEdge); !errors.Is(err, gpio.ErrInvalidMode) {
		t.Fatalf("WatchCh on an output returned %v, want ErrInvalidMode", err)
	}
	if err := p.SetValue(gpio.High); err != nil {
		t.Errorf("SetValue after the rejected watch: %v - the line is no longer an output", err)
	}
	_ = p.SetValue(gpio.Low)
}

// TestWatchAfterClose checks the sentinel for a watch on a closed pin.
func TestWatchAfterClose(t *testing.T) {
	p, err := NewPin(testLine(t), WithMode(gpio.Input))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := p.WatchCh(gpio.RisingEdge); !errors.Is(err, gpio.ErrClosed) {
		t.Errorf("WatchCh after Close returned %v, want ErrClosed", err)
	}
}

// TestValueAfterClose is the regression test for go-gpiocdev's errors after
// Close: Value reported its own ErrClosed, and SetValue ErrPermissionDenied,
// because Close releases an output as an input.
func TestValueAfterClose(t *testing.T) {
	p, err := NewPin(testLine(t), WithMode(gpio.Output))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}

	if err := p.SetValue(gpio.High); !errors.Is(err, gpio.ErrClosed) {
		t.Errorf("SetValue after Close returned %v, want ErrClosed", err)
	}
	if _, err := p.Value(); !errors.Is(err, gpio.ErrClosed) {
		t.Errorf("Value after Close returned %v, want ErrClosed", err)
	}
	// A closed output reports ErrClosed, as in rpiemu, not ErrInvalidMode.
	if _, err := p.WatchCh(gpio.RisingEdge); !errors.Is(err, gpio.ErrClosed) {
		t.Errorf("WatchCh after Close returned %v, want ErrClosed", err)
	}
}
