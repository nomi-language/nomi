package irbuild

// A list, vector, set or map literal with spreads that is not one of the
// forms with a lowering of their own: `[..xs, y]`, `[x, ..v, y]`,
// `[..s, ..t]`, `{"a" => 1, ..m}`. The forms with one spread where it
// shares the existing collection keep theirs: a list's spread written last
// (`[x, ..xs]`, the elements consed onto it, listMakeOf), a vector's at
// either end (vectorUpdate), and a set's or map's written first
// (`[..s, x]`, setUpdate; `{..m, k => v}`, mapUpdate).
//
// A spread in a list, vector or set literal may be any Iter of the
// literal's elements (an Option, a Set in a list, an Iter chain): it is
// materialized as the literal's collection where it is lowered, with
// `Iter.to_list`, `Iter.to_vector` or `Iter.to_set` (collectSpread), and
// then joined as a spread of the collection is.
//
// Here the literal is cut into parts, each spread and each run of elements
// between them, and the parts are joined in the order written with the
// collection's own join: `List.concat`, `Vector.concat`, `Set.union` or
// `Map.merge`, so a later map key replaces an earlier one's value, as `+`
// does. The spreads are evaluated first, from left to right, then the
// elements from left to right, as in the one-spread forms.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// spreadJoin is a collection literal's shape (a List, Vector, Set or Map),
// which a literal is built as (literalKind), and the collection's join, for
// the literals with spreads.
type spreadJoin struct {
	// owner and method name the join: `List.concat`.
	owner, method string
	// typed reports whether k is a typed collection of the literal's
	// shape, which fixes the literal's kind.
	typed func(k kind) bool
	// fromRight joins the last two parts first, as a List is joined, so
	// each element is copied once.
	fromRight bool
	// collect is the `Iter` function that makes another Iter a collection
	// of the shape: `to_list`. It is empty for a map, whose spreads are
	// maps.
	collect string
	// seq is the shape as the checker names a bracket literal's collection
	// (ast.ListLit.Kind), for a shape with collect.
	seq ast.SeqKind
}

var (
	listSpreadJoin = spreadJoin{
		owner:  "List",
		method: "concat",
		typed:  func(k kind) bool { return k.tag == tagList },
		// A List is cons cells: `List.concat(a, b)` copies a and shares b.
		fromRight: true,
		collect:   "to_list",
		seq:       ast.SeqList,
	}
	vectorSpreadJoin = spreadJoin{
		owner:  "Vector",
		method: "concat",
		typed: func(k kind) bool {
			_, ok := vectorElem(k)
			return ok
		},
		collect: "to_vector",
		seq:     ast.SeqVector,
	}
	setSpreadJoin = spreadJoin{
		owner:  "Set",
		method: "union",
		typed: func(k kind) bool {
			_, ok := setElem(k)
			return ok
		},
		collect: "to_set",
		seq:     ast.SeqSet,
	}
	mapSpreadJoin = spreadJoin{
		owner:  "Map",
		method: "merge",
		typed: func(k kind) bool {
			return k.tag == tagMap && k.comp != nil && len(k.comp.parts) == 2
		},
	}
)

// spreadPart is one part of a literal with spreads, lowered: a spread's
// collection or a run of elements built as a literal of its own. at is the
// node it is reported at.
type spreadPart struct {
	at ast.Node
	v  ir.Temp
	k  kind
}

// spreadHeads lowers the heads of the spreads of the literal at from left
// to right, each made a collection of the literal's (collectSpread).
func (bl *irScalarBuilder) spreadHeads(at ast.Node, spreads []ast.Spread, join spreadJoin) ([]spreadPart, bool) {
	out := make([]spreadPart, len(spreads))
	for i, sp := range spreads {
		v, k, _, ok := bl.lower(sp.Head)
		if !ok {
			return nil, false
		}
		if out[i], ok = bl.collectSpread(at, spreadPart{at: sp.Head, v: v, k: k}, join); !ok {
			return nil, false
		}
	}
	return out, true
}

