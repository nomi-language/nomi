package irbuild

import (
	"maps"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// MAP PATTERNS AND MAP DESTRUCTURING.
//
// An entry `key => pat` is a lookup and a test on what it found: the key
// expression is evaluated, `Map.get(subject, key)` answers a Maybe, a
// MatchVariant asks for `Some`, and the payload is matched against `pat`.
// That is a lookup, then the entry's pattern on the value, through nodes
// the VM already runs: one probe per key, and the
// key is an operand evaluated in entry order, each one after the earlier
// entries' lookups and tests.
//
// A key sees the names in scope before the pattern, never a name the pattern
// itself binds, because the checker resolves it there (analysis/builder.go
// walks every key of a map pattern before it defines the pattern's names).
// In `{"mode" => a, a => b}` the second key is the `a` bound before the
// pattern. Each key is lowered in a copy of the scope the pattern started in
// (mapKeyScope), so a name an earlier entry bound is not visible to it.
//
// A pattern is partial: it names the keys it cares about and says nothing
// about the rest, so `{}` matches every map.
//
// In a `case`, a missing key or a failed value test selects the next arm. In
// a destructuring statement (`{"k" => v} = m`) a missing key faults with
// rt.MapKeyMissingError's text through ir.NewMapKeyMissing.

// mapEntryGet lowers one entry's key and looks it up in subj: the Maybe the
// lookup answers, its kind and its Some variant, and the key's temporary.
func (bl *irScalarBuilder) mapEntryGet(at ast.Node, subj ir.Temp, mk kind, keyNode ast.Node, keys *irLexicalScope) (got ir.Temp, maybe kind, some *variantDef, key ir.Temp, ok bool) {
	if mk.tag != tagMap || !irRetainedMapKind(mk) {
		return ir.NoTemp, kindInvalid, nil, ir.NoTemp, false
	}
	keyK, valK := mk.comp.parts[0], mk.comp.parts[1]
	// A key of another type than the map's can never be found, and the
	// checker admits it (`{Color.Blue => v}` over a Map<Bool, String>): the
	// lookup is made anyway and misses, so the key
	// expression still runs.
	var kk kind
	if keys != nil {
		cur := bl.saveScope()
		bl.restoreScope(*keys)
		bl.cloneScope()
		key, kk, _, ok = bl.lower(keyNode)
		bl.restoreScope(cur)
	} else {
		key, kk, _, ok = bl.lower(keyNode)
	}
	if !ok || (kk != keyK && !irMapKeyKind(kk)) {
		if ok {
			irDeclineNote("a map pattern key of kind " + kk.nomi() + " over " + mk.nomi())
		}
		return ir.NoTemp, kindInvalid, nil, ir.NoTemp, false
	}
	maybe, ok = bl.g.mapResultKind("maybe", keyK, valK, at)
	if !ok || maybe.tag != tagNamed || !irRetainedEnumKind(maybe.def) {
		irDeclineNote("a map pattern over values outside the retained Maybe payloads: " + mk.nomi())
		return ir.NoTemp, kindInvalid, nil, ir.NoTemp, false
	}
	some = maybe.def.variant("Some")
	if some == nil || len(some.payloads) != 1 {
		return ir.NoTemp, kindInvalid, nil, ir.NoTemp, false
	}
	if ok := bl.g.irMapKeyOK(keyK); !ok {
		return ir.NoTemp, kindInvalid, nil, ir.NoTemp, false
	}
	c := ir.NewHostCall(bl.g.irNodePos(keyNode), bl.f.NewTemp(), ir.OrdinaryCall,
		bl.g.irCalleeSym("Map.get", "Map.get"), subj, key)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: maybe, deferrable: true})
	return c.Dst(), maybe, some, key, true
}

// mapKeyScope copies the scope a map pattern of n entries starts in, for its
// keys to be lowered in. A pattern with one entry needs none: its only key is
// lowered before the pattern binds anything.
func (bl *irScalarBuilder) mapKeyScope(n int) *irLexicalScope {
	if n < 2 {
		return nil
	}
	return &irLexicalScope{maps.Clone(bl.bound), maps.Clone(bl.boundK), maps.Clone(bl.sh.syms)}
}

