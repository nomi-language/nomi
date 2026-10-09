package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// Irrefutable parameter destructuring (spec §5, "Parameter Destructuring").
//
// A function-definition or lambda parameter may carry an irrefutable
// destructuring pattern in place of a bare name. The two analyzer pieces this
// file owns are shared by the signature builder (buildFuncType) and the body
// checker (checkFunc / checkLambda):
//
//   - paramPatternType derives a parameter's type. The type comes from either
//     the `: T` annotation or — for self-typing patterns whose head names a
//     concrete type (distinct `Dur(x)`, typed struct `Point{x, y}`, qualified
//     single-variant enum `E.V(x)`) — the pattern head. Type-less shapes
//     (tuple, anon struct, dot-variant, plain ident) require an annotation.
//
//   - checkParamPatternRefutable rejects refutable patterns (multi-variant
//     enum variants, map / list / literal patterns, or any pattern nesting
//     one). It is type-driven: `Dur(x)` (distinct, irrefutable) and `Some(x)`
//     (enum variant, refutable) are syntactically identical EnumPatterns, so
//     only resolving the head against the parameter's type distinguishes them.

const refutableParamMsg = "refutable pattern in parameter; bind the parameter and use a `case` in the body"

// paramShapeName returns a human-readable name for a pattern shape, used in
// the "<shape> parameter needs a type annotation" diagnostic.
func paramShapeName(pattern ast.Node) string {
	switch ast.WithoutAs(pattern).(type) {
	case *ast.TuplePattern:
		return "tuple"
	case *ast.StructPattern:
		return "struct"
	case *ast.EnumPattern:
		return "variant"
	case *ast.MapPattern:
		return "map"
	case *ast.ListPattern:
		return "list"
	}
	return "destructuring"
}

// paramPatternHeadType resolves the concrete type a self-typing pattern head
// names, or returns (nil, nil) if the pattern is type-less (no head to read a
// type from). A non-nil error reports a genuine resolution failure.
func paramPatternHeadType(pattern ast.Node, reg *TypeRegistry, typeParams map[string]*TypeParam_, refs map[Pos]*Symbol) (Type, error) {
	// `Point{x, y} as p` is typed by its pattern's head.
	switch p := ast.WithoutAs(pattern).(type) {
	case *ast.EnumPattern:
		switch v := p.Variant.(type) {
		case *ast.SimpleType:
			// `Dur(x)` — head names the distinct/enum type directly.
			return ResolveTypeExpr(v, reg, typeParams, refs)
		case *ast.QualifiedType:
			// `units.Days(n)` may name a qualified distinct type.
			// Prefer the full dotted type when it exists; otherwise `E.V(x)`
			// is an enum variant pattern where the qualifier E is the type and
			// V is the variant.
			if t, err := ResolveTypeExpr(v, reg, typeParams, refs); err == nil && t != nil {
				return t, nil
			}
			head := &ast.SimpleType{Name: v.Module, Line: v.ModuleLine, Col: v.ModuleCol}
			return ResolveTypeExpr(head, reg, typeParams, refs)
		default:
			// `.V(x)` (DotVariantType) — no type head; needs an annotation.
			return nil, nil
		}
	case *ast.StructPattern:
		if p.TypeName == nil {
			return nil, nil // anonymous struct `{x, y}` — no type head
		}
		if qt, ok := p.TypeName.(*ast.QualifiedType); ok {
			// `Outer.Inner{...}` may name a qualified struct type. Prefer the
			// full dotted type when it exists; otherwise `E.V{...}` is an enum
			// struct variant pattern where the qualifier E is the type and V is
			// the variant (e.g. `Shape.Circle{radius}`).
			if t, err := ResolveTypeExpr(qt, reg, typeParams, refs); err == nil && t != nil {
				return t, nil
			}
			head := &ast.SimpleType{Name: qt.Module, Line: qt.ModuleLine, Col: qt.ModuleCol}
			return ResolveTypeExpr(head, reg, typeParams, refs)
		}
		return ResolveTypeExpr(p.TypeName, reg, typeParams, refs)
	}
	return nil, nil
}

