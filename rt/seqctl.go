package rt

// `Iter.reduce` whose callback carries a control signal.
//
// This is the one place in the push protocol where a Nomi `break` really has to
// become a value: the callback is driven by the source's own `each_while`, so
// there is no Go loop for a Go `break` to target. `Iter.loop` is not like this —
// it has no source at all and its callback is inlined into a real `for` — which
// is why only these two functions exist rather than a control-carrying variant
// of every terminal.
//
// The encoding is a second return value, `(acc, keepGoing)`, and it is chosen
// against two alternatives:
//
//   - a sentinel accumulator value, which needs a value no accumulator can be.
//     Nomi has no zero values, so there is none.
//   - a control struct, or widening every lowered lambda to return a signal.
//     internal/irbuild/lambda.go emits a bare Go func literal deliberately, so
//     Go's inliner and devirtualizer see through it; changing the
//     representation of every Nomi lambda to serve one caller pays everywhere
//     and buys in one place. internal/irbuild therefore specializes the callback
//     shape only where the body can actually produce a signal, decided
//     lexically, and everything else keeps the plain `func(fr, acc, item) U`.
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

// SeqReduceFirstCtl is SeqReduceCtl for a callback with no seed, where the
// first element seeds the accumulator and the callback never runs for it.
//
// An empty source faults with the same text as the seedless fold without
// control flow, from the same place — there is one encoding of that message
// (see emptyReduceText).
//
// U and T are one type by construction here, as they are in SeqReduceFirst: a
// fold the first element seeds has an accumulator of the element's type.
func SeqReduceFirstCtl[T any](fr *Frame, src Seq[T],
	f func(fr *Frame, acc T, item T) (T, bool)) T {
	var acc T
	seeded := false
	src.Run(fr, func(fr *Frame, item T) bool {
		if !seeded {
			acc, seeded = item, true
			return true
		}
		next, keep := f(fr, acc, item)
		acc = next
		return keep
	})
	if !seeded {
		Trap(emptyReduceText())
	}
	return acc
}
