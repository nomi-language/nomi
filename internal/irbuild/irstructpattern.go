package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// STRUCT AND DISTINCT PATTERNS.
//
// A struct pattern (`User{name: "Alice"}`, `Point{x:, y:}`) reads each named
// field in the pattern's order and binds or tests it; a distinct pattern
// (`Id(x)`) reads the wrapped value and matches its payload pattern. The
// subject's type is the pattern's (the checker requires it), so there is no
// tag to test: only the sub-patterns can fail.

// irNominalCaseKind is a subject whose struct or distinct patterns read it
// with no tag test.
func irNominalCaseKind(k kind) bool {
	if irRetainedRecordKind(k) {
		// An anonymous record's `{x: 10, y:}` pattern reads its fields the
		// same way; the checker types the pattern against the record.
		return true
	}
	return k.tag == tagNamed && k.def != nil && (irRetainedStructKind(k.def) || irDistinctOverValue(k.def))
}

// nominalCaseTest tests a struct or distinct pattern over subj, leaving the
// builder where its last test passed with the pattern's names bound, and
// branching to next on failure.
func (bl *irScalarBuilder) nominalCaseTest(pattern ast.Node, subj ir.Temp, k kind, next *ir.Block) bool {
	if p, isStruct := pattern.(*ast.StructPattern); isStruct && irRetainedRecordKind(k) {
		if p.TypeName != nil {
			// "struct pattern names T, but the value is the anonymous
			// struct" is the checker's error.
			panic("irbuild: a struct pattern that names a type, over an anonymous struct: " + k.nomi())
		}
		return bl.structFieldsTest(p, next, func(f ast.StructPatternField) (ir.Temp, kind) {
			part, found := anonFieldKind(k, f.Name)
			if !found {
				// The checker rejects a field the record does not have.
				panic("irbuild: a record pattern's field " + f.Name + " over " + k.nomi())
			}
			read := ir.NewProjRecordField(bl.g.irNodePos(p), bl.f.NewTemp(), subj, f.Name, irParamShape(part))
			bl.b.Append(read)
			bl.side(read.Dst(), irScalarSide{k: part})
			return read.Dst(), part
		})
	}
	if k.tag != tagNamed || k.def == nil {
		return false
	}
	switch p := pattern.(type) {
	case *ast.StructPattern:
		if !irRetainedStructKind(k.def) {
			return false
		}
		// A written type name names this struct, directly or through an
		// alias (`Human{age:} = person`): the checker rejects any other
		// (structPatternNames), so the name selects nothing here.
		return bl.structFieldsTest(p, next, func(f ast.StructPatternField) (ir.Temp, kind) {
			fd := k.def.field(f.Name)
			if fd == nil {
				// The checker rejects a field the struct does not have.
				panic("irbuild: a struct pattern's field " + f.Name + " over " + k.nomi())
			}
			read := ir.NewProjField(bl.g.irNodePos(p), bl.f.NewTemp(), subj,
				bl.g.irTypes().Symbol(fd, f.Name), f.Name, irParamShape(fd.k))
			bl.b.Append(read)
			bl.side(read.Dst(), irScalarSide{k: fd.k})
			return read.Dst(), fd.k
		})
	case *ast.MapPattern:
		// `Kvs{"a" => v}` over a distinct of a map, or the bare `{"a" => v}`
		// that matches against the inner map: the inner map's pattern. A
		// written prefix, bare or qualified (`kinds.Kvs{...}`), names the
		// scrutinee's type: the checker rejects any other, so the name
		// selects nothing here, as for a struct pattern above.
		if !irCompositeDistinct(k.def) || k.def.inner.tag != tagMap {
			return false
		}
		inner := bl.distinctProjection(p, subj, k, true, "irstructpattern.go nominalCaseTest")
		anon := *p
		anon.TypeName = nil
		return bl.patternTest(bl.g.carryPattern(&anon, p), inner, k.def.inner, next)
	case *ast.ListPattern:
		switch p.Kind {
		case ast.SeqVector:
			// `Path[a, ..]` over a distinct of a vector, or the bare `[a, ..]`
			// that matches against the inner vector.
			if !irDistinctOverVector(k) {
				return false
			}
		case ast.SeqSet:
			if !irDistinctOverSet(k) {
				return false
			}
		default:
			// `Items[a, b]` over a distinct of a list, or the bare `[a, b]`
			// that matches against the inner list: the inner list's pattern.
			if !irCompositeDistinct(k.def) || k.def.inner.tag != tagList {
				// The checker admits a list pattern over a named type only
				// for a distinct of a list.
				panic("irbuild: a list pattern over " + k.def.nomi + ", which is no distinct of a list")
			}
		}
		inner := bl.distinctProjection(p, subj, k, true, "irstructpattern.go nominalCaseTest")
		anon := *p
		anon.TypeName = nil
		return bl.patternTest(bl.g.carryPattern(&anon, p), inner, k.def.inner, next)
	case *ast.EnumPattern:
		if !irDistinctOverValue(k.def) || (p.Binding != "" && p.Payload != nil) {
			return false
		}
		// `Pt(p)` or `kinds.Pt(p)`: the head names the scrutinee's
		// distinct, which the checker holds it to, so it selects nothing.
		inner := bl.distinctProjection(p, subj, k, true, "irstructpattern.go nominalCaseTest")
		if p.Binding != "" {
			bl.patternBinding(p, p.Binding, inner, k.def.inner)
		}
		if p.Payload == nil {
			return true
		}
		return bl.patternTest(p.Payload, inner, k.def.inner, next)
	}
	return false
}

// structFieldsTest tests a struct pattern's fields in the pattern's order:
// each field but a `_` read by read, then bound (`name:`) or matched by its
// sub-pattern, so a later field is read only once the tests before it
// passed.
func (bl *irScalarBuilder) structFieldsTest(pat *ast.StructPattern, next *ir.Block, read func(ast.StructPatternField) (ir.Temp, kind)) bool {
	for _, f := range pat.Fields {
		if _, wild := f.Pattern.(*ast.WildcardPattern); wild {
			// A `_` binds and tests nothing, so nothing is read.
			continue
		}
		v, fk := read(f)
		if f.Pattern == nil {
			bl.patternBinding(pat, f.Binding, v, fk)
			continue
		}
		if !bl.patternTest(f.Pattern, v, fk, next) {
			return false
		}
	}
	return true
}
