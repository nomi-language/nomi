package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// An iterator's Display is rt's rendering of a `Seq`, which is what a
// `values:` row or a `defined as:` block prints for one.

// Runtime sequences stop with a Bool, not an error. This private carrier
// unwinds a failing VM callback to its driving instruction without replacing
// the original fault or mistaking it for an ordinary early stop.
type iterationFailure struct{ err error }

func (m *Machine) iterInstr(fr *frame, n *ir.Iter) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if failure, ok := p.(iterationFailure); ok {
				err = failure.err
				return
			}
			if failure, ok := p.(keyFailure); ok {
				err = failure.err
				return
			}
			if fault, ok := p.(*rt.Error); ok {
				err = &Fault{err: fault}
				return
			}
			panic(p)
		}
	}()
	if n.Signalling() && n.Op() != ir.IterMap && n.Op() != ir.IterFilter && n.Op() != ir.IterReduce &&
		n.Op() != ir.IterTakeWhile && n.Op() != ir.IterEach && n.Op() != ir.IterIterate {
		return fmt.Errorf("vm: signalling %s is not supported", n.Op())
	}
	src, err := fr.read(n.Arg(0))
	if err != nil {
		return err
	}
	if n.Op() == ir.IterFrom {
		start, ok := src.(int64)
		if !ok {
			return fmt.Errorf("vm: Iter.from reads %T, not an Int", src)
		}
		fr.write(n.Dst(), rt.SeqMap(rt.SeqFrom(start), func(_ *rt.Frame, i int64) any { return int64(i) }))
		return nil
	}
	if n.Op() == ir.IterRepeat {
		fr.write(n.Dst(), rt.SeqRepeat(src))
		return nil
	}
	if n.Op() == ir.IterIterate {
		f, err := iterCallback(fr, n, 1, 1)
		if err != nil {
			return err
		}
		if n.Signalling() {
			fr.write(n.Dst(), rt.SeqIterateCtl(src, func(runtime *rt.Frame, state any) (any, rt.Ctl) {
				next, ctl := adapterOutcome(m.iterInvoke(f, runtime, state))
				if ctl == rt.CtlSkip {
					panic(iterationFailure{fmt.Errorf("Iter.iterate: `continue` is not supported in the step callback (the step *is* the state-advancement; use Iter.filter to skip states)")})
				}
				return next, ctl
			}))
			return nil
		}
		fr.write(n.Dst(), rt.SeqIterate(src, func(runtime *rt.Frame, state any) any {
			return m.iterInvoke(f, runtime, state)
		}))
		return nil
	}
	if n.Op() == ir.IterKnownCount && n.Over() != ir.IterOverSeq {
		return knownCount(fr, n, src)
	}
	if n.Op() == ir.IterCount && n.Over() == ir.IterOverMap {
		m, ok := src.(vmap)
		if !ok {
			return fmt.Errorf("vm: map count reads %T, not a Map", src)
		}
		fr.write(n.Dst(), rt.MapSize(m))
		return nil
	}
	if n.Op() == ir.IterCount && n.Over() == ir.IterOverList {
		xs, ok := src.(*list)
		if !ok {
			return fmt.Errorf("vm: list count reads %T, not a list", src)
		}
		fr.write(n.Dst(), int64(rt.ListCellCount[any](xs)))
		return nil
	}
	if n.Op() == ir.IterView {
		if n.Over() == ir.IterOverRange {
			return m.rangeView(fr, n, src)
		}
		if n.Over() == ir.IterOverUserImpl {
			return m.userView(fr, n, src)
		}
		if n.Over() == ir.IterOverMap {
			m, ok := src.(vmap)
			if !ok {
				return fmt.Errorf("vm: map iteration reads %T, not a Map", src)
			}
			fr.write(n.Dst(), sourceView(rt.MapSeq(m, pair), src, func(*rt.Frame) rt.Maybe[int64] { return rt.MapKnownCount(m) }))
			return nil
		}
		if n.Over() == ir.IterOverSet {
			items, err := setItems(src)
			if err != nil {
				return err
			}
			fr.write(n.Dst(), sourceView(rt.MapKeysSeq(items), src, func(*rt.Frame) rt.Maybe[int64] { return rt.MapKnownCount(items) }))
			return nil
		}
		if n.Over() == ir.IterOverVector {
			v, ok := src.(rt.Vector[any])
			if !ok {
				return fmt.Errorf("vm: vector iteration reads %T, not a Vector", src)
			}
			fr.write(n.Dst(), sourceView(rt.VectorSeq(v), src, func(*rt.Frame) rt.Maybe[int64] { return rt.VectorKnownCount(v) }))
			return nil
		}
		if n.Over() == ir.IterOverBytes {
			data, ok := src.(rt.Bytes)
			if !ok {
				return fmt.Errorf("vm: byte iteration reads %T, not Bytes", src)
			}
			fr.write(n.Dst(), sourceView(rt.SeqMap(rt.BytesSeq(data), func(_ *rt.Frame, b rt.Byte) any { return b }), src, nil))
			return nil
		}
		if n.Over() == ir.IterOverString {
			text, ok := src.(string)
			if !ok {
				return fmt.Errorf("vm: string iteration reads %T, not a string", src)
			}
			fr.write(n.Dst(), sourceView(rt.SeqMap(rt.StringSeq(text), func(_ *rt.Frame, cluster string) any { return cluster }), src, nil))
			return nil
		}
		if n.Over() != ir.IterOverList {
			return fmt.Errorf("vm: iteration view requires a list domain")
		}
		xs, ok := src.(*list)
		if !ok {
			return fmt.Errorf("vm: iteration view reads %T, not a list", src)
		}
		fr.write(n.Dst(), sourceView(rt.ListCellSeq[any](xs), src, func(*rt.Frame) rt.Maybe[int64] { return rt.ListCellKnownCount[any](xs) }))
		return nil
	}
	seq, ok := src.(rt.Seq[any])
	if !ok {
		return fmt.Errorf("vm: iteration reads %T, not a sequence", src)
	}
	if m.fuel != nil {
		seq = m.metered(seq)
	}
	switch n.Op() {
	case ir.IterMap, ir.IterFilter:
		callback, err := fr.read(n.Arg(1))
		if err != nil {
			return err
		}
		f, ok := callback.(*functionValue)
		if !ok || f.arity != 1 {
			return fmt.Errorf("vm: %s requires a unary function", n.Op())
		}
		if n.Signalling() && n.Op() == ir.IterMap {
			fr.write(n.Dst(), rt.SeqMapCtl(seq, func(runtime *rt.Frame, item any) (any, rt.Ctl) {
				return adapterOutcome(m.iterInvoke(f, runtime, item))
			}))
		} else if n.Signalling() {
			fr.write(n.Dst(), rt.SeqFilterCtl(seq, func(runtime *rt.Frame, item any) (bool, rt.Ctl) {
				result, ctl := adapterOutcome(m.iterInvoke(f, runtime, item))
				if ctl == rt.CtlSkip || ctl == rt.CtlStop {
					return false, ctl
				}
				accepted, ok := asBool(result)
				if !ok {
					panic(iterationFailure{fmt.Errorf("vm: filter callback returned %T, not Bool", result)})
				}
				return accepted, ctl
			}))
		} else if n.Op() == ir.IterMap {
			fr.write(n.Dst(), rt.SeqMap(seq, func(runtime *rt.Frame, item any) any {
				return m.iterInvoke(f, runtime, item)
			}))
		} else {
			fr.write(n.Dst(), rt.SeqFilter(seq, func(runtime *rt.Frame, item any) bool {
				result := m.iterInvoke(f, runtime, item)
				accepted, ok := asBool(result)
				if !ok {
					panic(iterationFailure{fmt.Errorf("vm: filter callback returned %T, not Bool", result)})
				}
				return accepted
			}))
		}
	case ir.IterReduce:
		callback, err := fr.read(n.Arg(n.NumArgs() - 1))
		if err != nil {
			return err
		}
		f, ok := callback.(*functionValue)
		if !ok || f.arity != 2 {
			return fmt.Errorf("vm: reduce requires a binary function")
		}
		if n.Signalling() {
			fold := func(runtime *rt.Frame, acc, item any) (any, bool) {
				return reduceOutcome(m.iterInvoke(f, runtime, acc, item))
			}
			if n.NumArgs() == 2 {
				fr.write(n.Dst(), rt.SeqReduceFirstCtl(fr.runtime, seq, fold))
				return nil
			}
			seed, err := fr.read(n.Arg(1))
			if err != nil {
				return err
			}
			fr.write(n.Dst(), rt.SeqReduceCtl(fr.runtime, seq, seed, fold))
			return nil
		}
		fold := func(runtime *rt.Frame, acc, item any) any { return m.iterInvoke(f, runtime, acc, item) }
		if n.NumArgs() == 2 {
			fr.write(n.Dst(), rt.SeqReduceFirst(fr.runtime, seq, fold))
		} else {
			seed, err := fr.read(n.Arg(1))
			if err != nil {
				return err
			}
			fr.write(n.Dst(), rt.SeqReduce(fr.runtime, seq, seed, fold))
		}
	case ir.IterJoin:
		arg, err := fr.read(n.Arg(1))
		if err != nil {
			return err
		}
		separator, ok := arg.(string)
		if !ok {
			return fmt.Errorf("vm: String.join requires a String separator, not %T", arg)
		}
		text, ok := rt.SeqJoin(fr.runtime, seq, separator)
		if !ok {
			return fmt.Errorf("vm: String.join reads an element that is not a String")
		}
		fr.write(n.Dst(), text)
	case ir.IterTake:
		arg, err := fr.read(n.Arg(1))
		if err != nil {
			return err
		}
		count, ok := arg.(int64)
		if !ok {
			return fmt.Errorf("vm: take requires an Int count")
		}
		fr.write(n.Dst(), rt.SeqTake(seq, count))
	case ir.IterSortWith:
		arg, err := fr.read(n.Arg(1))
		if err != nil {
			return err
		}
		cmp, ok := arg.(*functionValue)
		if !ok || cmp == nil || cmp.arity != 2 {
			return fmt.Errorf("vm: sort_with requires a binary comparator")
		}
		desc, err := sortDescending(fr, n, 2)
		if err != nil {
			return err
		}
		out := rt.SeqSortWithCells[any, list](fr.runtime, seq, func(runtime *rt.Frame, a, b any) rt.Ordering {
			if desc {
				a, b = b, a
			}
			return iterOrdering(m.iterInvoke(cmp, runtime, a, b), "sort comparator")
		})
		fr.write(n.Dst(), out)
	case ir.IterSortBy:
		fns := [2]*functionValue{}
		for i := range fns {
			arg, err := fr.read(n.Arg(i + 1))
			if err != nil {
				return err
			}
			f, ok := arg.(*functionValue)
			if !ok || f == nil || f.arity != i+1 {
				return fmt.Errorf("vm: sort_by operand %d is %T, not its key function or ordering", i+1, arg)
			}
			fns[i] = f
		}
		key, cmp := fns[0], fns[1]
		desc, err := sortDescending(fr, n, 3)
		if err != nil {
			return err
		}
		out := rt.SeqSortWithCells[any, list](fr.runtime, seq, func(runtime *rt.Frame, a, b any) rt.Ordering {
			if desc {
				// std's Descending arm is `compare(key(b), key(a))`.
				a, b = b, a
			}
			ka := m.iterInvoke(key, runtime, a)
			kb := m.iterInvoke(key, runtime, b)
			return iterOrdering(m.iterInvoke(cmp, runtime, ka, kb), "sort_by ordering")
		})
		fr.write(n.Dst(), out)
	case ir.IterAny, ir.IterAll, ir.IterEachWhile:
		callback, err := fr.read(n.Arg(1))
		if err != nil {
			return err
		}
		f, ok := callback.(*functionValue)
		if !ok || f.arity != 1 {
			return fmt.Errorf("vm: %s requires a unary predicate", n.Op())
		}
		pred := func(runtime *rt.Frame, item any) bool {
			result := m.iterInvoke(f, runtime, item)
			b, ok := asBool(result)
			if !ok {
				panic(iterationFailure{fmt.Errorf("vm: %s predicate returned %T, not Bool", n.Op(), result)})
			}
			return b
		}
		switch n.Op() {
		case ir.IterAny:
			fr.write(n.Dst(), boolValue(rt.SeqAny(fr.runtime, seq, pred)))
		case ir.IterAll:
			fr.write(n.Dst(), boolValue(rt.SeqAll(fr.runtime, seq, pred)))
		default:
			fr.write(n.Dst(), boolValue(rt.SeqEachWhile(fr.runtime, seq, pred)))
		}
	case ir.IterFind:
		callback, err := fr.read(n.Arg(1))
		if err != nil {
			return err
		}
		f, ok := callback.(*functionValue)
		if !ok || f.arity != 1 {
			return fmt.Errorf("vm: find requires a unary predicate")
		}
		fr.write(n.Dst(), maybeOf(rt.SeqFind(fr.runtime, seq, func(runtime *rt.Frame, item any) bool {
			result := m.iterInvoke(f, runtime, item)
			b, ok := asBool(result)
			if !ok {
				panic(iterationFailure{fmt.Errorf("vm: find predicate returned %T, not Bool", result)})
			}
			return b
		})))
	case ir.IterAt:
		arg, err := fr.read(n.Arg(1))
		if err != nil {
			return err
		}
		index, ok := arg.(int64)
		if !ok {
			return fmt.Errorf("vm: Iter.at requires an Int index")
		}
		fr.write(n.Dst(), maybeOf(rt.SeqAt(fr.runtime, seq, index)))
	case ir.IterPartition:
		callback, err := fr.read(n.Arg(1))
		if err != nil {
			return err
		}
		f, ok := callback.(*functionValue)
		if !ok || f.arity != 1 {
			return fmt.Errorf("vm: partition requires a unary predicate")
		}
		fr.write(n.Dst(), rt.SeqPartitionCells[any, list](fr.runtime, seq, func(runtime *rt.Frame, item any) bool {
			result := m.iterInvoke(f, runtime, item)
			b, ok := asBool(result)
			if !ok {
				panic(iterationFailure{fmt.Errorf("vm: partition predicate returned %T, not Bool", result)})
			}
			return b
		}, func(yes, no *list) any {
			return pair(yes, no)
		}))
	case ir.IterGroupBy:
		callback, err := fr.read(n.Arg(1))
		if err != nil {
			return err
		}
		f, ok := callback.(*functionValue)
		if !ok || f.arity != 1 {
			return fmt.Errorf("vm: group_by requires a unary key function")
		}
		k := m.keysFor(fr)
		groups := rt.SeqGroupByCells[any, any, list](fr.runtime, seq, k.HashFunc(), k.EqualFunc(),
			func(runtime *rt.Frame, item any) any {
				return m.iterInvoke(f, runtime, item)
			}, func(xs *list) any { return xs })
		fr.write(n.Dst(), groups)
	case ir.IterFirst:
		fr.write(n.Dst(), maybeOf(rt.SeqFirst(fr.runtime, seq)))
	case ir.IterLast:
		fr.write(n.Dst(), maybeOf(rt.SeqLast(fr.runtime, seq)))
	case ir.IterKnownCount:
		fr.write(n.Dst(), countValue(rt.SeqKnownCount(fr.runtime, seq)))
	case ir.IterReverse:
		fr.write(n.Dst(), rt.SeqReverseCells[any, list](fr.runtime, seq))
	case ir.IterWithIndex:
		fr.write(n.Dst(), rt.SeqWithIndex(seq, func(i int64, x any) any {
			return pair(i, x)
		}))
	case ir.IterZip:
		arg, err := fr.read(n.Arg(1))
		if err != nil {
			return err
		}
		right, ok := arg.(rt.Seq[any])
		if !ok {
			return fmt.Errorf("vm: zip reads %T, not a sequence", arg)
		}
		fr.write(n.Dst(), rt.SeqZip(seq, right, func(a, b any) any {
			return pair(a, b)
		}))
	case ir.IterConcat:
		arg, err := fr.read(n.Arg(1))
		if err != nil {
			return err
		}
		right, ok := arg.(rt.Seq[any])
		if !ok {
			return fmt.Errorf("vm: concat reads %T, not a sequence", arg)
		}
		fr.write(n.Dst(), rt.SeqConcat(seq, right))
	case ir.IterCount:
		fr.write(n.Dst(), int64(rt.SeqCount(fr.runtime, seq)))
	case ir.IterEmpty:
		fr.write(n.Dst(), boolValue(rt.SeqEmpty(fr.runtime, seq)))
	case ir.IterNotEmpty:
		fr.write(n.Dst(), boolValue(rt.SeqNotEmpty(fr.runtime, seq)))
	case ir.IterToList:
		fr.write(n.Dst(), rt.SeqToListCells[any, list](fr.runtime, seq))
	case ir.IterDrop, ir.IterChunks:
		arg, err := fr.read(n.Arg(1))
		if err != nil {
			return err
		}
		count, ok := arg.(int64)
		if !ok {
			return fmt.Errorf("vm: %s requires an Int", n.Op())
		}
		if n.Op() == ir.IterDrop {
			fr.write(n.Dst(), rt.SeqDrop(seq, count))
		} else {
			fr.write(n.Dst(), rt.SeqMap(rt.SeqChunks(seq, count), func(_ *rt.Frame, xs *list) any { return xs }))
		}
	case ir.IterTakeWhile, ir.IterDropWhile:
		f, err := iterCallback(fr, n, 1, 1)
		if err != nil {
			return err
		}
		pred := func(runtime *rt.Frame, item any) bool {
			result := m.iterInvoke(f, runtime, item)
			b, ok := asBool(result)
			if !ok {
				panic(iterationFailure{fmt.Errorf("vm: %s predicate returned %T, not Bool", n.Op(), result)})
			}
			return b
		}
		if n.Signalling() {
			fr.write(n.Dst(), rt.SeqTakeWhileCtl(seq, func(runtime *rt.Frame, item any) (bool, rt.Ctl) {
				result, ctl := adapterOutcome(m.iterInvoke(f, runtime, item))
				if ctl == rt.CtlSkip || ctl == rt.CtlStop {
					return false, ctl
				}
				keep, ok := asBool(result)
				if !ok {
					panic(iterationFailure{fmt.Errorf("vm: take_while callback returned %T, not Bool", result)})
				}
				return keep, ctl
			}))
			return nil
		}
		if n.Op() == ir.IterTakeWhile {
			fr.write(n.Dst(), rt.SeqTakeWhile(seq, pred))
		} else {
			fr.write(n.Dst(), rt.SeqDropWhile(seq, pred))
		}
	case ir.IterCycle:
		fr.write(n.Dst(), rt.SeqCycle(seq))
	case ir.IterEach:
		f, err := iterCallback(fr, n, 1, 1)
		if err != nil {
			return err
		}
		if n.Signalling() {
			// std's `each` folds and discards: `break` stops the walk and
			// every other signal carries on.
			fr.write(n.Dst(), rt.SeqEachCtl(fr.runtime, seq, func(runtime *rt.Frame, item any) bool {
				_, ctl := adapterOutcome(m.iterInvoke(f, runtime, item))
				return ctl != rt.CtlStop && ctl != rt.CtlEmitStop
			}))
			return nil
		}
		fr.write(n.Dst(), rt.SeqEach(fr.runtime, seq, func(runtime *rt.Frame, item any) any {
			return m.iterInvoke(f, runtime, item)
		}))
	case ir.IterToSet:
		// `Iter.to_set`: members in first-occurrence
		// order, a repeated member keeping its first position.
		var items vmap
		k := m.keysFor(fr)
		seq.Run(fr.runtime, func(_ *rt.Frame, item any) bool {
			items = mapPut(k, items, item, true)
			return true
		})
		fr.write(n.Dst(), setFromMap(items))
	case ir.IterToVector:
		fr.write(n.Dst(), rt.SeqToVector(fr.runtime, seq))
	case ir.IterToMap:
		var failed error
		k := m.keysFor(fr)
		out := rt.SeqToMap(fr.runtime, seq, k.HashFunc(), k.EqualFunc(),
			func(item any) any { return pairPart(item, 0, &failed) },
			func(item any) any { return pairPart(item, 1, &failed) })
		if failed != nil {
			return failed
		}
		fr.write(n.Dst(), out)
	case ir.IterFlatMap, ir.IterFlatMapList:
		f, err := iterCallback(fr, n, 1, 1)
		if err != nil {
			return err
		}
		if n.Op() == ir.IterFlatMapList {
			fr.write(n.Dst(), rt.SeqFlatMapList(seq, func(runtime *rt.Frame, item any) *list {
				return iterListResult(m.iterInvoke(f, runtime, item))
			}))
		} else {
			fr.write(n.Dst(), rt.SeqFlatMap(seq, func(runtime *rt.Frame, item any) rt.Seq[any] {
				inner, ok := m.iterInvoke(f, runtime, item).(rt.Seq[any])
				if !ok {
					panic(iterationFailure{fmt.Errorf("vm: flat_map callback returned no sequence")})
				}
				return inner
			}))
		}
	case ir.IterFlatten, ir.IterFlattenList:
		var flat rt.Seq[any]
		if n.Op() == ir.IterFlattenList {
			flat = rt.SeqFlatMapList(seq, func(_ *rt.Frame, item any) *list { return iterListResult(item) })
		} else {
			flat = rt.SeqFlatMap(seq, func(_ *rt.Frame, item any) rt.Seq[any] {
				inner, ok := item.(rt.Seq[any])
				if !ok {
					panic(iterationFailure{fmt.Errorf("vm: flatten reads %T, not a sequence", item)})
				}
				return inner
			})
		}
		fr.write(n.Dst(), rt.SeqToListCells[any, list](fr.runtime, flat))
	case ir.IterChunkBy:
		f, err := iterCallback(fr, n, 1, 1)
		if err != nil {
			return err
		}
		chunks := rt.SeqChunkBy(seq, func(runtime *rt.Frame, item any) any {
			return m.iterInvoke(f, runtime, item)
		}, m.keysFor(fr).EqualFunc())
		fr.write(n.Dst(), rt.SeqMap(chunks, func(_ *rt.Frame, xs *list) any { return xs }))
	default:
		return fmt.Errorf("vm: iteration operation %s is not supported", n.Op())
	}
	return nil
}

