package irbuild

import (
	"maps"
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// `Hashable.hash` over a tuple. A tuple is Hashable structurally, as it is
// Equatable structurally (spec §Hashable on tuples): its hash is the derive
// mix over its slots, `Hashable.hash(t0) * 31 + Hashable.hash(t1) ...`, with
// wrapping arithmetic, which is what `derive Hashable` answers for a
// positional payload of the same slots without the variant index. Each
// slot's hash is its own type's `Hashable.hash`, so a slot of a declared type
// with a hand-written impl hashes through it.
//
// It is lowered as that expression: each slot is projected and bound to a
// name no program can spell, and the mix is lowered as the Nomi a derive
// would write. The synthesized nodes sit at column 0 of the call's line,
// where the checker records nothing, so no route reads the call's own
// checker facts for them.

// tupleHashCall lowers `Hashable.hash(x)` when x is a tuple. handled is false
// when the operand is not one, and the caller lowers the call as usual.
func (bl *irScalarBuilder) tupleHashCall(t *ast.Call) (v ir.Temp, k kind, mobile, ok, handled bool) {
	if len(t.Args) != 1 || namedArgNode(t.Args) != nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	ft := bl.g.checkedCallSignature(t)
	if ft == nil || len(ft.Params) != 1 || bl.g.project(ft.Params[0]).tag != tagTuple {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	subj, sk, _, sok := bl.lower(t.Args[0])
	if !sok || !irRetainedTupleKind(sk) {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	if bl.qualRecord == t {
		// In an assertion subject the tuple is the call's one row.
		bl.qualRecordArgs = irQualArgs{temps: []ir.Temp{subj}, kinds: []kind{sk}, mobile: []bool{true}, ok: true}
		bl.qualRecordSeen = true
	}
	// The synthesized slots have no source to name in a `values:` row.
	recording, qualRecord := bl.recording, bl.qualRecord
	bl.recording, bl.qualRecord = 0, nil
	defer func() { bl.recording, bl.qualRecord = recording, qualRecord }()
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	line := t.Line
	var mix ast.Node
	for i, part := range sk.comp.parts {
		name := "%hash" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + "." + strconv.Itoa(i)
		bl.patternBinding(t, name, bl.tupleProjection(t, subj, i, part), part)
		hash := &ast.Call{
			Func: &ast.FieldAccess{
				Object: &ast.TypeIdent{Name: "Hashable", Line: line},
				Field:  &ast.Ident{Name: "hash", Line: line},
				Line:   line,
			},
			Args: []ast.Node{&ast.Ident{Name: name, Line: line}},
			Line: line,
		}
		if mix == nil {
			mix = hash
			continue
		}
		mix = &ast.Binary{
			Left:     &ast.Binary{Left: mix, Op: "*", Right: &ast.IntLit{Value: 31, Line: line}, Wrapping: true, Line: line},
			Op:       "+",
			Right:    hash,
			Wrapping: true,
			Line:     line,
		}
	}
	v, k, mobile, ok = bl.lower(mix)
	if ok && k != kindInt {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	return v, k, mobile, ok, true
}
