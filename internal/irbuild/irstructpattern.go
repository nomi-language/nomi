package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// STRUCT AND DISTINCT PATTERNS IN A `case`.
//
// A struct pattern (`User{name: "Alice"}`, `Point{x, y}`) reads each named
// field in the pattern's order and binds or tests it; a distinct pattern
// (`Id(x)`) reads the wrapped value and matches its payload pattern. The
// subject's type is the pattern's (the checker requires it), so there is no
// tag to test: only the sub-patterns can fail, and a failure selects the
// next arm.

// irNominalCaseKind is a `case` subject whose arms are struct or distinct
// patterns.
func irNominalCaseKind(k kind) bool {
	if irRetainedRecordKind(k) {
		// An anonymous record's `{x: 10, y}` pattern reads its fields the
		// same way; the checker types the pattern against the record.
		return true
	}
	return k.tag == tagNamed && k.def != nil && (irRetainedStructKind(k.def) || irDistinctOverValue(k.def))
}

// nominalCaseTest tests a struct or distinct pattern over subj, ending in arm
// on success with the pattern's names bound and branching to next on
// failure.
func (bl *irScalarBuilder) nominalCaseTest(pattern ast.Node, subj ir.Temp, k kind, arm, next *ir.Block) bool {
	if p, isStruct := pattern.(*ast.StructPattern); isStruct && irRetainedRecordKind(k) {
		if p.TypeName != nil || !bl.recordCaseFields(p, subj, k, next) {
			return false
		}
		bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(pattern), arm.ID()))
		bl.b = arm
		return true
	}
	if k.tag != tagNamed || k.def == nil {
		return false
	}
	switch p := pattern.(type) {
	case *ast.StructPattern:
		if !irRetainedStructKind(k.def) {
			return false
		}
		if p.TypeName != nil {
			if _, member, ok := patternHead(p.TypeName); !ok || member != k.def.nomi {
				return false
			}
		}
		for _, f := range p.Fields {
			fd := k.def.field(f.Name)
			if fd == nil {
				return false
			}
			if _, wild := f.Pattern.(*ast.WildcardPattern); wild {
				continue
			}
			read := ir.NewProjField(bl.g.irNodePos(p), bl.f.NewTemp(), subj,
				bl.g.irTypes().Symbol(fd, f.Name), f.Name, irParamShape(fd.k))
			bl.b.Append(read)
			bl.side(read.Dst(), irScalarSide{k: fd.k})
			switch sub := f.Pattern.(type) {
			case nil:
				bl.patternBinding(p, f.Binding, read.Dst(), fd.k)
				continue
			case *ast.IdentPattern:
				bl.patternBinding(sub, sub.Name, read.Dst(), fd.k)
				continue
			}
			passed := bl.f.NewBlock(bl.g.irNodePos(f.Pattern), "field test")
			if !bl.patternValueTest(f.Pattern, read.Dst(), fd.k, passed, next) {
				return false
			}
		}
	case *ast.MapPattern:
		// `Kvs{"a" => v}` over a distinct of a map, or the bare `{"a" => v}`
		// that matches against the inner map: the inner map's pattern.
		if !irCompositeDistinct(k.def) || k.def.inner.tag != tagMap || (p.TypeName != nil && typeText(p.TypeName) != k.def.nomi) {
			return false
		}
		inner := bl.distinctProjection(p, subj, k, true, "irstructpattern.go nominalCaseTest")
		anon := *p
		anon.TypeName = nil
		return bl.mapCaseTest(&anon, inner, k.def.inner, arm, next)
	case *ast.ListPattern:
		// `Items[a, b]` over a distinct of a list: the inner list's pattern.
		if !irCompositeDistinct(k.def) || k.def.inner.tag != tagList || p.TypeName == nil || typeText(p.TypeName) != k.def.nomi {
			return false
		}
		inner := bl.distinctProjection(p, subj, k, true, "irstructpattern.go nominalCaseTest")
		anon := *p
		anon.TypeName = nil
		return bl.listCaseTest(&anon, inner, k.def.inner, arm, next)
	case *ast.EnumPattern:
		if !irDistinctOverValue(k.def) || (p.Binding != "" && p.Payload != nil) {
			return false
		}
		if owner, member, ok := patternHead(p.Variant); !ok || owner != "" || member != k.def.nomi {
			return false
		}
		inner := bl.distinctProjection(p, subj, k, true, "irstructpattern.go nominalCaseTest")
		if p.Binding != "" {
			bl.patternBinding(p, p.Binding, inner, k.def.inner)
		}
		if p.Payload == nil {
			break
		}
		passed := bl.f.NewBlock(bl.g.irNodePos(p.Payload), "inner test")
		if !bl.patternValueTest(p.Payload, inner, k.def.inner, passed, next) {
			return false
		}
	default:
		return false
	}
	bl.b.SetTerm(ir.NewJump(bl.g.irNodePos(pattern), arm.ID()))
	bl.b = arm
	return true
}

// recordCaseFields reads and tests an anonymous record pattern's fields, as
// the struct arm above does for a declared struct.
func (bl *irScalarBuilder) recordCaseFields(p *ast.StructPattern, subj ir.Temp, k kind, next *ir.Block) bool {
	for _, f := range p.Fields {
		part, found := anonFieldKind(k, f.Name)
		if !found {
			return false
		}
		if _, wild := f.Pattern.(*ast.WildcardPattern); wild {
			continue
		}
		read := ir.NewProjRecordField(bl.g.irNodePos(p), bl.f.NewTemp(), subj, f.Name, irParamShape(part))
		bl.b.Append(read)
		bl.side(read.Dst(), irScalarSide{k: part})
		switch sub := f.Pattern.(type) {
		case nil:
			bl.patternBinding(p, f.Binding, read.Dst(), part)
			continue
		case *ast.IdentPattern:
			bl.patternBinding(sub, sub.Name, read.Dst(), part)
			continue
		}
		passed := bl.f.NewBlock(bl.g.irNodePos(f.Pattern), "field test")
		if !bl.patternValueTest(f.Pattern, read.Dst(), part, passed, next) {
			return false
		}
	}
	return true
}