func (m *Machine) iterInvoke(f *functionValue, runtime *rt.Frame, args ...any) any {
	// The operands do not outlive the call, so a driver's variadic slice
	// stays on its stack; only a host callable, which may keep them, gets a
	// copy.
	var v any
	var err error
	if f.host != nil {
		v, err = f.host(runtime, append([]any(nil), args...))
	} else {
		v, err = m.activateSlot(f.fnSlot(m), args, f.captures, runtime, false, m.depth, nil)
	}
	if err != nil {
		panic(iterationFailure{err})
	}
	return v
}

// rangeView walks a boxed Range with the element's linked `Comparable.compare`
// and `Discrete.next` bodies, through the runtime's own Range driver.
func (m *Machine) rangeView(fr *frame, n *ir.Iter, src any) error {
	r, err := rangeBacking(src)
	if err != nil {
		return err
	}
	fns := [2]*functionValue{}
	for i := range fns {
		v, err := fr.read(n.Arg(i + 1))
		if err != nil {
			return err
		}
		f, ok := v.(*functionValue)
		if !ok || f == nil || f.arity != 2-i {
			return fmt.Errorf("vm: range view operand %d is %T, not its walk function", i+1, v)
		}
		fns[i] = f
	}
	cmp, next := fns[0], fns[1]
	var count func(*rt.Frame) rt.Maybe[int64]
	if n.NumArgs() == 4 {
		// A view entering a declared `Iter<T>` position carries the
		// element's `Discrete.steps_between`, so it answers the Range's
		// known_count (rt.Seq's Count).
		v, err := fr.read(n.Arg(3))
		if err != nil {
			return err
		}
		steps, ok := v.(*functionValue)
		if !ok || steps == nil || steps.arity != 2 {
			return fmt.Errorf("vm: range view operand 3 is %T, not its distance function", v)
		}
		count = func(runtime *rt.Frame) rt.Maybe[int64] {
			return rt.RangeKnownCount(runtime, r, func(runtime *rt.Frame, a, b any) rt.Ordering {
				return iterOrdering(m.iterInvoke(cmp, runtime, a, b), "range comparator")
			}, func(runtime *rt.Frame, a, b any) rt.Maybe[int64] {
				return rangeSteps(m.iterInvoke(steps, runtime, a, b))
			})
		}
	}
	fr.write(n.Dst(), sourceView(rt.RangeSeq(r, func(runtime *rt.Frame, a, b any) rt.Ordering {
		return iterOrdering(m.iterInvoke(cmp, runtime, a, b), "range comparator")
	}, func(runtime *rt.Frame, v any) rt.Maybe[any] {
		res := m.iterInvoke(next, runtime, v)
		payload, isSome, ok := maybeParts(res)
		if !ok {
			panic(iterationFailure{fmt.Errorf("vm: range successor returned %T, want Maybe", res)})
		}
		if isSome {
			return rt.Maybe[any]{Tag: rt.TagSome, Some: payload}
		}
		return rt.Maybe[any]{Tag: rt.TagNone}
	}), src, count))
	return nil
}

