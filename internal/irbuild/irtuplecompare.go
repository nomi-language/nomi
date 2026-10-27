package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// `Comparable` over a tuple. A tuple is Comparable when every element is
// (spec §Comparable on tuples, internal/analysis/comparable_structural.go):
// it compares lexicographically, the first elements by their own type's
// `Comparable.compare`, and the next pair only when those are Equal. So
// `#(1, "b") < #(2, "a")`, and a later element's compare never runs once an
// earlier one decides.
//
// Three routes reach it: `Comparable.compare(a, b)` on two tuples
// (tupleCompareCall), an ordering operator on them (compareOperands), and a
// comparator taken as a function value (`Iter.sort` of tuples, `Iter.sort_by`
// with a tuple key: sortComparator and comparatorOperand, tupleComparator).
// Each lowers the same chain, tupleOrdering: per element its compare, then a
// branch on whether that answer is Less or Greater, which is the answer, or
// Equal, which goes on to the next element. The last element's answer is
// the tuple's.

// tupleCompareCall lowers `Comparable.compare(a, b)` when both operands are
// tuples. handled is false when they are not, and the caller lowers the call
// as usual.
func (bl *irScalarBuilder) tupleCompareCall(t *ast.Call) (v ir.Temp, k kind, mobile, ok, handled bool) {
	if len(t.Args) != 2 || namedArgNode(t.Args) != nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	ft := bl.g.checkedCallSignature(t)
	if ft == nil || len(ft.Params) != 2 || bl.g.project(ft.Params[0]).tag != tagTuple {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok || args.kinds[0] != args.kinds[1] || !irRetainedTupleKind(args.kinds[0]) {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	if bl.qualRecord == t {
		// In an assertion subject the two tuples are the call's rows.
		bl.qualRecordArgs = args
		bl.qualRecordSeen = true
	}
	v, ok = bl.tupleOrdering(t, args.temps[0], args.temps[1], args.kinds[0])
	if !ok {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	return v, stdEnumKind(stdEnumOrdering), true, true, true
}

// tupleRankedCompare lowers `<`, `<=`, `>` or `>=` on two tuples: their
// Ordering, then its rank.
func (bl *irScalarBuilder) tupleRankedCompare(t *ast.Binary, op ir.CompareOp, lhs, rhs ir.Temp, k kind) (ir.Temp, kind, bool, bool) {
	ordered, ok := bl.tupleOrdering(t, lhs, rhs, k)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	c := ir.NewCompareRank(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), op, ordered)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindBool})
	return c.Dst(), kindBool, false, true
}

// tupleComparable reports whether every element of tuple k has a compare
// the builder can name, at any depth of tuples.
func (bl *irScalarBuilder) tupleComparable(k kind) bool {
	if !irRetainedTupleKind(k) {
		return false
	}
	for _, part := range k.comp.parts {
		if part.tag == tagTuple {
			if !bl.tupleComparable(part) {
				return false
			}
			continue
		}
		if _, _, _, ok := bl.sortComparator(part); !ok {
			return false
		}
	}
	return true
}

