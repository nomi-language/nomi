# Benchmarks

Nomi's benchmark suite: open-source cross-language benchmarks written in
idiomatic Nomi, a runner that times them against Go, and notes on where the
time goes.

| Path | What it holds |
|---|---|
| `game/` | The ten programs of [The Computer Language Benchmarks Game](https://benchmarksgame-team.pages.debian.net/benchmarksgame/), written from the benchmarks' published rules. |
| `game/expected/` | The published output files each program is checked against at its check size. |
| `game/go/` | The Benchmarks Game's Go programs, unchanged, with their licence: the baseline. A Go module of its own, so the compiler's `go build ./...` and `go vet ./...` skip it. |
| `awfy/` | [Are We Fast Yet](https://github.com/smarr/are-we-fast-yet)'s nine micro and five macro benchmarks, with its harness (`harness.nomi`) and licence. |
| `probes/` | Programs that time single operations (a call, an `Iter` helper on a tiny list, a `case`, `Map.get`). |
| `engines/` | The engine benchmarks `TestEngineBench` (vmhost) runs. |
| `run`, `runner/` | The runner. |
| `RESULTS.md` | The runner's last results table. |
| `FINDINGS.md` | Where the time goes in the slowest benchmarks, from CPU profiles. |

## Running

```bash
benchmarks/run
```

builds `nomi` from this checkout and the Go baselines, checks every program's
output, times each one under `nomi run` and as a `nomi build` executable, and
writes `RESULTS.md`. Its flags: `-runs N` (timed runs, 5; the median is
reported), `-warmup N` (untimed runs first, 1), `-only <regexp>` (benchmark
names), `-out <file>` (`-` prints the table), `-check` (output checks only).

A single program runs as any Nomi program does. A Benchmarks Game program
takes its size, and the FASTA readers read standard input:

```bash
nomi run benchmarks/game/nbody.nomi 1000
```

```bash
nomi run benchmarks/game/revcomp.nomi < benchmarks/game/expected/fasta-output.txt
```

An Are We Fast Yet program takes the suite's outer and inner iteration
counts and prints `verified` when its result checks out:

```bash
nomi run benchmarks/awfy/richards.nomi 1 10
```

## How the programs are written

The Benchmarks Game programs follow each benchmark's rules and print its
exact output. They are written as idiomatic Nomi: values are never updated
in place, state is threaded through folds and tail calls, and the
collections are Nomi's own (`List`, `Vector`, `Map`). The Haskell, OCaml and
Elixir programs are the model for the immutable approach.

The Are We Fast Yet programs follow the suite's
[porting guidelines](https://github.com/smarr/are-we-fast-yet/blob/master/docs/guidelines.md):
the same classes, method names and control flow as the reference
implementations. The guidelines assume mutable objects and fixed-size
arrays, which Nomi does not have, so each program's header comment lists
where it departs: the object graph is a value each operation returns anew, a
mutable array is a `Vector` replaced by `Vector.set`, and a loop is a fold or
a tail call.

## Profiling

`nomi run --profile` prints, when the program ends, where its time went by
Nomi function and by line; `--profile=<file>` also writes a pprof file
(`docs/tooling.md`, "Where the time goes: `--profile`"). A built executable
takes `NOMI_PROFILE=1` or `NOMI_PROFILE=<file>`:

```bash
nomi run --profile benchmarks/game/nbody.nomi 200000
```

For work on the VM itself, `NOMI_CPUPROFILE=<file>` writes a Go CPU profile
of the run, whose frames are the VM's Go functions (`internal/vm`) and the
runtime's (`rt`), not the program's:

```bash
NOMI_CPUPROFILE=nbody.prof nomi run benchmarks/game/nbody.nomi 200000
```

```bash
go tool pprof -top nomi nbody.prof
```