// sourceView marks seq as the view of src: rt.Seq's Src, which Debug renders,
// and Count, the source's known_count override (nil for the default).
func sourceView(seq rt.Seq[any], src any, count func(*rt.Frame) rt.Maybe[int64]) rt.Seq[any] {
	seq.Src, seq.Count = src, count
	return seq
}

// userView walks a value whose own `impl Iter` declares its `each_while`,
// through rt.UserSeq. Each run hands the linked body the
// receiver and the driver's `yield` as a host-backed function value, so a
// consumer's stop crosses back out through the program's own recursion.
func (m *Machine) userView(fr *frame, n *ir.Iter, src any) error {
	v, err := fr.read(n.Arg(1))
	if err != nil {
		return err
	}
	each, ok := v.(*functionValue)
	if !ok || each == nil || each.arity != 2 {
		return fmt.Errorf("vm: user source view operand 1 is %T, not its each_while", v)
	}
	view := rt.UserSeq[any](src, func(runtime *rt.Frame, recv any, yield func(*rt.Frame, any) bool) bool {
		y := &functionValue{arity: 1, host: func(runtime *rt.Frame, args []any) (any, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("vm: each_while's yield takes 1 operand, got %d", len(args))
			}
			return boolValue(yield(runtime, args[0])), nil
		}}
		result := m.iterInvoke(each, runtime, recv, y)
		more, ok := asBool(result)
		if !ok {
			panic(iterationFailure{fmt.Errorf("vm: each_while returned %T, not Bool", result)})
		}
		return more
	})
	// The builder views only a source that inherits known_count's
	// declining default, so the view has no Count.
	fr.write(n.Dst(), sourceView(view, src, nil))
	return nil
}