// tupleOrdering answers the Ordering of two tuples of kind k, lhs and rhs,
// in a temporary the chain's blocks write; the builder continues in the
// block after it.
func (bl *irScalarBuilder) tupleOrdering(at ast.Node, lhs, rhs ir.Temp, k kind) (ir.Temp, bool) {
	if !bl.tupleComparable(k) {
		return ir.NoTemp, false
	}
	pos := bl.g.irNodePos(at)
	ord := stdEnumKind(stdEnumOrdering)
	dst := bl.f.NewTemp()
	done := bl.f.NewBlock(pos, "tuple compare done")
	last := len(k.comp.parts) - 1
	for i, part := range k.comp.parts {
		a := bl.tupleProjection(at, lhs, i, part)
		b := bl.tupleProjection(at, rhs, i, part)
		c, ok := bl.elemOrdering(at, a, b, part)
		if !ok {
			return ir.NoTemp, false
		}
		if i == last {
			bl.b.Append(ir.NewCopy(pos, dst, c))
			bl.b.SetTerm(ir.NewJump(pos, done.ID()))
			break
		}
		decided := bl.f.NewBlock(pos, "tuple compare decided")
		decided.Append(ir.NewCopy(pos, dst, c))
		decided.SetTerm(ir.NewJump(pos, done.ID()))
		notLess := bl.f.NewBlock(pos, "tuple compare not less")
		next := bl.f.NewBlock(pos, "tuple compare next")
		lt := ir.NewCompareRank(pos, bl.f.NewTemp(), ir.OpLt, c)
		bl.b.Append(lt)
		bl.side(lt.Dst(), irScalarSide{k: kindBool})
		bl.b.SetTerm(ir.NewBranch(pos, lt.Dst(), decided.ID(), notLess.ID()))
		bl.b = notLess
		gt := ir.NewCompareRank(pos, bl.f.NewTemp(), ir.OpGt, c)
		bl.b.Append(gt)
		bl.side(gt.Dst(), irScalarSide{k: kindBool})
		bl.b.SetTerm(ir.NewBranch(pos, gt.Dst(), decided.ID(), next.ID()))
		bl.b = next
	}
	bl.b = done
	bl.side(dst, irScalarSide{k: ord})
	return dst, true
}

// elemOrdering answers the Ordering of two values of one tuple element's
// kind: a nested tuple's chain, or a direct call to the compare sortComparator
// chooses for it.
func (bl *irScalarBuilder) elemOrdering(at ast.Node, a, b ir.Temp, k kind) (ir.Temp, bool) {
	if k.tag == tagTuple {
		return bl.tupleOrdering(at, a, b, k)
	}
	local, sibling, std, ok := bl.sortComparator(k)
	if !ok {
		return ir.NoTemp, false
	}
	pos := bl.g.irNodePos(at)
	var call *ir.Call
	switch {
	case sibling != nil:
		call = ir.NewCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, sibling, a, b)
	case local != nil:
		call = ir.NewCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, bl.g.irCalleeSym(local, k.nomi()+"."+local.name), a, b)
	case std.irBody == nil:
		call = ir.NewHostCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, bl.g.irCalleeSym(std, std.key), a, b)
	default:
		call = ir.NewCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, std.irBody.Sym(), a, b)
	}
	bl.b.Append(call)
	bl.side(call.Dst(), irScalarSide{k: stdEnumKind(stdEnumOrdering), deferrable: true})
	return call.Dst(), true
}

// tupleComparator is tuple k's compare as a function value, `(k, k) ->
// Ordering`, for an operation that takes its comparator as one (`Iter.sort`).
func (bl *irScalarBuilder) tupleComparator(at ast.Node, k kind) ir.Temp {
	pos := bl.g.irNodePos(at)
	ord := stdEnumKind(stdEnumOrdering)
	f := ir.NewFunc(pos, "tuple compare")
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(pos, "entry")
	child := &irScalarBuilder{g: bl.g, sh: sh, f: f, b: sh.entry, parent: bl, bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	a := bl.g.irAddParam(f, ir.NewSymbol("a"), k)
	b := bl.g.irAddParam(f, ir.NewSymbol("b"), k)
	c, ok := child.tupleOrdering(at, a, b, k)
	if !ok {
		// sortComparator admitted k only when tupleComparable holds, and
		// tupleOrdering declines for no other reason.
		panic("irbuild: tupleComparator over a tuple tupleComparable refused: " + k.nomi())
	}
	child.b.SetTerm(ir.NewReturn(pos, c))
	fv := ir.NewFuncValue(pos, bl.f.NewTemp(), f)
	bl.b.Append(fv)
	bl.side(fv.Dst(), irScalarSide{k: funcKindIn(bl.g, []kind{k, k}, ord)})
	return fv.Dst()
}
