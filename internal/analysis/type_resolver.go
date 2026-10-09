package analysis

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/ast"
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
}

func (e TypeError) Error() string {
	return fmt.Sprintf("line %d, col %d: %s", e.Line, e.Col, e.Message)
}

// ResolveTypeExpr converts a TypeExpr AST node into a Type.
//
// `typeParams` maps type parameter names to their TypeParam_ values (nil
// if no generics in scope).
//
// `refs` is an optional file-level References map. When non-nil, every
// nested *ast.AnonStructType node has a SymbolField written at each
// field name's position so the LSP can show `name: String` on hover —
// the same shape checkStructLit uses for anon-struct *literals*. Pass
// nil from contexts where reference registration isn't desired (e.g.,
// resolving an interface bound where there's no field-name UX to surface).
func ResolveTypeExpr(te ast.TypeExpr, reg *TypeRegistry, typeParams map[string]*TypeParam_, refs map[Pos]*Symbol) (Type, error) {
	return resolveTypeExpr(te, reg, typeParams, refs, false)
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
func ResolveDeclaredType(te ast.TypeExpr, reg *TypeRegistry, typeParams map[string]*TypeParam_, refs map[Pos]*Symbol) (Type, error) {
	return resolveTypeExpr(te, reg, typeParams, refs, true)
}

// resolveTypeExpr carries `declared` through the whole recursion, so a nested
// argument is held to the same standard as its head: `List<Box>` in a
// parameter type rejects the `Box`.
func resolveTypeExpr(te ast.TypeExpr, reg *TypeRegistry, typeParams map[string]*TypeParam_, refs map[Pos]*Symbol, declared bool) (Type, error) {
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
			resolved, err := resolveTypeExpr(p, reg, typeParams, refs, declared)
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
				switch bt := t.(type) {
				case *EnumType:
					inst := &EnumType{
						Origin:           bt.Origin,
						Name:             bt.Name,
						TypeArgs:         args,
						Variants:         bt.Variants,
						TypeParams:       bt.TypeParams,
						TypeParamDefs:    bt.TypeParamDefs,
						Opaque:           bt.Opaque,
						OwningSourceFile: bt.OwningSourceFile,
					}
					inst.unbuilt = bt.unbuilt.recordInstance(inst)
					return inst, nil
				case *StructType:
					inst := &StructType{
						Origin:           bt.Origin,
						Name:             bt.Name,
						TypeArgs:         args,
						Fields:           bt.Fields,
						TypeParams:       bt.TypeParams,
						TypeParamDefs:    bt.TypeParamDefs,
						Opaque:           bt.Opaque,
						OwningSourceFile: bt.OwningSourceFile,
					}
					inst.unbuilt = bt.unbuilt.recordInstance(inst)
					return inst, nil
				case *InterfaceType:
					inst := &InterfaceType{
						Origin:        bt.Origin,
						Name:          bt.Name,
						Methods:       bt.Methods,
						TypeParams:    bt.TypeParams,
						TypeParamDefs: bt.TypeParamDefs,
						TypeArgs:      args,
						SelfParam:     bt.SelfParam,
					}
					inst.unbuilt = bt.unbuilt.recordInstance(inst)
					return inst, nil
				case *DistinctType:
					// Generic opaque externs (`host type Task<T>`,
					// `host type Channel<T>`): instantiate the shell with
					// the resolved TypeArgs so unification at call sites
					// reaches the inner T. The TypeParamDefs pointer is
					// shared with the registry shell, so substitution in
					// checkGenericCall solves the right TypeParam_.
					return &DistinctType{
						Origin:           bt.Origin,
						Name:             bt.Name,
						Inner:            bt.Inner,
						Opaque:           bt.Opaque,
						OwningSourceFile: bt.OwningSourceFile,
						TypeParams:       bt.TypeParams,
						TypeParamDefs:    bt.TypeParamDefs,
						TypeArgs:         args,
					}, nil
				default:
					return t, nil
				}
			}
			return nil, unknownTypeError(reg, n.Line, n.Col, n.Name)
		}

	case *ast.FuncType:
		params := make([]Type, len(n.Params))
		for i, p := range n.Params {
			resolved, err := resolveTypeExpr(p, reg, typeParams, refs, declared)
			if err != nil {
				return nil, err
			}
			params[i] = resolved
		}

		if n.Return == nil {
			// No arrow — this is a tuple type.
			return &TupleType{Elems: params}, nil
		}

		ret, err := resolveTypeExpr(n.Return, reg, typeParams, refs, declared)
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
			return resolveTypeExpr(base, reg, typeParams, refs, declared)
		}
		return nil, TypeError{Line: n.ModuleLine, Col: n.ModuleCol, Message: fmt.Sprintf("unknown type %q", qualName)}

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
		// When `refs` is non-nil, register a self-referential SymbolField at
		// each field name's position. The anon struct type has no upstream
		// declaration site — the literal/type position IS the declaration —
		// so the symbol owns its own Pos and Type. This mirrors checkStructLit
		// for anon struct *literals* so hover behaves consistently across
		// value and type positions.
		fields := make([]FieldDef, 0, len(n.Fields))
		seen := make(map[string]bool, len(n.Fields))
		for _, f := range n.Fields {
			// Spec §4 again, and the TYPE half of the same gap the literal
			// path had: a parameter or binding annotated
			// `{ok?: Int, ok_PRED: Int}` reaches the IR builder without ever
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
			ty, err := resolveTypeExpr(f.TypeAnnotation, reg, typeParams, refs, declared)
			if err != nil {
				return nil, err
			}
			fields = append(fields, FieldDef{Name: f.Name, Type: ty, Line: f.Line, Col: f.Col})
			if refs != nil && f.Line > 0 {
				refs[Pos{Line: f.Line, Col: f.Col}] = &Symbol{
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
	var candidates []string
	if reg != nil {
		candidates = reg.Names()
	}
	return e.WithHint(didYouMean(name, candidates))
}
