package lsp

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// ctxPatternField is a field name in a struct pattern. Its value sits apart
// from completion_syntax.go's list so that list need not change for it.
const ctxPatternField ctxKind = 100

// classifyPatternField recognizes a field name being typed in a struct
// pattern: `Point{‸}`, `Point{x, ‸}`, `.Rect{‸}`, `{x, ‸} = p`, wherever a
// pattern stands (a case arm, a destructuring binding, an `assert` pattern, a
// parameter, a variant's payload).
func classifyPatternField(ctx *completionContext) bool {
	h := ctx.hit
	if h.field != "Name" {
		return false
	}
	if _, ok := firstOf(h.at(0)).(*ast.StructPatternField); !ok {
		return false
	}
	switch firstOf(h.at(1)).(type) {
	case *ast.StructPattern, *ast.StructDestructure:
		ctx.kind = ctxPatternField
		return true
	}
	return false
}

func firstOf(v any, _ string) any { return v }

// patternFieldCandidates offers the fields of the struct a pattern matches
// that the pattern does not name yet, in declaration order. Accepting one
// inserts the bare name, the punned form that binds the field to a name of
// its own.
func (r *completionRequest) patternFieldCandidates() []candidate {
	h := r.ctx.hit
	var written []ast.StructPatternField
	switch p := firstOf(h.at(1)).(type) {
	case *ast.StructPattern:
		written = p.Fields
	case *ast.StructDestructure:
		written = p.Fields
	}
	has := map[string]bool{}
	for _, f := range written {
		if !strings.Contains(f.Name, completionSentinel) {
			has[f.Name] = true
		}
	}
	t := r.patternType(h, 1)
	var out []candidate
	for i, c := range fieldCandidates(fieldsOf(t), r.declOf(t)) {
		if has[c.label] {
			continue
		}
		c.order = i
		out = append(out, c)
	}
	return out
}

// patternType is the type of the value the pattern at hop matches: the type
// its head names, or, for a head that names none (`{x}`, `.Rect{w}`,
// `Rect{w}`), the type its position supplies.
func (r *completionRequest) patternType(h *sentinelHit, hop int) analysis.Type {
	switch p := firstOf(h.at(hop)).(type) {
	case *ast.StructDestructure:
		return r.exprType(p.Value)
	case *ast.StructPattern:
		if p.TypeName != nil {
			return r.namedPatternType(h, hop, p.TypeName)
		}
	case *ast.EnumPattern:
		return r.namedPatternType(h, hop, p.Variant)
	}
	return r.patternSlotType(h, hop)
}

// namedPatternType is the payload a pattern head matches: a struct or
// distinct type by its name, or a variant's payload (`.Rect`, `Rect`,
// `Shape.Rect`), the enum read from the head or from the position.
func (r *completionRequest) namedPatternType(h *sentinelHit, hop int, te ast.TypeExpr) analysis.Type {
	if dv, ok := te.(*ast.DotVariantType); ok {
		return variantPatternPayload(r.patternSlotType(h, hop), dv.Name)
	}
	if t := r.typeOfTypeExpr(te); t != nil {
		if _, isEnum := analysis.ResolveTypeVar(t).(*analysis.EnumType); !isEnum {
			return t
		}
	}
	name := te.TypeString()
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		if t := variantPatternPayload(typeOfTypeSymbol(r.scope.Lookup(name[:i])), name[i+1:]); t != nil {
			return t
		}
	}
	return variantPatternPayload(r.patternSlotType(h, hop), variantNameOf(te))
}

// patternSlotType is the type the position of the pattern at hop supplies:
// a case's subject, a destructuring binding's or an assertion's value, a
// parameter's annotation, a variant's payload, a struct field, a tuple slot.
func (r *completionRequest) patternSlotType(h *sentinelHit, hop int) analysis.Type {
	_, field := h.at(hop)
	switch p := firstOf(h.at(hop + 1)).(type) {
	case *ast.CaseBranch:
		if c, ok := firstOf(h.at(hop + 2)).(*ast.Case); ok && field == "Pattern" && c.Value != nil {
			return r.exprType(c.Value)
		}
	case *ast.PatternBinding:
		if field == "Pattern" {
			return r.exprType(p.Value)
		}
	case *ast.PatternDestructure:
		if field == "Pattern" {
			return r.exprType(p.Value)
		}
	case *ast.Param:
		if field == "Destructure" && p.TypeAnnotation != nil {
			return r.typeOfTypeExpr(p.TypeAnnotation)
		}
	case *ast.EnumPattern:
		if field == "Payload" {
			return r.patternType(h, hop+1)
		}
	case *ast.StructPatternField:
		if field == "Pattern" {
			if f, ok := fieldOf(r.patternType(h, hop+2), p.Name); ok {
				return f.Type
			}
		}
	case *ast.TuplePattern:
		if field != "Patterns" {
			return nil
		}
		idx := h.path[len(h.path)-1-hop].index
		if tt, ok := analysis.ResolveTypeVar(r.patternSlotType(h, hop+1)).(*analysis.TupleType); ok && idx >= 0 && idx < len(tt.Elems) {
			return tt.Elems[idx]
		}
		if p.Flat {
			// A flat payload `.V(a, b)` destructures the variant's tuple.
			if tt, ok := analysis.ResolveTypeVar(r.patternType(h, hop+2)).(*analysis.TupleType); ok && idx >= 0 && idx < len(tt.Elems) {
				return tt.Elems[idx]
			}
		}
	}
	return nil
}

// variantPatternPayload is what a pattern on one variant of enum matches
// inside it: a struct variant's fields as an anonymous struct, an embedded
// type, or a positional variant's payload.
func variantPatternPayload(enum analysis.Type, variant string) analysis.Type {
	et, ok := analysis.ResolveTypeVar(enum).(*analysis.EnumType)
	if !ok {
		return nil
	}
	var subs map[*analysis.TypeParam_]analysis.Type
	if len(et.TypeArgs) > 0 && len(et.TypeArgs) == len(et.TypeParamDefs) {
		subs = make(map[*analysis.TypeParam_]analysis.Type, len(et.TypeArgs))
		for i, p := range et.TypeParamDefs {
			subs[p] = et.TypeArgs[i]
		}
	}
	sub := func(t analysis.Type) analysis.Type {
		if subs == nil || t == nil {
			return t
		}
		return analysis.Substitute(t, subs)
	}
	for _, v := range et.Variants {
		if v.Name != variant {
			continue
		}
		switch v.Kind {
		case analysis.VariantStruct:
			fields := make([]analysis.FieldDef, len(v.Fields))
			for i, f := range v.Fields {
				fields[i] = f
				fields[i].Type = sub(f.Type)
			}
			return &analysis.AnonStructType{Fields: fields}
		case analysis.VariantEmbedded:
			return sub(v.Embedded)
		}
		return sub(v.DataType)
	}
	return nil
}