// iterOrdering reads a callback's Ordering result inside a sequence driver.
func iterOrdering(v any, what string) rt.Ordering {
	ord, ok := orderingOf(v)
	if !ok {
		panic(iterationFailure{fmt.Errorf("vm: %s must return Ordering", what)})
	}
	return ord
}

// ctlValue is a signalling callback's answer when a control statement
// produced it: the value, if any, and what the calling iteration does next.
// It never escapes the driver that unwraps it.
type ctlValue struct {
	v   any
	ctl ir.Ctl
}

func (*ctlValue) OpaqueText() string { return "<control>" }

// adapterOutcome reads a map or filter callback's answer as rt.SeqMapCtl's
// (value, Ctl) pair. An ordinary return is CtlEmit.
func adapterOutcome(v any) (any, rt.Ctl) {
	c, ok := v.(*ctlValue)
	if !ok {
		return v, rt.CtlEmit
	}
	switch c.ctl {
	case ir.CtlSkip:
		return nil, rt.CtlSkip
	case ir.CtlEmitStop:
		return c.v, rt.CtlEmitStop
	case ir.CtlStop:
		return nil, rt.CtlStop
	}
	panic(iterationFailure{fmt.Errorf("vm: adapter callback answered %s", c.ctl)})
}

// reduceOutcome reads a reduce callback's answer as rt.SeqReduceCtl's
// (accumulator, keepGoing) pair. An ordinary return keeps going.
func reduceOutcome(v any) (any, bool) {
	c, ok := v.(*ctlValue)
	if !ok {
		return v, true
	}
	if c.ctl != ir.CtlEmitStop {
		panic(iterationFailure{fmt.Errorf("vm: reduce callback answered %s", c.ctl)})
	}
	return c.v, false
}

