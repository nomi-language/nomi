package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// MAP PATTERNS.
//
// An entry `key => pat` is a lookup and a test on what it found: the key
// expression is evaluated, `Map.get(subject, key)` answers an Option, a
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
// pattern. A key reads the declaration the checker resolved it to, so a
// name an earlier entry bound is not what it reads (readKey).
//
// A pattern is partial: it names the keys it cares about and says nothing
// about the rest, so `{}` matches every map.
//
// A missing key or a failed value test selects the next arm, or the `else`
// of a binding (`{"k" => v} = m else ... end`): a map pattern is refutable.

// mapEntryGet lowers one entry's key and looks it up in subj: the Option the
// lookup answers, its kind and its Some variant.
func (bl *irScalarBuilder) mapEntryGet(at ast.Node, subj ir.Temp, mk kind, keyNode ast.Node) (got ir.Temp, option kind, some *variantDef, ok bool) {
	if mk.tag != tagMap || !irRetainedMapKind(mk) {
		return ir.NoTemp, kindInvalid, nil, false
	}
	keyK, valK := mk.comp.parts[0], mk.comp.parts[1]
	// A key of another type than the map's can never be found, and the
	// checker admits it (`{Color.Blue => v}` over a Map<Bool, String>): the
	// lookup is made anyway and misses, so the key
	// expression still runs.
	key, kk, _, ok := bl.lower(keyNode)
	if !ok || (kk != keyK && !irMapKeyKind(kk)) {
		if ok {
			bl.g.dc.note("a map pattern key of kind " + kk.nomi() + " over " + mk.nomi())
		}
		return ir.NoTemp, kindInvalid, nil, false
	}
	option, ok = bl.g.mapResultKind("option", keyK, valK, at)
	if !ok || option.tag != tagNamed || !irRetainedEnumKind(option.def) {
		bl.g.dc.note("a map pattern over values outside the retained Option payloads: " + mk.nomi())
		return ir.NoTemp, kindInvalid, nil, false
	}
	some = option.def.variant("Some")
	if some == nil || len(some.payloads) != 1 {
		return ir.NoTemp, kindInvalid, nil, false
	}
	if ok := bl.g.irMapKeyOK(keyK); !ok {
		return ir.NoTemp, kindInvalid, nil, false
	}
	c := ir.NewHostCall(bl.g.irNodePos(keyNode), bl.f.NewTemp(), ir.OrdinaryCall,
		bl.g.irCalleeSym("Map.get", "Map.get"), subj, key)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: option, deferrable: true})
	return c.Dst(), option, some, true
}

// mapSomeTest branches on whether got is Some, to found or miss, and leaves
// the builder in found with the payload read.
func (bl *irScalarBuilder) mapSomeTest(at ast.Node, got ir.Temp, option kind, some *variantDef, miss *ir.Block) (ir.Temp, kind) {
	match := bl.variantTest(at, got, option.def, some)
	bl.b.Append(match)
	bl.side(match.Dst(), irScalarSide{k: kindBool})
	bl.passOrFail(at, match.Dst(), miss)
	return bl.variantPayload(at, got, option.def, some), some.payloads[0].k
}

// mapCaseTest tests a map pattern with no prefix: every entry's key present
// and its value matching, in entry order, with the entries' names bound.
// Any failure branches to next.
func (bl *irScalarBuilder) mapCaseTest(pat *ast.MapPattern, subj ir.Temp, mk kind, next *ir.Block) bool {
	for _, e := range pat.Entries {
		got, option, some, ok := bl.mapEntryGet(pat, subj, mk, e.Key)
		if !ok {
			return false
		}
		value, vk := bl.mapSomeTest(pat, got, option, some, next)
		if !bl.patternTest(e.Pattern, value, vk, next) {
			return false
		}
	}
	return true
}