// collectSpread answers the spread p of the literal at, of join's shape, as
// a collection of the literal's: a spread of the shape as it is, and any
// other Iter (an Option, a Range, a Set in a list, an Iter chain)
// materialized with join's `Iter` function, `Iter.to_list(p)`, as the
// checker allows (spreadElemType). A typed
// spread enters the kind the checker gave the literal (literalKind) as a
// value enters any position of that kind: in `ws: List<Display> =
// [..xs, "a"]` the `List<Int>` is a `List<Display>` before anything is
// joined to it. Every literal with a spread takes its spreads from here,
// so none reads its own kind off a spread the checker typed it wider than.
func (bl *irScalarBuilder) collectSpread(at ast.Node, p spreadPart, join spreadJoin) (spreadPart, bool) {
	switch {
	case join.collect == "" || join.typed(p.k):
	default:
		line, col := nodePos(p.at)
		call := &ast.Call{Line: line, Col: col, Args: []ast.Node{p.at}}
		// The call stands for the spread collected: the collection of the
		// elements the checker recorded the spread walks.
		defer bl.g.carryType(call, bl.g.srcFA().SpreadCollected(join.seq, p.at))()
		args := irQualArgs{temps: []ir.Temp{p.v}, kinds: []kind{p.k}, mobile: []bool{true}, ok: true}
		v, k, _, ok := bl.iterCall(call, args, join.collect, nil)
		if !ok || !join.typed(k) {
			bl.g.dc.note("a spread of kind " + p.k.nomi() + " that Iter." + join.collect + " does not make a collection")
			return spreadPart{}, false
		}
		p = spreadPart{at: p.at, v: v, k: k}
	}
	// kindInvalid: sentinel — no position's kind; a spread enters the record alone.
	if k, recorded := bl.literalKind(at, join, kindInvalid); recorded && join.typed(p.k) && p.k != k {
		v, _, ok := bl.convertTo(p.at, p.v, p.k, k)
		if !ok {
			bl.g.dc.note("a spread of kind " + p.k.nomi() + " in a literal of kind " + k.nomi())
			return spreadPart{}, false
		}
		p.v, p.k = v, k
	}
	return p, true
}

// literalKind is the kind a collection literal at of join's shape is built
// at: the type the checker's records give its value (convertedKind), its
// own type (`[c, shape]` is a List<Shape>, and `[c]` checked where a
// List<Shape> is expected is one too) or the type its Conversion converts
// it to (`[p]` returned as a List<Named>). A literal the builder made has
// no record (a copy under a type's name, `Json.Obj{...}`'s map, a run of a
// literal with spreads, a derived body's literal), and takes want, the kind
// of the position it is lowered for. ok is false where neither is a
// collection of join's shape whose elements are all closed.
func (bl *irScalarBuilder) literalKind(at ast.Node, join spreadJoin, want kind) (kind, bool) {
	if k, recorded := bl.g.convertedKind(at); recorded && join.typed(k) && irFullyTyped(k) {
		return k, true
	}
	if join.typed(want) && irFullyTyped(want) {
		return want, true
	}
	return kindInvalid, false
}

// builtLiteralKind is literalKind of a literal the builder lowers, which
// declines where it is false: noteUnrecordedKind counts the site.
func (bl *irScalarBuilder) builtLiteralKind(at ast.Node, join spreadJoin, want kind) (kind, bool) {
	if k, ok := bl.literalKind(at, join, want); ok {
		return k, true
	}
	bl.noteUnrecordedKind(join.owner+" literal", at)
	bl.g.dc.note("a " + join.owner + " literal whose kind neither the checker's record nor its position gives")
	return kindInvalid, false
}

// joinSpreadParts joins two parts of a literal of kind k with join, each
// coerced to k first (an empty spread, `{}`, takes the literal's kind).
func (bl *irScalarBuilder) joinSpreadParts(at ast.Node, a, b spreadPart, k kind, join spreadJoin) (spreadPart, bool) {
	var ok bool
	if a.v, a.k, ok = bl.convertTo(a.at, a.v, a.k, k); !ok {
		return spreadPart{}, false
	}
	if b.v, b.k, ok = bl.convertTo(b.at, b.v, b.k, k); !ok {
		return spreadPart{}, false
	}
	line, col := nodePos(at)
	call := &ast.Call{Line: line, Col: col, Args: []ast.Node{a.at, b.at}}
	// The join stands for the literal: it carries the literal's type.
	defer bl.g.carryType(call, bl.g.recordedType(at))()
	args := irQualArgs{temps: []ir.Temp{a.v, b.v}, kinds: []kind{a.k, b.k}, mobile: []bool{true, true}, ok: true}
	var p *irQualPlan
	switch join.owner {
	case "List":
		p = bl.listCallPlan(call, args, join.method)
	case "Vector":
		p = bl.vectorCallPlan(call, args, join.method)
	case "Set":
		p = bl.setCallPlan(call, args, join.method)
	case "Map":
		p = bl.mapCallPlan(call, args, join.method)
	}
	if p == nil {
		return spreadPart{}, false
	}
	v, rk, _, ok := bl.qualEmit(call, args, p)
	if !ok || rk != k {
		return spreadPart{}, false
	}
	return spreadPart{at: at, v: v, k: k}, true
}

// joinAll joins parts, written in that order, into the literal at of kind
// k: from the left, or from the right for a join that copies its left
// operand (join.fromRight).
func (bl *irScalarBuilder) joinAll(at ast.Node, parts []spreadPart, k kind, join spreadJoin) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(parts) == 0 {
		panic("irbuild: joinAll: a literal on this path has at least one spread, so at least one part")
	}
	var acc spreadPart
	var ok bool
	if join.fromRight {
		acc = parts[len(parts)-1]
		for i := len(parts) - 2; i >= 0; i-- {
			if acc, ok = bl.joinSpreadParts(at, parts[i], acc, k, join); !ok {
				return no()
			}
		}
	} else {
		acc = parts[0]
		for _, p := range parts[1:] {
			if acc, ok = bl.joinSpreadParts(at, acc, p, k, join); !ok {
				return no()
			}
		}
	}
	if acc.v, acc.k, ok = bl.convertTo(at, acc.v, acc.k, k); !ok {
		return no()
	}
	return acc.v, acc.k, false, true
}

