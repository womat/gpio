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