// genericWithoutArgs reports whether a resolved type is a generic distinct or
// struct whose type arguments were not pinned (e.g. `Box(x)` with no
// annotation). Such a pattern can't fully self-type — it can't pin the type
// parameters — so it still requires an annotation.
func genericWithoutArgs(t Type) bool {
	switch ty := t.(type) {
	case *DistinctType:
		return len(ty.TypeParamDefs) > 0 && len(ty.TypeArgs) != len(ty.TypeParamDefs)
	case *StructType:
		return len(ty.TypeParamDefs) > 0 && len(ty.TypeArgs) != len(ty.TypeParamDefs)
	case *EnumType:
		return len(ty.TypeParamDefs) > 0 && len(ty.TypeArgs) != len(ty.TypeParamDefs)
	}
	return false
}

// paramPatternType derives a destructuring parameter's type. Precedence:
// explicit `: T` annotation first, then the self-typing pattern head. Returns
// a TypeError (with the parameter's position) when neither yields a type or
// when a generic head can't pin its type arguments.
//
// Plain (non-destructure) params are not this function's concern — the caller
// only invokes it for params with p.Destructure != nil.
func paramPatternType(p ast.Param, reg *TypeRegistry, typeParams map[string]*TypeParam_, fa *FileAnalysis) (Type, error) {
	if te := unnamedParam(p, fa, reg); te != nil {
		return nil, *te
	}
	refs := fa.References
	if p.TypeAnnotation != nil {
		return ResolveTypeExpr(p.TypeAnnotation, reg, typeParams, refs)
	}

	headTy, err := paramPatternHeadType(p.Destructure, reg, typeParams, refs)
	if err != nil {
		return nil, err
	}
	if headTy == nil {
		return nil, TypeError{
			Line:    p.Destructure.LineNum(),
			Col:     patternColOf(p.Destructure),
			Message: fmt.Sprintf("%s parameter needs a type annotation", paramShapeName(p.Destructure)),
		}
	}
	if genericWithoutArgs(headTy) {
		return nil, TypeError{
			Line:    p.Destructure.LineNum(),
			Col:     patternColOf(p.Destructure),
			Message: fmt.Sprintf("generic %s parameter needs a type annotation to pin its type arguments", paramShapeName(p.Destructure)),
		}
	}
	return headTy, nil
}

// unnamedParam reports a parameter written as a type with no name,
// `fn echo(String): String`. The parser reads a bare `String` in parameter
// position as a variant pattern with no payload, which binds nothing, so the
// parameter would have a type and no name. A `go`-bound fn has no body whose
// pattern check would reject it, so this runs for every declared function.
//
// When the bare name is a variant of an enum in scope that may not be written
// bare in a pattern (allowsBareVariantPattern's rule), `|One|` reads as an
// attempt to match it: if no type has that name the error is the pattern
// check's own "bare variant" one, and if a type does, the "needs a name"
// error also says how to match the variant.
func unnamedParam(p ast.Param, fa *FileAnalysis, reg *TypeRegistry) *TypeError {
	ep, ok := p.Destructure.(*ast.EnumPattern)
	if !ok || ep.Payload != nil || ep.Binding != "" || p.TypeAnnotation != nil {
		return nil
	}
	head, ok := ep.Variant.(*ast.SimpleType)
	if !ok {
		return nil
	}
	name := head.Name
	te := &TypeError{
		Line:    ep.Line,
		Col:     ep.Col,
		Message: fmt.Sprintf("parameter '%s' needs a name", name),
		Hints:   []string{fmt.Sprintf("`%s` is read as the parameter's type; write `name: %s`", name, name)},
	}
	enum := bareParamVariantEnum(fa, name, Pos{Line: head.Line, Col: head.Col})
	switch {
	case enum == "":
	case reg != nil && reg.Lookup(name) != nil:
		te.Hints = append(te.Hints, fmt.Sprintf("write `.%s` (or `%s.%s`) to match the variant", name, enum, name))
	default:
		te.Message = bareVariantPatternMessage(name, enum)
		te.Hints = []string{fmt.Sprintf("to bind the value, write `name: %s`", enum)}
	}
	return te
}

// bareParamVariantEnum returns the name of the enum in scope that declares
// variant name, when a bare pattern may not name that variant; otherwise "".
func bareParamVariantEnum(fa *FileAnalysis, name string, pos Pos) string {
	if fa == nil || fa.ModuleScope == nil || bareVariantPatternAllowed(fa, name, pos) {
		return ""
	}
	if sym := enumDeclaringVariant(fa.ModuleScope, name); sym != nil {
		return sym.Name
	}
	return ""
}