// mapSomeTest branches on whether got is Some, to found or miss, and leaves
// the builder in found with the payload read.
func (bl *irScalarBuilder) mapSomeTest(at ast.Node, got ir.Temp, maybe kind, some *variantDef, miss *ir.Block) (ir.Temp, kind) {
	match := bl.variantTest(at, got, maybe.def, some)
	bl.b.Append(match)
	bl.side(match.Dst(), irScalarSide{k: kindBool})
	found := bl.f.NewBlock(bl.g.irNodePos(at), "map value")
	bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(at), match.Dst(), found.ID(), miss.ID()))
	bl.b = found
	return bl.variantPayload(at, got, maybe.def, some), some.payloads[0].k
}

// mapCaseTest tests a map pattern in a `case` arm: every entry's key present
// and its value matching, in entry order. The builder ends in arm with the
// entries' names bound; any failure branches to next.
func (bl *irScalarBuilder) mapCaseTest(pattern ast.Node, subj ir.Temp, mk kind, arm, next *ir.Block) bool {
	pat, ok := pattern.(*ast.MapPattern)
	if !ok || pat.TypeName != nil {
		return false
	}
	keys := bl.mapKeyScope(len(pat.Entries))
	for i, e := range pat.Entries {
		target := arm
		if i < len(pat.Entries)-1 {
			target = bl.f.NewBlock(bl.g.irNodePos(pat), "map entry")
		}
		got, maybe, some, _, ok := bl.mapEntryGet(pat, subj, mk, e.Key, keys)
		if !ok {
			return false
		}
		value, vk := bl.mapSomeTest(pat, got, maybe, some, next)
		if !bl.patternValueTest(e.Pattern, value, vk, target, next) {
			return false
		}
	}
	if len(pat.Entries) == 0 {
		bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(pat), arm.ID()))
		bl.b = arm
	}
	return true
}

// patternValueTest matches a nested pattern against a value already read,
// ending in arm on success and branching to next on failure. A pattern it
// cannot test is the decline's position, unless a pattern inside it is.
func (bl *irScalarBuilder) patternValueTest(pat ast.Node, value ir.Temp, k kind, arm, next *ir.Block) bool {
	if bl.patternValueTestNode(pat, value, k, arm, next) {
		return true
	}
	irDeclineAtPattern(pat, "a nested pattern outside the retained tests: "+irNodeWord(pat)+" over "+k.nomi())
	return false
}