// knownCount answers `Iter.known_count` for a family the source's static kind
// resolved, through the rt function native names for that family.
func knownCount(fr *frame, n *ir.Iter, src any) error {
	var got rt.Maybe[int64]
	switch n.Over() {
	case ir.IterOverList:
		xs, ok := src.(*list)
		if !ok {
			return fmt.Errorf("vm: list known_count reads %T, not a list", src)
		}
		got = rt.ListCellKnownCount[any](xs)
	case ir.IterOverString:
		text, ok := src.(string)
		if !ok {
			return fmt.Errorf("vm: string known_count reads %T, not a String", src)
		}
		got = rt.StringKnownCount(text)
	case ir.IterOverVector:
		v, ok := src.(rt.Vector[any])
		if !ok {
			return fmt.Errorf("vm: vector known_count reads %T, not a Vector", src)
		}
		got = rt.VectorKnownCount(v)
	case ir.IterOverMap:
		m, ok := src.(vmap)
		if !ok {
			return fmt.Errorf("vm: map known_count reads %T, not a Map", src)
		}
		got = rt.MapKnownCount(m)
	case ir.IterOverSet:
		items, err := setItems(src)
		if err != nil {
			return err
		}
		got = rt.MapKnownCount(items)
	default:
		return fmt.Errorf("vm: known_count over %s is not supported", n.Over())
	}
	fr.write(n.Dst(), countValue(got))
	return nil
}

