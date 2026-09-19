# gpio

A backend-agnostic GPIO pin abstraction, developed primarily for the Raspberry
Pi.

`gpio` itself contains no hardware code: it defines the `Pin` interface, the
signal levels, modes, pull configurations and the edge event model. Two
backends implement it.

| Package             | Backend                                                        |
|---------------------|----------------------------------------------------------------|
| `gpio/rpi`          | Linux GPIO character device via `go-gpiocdev` — **Linux only** |
| `gpio/rpiemu`       | In-memory emulator for tests and development without hardware  |
| `gpio/internal/watch` | Event delivery shared by both backends, not importable from outside |

Depend on `gpio.Pin` in your own code and choose the backend at construction
time, so the emulator can be substituted in tests.

## The interface

```go
type Pin interface {
    Close() error

    SetValue(level Level) error
    Value() (Level, error)

    Number() int
    Info() string

    WatchCh(edges Edge) (<-chan Event, error)
    WatchFunc(edges Edge, f func(event Event)) error
    StopWatching() error
    DroppedEvents() uint64
}
```

Mode, pull resistor and debounce are fixed when the pin is requested, through
the options of the chosen backend — there are no setters. `Edge` is a bit mask
(`RisingEdge | FallingEdge`), `Event` carries the timestamp and the edge and
offers `IsRising()`, `IsFalling()` and `String()`.

## Watching edges

```go
pin, err := rpi.NewPin(17,
    rpi.WithMode(gpio.Input),
    rpi.WithPullup(gpio.PullUp),
    rpi.WithDebounce(5*time.Millisecond),
)
if err != nil {
    log.Fatal(err)
}
defer pin.Close()

events, err := pin.WatchCh(gpio.RisingEdge | gpio.FallingEdge)
if err != nil {
    log.Fatal(err)
}

for evt := range events {
    fmt.Println(evt)
}
```

Rules that hold for both backends:

- **One watcher at a time.** A second `WatchCh` or `WatchFunc` returns
  `ErrAlreadyWatching`; an edge mask selecting no edge returns
  `ErrInvalidEdgeConfig`, without claiming the pin.
- **The channel is closed** by `StopWatching` and by `Close`, so a consumer
  ranging over it terminates.
- **Only the requested edges are delivered.**
- **Events are dropped, never blocked.** The buffer holds 32 events; if the
  consumer does not keep up, events are discarded and counted by
  `DroppedEvents()`. For timing-sensitive work — a line decoder, say — read the
  channel in a dedicated goroutine that does nothing else.
- **`WatchFunc` delivers from a single goroutine**, so the callback is never
  called concurrently with itself and sees the edges in the order they
  occurred. A blocking callback stalls delivery and eventually costs events; it
  never stalls the kernel event handler.

## The emulator

`rpiemu` accepts the same options as `rpi`, so tests drive the exact same code
path. It adds one method that has no counterpart on real hardware:

```go
pin, _ := rpiemu.NewPin(17, rpiemu.WithMode(gpio.Input))
events, _ := pin.WatchCh(gpio.RisingEdge | gpio.FallingEdge)

pin.Drive(gpio.High) // delivers a rising edge to the watcher
pin.Drive(gpio.Low)
```

`SetValue` behaves like real hardware and refuses to write to an input pin.
`Drive` stands in for the outside world and works regardless of the configured
mode — that is what makes the emulator useful for testing *receiving* code such
as `manchester/decoder`. Debouncing and edge filtering apply to a level change
driven either way.

`rpiemu.NewPin` returns `rpiemu.Pin`, which embeds `gpio.Pin`, so it can be
passed anywhere a `gpio.Pin` is expected.

## Why the shared internal package

`gpio/internal/watch` owns the channel a consumer ranges over, the edge mask,
the drop counting and the shutdown that closes the channel. Both backends
differ in how they learn about an edge — a kernel callback for real hardware, a
simulated level change for the emulator — but not in what happens afterwards.

That logic lives in its own package for a practical reason: it is where the
concurrency bugs are, and `gpio/rpi` cannot be tested off a Raspberry Pi.
Keeping delivery there down to a one-line call means the untestable part is a
thin adapter over code that is tested under `-race`, including the case of an
edge arriving while the watcher is being shut down.

## Building and testing

`gpio/rpi` depends on the Linux GPIO character device and does not build on
macOS or Windows, so `go build ./...` and `go test ./...` fail there for that
package alone. Use a cross-target check to cover it:

```sh
go test -race ./gpio/...              # everything except gpio/rpi
GOOS=linux GOARCH=arm64 go vet ./...  # includes gpio/rpi
```

`gpio/rpi` has no tests — exercising it needs a real chip. Its example is
compiled as documentation but deliberately carries no `Output:` comment, so
`go test` does not try to run it.

Full API: `go doc github.com/womat/golib/gpio`, `.../gpio/rpi`,
`.../gpio/rpiemu`.
