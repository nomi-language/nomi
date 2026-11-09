package analysis

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/parser"
	"strings"
)

// TypeError represents a type resolution or checking error.
//
// Line and Col start the span the error is about; EndLine and EndCol are one
// past its last byte (1-based lines, byte columns). A zero EndLine means the
// error names only a point (CompleteSpans widens it to the token there).
// Message is the primary sentence. Hints are standalone "help:" sentences
// (a "did you mean"), and Related are other locations the error refers to
// (the declaration a requirement comes from, a first definition).
type TypeError struct {
	Line    int
	Col     int
	EndLine int
	EndCol  int
	Message string
	Hints   []string
	Related []RelatedInfo
	// Code is an optional machine-readable tag (e.g. "unused-import") that
	// lets downstream consumers — notably the LSP code-action handler —
	// recognize a diagnostic class without sniffing the message text. Empty
	// for the vast majority of diagnostics.
	Code string
	// File is the path of the file Line and Col are in, when the error may
	// be reported with another file's diagnostics: a missing impl a derive
	// in an imported file demands is reported with the entry's. Empty means
	// the file it is reported with.
	File string
	// Fix, when set, is the edit that turns the source into what the
	// error says Nomi writes (parser.Fix's Edits), for an editor's quick
	// fix and `nomi check --format json`.
	Fix *parser.Fix
}

func (e TypeError) Error() string {
	return fmt.Sprintf("line %d, col %d: %s", e.Line, e.Col, e.Message)
}

// ResolveTypeExpr converts a TypeExpr AST node into a Type.
//
// `typeParams` maps type parameter names to their TypeParam_ values (nil
// if no generics in scope).
//
// `fa` is the file the type expression is written in, or nil. When non-nil,
// every nested *ast.AnonStructType node has a SymbolField written in its
// References at each field name's position, so the LSP can show
// `name: String` on hover — the same shape checkStructLit uses for
// anon-struct *literals* — and each field of the resolved type names the
// file (FieldDef.File), so a use in another file finds the field there
// (anonFieldDeclared). Pass nil from contexts where reference registration
// isn't desired (e.g., resolving an interface bound where there's no
// field-name UX to surface).
func ResolveTypeExpr(te ast.TypeExpr, reg *TypeRegistry, typeParams map[string]*TypeParam_, fa *FileAnalysis) (Type, error) {
	return resolveTypeExpr(te, reg, typeParams, fa, false)
}

// ResolveDeclaredType is ResolveTypeExpr for a type written in a DECLARATION
// position — a parameter, a return type, a field, a binding annotation, a
// distinct-type inner, a typealias target, an enum variant payload, an
// interface method signature or field, a `where` bound. There a reference to a
// generic declaration must supply the arity the declaration declares:
// `Box` where `Box<T>` was declared leaves `T` unbound, which is accepted
// today and then fails at the first USE of the value rather than where the
// under-specified type was written.
//
// Two positions deliberately keep the lenient entry point, because in each the
// argument is recovered rather than missing:
//
//   - An `impl` block's INTERFACE header. `impl Iter for List<T>` names a
//     one-parameter interface with no arguments, and recordImplTypeArgsFromBlock
//     solves them by unifying the interface's declared method signatures against
//     the block's own (type_builder.go). Supplying them explicitly is allowed and
//     already arity-checked there. Sites across std/, tests/ and the tour
//     are this shape.
//   - A struct LITERAL's type name and a pattern's head. `Box{inner: 1}` takes
//     its argument from the field values, and there is no turbofish on a struct
//     literal — `Box<Int>{…}` parses as a comparison — so there is no spelling
//     that would satisfy a rule here. Many sites in the same trees are this shape.
func ResolveDeclaredType(te ast.TypeExpr, reg *TypeRegistry, typeParams map[string]*TypeParam_, fa *FileAnalysis) (Type, error) {
	return resolveTypeExpr(te, reg, typeParams, fa, true)
}

