# Where the time goes

Findings from CPU profiles of the slowest benchmarks, taken on `next` at
503e0bfeb on an Apple M2 Max, before the persistent vector landed; the
sections on vectors and on small lookups say what changed with it. The
ratios quoted here are from that first run. `RESULTS.md` holds the latest
run. Every
profile was taken with `NOMI_CPUPROFILE` and read with `go tool pprof`; the
frames are the VM's (`internal/vm`) and the runtime library's (`rt`).

The five slowest Benchmarks Game programs by ratio to Go are fannkuch-redux
(250×), mandelbrot (214×), n-body (134×), reverse-complement (84×) and
k-nucleotide (77×); spectral-norm (69×) is sixth and has a section too. Each
section below names the VM functions and rt operations that take the time,
with the share of the profile's samples.

On macOS the Go profiler charges time to a thread in a system call that the
call did not take. A first profile of reverse-complement put 66% of its
samples in the `write` system call under `io.print`, but the same program
with its output counted instead of printed ran in 2.54 s against 2.74 s, so
printing is 7% of it, and n-body's 31% in `runtime.kevent` is the
scheduler's idle wait. The sections below leave both out. Are We Fast Yet has no Go baseline in this suite; its
slowest programs (Havlak, CD, Sieve) are covered where they share a cause.

## Summary

| # | Hot spot | Where | Benchmarks |
|---|---|---|---|
| 1 | Interpreter dispatch: about 87 ns per iteration of a 15-operation float loop | `vm.(*Machine).loop`, `moveTailArgs`, `enterCode` | mandelbrot, every program |
| 2 | Allocation and GC: the collector runs on 2 to 3 more cores and takes 10 to 33% of samples | `runtime.gcBgMarkWorker`, `mallocgc` | k-nucleotide, spectral-norm, Sieve, Havlak, binary-trees |
| 3 | A struct update (`{..b, vx: v}`) takes the generic path: two slices, a field lookup by name, every scalar boxed | `vm.(*Machine).makeValue` → `rt.(*Record).With`, `rt.(*Layout).FieldIndex`, `vm.boxWord` | n-body, NBody, every state-threading program |
| 4 | Every generic construction (a cons, a list or vector literal, an update) runs under a `defer recoverKey` | `makeValue`'s `defer`: `runtime.(*_panic).nextDefer`, `deferprocStack` | fannkuch-redux, n-body |
| 5 | A host call looks its function up by name and passes `[]any` | `vm.(*Machine).crossInto`: `m.hosts[name]` (`runtime.mapaccess1_faststr`) | fannkuch-redux, k-nucleotide |
| 6 | `Iter.with_index` and `Iter.zip` allocate a tuple record per element | `vm.pair` → `rt.allocRecord` | spectral-norm |
| 7 | An Int range in a pipeline calls two Nomi functions per element and allocates a `Some` | `vm.(*Machine).rangeView` (its comparator and successor closures) | mandelbrot, spectral-norm, binary-trees |
| 8 | `Vector.set` and `Vector.push` copied the whole vector into a new `[]any` (fixed on `next` by the persistent vector, 2346db27c) | `rt.VectorSet`, `rt.VectorPush` | Havlak, Sieve |
| 9 | `Iter.windows` counts and copies the window for every element | `std/iter.nomi` `windows`/`slide` | k-nucleotide |
| 10 | Text walked a grapheme cluster at a time: a UAX #29 step and a new string per character | `rt.EachGraphemeCluster`, `uniseg`, `utf8.DecodeRuneInString` | reverse-complement, k-nucleotide |

Items 3 to 7 are fixed costs on paths a compiler can specialise: none of
them is needed for the semantics. Item 8 is fixed. Item 1 is the price of
the bytecode design itself.

## fannkuch-redux

3.37 s of samples at n = 9. `vm.(*Machine).loop` is 74% cumulative and 21%
flat; the GC mark workers are 9%.

- `vm.(*Machine).irInstr`, the VM's generic instruction path, is 44%
  cumulative. Under it, `callInstr` (17%) is host calls and `makeValue`
  (14%) is list construction.