// countValue boxes a known count.
func countValue(m rt.Maybe[int64]) any {
	return maybeOf(m)
}

// pairDesc is the descriptor of an untyped pair a driver builds: a map
// entry, a partition's two lists, an index and its element, a zip.
var pairDesc = rt.TupleDesc(rt.SlotRef, rt.SlotRef)

// pair is the tuple `(a, b)`.
func pair(a, b any) any { return pairDesc.Make(a, b) }

// iterCallback reads operand i of n as a function value of the given arity.
func iterCallback(fr *frame, n *ir.Iter, i, arity int) (*functionValue, error) {
	arg, err := fr.read(n.Arg(i))
	if err != nil {
		return nil, err
	}
	f, ok := arg.(*functionValue)
	if !ok || f == nil || f.arity != arity {
		return nil, fmt.Errorf("vm: %s requires a function of %d parameter(s)", n.Op(), arity)
	}
	return f, nil
}

// iterListResult is a callback's List result; the empty list is nil.
func iterListResult(v any) *list {
	xs, ok := v.(*list)
	if !ok && v != nil {
		panic(iterationFailure{fmt.Errorf("vm: an iteration callback returned %T, not a List", v)})
	}
	return xs
}

// pairPart is component i of a (key, value) tuple, recording the first
// element that is not one.
func pairPart(item any, i int, failed *error) any {
	rec, ok := item.(*rt.Record)
	if !ok || rec == nil || rec.Desc.Kind != rt.KindTuple || rec.NumFields() != 2 {
		if *failed == nil {
			*failed = fmt.Errorf("vm: Iter.to_map reads %T, not a pair", item)
		}
		return nil
	}
	return rec.Field(i)
}

// sortDescending reads a sort's optional `Direction` operand at index i:
// true for `Descending`, false for `Ascending` or when there is none.
func sortDescending(fr *frame, n *ir.Iter, i int) (bool, error) {
	if n.NumArgs() <= i {
		return false, nil
	}
	arg, err := fr.read(n.Arg(i))
	if err != nil {
		return false, err
	}
	v, ok := enumRecord(arg)
	if !ok {
		return false, fmt.Errorf("vm: %s: sort direction is %T, not a Direction", fr.fn.Name(), arg)
	}
	return variantName(v) == "Descending", nil
}
