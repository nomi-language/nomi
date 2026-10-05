package rt

// The lazy adapters whose callback carries a control signal, and `Iter.each`.
//
// seqctl.go's header explains why `Iter.reduce` needs a value encoding at all —
// the callback is driven by the source's own `each_while`, so there is no Go
// loop for a Go `break` to target — and every word of it applies here. What is
// different is that a reduce callback has an accumulator to answer with, so two
// register returns `(acc, keepGoing)` say everything: `continue` and a bare
// `break` both answer the accumulator unchanged and differ only in the bool.
//
// An adapter has no accumulator. `continue` in a `map` callback must produce no
// element, and there is no value it could name instead — Nomi has no zero values,
// so the sentinel that would collapse this back to
// two returns does not exist. The signal therefore needs a third and fourth
// state, and `Ctl` below is exactly four:
//
//	Ctl        Nomi spelling          what the driver does
//	CtlEmit    tail / `return v`      use the value, ask the source for more
//	CtlSkip    `continue`             produce nothing, ask the source for more
//	CtlEmitStop`break v`              use the value, then stop the source
//	CtlStop    `break`                produce nothing, stop the source
//
// Four states rather than a struct or an error for the same reason seqctl.go
// gives for two: `(V, Ctl)` is two registers, so
// TestSeqAdapterCtlAllocatesNothingPerElement can pin the allocation count as
// independent of the element count, and internal/irbuild widens only the callbacks
// whose bodies can actually signal (internal/irbuild/ctrlflow.go's ctrlSignalIn).
// Every other lowered lambda keeps the bare Go func literal Go's inliner sees
// through.
//
// # What "use the value" means is per family, and it is not uniform
//
// This is the one place a shared encoding could quietly produce a wrong answer,
// so it is spelled out per family rather than generalised:
//
//   - in `map` the value is the output element. `break v` emits `v` and then
//     ends.
//   - in `filter` and `take_while` the value is the keep/drop decision about the
//     current input element, not an output element. `break True` keeps the
//     current item then stops; `break False` drops it then stops.
//   - `filter` and `take_while` then differ from each other on `CtlEmit false`:
//     filter skips the element and asks for another, take_while ends. That one
//     line is the whole difference between the two functions and it is why they
//     are two functions here rather than one with a flag.
//
// # The `Run` answer is `completed || ended`, and the distinction is load-bearing
//
// `Seq.Run` reports whether the source ran to exhaustion rather than being
// stopped by a consumer. A callback's `break` is not a downstream consumer
// refusing an element — it is this stage finishing — so it answers true.
// `ended` is set from the downstream yield's own answer in the emitting
// cases (`ended = yield(...)`), so a `break v` whose element the consumer
// refuses answers false and a `take(3)` upstream of everything still shuts the
// whole chain down. Getting that backwards is invisible in a one-stage pipeline
// and wrong in `concat(map(a, f), b)`.

// Ctl is a callback's control outcome in an adapter position.
type Ctl uint8

const (
	// CtlEmit is a tail expression or a `return v`: the value stands and the
	// source is asked for another element.
	CtlEmit Ctl = iota
	// CtlSkip is `continue`: no value, and the source is asked for another
	// element.
	CtlSkip
	// CtlEmitStop is `break v`: the value stands and then the source stops.
	CtlEmitStop
	// CtlStop is a bare `break`: no value, and the source stops.
	CtlStop
)

// SeqMapCtl is `Iter.map(src, f)` whose callback can `break` or `continue`.
//
// The value is the output element, so `break v` emits `v` as the final element.
func SeqMapCtl[T, U any](src Seq[T], f func(fr *Frame, item T) (U, Ctl)) Seq[U] {
	return Seq[U]{Run: func(fr *Frame, yield func(fr *Frame, item U) bool) bool {
		ended := false
		completed := src.Run(fr, func(fr *Frame, item T) bool {
			v, c := f(fr, item)
			switch c {
			case CtlEmit:
				return yield(fr, v)
			case CtlSkip:
				return true
			case CtlEmitStop:
				ended = yield(fr, v)
				return false
			default:
				ended = true
				return false
			}
		})
		return completed || ended
	}}
}

// SeqFilterCtl is `Iter.filter(src, pred)` whose predicate can `break` or
// `continue`.
//
// The value is the keep/drop decision about the current input element, never an
// output element — see the file header's per-family table.
func SeqFilterCtl[T any](src Seq[T], f func(fr *Frame, item T) (bool, Ctl)) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		ended := false
		completed := src.Run(fr, func(fr *Frame, item T) bool {
			keep, c := f(fr, item)
			switch c {
			case CtlEmit:
				if keep {
					return yield(fr, item)
				}
				return true
			case CtlSkip:
				return true
			case CtlEmitStop:
				if keep {
					ended = yield(fr, item)
					return false
				}
				ended = true
				return false
			default:
				ended = true
				return false
			}
		})
		return completed || ended
	}}
}

// SeqTakeWhileCtl is `Iter.take_while(src, pred)` whose predicate can `break` or
// `continue`.
//
// SeqFilterCtl differs on one line: a `CtlEmit false` here ends the sequence
// where filter skips the element and carries on. That is the whole of what
// distinguishes the two functions.
func SeqTakeWhileCtl[T any](src Seq[T], f func(fr *Frame, item T) (bool, Ctl)) Seq[T] {
	return Seq[T]{Run: func(fr *Frame, yield func(fr *Frame, item T) bool) bool {
		ended := false
		completed := src.Run(fr, func(fr *Frame, item T) bool {
			keep, c := f(fr, item)
			switch c {
			case CtlEmit:
				if keep {
					return yield(fr, item)
				}
				ended = true
				return false
			case CtlSkip:
				return true
			case CtlEmitStop:
				if keep {
					ended = yield(fr, item)
					return false
				}
				ended = true
				return false
			default:
				ended = true
				return false
			}
		})
		return completed || ended
	}}
}

// SeqEachCtl is `Iter.each(src, f)` whose callback can `break`, `continue` or
// `return`.
//
// Two registers would be one too many here, so the callback answers a bare
// `keepGoing`. That is not a simplification of the four-state encoding but a
// consequence of what `each` IS: std defines it as
// `case reduce(source, |_acc = 0, item| f(item)) { _ -> Unit }`
// (std/iter.nomi:235-239), so the accumulator is built and then discarded, and
// every signal reduces to "does the fold continue". `continue`, `return` and a
// tail all continue; `break` stops. The Unit answer is std's own `case … { _ ->
// Unit }`.
//
// Driven directly off `src.Run` rather than through SeqReduceCtl with a discarded
// accumulator, because a discarded accumulator would still need a type and a
// seed, and `each` has neither.
func SeqEachCtl[T any](fr *Frame, src Seq[T], f func(fr *Frame, item T) bool) Unit {
	src.Run(fr, f)
	return Unit{}
}