- `makeValue` here is `[x, ..front]` in `prefix_reversed` and
  `[head, ..insert_at(...)]` in `insert_at`: `vm.cons` and
  `rt.ConsCell` (8%). A cons is one allocation, but it goes through the
  generic path: `frame.read` boxes each operand, and the function's
  `defer recoverKey(&err)` costs `runtime.(*_panic).nextDefer` plus
  `deferprocStack`, 30% of `makeValue`'s own time.
- `crossInto` (14%) is `List.concat` (`vm.listHost`, `rt.ListCellConcat`,
  7%) and `Vector.get`/`Vector.set` (`vm.vectorHost`, 4%), and 13% of it is
  `runtime.mapaccess1_faststr`: `crossInto` finds the host function with
  `m.hosts[name]` on every call (`internal/vm/vm.go:1040`), and the
  arguments travel as a fresh `[]any`.
- `moveTailArgs` is 4%: the recursive helpers are tail calls.

## mandelbrot

2.71 s of samples at n = 1000. `vm.(*Machine).loop` is 49% flat, the
highest of any profile: this program is almost all arithmetic.

- The escape test `bounded` runs 25.4 million times (counted with an
  equivalent Go loop) in 2.2 s of VM time: 87 ns per iteration of about 15
  bytecode operations (two multiplies, an add and compare, two branches,
  four arithmetic operations for the next point, a tail call with five
  arguments). Go runs the same iteration in about 1 ns.
- `moveTailArgs` (10%) and `enterCode` (4%) are the tail call: each
  iteration moves five arguments through the generic argument path.
- The pixel loop is an `Iter.reduce` over `0..8` inside an `Iter.map` over
  the bytes of a row, so each pixel crosses from Go back into Nomi through
  `iterInvoke`, and each element of the `0..8` range calls the range's
  successor (`rangeView.func3`, 4%, allocating a `Some` with
  `rt.(*TypeDesc).NewVariant`) and comparator (`rangeView.func2`, 1.5%)
  as Nomi functions.

## n-body

3.14 s of samples at n = 200,000; 31% of them are `runtime.kevent` under
`runtime.netpoll`, the scheduler's idle wait, which the macOS profiler
charges to an idle thread; leaving it out, the VM is 64% of the rest.

- `makeValue` is 17% of all samples, and it is the struct updates
  `{..b, vx: .. - dx * om, ...}`: 10 pairs a step, two updates a pair, and
  a move per body. `rt.(*Record).With` (7%) clones the 7-field record,
  which is two allocations, since a record with more than 4 words gets its
  word slice separately (`rt.allocRecord`). `makeValue` itself allocates
  an index slice and a value slice per update (`runtime.makeslice`), looks
  each field up by name (`rt.(*Layout).FieldIndex`, a string compare per
  field, 2%), and reads each new value through `frame.read`, which boxes a
  `Float` into an `any` (`vm.boxWord`, 4%) only for `setSlot` to unbox it
  again. The `defer` in `makeValue` is another 2.5%.
- The cost probe measures one `{..p, x: i}` at 180 ns
  (`benchmarks/probes/costs.nomi`). A struct literal does not pay this:
  it compiles to `opMake`, which writes each register straight into its
  slot. An update with its field indices and slot kinds resolved at
  compile time could take the same path.
- `iterInstr` → `rt.SeqMapReduceCells` (26%) is the pair loop's
  `Iter.map_reduce`, one closure call per pair from Go back into the VM.

## reverse-complement

2.62 s of samples on the fasta 1,000,000 input (10 million bases on 166,671 lines), profiled with the output
counted rather than printed (see above). The VM is 71%.

- Every base is a grapheme cluster: `String.reverse` splits the joined
  sequence into clusters, and `Iter.map(complement)` walks it again through
  `rt.EachGraphemeCluster`, which decodes runes (`utf8.DecodeRuneInString`,
  10% flat) and asks `uniseg` for each boundary, and hands each base to the
  VM as a new one-character string (`mallocgcSmallScanNoHeader`, 11%).
- `complement` is a `case` of 16 arms over 31 string patterns
  (`"A" or "a" -> "T"`), each pattern a string compare (`runtime.memequal`, 5%) on the VM's loop, called
  once per base: 10 million calls from Go back into Nomi.
- `Iter.chunks(60)` then builds a 60-element list per line and
  `String.join` copies it into the line.

The language has no byte-level text type a program like this can map over:
`Bytes` has `get`, `slice` and `to_list`, and no `map`. The Go program
works on a `[]byte` with a 256-entry table.