// resolveTypeExpr carries `declared` through the whole recursion, so a nested
// argument is held to the same standard as its head: `List<Box>` in a
// parameter type rejects the `Box`.
func resolveTypeExpr(te ast.TypeExpr, reg *TypeRegistry, typeParams map[string]*TypeParam_, fa *FileAnalysis, declared bool) (Type, error) {
	switch n := te.(type) {
	case *ast.SimpleType:
		// Check type params first (for generic T).
		if typeParams != nil {
			if tp, ok := typeParams[n.Name]; ok {
				return tp, nil
			}
		}
		// Then check registry.
		if t := reg.Lookup(n.Name); t != nil {
			if declared {
				// `Route` where `Route<P = Unit>` was declared is
				// `Route<Unit>` (default_type_args.go).
				if !hasTypeArgs(t) && takesDefaultArgs(t, 0) {
					return instantiateNominal(t, withDefaultArgs(t, []Type{})), nil
				}
				if terr := checkGenericArity(t, n.Line, n.Col); terr != nil {
					return nil, *terr
				}
			}
			return t, nil
		}
		return nil, unknownTypeError(reg, n.Line, n.Col, n.Name)

	case *ast.GenericType:
		// Resolve all type arguments recursively.
		args := make([]Type, len(n.Params))
		for i, p := range n.Params {
			resolved, err := resolveTypeExpr(p, reg, typeParams, fa, declared)
			if err != nil {
				return nil, err
			}
			args[i] = resolved
		}

		switch n.Name {
		case "List":
			if len(args) != 1 {
				return nil, TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf("List expects 1 type argument, got %d", len(args))}
			}
			return &ListType{Elem: args[0]}, nil

		case "Map":
			if len(args) != 2 {
				return nil, TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf("Map expects 2 type arguments, got %d", len(args))}
			}
			return &MapType{Key: args[0], Val: args[1]}, nil

		case "Partial":
			// `Partial<T>` — the deep-partial parameter type used by
			// `Struct.update`. A compiler-known type *operator*, recognized only
			// here by name: unlike `List`/`Map` (which have real `host type`
			// declarations in std/ plus generic-syntax sugar), `Partial` has NO
			// `.nomi` declaration — it has no inhabitants (no values, no
			// constructor, no methods), exists only in parameter position, and
			// lowers to the deep-partial matcher. See partial.go.
			if len(args) != 1 {
				return nil, TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf("Partial expects 1 type argument, got %d", len(args))}
			}
			// Partial patches a struct's fields, so a concrete inner must be a
			// struct. A type-param inner (Struct.update's own `Partial<T>`, or
			// a user generic patch fn) is deferred to the call site, where
			// checkPartialParamStructs validates it once T is solved.
			if isSolvedParam(args[0]) && !isStructShaped(args[0]) {
				return nil, TypeError{Line: n.Line, Col: n.Col, Message: fmt.Sprintf(
					"Partial<%s> is invalid: `%s` is not a struct, so it has no fields to patch", args[0], args[0])}
			}
			return &PartialType{Inner: args[0]}, nil

		default:
			// Look up base type in registry and instantiate with type args.
			if t := reg.Lookup(n.Name); t != nil {
				if declared {
					if terr := checkGenericArityArgs(t, len(args), n.Line, n.Col); terr != nil {
						return nil, *terr
					}
				}
				// `Pair<Int>` where `Pair<A, B = String>` was declared is
				// `Pair<Int, String>` (default_type_args.go).
				if takesDefaultArgs(t, len(args)) {
					args = withDefaultArgs(t, args)
				}
				return instantiateNominal(t, args), nil
			}
			return nil, unknownTypeError(reg, n.Line, n.Col, n.Name)
		}

	case *ast.FuncType:
		params := make([]Type, len(n.Params))
		for i, p := range n.Params {
			resolved, err := resolveTypeExpr(p, reg, typeParams, fa, declared)
			if err != nil {
				return nil, err
			}
			params[i] = resolved
		}

		if n.Return == nil {
			// No arrow — this is a tuple type.
			return &TupleType{Elems: params}, nil
		}

		ret, err := resolveTypeExpr(n.Return, reg, typeParams, fa, declared)
		if err != nil {
			return nil, err
		}
		return &FuncType{Params: params, Return: ret}, nil

	case *ast.QualifiedType:
		// Look up by full dotted name — the registry stores module-qualified types this way.
		qualName := n.TypeString()
		if t := reg.Lookup(qualName); t != nil {
			if declared {
				if terr := checkGenericArity(t, n.ModuleLine, n.ModuleCol); terr != nil {
					return nil, *terr
				}
			}
			return t, nil
		}
		// A qualified name carrying type arguments — `Probe.Reading<Int>` —
		// is registered under its bare dotted name, so TypeString's key
		// includes the arguments and never matches. Rebuild it as an
		// ordinary generic whose name is the dotted base and let the branch
		// above instantiate it, rather than repeating the per-kind
		// instantiation for a second spelling of the same thing.
		if member, ok := n.Member.(*ast.GenericType); ok {
			base := &ast.GenericType{
				Name:   n.Module + "." + member.Name,
				Params: member.Params,
				Line:   member.Line,
				Col:    member.Col,
			}
			return resolveTypeExpr(base, reg, typeParams, fa, declared)
		}
		e := TypeError{Line: n.ModuleLine, Col: n.ModuleCol, Message: fmt.Sprintf("unknown type %q", qualName)}
		if et, ok := reg.Lookup(n.Module).(*EnumType); ok {
			if member, ok := n.Member.(*ast.SimpleType); ok {
				e = e.WithHint(variantTypeHint(et, member.Name))
			}
		}
		return nil, e

	case *ast.SelfType:
		if typeParams != nil {
			if tp, ok := typeParams["self"]; ok {
				return tp, nil
			}
		}
		return nil, TypeError{Line: n.Line, Col: n.Col, Message: "self is only valid in interface definitions"}

	case *ast.AnonStructType:
		// Resolve each field's type recursively. Field names propagate as-is.
		// Returns the existing *analysis.AnonStructType, which already
		// participates in TypesEqual structural equality (see types.go).
		//
		// When `fa` is non-nil, register a self-referential SymbolField at
		// each field name's position in its References, and name the file
		// in the field (typeFieldFile). The anon struct type has no upstream
		// declaration site — the literal/type position IS the declaration —
		// so the symbol owns its own Pos and Type. This mirrors checkStructLit
		// for anon struct *literals* so hover behaves consistently across
		// value and type positions.
		fields := make([]FieldDef, 0, len(n.Fields))
		seen := make(map[string]bool, len(n.Fields))
		for _, f := range n.Fields {
			// Spec §2 *Naming Conventions* again, and the TYPE half of the same gap the literal
			// path had: a parameter or binding annotated
			// `.{is_ok: Int, ok_PRED: Int}` reaches the IR builder without ever
			// involving an anon struct LITERAL, so checking only the literal
			// would leave the same collision reachable by a second route.
			// Shares isSnakeCase with checker.requireSnakeCase rather than
			// restating the predicate.
			if !isSnakeCase(f.Name) {
				return nil, TypeError{Line: f.Line, Col: f.Col, Message: fmt.Sprintf("anon struct field name %q must be snake_case", f.Name)}
			}
			if seen[f.Name] {
				return nil, TypeError{Line: f.Line, Col: f.Col, Message: fmt.Sprintf("duplicate field '%s' in anon struct type", f.Name)}
			}
			seen[f.Name] = true
			ty, err := resolveTypeExpr(f.TypeAnnotation, reg, typeParams, fa, declared)
			if err != nil {
				return nil, err
			}
			fields = append(fields, FieldDef{Name: f.Name, Type: ty, Line: f.Line, Col: f.Col, File: typeFieldFile(fa, f.Line)})
			if fa != nil && fa.References != nil && f.Line > 0 {
				fa.References[Pos{Line: f.Line, Col: f.Col}] = &Symbol{
					Name: f.Name,
					Kind: SymbolField,
					Pos:  Pos{Line: f.Line, Col: f.Col},
					Type: ty,
				}
			}
		}
		return &AnonStructType{Fields: fields}, nil

	default:
		return nil, fmt.Errorf("unsupported TypeExpr node: %T", te)
	}
}

