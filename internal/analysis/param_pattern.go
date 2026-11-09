package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// Irrefutable parameter destructuring (spec §7, "Parameter Destructuring").
//
// A function-definition or lambda parameter may carry an irrefutable
// destructuring pattern in place of a bare name. The two analyzer pieces this
// file owns are shared by the signature builder (buildFuncType) and the body
// checker (checkFunc / checkLambda):
//
//   - paramPatternType derives a parameter's type. The type comes from either
//     the `: T` annotation or — for self-typing patterns whose head names a
//     concrete type (distinct `Dur(x)`, typed struct `Point{x, y}`, a
//     distinct over a tuple `Pair(a, b)`, qualified single-variant enum
//     `E.V(x)`) — the pattern head. Type-less shapes (tuple, anon struct,
//     dot-variant, plain ident) require an annotation.
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
	case *ast.VectorPattern:
		return "vector"
	case *ast.SetPattern:
		return "set"
	}
	return "destructuring"
}

// paramPatternHeadType resolves the concrete type a self-typing pattern head
// names, or returns (nil, nil) if the pattern is type-less (no head to read a
// type from). A non-nil error reports a genuine resolution failure.
func paramPatternHeadType(pattern ast.Node, reg *TypeRegistry, typeParams map[string]*TypeParam_, fa *FileAnalysis) (Type, error) {
	// `Point{x, y} as p` is typed by its pattern's head.
	switch p := ast.WithoutAs(pattern).(type) {
	case *ast.EnumPattern:
		switch v := p.Variant.(type) {
		case *ast.SimpleType:
			// `Dur(x)` — head names the distinct/enum type directly.
			return ResolveTypeExpr(v, reg, typeParams, fa)
		case *ast.QualifiedType:
			// `units.Days(n)` may name a qualified distinct type.
			// Prefer the full dotted type when it exists; otherwise `E.V(x)`
			// is an enum variant pattern where the qualifier E is the type and
			// V is the variant.
			if t, err := ResolveTypeExpr(v, reg, typeParams, fa); err == nil && t != nil {
				return t, nil
			}
			head := &ast.SimpleType{Name: v.Module, Line: v.ModuleLine, Col: v.ModuleCol}
			return ResolveTypeExpr(head, reg, typeParams, fa)
		default:
			// `.V(x)` (DotVariantType) — no type head; needs an annotation.
			return nil, nil
		}
	case *ast.StructPattern:
		if p.TypeName == nil {
			return nil, nil // anonymous struct `.{x, y}` — no type head
		}
		if qt, ok := p.TypeName.(*ast.QualifiedType); ok {
			// `Outer.Inner{...}` may name a qualified struct type. Prefer the
			// full dotted type when it exists; otherwise `E.V{...}` is an enum
			// struct variant pattern where the qualifier E is the type and V is
			// the variant (e.g. `Shape.Circle{radius}`).
			if t, err := ResolveTypeExpr(qt, reg, typeParams, fa); err == nil && t != nil {
				return t, nil
			}
			head := &ast.SimpleType{Name: qt.Module, Line: qt.ModuleLine, Col: qt.ModuleCol}
			return ResolveTypeExpr(head, reg, typeParams, fa)
		}
		return ResolveTypeExpr(p.TypeName, reg, typeParams, fa)
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
	if p.TypeAnnotation != nil {
		return ResolveTypeExpr(p.TypeAnnotation, reg, typeParams, fa)
	}

	headTy, err := paramPatternHeadType(p.Destructure, reg, typeParams, fa)
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

// resolveParamType resolves a parameter's written type, declared as
// ResolveDeclaredType reads it or not as ResolveTypeExpr does. A parameter
// written as its type alone whose name is also a variant in scope is
// reported instead (typeOnlyParamError).
func resolveParamType(p ast.Param, fa *FileAnalysis, reg *TypeRegistry, typeParams map[string]*TypeParam_, declared bool) (Type, error) {
	if te := typeOnlyParamError(p, fa, reg); te != nil {
		return nil, *te
	}
	if declared {
		return ResolveDeclaredType(p.TypeAnnotation, reg, typeParams, fa)
	}
	return ResolveTypeExpr(p.TypeAnnotation, reg, typeParams, fa)
}

// typeOnlyParamError reports a parameter written as its type alone,
// `fn stats(Request)` (ast.Param.TypeOnly), whose name is also a variant in
// scope, so it may be read as a pattern for that variant:
//
//   - A variant of an enum in scope that may not be written bare in a
//     pattern (allowsBareVariantPattern's rule), `fn f(One)`, reads as an
//     attempt to match it. With no type of that name the error is the
//     pattern check's own "bare variant" one. When a type has the name too,
//     the parameter is ambiguous, and the error says how to write each
//     reading.
//   - A variant that may be written bare, `fn f(None)`, is a pattern for
//     that variant, which no parameter may be; with a type of that name too
//     it is ambiguous as well.
//
// Otherwise it is nil, and the name resolves as the parameter's type, or as
// an unknown type.
func typeOnlyParamError(p ast.Param, fa *FileAnalysis, reg *TypeRegistry) *TypeError {
	if !p.TypeOnly {
		return nil
	}
	head, ok := p.TypeAnnotation.(*ast.SimpleType)
	if !ok {
		return nil
	}
	name := head.Name
	isType := reg != nil && reg.Lookup(name) != nil
	takeType := fmt.Sprintf("to take a `%s`, write `_: %s`, or `name: %s` to use it", name, name, name)
	te := &TypeError{Line: head.Line, Col: head.Col, Message: fmt.Sprintf("parameter '%s' needs a name", name)}
	if enum := bareParamVariantEnum(fa, name, Pos{Line: head.Line, Col: head.Col}); enum != "" {
		if isType {
			te.Hints = []string{
				fmt.Sprintf("`%s` names both a type and a variant of `%s`; %s", name, enum, takeType),
				fmt.Sprintf("write `.%s` (or `%s.%s`) to match the variant", name, enum, name),
			}
			return te
		}
		te.Message = bareVariantPatternMessage(name, enum)
		te.Hints = []string{fmt.Sprintf("to take the value, write `_: %s`, or `name: %s` to use it", enum, enum)}
		return te
	}
	if !bareVariantInScope(fa, name) {
		return nil
	}
	if isType {
		te.Hints = []string{fmt.Sprintf("`%s` names both a type and a variant; %s", name, takeType)}
		return te
	}
	te.Hints = []string{fmt.Sprintf("`%s` is a variant, not a type; take the value as `name: T`, with the type it belongs to, and match it in the body", name)}
	return te
}

// bareVariantInScope reports whether name is a variant a pattern may write
// bare: a prelude variant (`None`, `Ok`) or one an import binds by name.
func bareVariantInScope(fa *FileAnalysis, name string) bool {
	if isPreludeBareVariant(name) {
		return true
	}
	if fa == nil || fa.ModuleScope == nil {
		return false
	}
	return isImportedVariantSymbol(fa.ModuleScope.Lookup(name))
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
	case *ast.VectorPattern:
		return p.Col
	case *ast.SetPattern:
		return p.Col
	case *ast.AsPattern:
		return p.Col
	case *ast.OrPattern:
		return p.Col
	}
	return 0
}

// checkParamPatternRefutable reports an error for each refutable sub-pattern in
// a parameter position. It is type-driven, recursing into tuple/struct element
// types so a nested refutable pattern (`(.Some(x), n)`) is caught. addError is
// the checker's error sink (line, col, msg).
func (c *checker) checkParamPatternRefutable(pattern ast.Node, ty Type, addError func(line, col int, msg string)) {
	switch p := pattern.(type) {
	case *ast.WildcardPattern, *ast.IdentPattern:
		return

	case *ast.AsPattern:
		// The name matches anything; the pattern decides.
		c.checkParamPatternRefutable(p.Pattern, ty, addError)

	case *ast.OrPattern:
		// Alternatives that together match every value are irrefutable,
		// whatever each one matches alone: by the coverage rules a `case`
		// applies to its arms. An unknown type is checkPattern's to report.
		if ty != nil && !c.patternsExhaustiveOver(ty, p.Alternatives) {
			addError(p.Line, p.Col, refutableParamMsg)
		}

	case *ast.MapPattern:
		addError(p.Line, p.Col, refutableParamMsg)

	case *ast.ListPattern:
		addError(p.Line, p.Col, refutableParamMsg)

	case *ast.VectorPattern, *ast.SetPattern:
		// Refutable as a list pattern is: a vector's length and a set's
		// elements are not in their types.
		addError(p.LineNum(), patternColOf(p), refutableParamMsg)

	case *ast.EnumPattern:
		if _, isSelf := p.Variant.(*ast.SelfType); isSelf {
			// `self(p)` takes apart a distinct type, which is irrefutable;
			// its payload is matched against what self wraps.
			if p.Payload != nil {
				c.checkParamPatternRefutable(p.Payload, nil, addError)
			}
			return
		}
		// Distinct heads are irrefutable; multi-variant enum variants are not.
		switch ty := ty.(type) {
		case *DistinctType:
			// `Dur(x)` — irrefutable. Recurse into the payload against the
			// distinct's inner type so a refutable nested payload is caught.
			if p.Payload != nil {
				c.checkParamPatternRefutable(p.Payload, ty.Inner, addError)
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
				c.checkParamPatternRefutable(p.Payload, ty.Variants[0].DataType, addError)
			}
		default:
			// Unknown / unresolved type — let checkPattern report the
			// mismatch; don't double-report a refutability error.
		}

	case *ast.TuplePattern:
		if tt, ok := ty.(*TupleType); ok && len(tt.Elems) == len(p.Patterns) {
			for i, sub := range p.Patterns {
				c.checkParamPatternRefutable(sub, tt.Elems[i], addError)
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
			c.checkParamPatternRefutable(f.Pattern, ft, addError)
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

// paramWritten is a parameter as its function's signature writes it, for a
// diagnostic that names one with no name of its own: a pattern with its
// annotation (`.{id}: .{id: Uuid}`), or the type of one written as its type
// (`fn stats(Request)`). A named parameter is its name.
func paramWritten(p ast.Param) string {
	switch {
	case p.Destructure != nil && p.TypeAnnotation != nil:
		return patternSource(p.Destructure) + ": " + p.TypeAnnotation.TypeString()
	case p.Destructure != nil:
		return patternSource(p.Destructure)
	case p.TypeOnly && p.TypeAnnotation != nil:
		return p.TypeAnnotation.TypeString()
	}
	return p.Name
}

// paramNamed reports whether p has a name the program wrote, which a
// diagnostic quotes and a named argument can pass. The synthesized slot of a
// destructured parameter (`__destr_<line>_<col>`) and the `_` of one written
// as its type are not.
func paramNamed(p ast.Param) bool {
	return p.Destructure == nil && !p.TypeOnly && p.Name != ""
}

// paramLabel names p in a diagnostic: `parameter 'name'`, or `parameter`
// and the parameter as it is written when it has no name.
func paramLabel(p ast.Param) string {
	if paramNamed(p) {
		return fmt.Sprintf("parameter '%s'", p.Name)
	}
	return fmt.Sprintf("parameter `%s`", paramWritten(p))
}

// paramHead is what stands before a parameter's `: Type` in a signature a
// hint writes out: its pattern when it destructures, else its name.
func paramHead(p ast.Param) string {
	if p.Destructure != nil {
		return patternSource(p.Destructure)
	}
	return p.Name
}
