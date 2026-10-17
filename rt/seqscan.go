package rt

// `Iter.scan` and `Iter.map_reduce`: the two folds that answer more than the
// final state. Both take their seed as an argument for SeqReduce's reason:
// the seed is the callback lambda's first-parameter default, which the call
// site lifts out, and it belongs to the callback rather than the source.
//
// A signalling callback answers rt's adapter outcome (seqctladapt.go), not
// reduce's `#(acc, keepGoing)`, because both functions emit per element and
// `continue` must emit nothing:
//
//	Ctl          scan                             map_reduce
//	CtlEmit      the new state, emitted           the output kept, the state advanced
//	CtlSkip      nothing emitted, state unchanged no output, state unchanged
//	CtlEmitStop  the new state emitted, then stop the output kept, the state advanced, then stop
//	CtlStop      stop, nothing emitted            stop with the outputs and state so far

// SeqScan is `Iter.scan(src, f)`: the running fold, one state per element,
// the seed itself not emitted. Each Run starts again from the seed, so the
// sequence replays as every Seq does.
func SeqScan[T, U any](src Seq[T], seed U, f func(fr *Frame, acc U, item T) U) Seq[U] {
	return Seq[U]{Run: func(fr *Frame, yield func(fr *Frame, item U) bool) bool {
		acc := seed
		return src.Run(fr, func(fr *Frame, item T) bool {
			acc = f(fr, acc, item)
			return yield(fr, acc)
		})
	}}
}

// SeqScanCtl is SeqScan whose callback can `break` or `continue`. Its Run
// answers `completed || ended` for the reason seqctladapt.go gives.
func SeqScanCtl[T, U any](src Seq[T], seed U, f func(fr *Frame, acc U, item T) (U, Ctl)) Seq[U] {
	return Seq[U]{Run: func(fr *Frame, yield func(fr *Frame, item U) bool) bool {
		acc := seed
		ended := false
		completed := src.Run(fr, func(fr *Frame, item T) bool {
			next, c := f(fr, acc, item)
			switch c {
			case CtlEmit:
				acc = next
				return yield(fr, acc)
			case CtlSkip:
				return true
			case CtlEmitStop:
				acc = next
				ended = yield(fr, acc)
				return false
			default:
				ended = true
				return false
			}
		})
		return completed || ended
	}}
}

// SeqMapReduceCells is `Iter.map_reduce(src, f)` over the consumer's cell
// type: f answers each element's output and the next state, and the result
// is the outputs in source order with the final state, built by pair.
func SeqMapReduceCells[T, U, A any, N ListCellShape[U, N], P any](fr *Frame, src Seq[T], seed A,
	f func(fr *Frame, acc A, item T) (U, A), pair func(*N, A) P) P {
	acc := seed
	var outs []U
	src.Run(fr, func(fr *Frame, item T) bool {
		var out U
		out, acc = f(fr, acc, item)
		outs = append(outs, out)
		return true
	})
	return pair(listCellsOf[U, N](outs), acc)
}

// SeqMapReduceCtlCells is SeqMapReduceCells whose callback can `break` or
// `continue`. f answers the output, the next state and the outcome; the
// output and state are read only for CtlEmit and CtlEmitStop.
func SeqMapReduceCtlCells[T, U, A any, N ListCellShape[U, N], P any](fr *Frame, src Seq[T], seed A,
	f func(fr *Frame, acc A, item T) (U, A, Ctl), pair func(*N, A) P) P {
	acc := seed
	var outs []U
	src.Run(fr, func(fr *Frame, item T) bool {
		out, next, c := f(fr, acc, item)
		switch c {
		case CtlEmit:
			outs, acc = append(outs, out), next
			return true
		case CtlSkip:
			return true
		case CtlEmitStop:
			outs, acc = append(outs, out), next
			return false
		default:
			return false
		}
	})
	return pair(listCellsOf[U, N](outs), acc)
}