## spectral-norm

1.99 s of samples at n = 400. The VM is 64%; `mallocgc` is 23% and the GC
mark workers 13%.

- `vm.pair` → `rt.allocRecord` is 13% of samples: every element of
  `Iter.with_index(u)` and `Iter.zip(a, b)` is a new tuple record, built
  only to be taken apart by the lambda's `(j, uj)` pattern. The program
  makes 2 × 10 × 2 × 400 × 400 = 6.4 million of them.
- The inner sum is `Iter.reduce` over that pipeline, so each of the 6.4
  million steps is a closure call from Go (`iterInvoke`) plus a call to
  `a(i, j)`; `regStack.push`, `newFrame` and `codeOf` are the frame set-up
  each one pays.

## k-nucleotide

3.10 s of samples over 2.13 s of wall time at a 25,000-line input: the GC
runs beside the program. `runtime.gcBgMarkWorker` is 29% and `mallocgc`
16%; the VM's own work is 35%.

- `Iter.windows(k)` is written in Nomi (`std/iter.nomi`, `windows` and
  `slide`): for every element it counts the window twice (`Iter.count`,
  O(k)) and rebuilds it with `List.concat(rest, [item])` (O(k) cells), so
  a frame of length 18 allocates 18 cons cells per base. `String.join`
  then builds the key.
- `Iter.frequencies` is `Map.get` then `Map.put` per element:
  `rt.mapInsert` is 7%, under `crossInto`'s by-name lookup again.
- Before any of that, the sequence is `String.join` of the lines and then
  `Iter.to_list` of its grapheme clusters, one string per base.

## Havlak, Sieve, CD and DeltaBlue: `Vector.set` copies

The persistent vector on `next` (2346db27c) fixed this. At the same sizes,
in the second run of `RESULTS.md`:

| Benchmark | Copying vector | Persistent vector |
|---|---:|---:|
| Havlak, inner 1 | 44.1 s | 7.7 s |
| Sieve, inner 10 | 2.44 s | 0.11 s |
| CD, 250 aircraft | 6.6 s | 6.5 s |
| DeltaBlue, 3000 | 1.7 s | 2.2 s |

CD and DeltaBlue did not get faster: CD's vectors are short, and
DeltaBlue's port keeps its graph in maps, not vectors. The second run was
on a machine with a load average of about 9, so its smaller differences
are noise. A probe run under that load read `Vector.set` on a 6-element
vector at 880 ns, which looked like a regression from the copy's 435 ns.
It was the load: on a quiet machine, with rt allocating a vector of up to
32 elements in one piece (b86cafaae), it is 283 to 307 ns over three runs
of `benchmarks/probes/costs.nomi`, faster than the copy was.

What follows is the profile from before the fix. `rt.VectorSet` and
`rt.VectorPush` copied the whole backing `[]any`. Sieve at 10 inner iterations has 289% CPU
utilisation: the copies themselves are cheap (`runtime.memmove`, 1%), but
each `Vector.set` on its 5,000 flags allocates an 80 KB `[]any` that the
collector must scan, so `gcBgMarkWorker` is 31% and the threads it
coordinates (`usleep`, `pthread_kill`, `pthread_cond_wait`) most of the
rest. Havlak does tens of thousands of `Vector.set`s on 5,213-element
vectors per loop search and takes 44 s even at inner = 1. A
`Vector<Bool>` or `Vector<Int>` also stores each element as an `any`, 16
bytes and a pointer the collector scans, where a `[]bool` would be 1 byte.
The persistent vector on c64e429ac fixes the copying; the boxing stays.

## Small lookups and small allocations

The Lox interpreter's remaining cost on `next` with the persistent vector
is per operation: `Map.get` on a small map, `Vector.get`, and an `Ok` and a
tuple per evaluated expression. Each was profiled on its own: five million
iterations of a tail-recursive loop whose step does one operation, against
the same loop with an empty step (`benchmarks/probes/costs.nomi` has the
same operations).

| Operation, over the empty loop | Time | Allocations |
|---|---:|---|
| `Map.get` on 100 entries, matched | 197 ns | 4: the argument slice, the boxed key, the `Some` record and its payload slice |
| `Vector.get` on 100 entries, matched | 156 ns | 4, as `Map.get` |
| `Ok(i)` built in Nomi and matched | 105 ns | 1: the variant record, its `Int` in the record's word bank |
| `(i, i)` returned and taken apart | 118 ns | 1: the tuple record |