// patternColOf returns the column of a pattern node for diagnostic positions.
// Sibling of parser.patternCol (which synthesizes the destructure slot name);
// they differ only in the unmatched-node fallback — 0 here, LineNum there.
func patternColOf(n ast.Node) int {
	switch p := n.(type) {
	case *ast.TuplePattern:
		return p.Col
	case *ast.StructPattern:
		return p.Col
	case *ast.EnumPattern:
		return p.Col
	case *ast.MapPattern:
		return p.Col
	case *ast.ListPattern:
		return p.Col
	case *ast.AsPattern:
		return p.Col
	}
	return 0
}

// checkParamPatternRefutable reports an error for each refutable sub-pattern in
// a parameter position. It is type-driven, recursing into tuple/struct element
// types so a nested refutable pattern (`(.Some(x), n)`) is caught. addError is
// the checker's error sink (line, col, msg).
func checkParamPatternRefutable(pattern ast.Node, ty Type, reg *TypeRegistry, addError func(line, col int, msg string)) {
	switch p := pattern.(type) {
	case *ast.WildcardPattern, *ast.IdentPattern:
		return

	case *ast.AsPattern:
		// The name matches anything; the pattern decides.
		checkParamPatternRefutable(p.Pattern, ty, reg, addError)

	case *ast.MapPattern:
		addError(p.Line, p.Col, refutableParamMsg)

	case *ast.ListPattern:
		addError(p.Line, p.Col, refutableParamMsg)

	case *ast.EnumPattern:
		// Distinct heads are irrefutable; multi-variant enum variants are not.
		switch ty := ty.(type) {
		case *DistinctType:
			// `Dur(x)` — irrefutable. Recurse into the payload against the
			// distinct's inner type so a refutable nested payload is caught.
			if p.Payload != nil {
				checkParamPatternRefutable(p.Payload, ty.Inner, reg, addError)
			}
		case *EnumType:
			if len(ty.Variants) != 1 {
				addError(p.Line, p.Col, refutableParamMsg)
				return
			}
			// Single-variant enum — irrefutable, including an `embeds` variant
			// (`enum Ident { embeds UserId }`). For params the binding is
			// consistent: `applyDestructure` unwraps the inner value (both the
			// VariantVal and the embeds-coerced DistinctVal paths) and the
			// checker types the binding as that inner type — so `fn f(Ident.UserId(n))`
			// binds n to UserId's inner, matching the `case` form. (The historic
			// guard here predated that confirmation; the divergence it feared
			// lives only in matchPattern's case path, which params don't use.)
			// Recurse into the payload against the variant's data type.
			if p.Payload != nil {
				checkParamPatternRefutable(p.Payload, ty.Variants[0].DataType, reg, addError)
			}
		default:
			// Unknown / unresolved type — let checkPattern report the
			// mismatch; don't double-report a refutability error.
		}

	case *ast.TuplePattern:
		if tt, ok := ty.(*TupleType); ok && len(tt.Elems) == len(p.Patterns) {
			for i, sub := range p.Patterns {
				checkParamPatternRefutable(sub, tt.Elems[i], reg, addError)
			}
		}

	case *ast.StructPattern:
		fields := structFieldsOf(ty)
		for _, f := range p.Fields {
			if f.Pattern == nil {
				continue // plain binding / pun — irrefutable
			}
			var ft Type
			for _, sf := range fields {
				if sf.Name == f.Name {
					ft = sf.Type
					break
				}
			}
			checkParamPatternRefutable(f.Pattern, ft, reg, addError)
		}

	default:
		// Literal patterns (IntLit, StringLit, FloatLit, DecimalLit, …) are
		// refutable.
		addError(pattern.LineNum(), patternColOf(pattern), refutableParamMsg)
	}
}

// structFieldsOf returns the field list of a struct/anon-struct type, or nil
// for any other type.
func structFieldsOf(ty Type) []FieldDef {
	switch st := ty.(type) {
	case *StructType:
		return st.Fields
	case *AnonStructType:
		return st.Fields
	}
	return nil
}
