package rt

// `Iter.reduce` whose callback carries a control signal.
//
// This is the one place in the push protocol where a Nomi `break` has to
// become a value: the callback is driven by the source's own `each_while`, so
// the caller has no loop of its own to leave. Only these two functions
// exist, rather than a control-carrying variant of every terminal.
//
// The encoding is a second return value, `(acc, keepGoing)`, and it is chosen
// against two alternatives:
//
//   - a sentinel accumulator value, which needs a value no accumulator can be.
//     Nomi has no zero values, so there is none.
//   - a control struct, or widening every callback to return a signal,
//     which every reduce would pay for and one would use. The VM calls
//     SeqReduceCtl only for a reduce whose callback can produce a signal
//     (internal/vm/iter.go), and every other reduce keeps the plain
//     `func(fr, acc, item) U`.
//
// Two register returns cost nothing per element: TestSeqCtlAllocatesNothingPerElement
// pins the count as independent of the element count, which is the same
// statement TestSeqAllocatesNothingPerElement makes about the protocol itself
// and the only form of it that distinguishes "allocates once per pipeline"
// (fine) from "allocates once per element" (a regression in the push
// protocol's main cost property).
//
// The mapping from a callback's control flow, one line each:
//
//	return v / tail value  ->  (v, true)
//	a Unit tail            ->  (acc, true)
//	continue               ->  (acc, true)
//	break v                ->  (v, false)
//	break                  ->  (acc, false)

// SeqReduceCtl folds src from seed with a callback that may stop the fold.
//
// The `false` answer stops the source, not merely this stage: it is returned
// straight out of the yield, which is what `Iter.take` relies on and what makes
// `break` over an unbounded source terminate. The accumulator at the moment of
// the stop is the answer.
func SeqReduceCtl[T, U any](fr *Frame, src Seq[T], seed U,
	f func(fr *Frame, acc U, item T) (U, bool)) U {
	acc := seed
	src.Run(fr, func(fr *Frame, item T) bool {
		next, keep := f(fr, acc, item)
		acc = next
		return keep
	})
	return acc
}