// typeFieldFile is the file a field an anonymous struct type names at line
// in fa is declared in, for FieldDef.File: fa's path when it is a user file
// with one, as literalFieldDef records a literal's label. A stdlib file, a
// path-less one and the synthesized band name none, and their fields are
// linked only within the file that resolves them (anonFieldDecl).
func typeFieldFile(fa *FileAnalysis, line int) string {
	if fa == nil || line <= 0 || IsSynthesizedLine(line) || IsStdlibKey(fa.Origin) {
		return ""
	}
	return fa.FilePath
}

// unknownTypeError is the error for a type name written at line:col that
// names no type. Its hint names a dotted type whose last segment matches,
// since writing `Reading` for `Probe.Reading` is the predictable mistake: the
// qualifier looks like a path that could be dropped, and is not.
func unknownTypeError(reg *TypeRegistry, line, col int, name string) TypeError {
	e := TypeError{Line: line, Col: col, EndLine: line, EndCol: col + len(name), Message: fmt.Sprintf("unknown type %q", name)}
	if reg != nil && !strings.Contains(name, ".") {
		if full := reg.SuggestDotted(name); full != "" {
			return e.WithHint(fmt.Sprintf("did you mean %q? That is one name, not %q inside %q",
				full, name, full[:strings.LastIndex(full, ".")]))
		}
	}
	if !strings.Contains(name, ".") {
		if h := stdTypeImportHint(name); h != "" {
			return e.WithHint(h)
		}
	}
	if reg != nil && !strings.Contains(name, ".") {
	}
	if reg != nil && !strings.Contains(name, ".") {
		if et := reg.EnumWithVariant(name); et != nil {
			return e.WithHint(variantTypeHint(et, name))
		}
	}
	var candidates []string
	if reg != nil {
		candidates = reg.Names()
	}
	return e.WithHint(didYouMean(name, candidates))
}

// variantTypeHint explains, for a type name that names a variant of et, that
// a variant declared inside an enum is not a type (spec §4 *Variants as
// Parameter Types*), and how to write what was meant. It is empty when et has
// no such variant.
func variantTypeHint(et *EnumType, variant string) string {
	for _, v := range et.Variants {
		if v.Name != variant {
			continue
		}
		if v.Embedded != nil {
			return fmt.Sprintf("write `%s`, the embedded variant's own type", v.Embedded)
		}
		if strings.HasPrefix(et.Origin, "std/") {
			return fmt.Sprintf("`%s` is a variant of `%s`, not a type: take a `%s`", variant, et.Name, et.Name)
		}
		return fmt.Sprintf("`%s` is a variant of `%s`, not a type: take a `%s`, or declare `%s` on its own and write `embeds %s` in `%s`",
			variant, et.Name, et.Name, variant, variant, et.Name)
	}
	return ""
}
