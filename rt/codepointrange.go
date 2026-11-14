package rt

// CodepointRangeSeq is RangeSeq over a `Range<Codepoint>` with std's
// Codepoint impls run in Go, as IntRangeSeq is for Int: `Comparable.compare`
// orders the scalar values, `Discrete.next` steps over the UTF-16 surrogate
// block (0xD800–0xDFFF) and saturates at 0x10FFFF, and Count is std's
// `known_count` (`steps_between`, which leaves the surrogates out, plus one
// for an inclusive end). A walk calls no Nomi function and builds no Maybe
// per element. Its ends and its elements are Codepoint records: one word
// slot over the descriptor the range's start carries.
func CodepointRangeSeq(r Range[Value]) Seq[Value] {
	return Seq[Value]{Run: func(fr *Frame, yield func(fr *Frame, item Value) bool) bool {
		return codepointRangeEachWhile(fr, r, yield)
	}, Count: func(*Frame) Maybe[int64] { return codepointRangeKnownCount(r) }, Endless: rangeEndless(r)}
}

const (
	codepointMax        = 0x10FFFF
	codepointBeforeGap  = 0xD7FF // the last scalar value below the surrogates
	codepointAfterGap   = 0xE000 // the first above them
	codepointGapCodes   = codepointAfterGap - codepointBeforeGap - 1
	codepointNotAScalar = -1
)

// codepointOf is a Codepoint record's scalar value.
func codepointOf(v Value) (*Record, int64) {
	rec, ok := v.(*Record)
	if !ok || rec == nil || len(rec.W) != 1 {
		return nil, codepointNotAScalar
	}
	return rec, int64(rec.W[0])
}

// codepointRangeEachWhile is RangeEachWhile with std's Codepoint `compare`
// and `next` built in: the bound is checked before each step, and the
// largest scalar value, whose `next` is None, is yielded once more as the
// last element.
func codepointRangeEachWhile(fr *Frame, r Range[Value], yield func(fr *Frame, item Value) bool) bool {
	start, cur := codepointOf(r.Start)
	if start == nil {
		panic("rt: CodepointRangeSeq over a start that is no Codepoint")
	}
	end, bounded := int64(0), r.End.Tag == TagSome
	if bounded {
		var endRec *Record
		if endRec, end = codepointOf(r.End.Some); endRec == nil {
			panic("rt: CodepointRangeSeq over an end that is no Codepoint")
		}
	}
	// The start is yielded as it is; each later element is a new record.
	item := Value(start)
	for {
		if bounded && (cur > end || (cur == end && !r.Inclusive)) {
			return true
		}
		if cur == codepointMax {
			return yield(fr, item)
		}
		if !yield(fr, item) {
			return false
		}
		if cur == codepointBeforeGap {
			cur = codepointAfterGap
		} else {
			cur++
		}
		rec := start.Desc.New()
		rec.W[0] = uint64(cur)
		item = rec
	}
}

// codepointRangeKnownCount is RangeKnownCount with std's Codepoint
// `steps_between`: the distance between the two scalar values, less the
// surrogate block when the range spans it.
func codepointRangeKnownCount(r Range[Value]) Maybe[int64] {
	if r.End.Tag != TagSome {
		return None[int64]()
	}
	_, a := codepointOf(r.Start)
	_, b := codepointOf(r.End.Some)
	if a > b {
		return Some(int64(0))
	}
	n := b - a
	if a <= codepointBeforeGap && b >= codepointAfterGap {
		n -= codepointGapCodes
	}
	if r.Inclusive {
		n++
	}
	return Some(n)
}