The times are from `benchmarks/probes/costs.nomi` on a quiet machine; the
profiles' shares below were taken while other test runs shared it.

- **The lookup itself is cheap.** `rt.VectorGet` is about 4 ns of the
  160, and `rt.MapLookup` (the trie walk and `rt.(*Keys).Equal`) about
  14 ns of the 200.
- **The `Some` is the biggest single cost.** The host returns an
  `rt.Maybe`, and `vm.maybeOf` → `vm.some` → `rt.(*TypeDesc).MakeVariant`
  → `rt.allocRecord` builds the prelude's `Maybe` record. Its payload is an
  `any`, so the variant takes a reference slot, and `allocRecord`
  allocates the slot slice (`r.R = make([]any, l.NR)`) beside the record:
  two allocations for one `Some(Int)`. This path is 14 to 16% of each
  profile, and the collector work it causes (`gcBgMarkWorker`) about as
  much again. A `Maybe<Int>` built by Nomi code (`opMake`) puts the `Int`
  in the word bank in one allocation; the host path could use the
  variant's typed layout the same way, or, when the caller matches the
  result at once, return the tag and payload in registers with no record
  at all.
- **The host call is generic.** `vm.(*Machine).callInstr` →
  `invoke` → `crossInto` builds the arguments as a new `[]any`
  (`vm.callOperands`, `runtime.makeslice`), boxes the `Int` key into an
  `any` (`vm.boxWord`, an allocation for values outside Go's small-integer
  cache), and finds the host function with `m.hosts[name]`
  (`runtime.mapaccess1_faststr`) on every call. That is 9 to 10% of the
  `Map.get` profile before the host function starts. `Map.get` and
  `Vector.get` could be bytecode instructions, as `opMake` is for a struct
  literal: read the collection and key from registers, look up, and write
  the payload and tag to registers.
- **`Ok` and the tuple are one allocation each**, through `opMake`
  (`rt.(*TypeDesc).New` or `NewVariant`, then `rt.allocRecord`). Their
  cost is that allocation, about 13% of each profile, and the collector
  work after it, another 14 to 17%. The tuple's elements are then read
  back through `vm.projValue` and `rt.(*Record).Field` on the generic
  `irInstr` path (6%). A function returning a small tuple or `Result` that
  its caller takes apart at once could return the elements in registers
  (multiple return values) and skip the record.
- **The loop around them** is the call and return: `moveTailArgs`,
  `enterCode`, `regStack.push`/`pop`, about 10 to 13% of each profile.
  This is the 22 to 35 ns a plain call costs.

So the 160 to 200 ns of a small `get` is roughly a quarter allocation
(`Some` and the argument slice), a quarter the collection of that garbage,
a fifth the generic host-call path, and under a tenth the lookup.

## The Lox measurements, checked

`benchmarks/probes/costs.nomi` times each operation in a tail-recursive loop
of a million iterations and subtracts the loop's own cost (106 ns per
iteration, most of it the call through a closure).

| Operation | Lox app's figure | Measured here |
|---|---|---|
| Plain call `inc(i)` | 30 ns | 35 ns |
| `Iter.map([], f) \|> Iter.to_list()` | 0.5 to 2 µs | 755 ns |
| `Iter.map([i], f) \|> Iter.to_list()` | | 891 ns |
| `Iter.map([i, i], f) \|> Iter.to_list()` | | 1,225 ns |
| `Iter.reduce([i, i], 0, +)` | | 638 ns |
| `Iter.any_match([i, i], > 5)` | | 498 ns |
| `Iter.sum([i, i])` | | 602 ns |
| `Iter.count([i, i])` | | 180 ns |
| `case (a, b)` over four tuple arms | 280 ns more than nested | 162 ns, 101 ns more than nested `case`s (61 ns) |
| `Map.get`, 10 entries | 300 ns | 183 ns |
| `Map.get`, 3M entries | 1.3 µs | 1,123 ns |
| `Map.put`, 10 entries | | 417 ns |
| `Vector.set`, 6 entries | | 435 ns |
| Struct update `{..p, x: i}` | | 180 ns |
| A `[i, i]` literal, matched | | 121 ns |