// seqSpreadLit lowers a list, vector or set literal at with spreads:
// items are its elements, spreads its spreads, and run builds a run of
// elements as a literal of the literal's kind k.
func (bl *irScalarBuilder) seqSpreadLit(at ast.Node, items []ast.Node, spreads []ast.Spread, join spreadJoin,
	run func(items []ast.Node, k kind) (ir.Temp, kind, bool, bool)) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	heads, ok := bl.spreadHeads(at, spreads, join)
	if !ok {
		return no()
	}
	// kindInvalid: sentinel — a literal with spreads is the program's, so its record gives its kind.
	k, ok := bl.builtLiteralKind(at, join, kindInvalid)
	if !ok {
		return no()
	}
	parts := make([]spreadPart, 0, len(spreads)*2+1)
	next := 0
	addRun := func(end int) bool {
		if end == next {
			return true
		}
		v, rk, _, ok := run(items[next:end], k)
		if !ok {
			return false
		}
		parts = append(parts, spreadPart{at: items[next], v: v, k: rk})
		next = end
		return true
	}
	for i, sp := range spreads {
		if !addRun(sp.At) {
			return no()
		}
		parts = append(parts, heads[i])
	}
	if !addRun(len(items)) {
		return no()
	}
	return bl.joinAll(at, parts, k, join)
}

// listSpreadLit lowers a list literal with spreads other than one written
// last.
func (bl *irScalarBuilder) listSpreadLit(t *ast.ListLit) (ir.Temp, kind, bool, bool) {
	return bl.seqSpreadLit(t, t.Items, t.Spreads, listSpreadJoin, func(items []ast.Node, k kind) (ir.Temp, kind, bool, bool) {
		return bl.listMakeOf(t, items, nil, k)
	})
}

// vectorSpreadLit lowers a vector literal with spreads other than one at
// either end.
func (bl *irScalarBuilder) vectorSpreadLit(t *ast.ListLit) (ir.Temp, kind, bool, bool) {
	return bl.seqSpreadLit(t, t.Items, t.Spreads, vectorSpreadJoin, func(items []ast.Node, k kind) (ir.Temp, kind, bool, bool) {
		run := *t
		run.Items, run.Spreads = items, nil
		return bl.vectorMakeOf(&run, k)
	})
}

// setSpreadLit lowers a set literal with spreads other than one written
// first.
func (bl *irScalarBuilder) setSpreadLit(t *ast.ListLit) (ir.Temp, kind, bool, bool) {
	return bl.seqSpreadLit(t, t.Items, t.Spreads, setSpreadJoin, func(items []ast.Node, k kind) (ir.Temp, kind, bool, bool) {
		run := *t
		run.Items, run.Spreads = items, nil
		return bl.setMakeOf(&run, k)
	})
}

// mapSpreadLit lowers a map literal with spreads other than one written
// first. The parts are joined from the left, and a run of entries after a
// spread whose value is an update literal (`"k" => {.., "a" => 1}`) reads
// the value at its key from the map the parts before it make.
func (bl *irScalarBuilder) mapSpreadLit(t *ast.MapLit) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	heads, ok := bl.spreadHeads(t, t.Spreads, mapSpreadJoin)
	if !ok {
		return no()
	}
	// kindInvalid: sentinel — a literal with spreads is the program's, so its record gives its kind.
	k, ok := bl.builtLiteralKind(t, mapSpreadJoin, kindInvalid)
	if !ok {
		return no()
	}
	var acc spreadPart
	have := false
	next := 0
	add := func(p spreadPart) bool {
		if !have {
			acc, have = p, true
			return true
		}
		var ok bool
		acc, ok = bl.joinSpreadParts(t, acc, p, k, mapSpreadJoin)
		return ok
	}
	addRun := func(end int) bool {
		if end == next {
			return true
		}
		run := *t
		run.Entries, run.Spreads = t.Entries[next:end], nil
		at := t.Entries[next].Key
		next = end
		if have && mapHasHole(&run) {
			var ok bool
			if acc.v, acc.k, ok = bl.convertTo(acc.at, acc.v, acc.k, k); !ok {
				return false
			}
			acc.v = bl.holdTemp(acc.at, acc.v, acc.k)
			v, rk, _, ok := bl.mapEntriesOver(&run, k, acc.v, acc.k)
			return ok && add(spreadPart{at: at, v: v, k: rk})
		}
		v, rk, _, ok := bl.mapEntries(&run, k)
		return ok && add(spreadPart{at: at, v: v, k: rk})
	}
	for i, sp := range t.Spreads {
		if !addRun(sp.At) || !add(heads[i]) {
			return no()
		}
	}
	if !addRun(len(t.Entries)) {
		return no()
	}
	return bl.joinAll(t, []spreadPart{acc}, k, mapSpreadJoin)
}
