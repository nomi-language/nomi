package vm

import (
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// Lent tuples: a pairing stage (`Iter.with_index`, `Iter.zip`, a Map's
// entries) builds a tuple per element, and the commonest consumer takes it
// apart at once (`fn sum, (j, uj) -> ...`). When the consumer is done with
// each element by the time its callback returns, the stage hands every
// element the same tuple, rewritten (rt.Seq's Reusing), so a run allocates
// one tuple instead of one per element.
//
// The consumer decides, when the pipeline is built (lendsItems). It must be
// a stage whose element goes no further than its callback (map, reduce,
// scan, map_reduce, flat_map, each, any_match, all_match, each_while) or
// a terminal that keeps only the parts (to_map, count, is_empty), and the
// callback must read its element only through tuple projections
// (borrowsParam): a body that returns the tuple, stores it, captures it,
// matches on it or passes it on keeps a tuple of its own. A debug machine
// never lends, since a stopped frame shows its parameters.

// pairing is a pairing stage's sequence: fresh is the stage with a new
// tuple per element, and each walks the stage's elements as a tuple
// constructor would see them. Its Reusing form builds one tuple per run.
func pairing(fresh rt.Seq[any], each func(fr *rt.Frame, pair func(a, b any) any, yield func(*rt.Frame, any) bool) bool) rt.Seq[any] {
	fresh.Reusing = func() rt.Seq[any] {
		out := fresh
		out.Reusing = nil
		out.Run = func(fr *rt.Frame, yield func(*rt.Frame, any) bool) bool {
			p := pairDesc.New()
			return each(fr, func(a, b any) any {
				p.R[0], p.R[1] = a, b
				return p
			}, yield)
		}
		return out
	}
	return fresh
}

// withIndexSeq is `Iter.with_index(seq)`.
func withIndexSeq(seq rt.Seq[any]) rt.Seq[any] {
	return pairing(rt.SeqWithIndex(seq, func(i int64, x any) any { return pair(i, x) }),
		func(fr *rt.Frame, mk func(a, b any) any, yield func(*rt.Frame, any) bool) bool {
			return rt.WithIndexEach(fr, seq, func(i int64, x any) any { return mk(i, x) }, yield)
		})
}

// zipSeq is `Iter.zip(left, right)`.
func zipSeq(left, right rt.Seq[any]) rt.Seq[any] {
	cursor := directCursor(right)
	return pairing(rt.SeqZipCursor(left, right, cursor, pair),
		func(fr *rt.Frame, mk func(a, b any) any, yield func(*rt.Frame, any) bool) bool {
			return rt.SeqZipCursor(left, right, cursor, mk).Run(fr, yield)
		})
}

// directCursor makes a cursor over seq's elements that steps its source
// directly, when seq is the view of a List, a Vector or a `Range<Int>`
// (its Src, which sourceView sets), and answers nil for any other sequence,
// which zip pulls through a coroutine.
func directCursor(seq rt.Seq[any]) func() func() (any, bool) {
	switch src := seq.Src.(type) {
	case *list:
		return func() func() (any, bool) { return rt.ListCellCursor[any, list](src) }
	case rt.Vector[any]:
		return func() func() (any, bool) {
			i := 0
			return func() (any, bool) {
				if i >= src.Len() {
					return nil, false
				}
				i++
				return src.At(i - 1), true
			}
		}
	case *rt.Record:
		r, err := rangeBacking(src)
		if err != nil || !rt.IsInt(r.Start) || (r.End.Tag == rt.TagSome && !rt.IsInt(r.End.Some)) {
			return nil
		}
		return func() func() (any, bool) { return rt.IntRangeCursor(r) }
	}
	return nil
}

// mapEntriesSeq is a Map viewed as its `(K, V)` pairs.
func mapEntriesSeq(m vmap) rt.Seq[any] {
	return pairing(rt.MapSeq(m, pair),
		func(fr *rt.Frame, mk func(a, b any) any, yield func(*rt.Frame, any) bool) bool {
			return rt.MapEachWhile(fr, m, mk, yield)
		})
}

// lendsItems reports whether n, consuming a sequence, is done with each
// element when the element's yield returns, so a pairing source may lend
// it one tuple for the whole run.
func (m *Machine) lendsItems(fr *frame, n *ir.Iter) bool {
	if m.dbg != nil {
		return false
	}
	callback, param := -1, 0
	switch n.Op() {
	case ir.IterToMap, ir.IterCount, ir.IterEmpty:
		return true
	case ir.IterMap, ir.IterFlatMap, ir.IterFlatMapList, ir.IterEach, ir.IterAny, ir.IterAll, ir.IterEachWhile:
		callback = 1
	case ir.IterReduce:
		callback, param = n.NumArgs()-1, 1
	case ir.IterScan, ir.IterMapReduce:
		callback, param = 2, 1
	default:
		return false
	}
	if callback >= n.NumArgs() {
		return false
	}
	v, err := fr.read(n.Arg(callback))
	if err != nil {
		return false
	}
	f, ok := v.(*functionValue)
	if !ok || f == nil || f.host != nil || f.body == nil {
		return false
	}
	return m.codes.borrowsParam(f.body, param)
}

// borrowsParam reports whether f reads parameter i only as the subject of
// tuple projections, so the tuple it is given is never kept past the call.
func (t *codeTable) borrowsParam(f *ir.Func, i int) bool {
	if v, ok := t.borrows.Load(f); ok {
		return v.(uint64)&(1<<i) != 0
	}
	var mask uint64
	for j := range f.Params() {
		if j < 64 && paramOnlyProjected(f, j) {
			mask |= 1 << j
		}
	}
	t.borrows.Store(f, mask)
	return mask&(1<<i) != 0
}

func paramOnlyProjected(f *ir.Func, i int) bool {
	p := f.Params()[i]
	var uses []ir.Temp
	projected := false
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if r, ok := in.(*ir.Ref); ok && r.Kind() == ir.RefLocal && r.Sym() == p.Sym {
				// A read of the parameter by name holds the tuple itself.
				return false
			}
			uses = in.AppendUses(uses[:0])
			for _, u := range uses {
				if u != p.Temp {
					continue
				}
				pr, ok := in.(*ir.Proj)
				if !ok || pr.Kind() != ir.ProjSlot {
					return false
				}
				projected = true
			}
		}
		if t := b.Term(); t != nil {
			for _, u := range t.AppendUses(uses[:0]) {
				if u == p.Temp {
					return false
				}
			}
		}
	}
	return projected
}