func (bl *irScalarBuilder) patternValueTestNode(pat ast.Node, value ir.Temp, k kind, arm, next *ir.Block) bool {
	jump := func() bool {
		bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(pat), arm.ID()))
		bl.b = arm
		return true
	}
	switch p := pat.(type) {
	case *ast.WildcardPattern:
		return jump()
	case *ast.IdentPattern:
		bl.patternBinding(p, p.Name, value, k)
		return jump()
	case *ast.AsPattern:
		// `P as name`: P's test, then name bound on its success edge.
		if !bl.patternValueTest(p.Pattern, value, k, arm, next) {
			return false
		}
		bl.patternBinding(p, p.Name, value, k)
		return true
	case *ast.OrPattern:
		return bl.orPatternTest(p, value, k, arm, next)
	case *ast.IntLit, *ast.StringLit, *ast.CodepointLit, *ast.FloatLit, *ast.DecimalLit:
		// A Float or Decimal literal is tested as a top-level `case` arm
		// tests it (litFor): MatchLit, which the VM answers with rt.Equal,
		// so EqFloat and EqDecimal decide at every depth.
		lit, lk, _, ok := bl.litFor(p, k)
		if !ok || lk != k {
			return false
		}
		m := ir.NewMatchLitInto(bl.g.irNodePos(p), bl.f.NewTemp(), value, lit)
		bl.b.Append(m)
		bl.side(m.Dst(), irScalarSide{k: kindBool})
		bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(p), m.Dst(), arm.ID(), next.ID()))
		bl.b = arm
		return true
	case *ast.Binary:
		return bl.strPrefixTest(p, value, k, arm, next)
	case *ast.MapPattern:
		if irDistinctOverKind(k, tagMap) {
			return bl.nominalCaseTest(p, value, k, arm, next)
		}
		if p.TypeName != nil {
			if k.tag != tagNamed || !irRetainedEnumKind(k.def) {
				return false
			}
			return bl.enumCaseTest(p, value, k, arm, next)
		}
		return bl.mapCaseTest(p, value, k, arm, next)
	case *ast.TuplePattern:
		if !irRetainedTupleKind(k) {
			return false
		}
		if tupleCaseIrrefutable(p, k) {
			bl.tupleCaseBindings(p, value, k)
			return jump()
		}
		return bl.tupleCaseTest(p, value, k, arm, next)
	case *ast.EnumPattern, *ast.StructPattern:
		if irNominalCaseKind(k) {
			return bl.nominalCaseTest(p, value, k, arm, next)
		}
		if k != kindBool && (k.tag != tagNamed || !irRetainedEnumKind(k.def)) {
			return false
		}
		return bl.enumCaseTest(p, value, k, arm, next)
	case *ast.ListPattern:
		if irDistinctOverKind(k, tagList) {
			return bl.nominalCaseTest(p, value, k, arm, next)
		}
		if p.TypeName != nil {
			if k.tag != tagNamed || !irRetainedEnumKind(k.def) {
				return false
			}
			return bl.enumCaseTest(p, value, k, arm, next)
		}
		if !irRetainedListKind(k) && !irListTransportKind(k) {
			return false
		}
		return bl.listCaseTest(p, value, k, arm, next)
	}
	return false
}

// mapBinding lowers `{key => name, ...} = m`. Each key is looked up in entry
// order; a missing one faults with rt.MapKeyMissingError's text at the
// statement's line, and a present one binds its value under the plain
// binding's rebind rule (destructureBind).
func (bl *irScalarBuilder) mapBinding(t *ast.MapDestructure) bool {
	subj, mk, _, ok := bl.lower(t.Value)
	if !ok || mk.tag != tagMap || !irRetainedMapKind(mk) {
		if ok {
			irDeclineNote("a map destructure outside the retained map domain: " + mk.nomi())
		}
		return false
	}
	if !irHeldValue(bl.f, subj, bl.sides) {
		c := ir.NewCopy(bl.g.irNodePos(t.Value), bl.f.NewTemp(), subj)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: mk, copy: irCopyHold})
		subj = c.Dst()
	}
	done := bl.f.NewBlock(bl.g.irNodePos(t), "map destructured")
	keys := bl.mapKeyScope(len(t.Entries))
	for _, e := range t.Entries {
		id, named := e.Pattern.(*ast.IdentPattern)
		_, wildcard := e.Pattern.(*ast.WildcardPattern)
		if !named && !wildcard {
			irDeclineNote("a map destructure entry outside names and wildcards")
			return false
		}
		got, maybe, some, key, ok := bl.mapEntryGet(t, subj, mk, e.Key, keys)
		if !ok {
			return false
		}
		miss := bl.f.NewBlock(bl.g.irNodePos(t), "map key missing")
		miss.Append(ir.NewMapKeyMissing(bl.g.irNodePos(t), key))
		miss.SetTerm(ir.NewJump(bl.g.irNodePos(t), done.ID()))
		value, vk := bl.mapSomeTest(t, got, maybe, some, miss)
		if named && !ast.IsDiscardName(id.Name) {
			line, col := nodePos(id)
			if !bl.destructureBind(id, id.Name, line, col, vk, func() ir.Temp { return value }) {
				return false
			}
		}
	}
	bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(t), done.ID()))
	bl.b = done
	bl.irDiscardStmtUnit(t)
	return true
}
