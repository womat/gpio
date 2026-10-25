# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository layout

`github.com/womat/gpio` is a GPIO abstraction for Raspberry Pi services, split out of
`github.com/womat/golib` (where it lived as `golib/gpio`) with its history. The copy in golib
is deprecated and removed there in golib v2; consumers (`tadl`, `s0meter`, `relayhat`) move
over one by one.

Everything under `demo/` is a **separate Go module** with its own `go.mod` and a
`replace github.com/womat/gpio => ../..`, so demos always build against the local working copy.
`go build ./...` at the root ignores the demos.

## Commands

```sh
go test -race . ./rpiemu/ ./internal/...   # everything that builds on macOS
GOOS=linux go vet ./...                     # includes rpi
scripts/check.sh                            # go fix, go mod tidy, vet, golangci-lint, govulncheck, all modules
scripts/check.sh --check --skip=vuln        # report only, no network
```

`rpi` wraps `go-gpiocdev` and only compiles on Linux; on macOS `go vet ./...` /
`go build ./...` fail there. `scripts/check.sh` defaults to `GOOS=linux` for that reason —
prefer it over running the tools by hand.

Demos: `cd demo/<name>` first, then use its `Makefile`; `make deploy` scp's the binary to
`$(PI_USER)@$(PI_HOST)`.

There is one branch, `main`; a version is an annotated tag `vX.Y.Z` on it. Only the tag, no
GitHub release: the tag message is the release notes (Wolfgang, 23.10.2026).

**This repository always authenticates as `womat`.** The remote goes through the
`github-priv` host alias from `~/.ssh/config`; a fresh clone needs
`git remote set-url origin git@github-priv:womat/gpio.git`.

## Architecture

`gpio` defines the `Pin` interface (levels, modes, pulls, edge events) and contains no
hardware code. Two implementations satisfy it: `rpi` (Linux character device via
go-gpiocdev) and `rpiemu` (in-memory emulator for tests and development on non-Pi machines).
Code that consumes GPIO should depend on `gpio.Pin`, never on a backend. Mode, pull resistor
and debounce are fixed at construction through each backend's options (`WithX(...)`) — the
interface has no setters.

The emulator adds `Drive(level)`, which simulates the outside world changing the line and
works on input pins, which `SetValue` deliberately refuses. That is how receiving code is
tested without hardware; `rpiemu.NewPin` returns `rpiemu.Pin`, which embeds `gpio.Pin`.

Both backends share `internal/watch`: the channel a consumer ranges over, the edge mask, drop
counting and the shutdown that closes the channel. `gpio.Event.Missed` counts the edges lost
right before an event (dropped in Go, or in the kernel as seen by `rpi` through the line's
sequence number). The package exists because that logic is where the concurrency bugs live,
and `rpi` itself cannot be tested off a Pi — keeping delivery there down to a one-line call
means the untestable part is a thin adapter over code that is tested under `-race`.

## Conventions

- The package doc comment with a runnable `# Example usage` section lives in the package's
  main file, and `README.md` covers all three packages; keep both updated when the API
  changes. The README ends with a *Not addressed* section — decisions against a fix go there.
- Docs and comments do not point to packages in other repositories as examples
  ("such as the decoder in …"); say what kind of code it is ("a line decoder").
  Such packages move — the Manchester decoder went from golib to
  `womat/manchester` — and the pointer goes stale unnoticed.
- Sentinel errors are exported vars prefixed `gpio:`, compared with `errors.Is`. Don't change
  an existing message — services consume this module.
- Every `Pin` has an explicit `Close()` that callers must defer.
- Commit subjects follow Conventional Commits, `type(scope): description`, types `feat`,
  `fix`, `docu`, `chore`, `refactor`.

## Known state

`rpi` has tests without a line only for its pure helpers (`eventTime`, `seqGap`), the event
`handler` and a failing `NewPin`, which build on Linux alone and therefore run in CI only;
anything touching a line needs a real GPIO chip. Tests that do skip unless told which lines to
use, so they run on a Pi only: the other tests in `close_test.go` need `RPI_TEST_LINE` (a free
line), `loopback_test.go` needs `RPI_TEST_LOOP` (wired output:input
pairs, direct connection first). `GOOS=linux GOARCH=arm64 go test -c ./rpi`, copy the binary
over, `RPI_TEST_LINE=20 RPI_TEST_LOOP=18:25 ./rpi.test -test.v`. Test against the direct
connection 18→25 (18 output); the optocouplers (13→6, 12→26) are good for 1 kHz only
(Wolfgang, 23.10.2026) and may be appended for the slow edge test. The bench is the
Pi 3B+ `pi3b` (ssh alias, user `pv`), wired as `demo/wirecheck` describes. Do not assert exact
edge counts at or above 10 kHz (edges per second): the Pi merges faster edges before the kernel numbers them (measured
in `TestLoopbackBurst`'s comment), and at 10 kHz about one run in 100 loses edges unseen,
so `TestLoopbackPaced` fails now and then — known and open, not a regression (review 2026-10,
section 1, *Still open*).
Its example is compiled but carries no `Output:` comment, so `go test` does not try to run it.

Linter configuration is `.golangci.yml`; the demo modules find it by walking up. `errcheck`
ignores `Pin.Close` (callers defer it) and test files. **Do not widen these exclusions to make a
new finding go away; fix the finding or record the decision.**

**CI:** `.github/workflows/ci.yml`, on pushes to `main`, pull requests, weekly and by hand, runs
on Linux. `setup-go` takes the Go version from `go.mod` with `check-latest`, so the weekly run
catches a toolchain advisory without a commit. golangci-lint and govulncheck are pinned in
`ci.yml` and in `scripts/check.sh`; raise both together.
Actions are pinned to a commit SHA with the release in a comment; dependabot updates the pins
and the Go dependencies of all modules. Jobs: `format`, `checks`
(`scripts/check.sh --check`, including vet), `library` (`go test -race -cover`), `cross` (four
Linux targets besides the amd64 the library job builds — Windows and macOS are impossible because of `rpi`), `demos`. All modules
declare `go 1.27`, the same as golib and its consumers; keep them in step.