- **Iter helpers on tiny inputs: confirmed.** 0.5 to 1.2 µs on zero to two
  elements, 15 to 35 plain calls' worth. `Iter.map` over an empty list
  still costs 755 ns, which is building the `Seq` closure and running the
  `to_list` driver over it; each element then adds about 230 ns, the
  callback's crossing from Go back into Nomi and the cons. `Iter.count`
  on a list answers from its known length and is cheap.
- **Tuple `case`: corrected.** About 100 ns more than nested `case`s on
  this machine, not 280 ns. A profile of ten million `case (a, b)` matches
  shows why: the subject is allocated as a tuple record (`opMake`,
  `frame.fill`, `mallocgc`), and each arm reads its elements through
  `vm.projValue` on the generic `irInstr` path, boxing them through
  `frame.read` and `frame.write`. Matching the two scrutinees directly,
  without building the tuple, would remove both.
- **`Map.get`: confirmed in shape, a little lower.** 183 ns on 10 entries
  (the figure includes the `case` on the `Maybe`) and 1.1 µs on 3 million,
  where the HAMT walk misses the cache at each level.

## Bugs found while porting

Each was reported to the compiler's maintainers with a reproducer. The
three are fixed on `next`, and the ports no longer work around them.

- **A bare self-call in a generic impl is refused** (fixed). Inside `impl Tree<K>`,
  a function with a `where` bound that calls itself as `has(left, key)` is
  accepted by the front end and refused by `nomi run` with "this call to
  `has` is not supported yet". `Tree.has(left, key)` works.
- **`String.slice` is O(length of the string) per call** (fixed: a
  left-to-right scan is now linear).
  `rt.StringSlice` splits the whole string into grapheme clusters on
  every call, so reading 20,000 characters one `slice` at a time took
  6 s.
- **A parameter written as its type alone did not parse when the type had
  type arguments** (fixed). `fn f(Request)` worked; `fn f(Vector<Int>, i:
  Int)`, `fn f(List<Int>)` and `fn f(Map<Int, Int>)` were "expected ')'
  after parameters".
- **Smaller.** `0..<n` reports "range requires a right-hand operand;
  open-ended ranges have been removed", which does not say that `0..n` is
  the exclusive range. `typealias X = T` reports "expected type name, got
  `=`" without suggesting `typealias X T`. A one-line lambda whose body
  discards a value, `fn _ -> _ = f(x)`, does not parse; the form with
  `end` does.

## A profiler for Nomi users

`nomi run --profile` is built (`docs/tooling.md`, "Where the time goes:
`--profile`"; `internal/vm/profile.go`). It reports time by Nomi function
and by line, marks std functions and host functions such as `Map.get`, and
with `--profile=<file>` writes a pprof file for `go tool pprof`'s graph and
flame graph. A built executable takes `NOMI_PROFILE`.

How it works, and where it departs from the first sketch:

- **No new check in the loop.** A profiling machine gets a fuel that
  watches nothing, so the samples are taken at the calls and backward
  branches every fuel already burns at, plus at each activation's return
  and each host call's return. A machine that is not profiling has no fuel
  and tests one field (`Machine.hooked`) where a debug machine's paths
  already branched. Against `next` without the profiler, plain runs of
  mandelbrot, n-body, List and fib are within 0.4% (alternating runs,
  medians of 12). A profiled run is 3 to 7% slower, plus about 4 ms to
  start the sampler and print the table.
- **Ticks, not a flag.** A timer adds a tick each millisecond and the next
  safe point takes them all, so a host call that runs for 50 ms is charged
  50 ms when it returns.
- **The logical stack without the debugger.** A bytecode call runs on its
  caller's register stack, so the callers are the frames below. A callback
  run from Go gets a stack of its own, which a profiling machine links to
  the frame that started it. A tail call from the program into std keeps
  the program's function under the std one, so a function ending in a
  pipeline still shows. A caller is shown by its function, not its line,
  since a plain machine does not save a caller's pc.
- **The collector** is reported as its CPU total from `runtime/metrics`,
  since it runs beside the program.

Its first profile of fannkuch-redux puts 53% of the time in
`prefix_reversed`, 7.5% in `List.concat` and 3% in `Vector.set`, which the
Go profile above only showed as `makeValue`, `rt.ConsCell` and `crossInto`.
