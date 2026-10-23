# gpio

[![CI](https://github.com/womat/gpio/actions/workflows/ci.yml/badge.svg)](https://github.com/womat/gpio/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/womat/gpio.svg)](https://pkg.go.dev/github.com/womat/gpio)

A backend-agnostic GPIO pin abstraction, developed primarily for the Raspberry
Pi.

```sh
go get github.com/womat/gpio
```

This module used to be part of
[github.com/womat/golib](https://github.com/womat/golib) as `golib/gpio`. The
API is unchanged; only the import path differs. The copy in golib is
deprecated and is removed there in golib v2.

`gpio` itself contains no hardware code: it defines the `Pin` interface, the
signal levels, modes, pull configurations and the edge event model. Two
backends implement it.

| Package | Backend |
|---|---|
| `github.com/womat/gpio/rpi` | Linux GPIO character device via `go-gpiocdev` — **Linux only** |
| `github.com/womat/gpio/rpiemu` | In-memory emulator for tests and development without hardware |
| `internal/watch` | Event delivery shared by both backends, not importable from outside |

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
(`RisingEdge | FallingEdge`), `Event` carries the timestamp, the edge and the
number of edges lost immediately before it (`Missed`), and offers `IsRising()`,
`IsFalling()` and `String()`.

Both backends offer the same three options: `WithMode(gpio.Input|gpio.Output)`,
`WithPullup(gpio.PullNone|gpio.PullUp|gpio.PullDown)` and
`WithDebounce(time.Duration)`. `rpi.NewPin` returns a `gpio.Pin`;
`rpiemu.NewPin` returns `rpiemu.Pin`, which embeds it (see [the
emulator](#the-emulator)).

`rpi` always requests its lines from the chip named by the exported constant
`rpi.Chip` (`"gpiochip0"`). It is a constant, not an option — a board that
exposes its header on a different chip device is not supported.

`rpi`'s `Close` releases the line as an input, outputs included, so a released
output stops driving — the same state the Pi's pins are in after boot. Its
pull is not changed: an output requested without `WithPullup` keeps whatever
bias the pin has (on a Pi by default pull-up for GPIO 0–8 and pull-down from
9, unless something has changed it). Whether that rest state is safe depends
on the circuit, not on this package: a driver with a high-impedance input,
such as a transistor without a base pull-down or an active-low relay module,
can switch or chatter on an open input. Put an external pull resistor to the
safe level on such a line; it also covers the time before the service starts
and after a crash, when `Close` never runs.

### Sentinel errors

| Error | Returned when |
|---|---|
| `ErrAlreadyWatching` | a second `WatchCh`/`WatchFunc` while a watcher is active |
| `ErrClosed` | `SetValue`, `Value`, `WatchCh` or `WatchFunc` (and `rpiemu`'s `Drive`) on a pin that has been closed |
| `ErrInvalidEdgeConfig` | the edge mask selects no edge; the pin is not claimed |
| `ErrInvalidLevel` | `SetValue` gets a level that is neither `High` nor `Low` |
| `ErrInvalidMode` | `SetValue` on a pin that is not an output, or `WatchCh`/`WatchFunc` on an output |
| `ErrInvalidPullMode` | nothing — declared for symmetry, currently unused |
| `ErrNilCallback` | `WatchFunc` gets a nil callback; the pin is not claimed |

`ErrInvalidLevel`, and `ErrInvalidMode` for `SetValue`, are only produced by
`rpiemu`; on real hardware the same situations surface as errors from
`go-gpiocdev`. The watch errors are the same in both backends.

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
- **Only an input can be watched.** `WatchCh` and `WatchFunc` on an output
  return `ErrInvalidMode`: edge detection would make `go-gpiocdev` request the
  line as an input, and the output would silently stop driving. A closed pin
  returns `ErrClosed`.
- **The channel is closed** by `StopWatching` and by `Close`, so a consumer
  ranging over it terminates.
- **Only the requested edges are delivered.**
- **Events are dropped, never blocked.** The buffer holds 32 events; if the
  consumer does not keep up, events are discarded and counted by
  `DroppedEvents()`. For timing-sensitive work — a line decoder, say — read the
  channel in a dedicated goroutine that does nothing else.
- **A gap is reported on the event after it.** `Event.Missed` counts the edges
  lost immediately before that event: dropped because the buffer was full, or —
  on `rpi` — lost in the kernel, detected through the per-line sequence
  number the kernel assigns. It is 0 normally. When it is not, the interval to
  the previous event spans more than one edge: a pulse counter must not derive
  a rate from it, and a line decoder must not take it for a bit period.
- **Edges faster than the interrupt handling are lost unseen.** Above roughly
  50 kHz the board merges edges before the kernel numbers them, so neither
  `Missed` nor `DroppedEvents()` can count them, and two consecutive events may
  report the same direction. Measured in edges per second on a Pi 3B+
  (Linux 6.18, direct loopback): nothing lost at 10 kHz, now and then one edge
  in 2000 at 20 kHz, a few in 4000 at 50 kHz, about 60 % at 400 kHz. Keep
  signals well below that rate. An input behind an optocoupler is slower
  still: plan for at most 1 kHz signal frequency there. The Pi kept detecting
  every edge from the EL817 and 4N35 on the test bench up to 10 kHz edges per
  second, but that counts edges, not the shape of the signal behind them.
- **Debounce on a Pi works in kernel ticks.** The GPIO controller has no
  debounce of its own; the kernel debounces with a timer that counts whole
  ticks, 4 ms at the usual `CONFIG_HZ=250`. On `rpi`, `WithDebounce` therefore
  rounds up to a tick, and an event is stamped when the timer runs, not at its
  edge. Every period up to 4 ms gives the same delay; above that, the delay
  rises in 4 ms steps. Shortest delay after the edge, measured on Pi 3B+,
  Linux 6.18.50+rpt-rpi-v8, `CONFIG_HZ=250`: 4 ms for 100 µs, 1 ms and 4 ms
  asked for, 8 ms for 5 ms, 12 ms for 10 ms, 20 ms for 20 ms; with 1 ms, edges
  5 ms apart were swallowed. Debounce suits a contact; a signal whose
  intervals matter, a pulse counter deriving a rate or a line decoder, should
  go without it.
- **`WatchFunc` delivers from a single goroutine**, so the callback is never
  called concurrently with itself and sees the edges in the order they
  occurred. A blocking callback stalls delivery and eventually costs events; it
  never stalls the kernel event handler. Events still buffered when
  `StopWatching` or `Close` runs are not passed to the callback; a call already
  under way can still be running when they return (see *Not addressed*).

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
mode — that is what makes the emulator useful for testing *receiving* code,
such as a line decoder. Only an input can be watched, as on real hardware, so
the edges a test observes come from `Drive`; debouncing and edge filtering
apply to them. The emulator's debounce is a lockout after each reported edge,
not the kernel's, which reports the level that has settled; the package doc
shows a sequence where the two differ.

`rpiemu.NewPin` returns `rpiemu.Pin`, which embeds `gpio.Pin`, so it can be
passed anywhere a `gpio.Pin` is expected. That is also why the emulator is the
one backend that returns `ErrInvalidMode` and `ErrInvalidLevel`: it checks what
real hardware lets the kernel reject.

## Why the shared internal package

`internal/watch` owns the channel a consumer ranges over, the edge mask,
the drop counting and the shutdown that closes the channel. Both backends
differ in how they learn about an edge — a kernel callback for real hardware, a
simulated level change for the emulator — but not in what happens afterwards.

That logic lives in its own package for a practical reason: it is where the
concurrency bugs are, and `rpi` cannot be tested off a Raspberry Pi.
Keeping delivery there down to a one-line call means the untestable part is a
thin adapter over code that is tested under `-race`, including the case of an
edge arriving while the watcher is being shut down.

## Building and testing

`rpi` depends on the Linux GPIO character device and does not build on macOS
or Windows, so `go build ./...` and `go test ./...` fail there for that
package alone. Use a cross-target check to cover it:

```sh
go test -race . ./rpiemu/ ./internal/...  # everything except rpi
GOOS=linux GOARCH=arm64 go vet ./...      # includes rpi
```

`scripts/check.sh` runs `go fix`, `go vet`, `golangci-lint` and `govulncheck`
over this module and the demos, with `GOOS=linux` by default; `--check`
reports pending `go fix` work without writing, `--skip=vuln` drops the step
that needs a network. CI (`.github/workflows/ci.yml`) runs on Linux, where
`rpi` builds natively.

Coverage: 82.8 % in `gpio`, 90.9 % in `rpiemu`, 100 % in `internal/watch` —
that last one is the number that matters, since it is the code `rpi` cannot
test for itself.

In CI, `rpi` is tested only through its pure helpers — the conversion of the
kernel's edge timestamp and the gap detection from its sequence numbers. The
tests that touch a line skip unless told which lines to use, so they run on a
Pi only:

```sh
GOOS=linux GOARCH=arm64 go test -c ./rpi        # copy rpi.test to the Pi, then:
RPI_TEST_LINE=20 RPI_TEST_LOOP=18:25 ./rpi.test -test.v
```

`RPI_TEST_LINE` names a free line for the `Close` tests. `RPI_TEST_LOOP` lists
output:input pairs wired to each other (the bench `demo/wirecheck` describes;
run it first). The direct connection 18→25, with 18 as the output, is the
one that tests `rpi`; optocoupler pairs can be appended
(`18:25,13:6,12:26`) and then also run the slow edge test at 250 Hz, below
their 1 kHz limit. The loopback tests check
that every edge arrives once, in the right direction and in the time window of
the `SetValue` that caused it, that intervals never run backwards at 400 kHz,
that the loss accounting is exact at 10 kHz, that `WithDebounce` suppresses
pulses shorter than its period, and that `StopWatching` and `Close` end a
watch. The burst tests use the first pair only, so put the direct
connection first. The race detector needs cgo and is not available in a cross
build.

The package example is compiled as documentation but deliberately carries no
`Output:` comment, so `go test` does not try to run it.

## Demos

Each demo under `demo/` is a separate Go module with
`replace github.com/womat/gpio => ../..`, so it always builds against the
working copy. `make` cross-compiles for a Raspberry Pi, `make deploy` copies
the binary over with `scp` — edit `PI_USER` and `PI_HOST` in the `Makefile`
first.

| Demo | Shows |
|---|---|
| `blinker` | Driving an output pin |
| `listener` | Watching edge events on an input pin |
| `wirecheck` | Checking the wiring of a test bench: output→input pairs (direct or through an optocoupler), cross-talk, sensor edges |

## Not addressed

Known and deliberately left alone:

- **CI does not test `rpi` against a line.** See above; the hardware tests run
  by hand on a Pi, the shared delivery logic is tested in CI, and the adapter
  around it is kept as thin as possible.
- **The chip device is a constant.** `rpi.Chip` is `"gpiochip0"`. A board that
  exposes its header elsewhere needs a code change.
- **The event buffer is fixed at 32.** `defaultBufferSize` in both backends;
  there is no option for it. A consumer that cannot keep up loses events; it
  learns about them through `DroppedEvents()` and `Event.Missed`, but cannot
  get them back.
- **One line per `Pin`.** `go-gpiocdev` can request and read a set of lines
  atomically; this wrapper does not expose that, so reading eight lines means
  eight syscalls and no guarantee they describe the same instant.
- **Mode, pull and debounce cannot be changed** after construction. Close the
  pin and request it again.
- **Invalid option values are ignored.** `WithMode` and `WithPullup` in both
  backends leave the default in place for an unknown value instead of failing,
  because an `Option` cannot return an error; changing that would change the
  option type for every consumer. So `ErrInvalidMode` and `ErrInvalidPullMode`
  are not used for an invalid option — `ErrInvalidMode` only reports a write
  to an input or a watch on an output, and `ErrInvalidPullMode` is never
  returned. It exists for symmetry with the other two.
- **`WithDebounce` makes an `rpi` pin an input.** `go-gpiocdev` requests a
  debounced line as an input, so `WithMode(gpio.Output)` followed by
  `WithDebounce` yields an input, and the reverse order drops the debounce.
  `rpi` follows that rather than reordering the options; `rpiemu` keeps the
  mode. Debounce is for inputs — do not combine the two.
- **`rpiemu` does not debounce like the kernel.** It locks out edges for the
  debounce period after each reported edge, so an edge within that time is
  never reported, even when the new level then holds longer than the period;
  the kernel reports the level that has settled. Rebuilding the kernel's
  behaviour needs a timer per pin and events that arrive after `Drive` has
  returned, which every test would then have to wait for. That is done once a
  consumer's test needs it, not before.
- **Watching the same `rpi` pin again right away can deliver an old edge.**
  `StopWatching` switches edge detection off, but events the kernel had
  queued or go-gpiocdev had read by then still arrive; a `WatchCh` right
  after can receive one, with a time from before the call. Measured on a
  Pi 3B+: 1–4 times in 100 restarts at 10 kHz; `Missed` stays right. Filtering
  them needs a lock around every delivered edge, for a case no service has:
  they stop watching only to shut down and request a new pin. A new pin is a
  new request to the kernel and never sees events of the old one; otherwise
  drop events older than the `WatchCh` call.
- **`StopWatching` and `Close` do not wait for a running callback.** They
  keep `WatchFunc` from starting on the events still buffered, but a call that
  already took its event can still be running, or just about to start, when
  they return. Waiting for it would deadlock a callback that stops its own
  watch, and telling that case apart needs the goroutine's identity, which Go
  does not expose. A consumer that tears down what its callback uses must
  synchronise with the callback itself.

Full API: `go doc github.com/womat/gpio`, `.../rpi`, `.../rpiemu`.
