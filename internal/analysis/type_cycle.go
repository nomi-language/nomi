package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Two cycle checks hold a declaration that is defined in terms of itself,
// and both run the one walk, typeCycle, over a different relation:
//
//   - a distinct type's representation (distinctCycle): what its inner type
//     and a refinement's parents hold, at every position, through the inner
//     types and parents of other distinct types. `type L List<L>` is a
//     cycle here.
//   - a value (valueCycle): what a value of a struct, an enum or a distinct
//     type must already hold before it can be built. A struct's value holds
//     each field without a default, an enum's holds one of its variants,
//     and a distinct type's holds its inner type. `struct S` with `s: S` is
//     a cycle here, and `struct S` with `next: Option<S>` is not.
//
// Each check runs where its declaration is built, after the declaration's
// own parts are set. A cycle runs through some declarations; the last of
// them to be built finds every other's parts already set, or set on the
// copies it reaches (unbuilt_decl.go), so the cycle is reported at it.

// cycleStep is one step of the walk: the type it reaches, and the name the
// reported path shows for it, or "" for a step the path does not name.
type cycleStep struct {
	label string
	t     Type
}

// cycleEdges answers the steps the walk takes from t, and whether a path
// back to the declaration must run through every one of them (every) or
// through any one. key, when not nil, names t while the walk is inside it:
// a relation whose steps can return to a type they left keys that type, and
// the walk answers no path for a type it is already inside.
type cycleEdges func(t Type) (steps []cycleStep, every bool, key any)

// typeCycle answers the labels along a path from the steps from back to
// the declaration decl, ending with decl's name, or nil when there is none.
// path is the labels the reported path starts with. decl is found by its
// declaration (sameDeclaration), not by pointer: a use with type arguments
// (`A<Int>`, `A<T>`, `B<A<T>>`) is a copy of the declaration's type
// (instantiateNominal), so a copy closes the same cycle the declaration
// would.
//
// A type the walk is already inside answers no path, as does one deeper
// than maxTypeWalkDepth, so the walk ends on a cycle that does not run
// through decl and on a generic type whose arguments grow at each step. A
// type from which no path leads, whatever the walk was inside, is not
// walked again.
func typeCycle(decl Type, from []cycleStep, every bool, path []string, edges cycleEdges) []string {
	w := &cycleWalk{decl: decl, edges: edges, inside: map[any]bool{}, none: map[Type]bool{}}
	found, _ := w.steps(from, every, path, 0)
	return found
}

type cycleWalk struct {
	decl   Type
	edges  cycleEdges
	inside map[any]bool
	none   map[Type]bool
}

// steps answers a path through steps, all of them when every is set. open
// reports that the answer rests on a type the walk was inside or on the
// depth bound, so no path from here is not a fact about the types alone.
func (w *cycleWalk) steps(steps []cycleStep, every bool, path []string, depth int) (found []string, open bool) {
	for _, s := range steps {
		p := path
		if s.label != "" {
			p = append(append([]string(nil), path...), s.label)
		}
		f, o := w.visit(s.t, p, depth)
		open = open || o
		switch {
		case every && f == nil:
			return nil, open
		case every && found == nil:
			found = f
		case !every && f != nil:
			return f, open
		}
	}
	return found, open
}

func (w *cycleWalk) visit(t Type, path []string, depth int) ([]string, bool) {
	if t == nil {
		return nil, false
	}
	if sameDeclaration(t, w.decl) {
		return append(append([]string(nil), path...), declarationName(w.decl)), false
	}
	if depth > maxTypeWalkDepth {
		return nil, true
	}
	if w.none[t] {
		return nil, false
	}
	steps, every, key := w.edges(t)
	if key != nil {
		if w.inside[key] {
			return nil, true
		}
		w.inside[key] = true
		defer delete(w.inside, key)
	}
	found, open := w.steps(steps, every, path, depth+1)
	if found == nil && !open {
		w.none[t] = true
	}
	return found, open
}

// sameDeclaration reports whether t is the declaration decl or a use of it:
// the same kind of type with decl's nominal identity.
func sameDeclaration(t, decl Type) bool {
	switch d := decl.(type) {
	case *StructType:
		s, ok := t.(*StructType)
		return ok && sameNominalIdentity(s.Origin, s.Name, d.Origin, d.Name)
	case *EnumType:
		e, ok := t.(*EnumType)
		return ok && sameNominalIdentity(e.Origin, e.Name, d.Origin, d.Name)
	case *DistinctType:
		dt, ok := t.(*DistinctType)
		return ok && sameNominalIdentity(dt.Origin, dt.Name, d.Origin, d.Name)
	}
	return false
}

// declarationName is the name a cycle's path ends with.
func declarationName(decl Type) string {
	switch d := decl.(type) {
	case *StructType:
		return d.Name
	case *EnumType:
		return d.Name
	case *DistinctType:
		return d.Name
	}
	return decl.String()
}

// distinctCycle reports whether inner, about to become dt's inner type or
// one of a refinement's parents, contains dt itself, directly or through
// the inner types and parents of other distinct types. It returns the
// names along the way, starting and ending with dt's, or nil when there is
// no cycle.
//
// A distinct type's inner type is its representation, held by pointer
// (DistinctType.Inner). `type L List<L>` would make that a cyclic graph, and
// every walk over types that follows Inner would recurse until the stack
// overflows. A struct's fields and an enum's variants are looked up through
// the declaration instead, which is why `struct T { kids: List<T> }` is the
// way to write a recursive type.
//
// Why no cycle through Inner or MoreParents survives: buildTypeDef is the
// only place a declaration's Inner and MoreParents are chosen, and it keeps
// none this reports. Every other assignment copies a declaration's
// (instantiateNominal, distinctBuilt) or rebuilds an instance's from it,
// which adds no edge back to a declaration. So the last declaration of a
// cycle to be built finds the cycle and does not keep its edge, and a walk
// that follows Inner, as collectTypeParamsByName does with no bound of its
// own, ends. For the same reason this relation needs no keys: apart from
// dt itself it has no cycle to be inside.
func distinctCycle(dt *DistinctType, inner Type) []string {
	return typeCycle(dt, []cycleStep{{t: inner}}, false, []string{dt.Name}, representationEdges)
}

// representationEdges is distinctCycle's relation: every position a type
// holds (typeChildren), and for a distinct type, its type arguments and,
// one step further along the path, the type it wraps and a refinement's other
// parents.
func representationEdges(t Type) ([]cycleStep, bool, any) {
	dt, ok := t.(*DistinctType)
	if !ok {
		kids := typeChildren(t)
		steps := make([]cycleStep, len(kids))
		for i, k := range kids {
			steps[i] = cycleStep{t: k}
		}
		return steps, false, nil
	}
	steps := make([]cycleStep, 0, len(dt.TypeArgs)+1+len(dt.MoreParents))
	for _, a := range dt.TypeArgs {
		steps = append(steps, cycleStep{t: a})
	}
	steps = append(steps, cycleStep{label: dt.Name, t: dt.Inner})
	for _, parent := range dt.MoreParents {
		steps = append(steps, cycleStep{label: dt.Name, t: parent})
	}
	return steps, false, nil
}

// valueCycle reports whether no value of decl, a struct, an enum or a
// distinct type just built, can be built without a value of decl already
// in hand: every way to build one holds one. It returns the path back to
// decl, starting with the part of decl that holds it, or nil.
func valueCycle(decl Type) []string {
	steps, every, _ := valueEdges(decl)
	return typeCycle(decl, steps, every, nil, valueEdges)
}

// valueEdges is valueCycle's relation: what a value of t must already hold
// before it can be built, decided by t's kind alone.
//
//   - A struct holds each field without a default, and an anonymous struct
//     each of its fields: any one that holds the declaration is enough.
//   - A tuple holds each element.
//   - A distinct type, a refinement among them, holds the type it wraps. A
//     host type wraps nothing a program declares.
//   - An enum holds one of its variants, so only when every variant holds
//     the declaration does the enum. A variant with no payload, as `None`
//     and an enum's leaf are, holds nothing, and neither does an enum with
//     no variants built.
//   - Every other type holds nothing a value must already have: a list, a
//     map or a function can be built empty or from nothing, and a primitive,
//     a type parameter, an interface or a conjunction stands for values of
//     other types.
//
// A generic type's parts are its declaration's, with its type arguments in
// place of its parameters, so a field of type `T` holds what `T` is.
func valueEdges(t Type) ([]cycleStep, bool, any) {
	switch tt := t.(type) {
	case *TypeVar:
		return []cycleStep{{t: tt.Resolved}}, false, nil
	case *TupleType:
		steps := make([]cycleStep, len(tt.Elems))
		for i, e := range tt.Elems {
			steps[i] = cycleStep{t: e}
		}
		return steps, false, nil
	case *AnonStructType:
		return fieldSteps("", tt.Fields, nil, nil), false, nil
	case *StructType:
		return fieldSteps(tt.Name+".", tt.Fields, tt.TypeParamDefs, tt.TypeArgs), false, declKey{tt.Origin, tt.Name}
	case *DistinctType:
		if tt.Inner == nil {
			return nil, false, nil
		}
		return []cycleStep{{label: tt.Name, t: substituteTypeParamDefs(tt.TypeParamDefs, tt.TypeArgs, tt.Inner)}}, false, declKey{tt.Origin, tt.Name}
	case *EnumType:
		if len(tt.Variants) == 0 {
			return nil, false, nil
		}
		steps := make([]cycleStep, 0, len(tt.Variants))
		for _, v := range tt.Variants {
			var payload Type
			switch {
			case v.Embedded != nil:
				payload = v.Embedded
			case v.DataType != nil:
				payload = v.DataType
			case len(v.Fields) > 0:
				payload = &AnonStructType{Fields: v.Fields}
			default:
				return nil, false, nil
			}
			steps = append(steps, cycleStep{label: tt.Name + "." + v.Name, t: substituteTypeParamDefs(tt.TypeParamDefs, tt.TypeArgs, payload)})
		}
		return steps, true, declKey{tt.Origin, tt.Name}
	}
	return nil, false, nil
}

// declKey names a nominal declaration while valueCycle's walk is inside it.
type declKey struct{ origin, name string }

// fieldSteps is a step to each field of fields without a default, with
// args in place of the parameters defs, labeled prefix and the field's
// name.
func fieldSteps(prefix string, fields []FieldDef, defs []*TypeParam_, args []Type) []cycleStep {
	steps := make([]cycleStep, 0, len(fields))
	for _, f := range fields {
		if f.HasDefault {
			continue
		}
		label := ""
		if prefix != "" {
			label = prefix + f.Name
		}
		steps = append(steps, cycleStep{label: label, t: substituteTypeParamDefs(defs, args, f.Type)})
	}
	return steps
}

// valueCycleError is the error at the declaration named name, of kind
// ("struct", "enum" or "type"), at line:col, no value of which can be built
// without one already, along path.
func valueCycleError(kind, name string, line, col int, path []string) TypeError {
	msg := fmt.Sprintf("%s '%s' is defined in terms of itself (%s)", kind, name, strings.Join(path, " → "))
	field := "a field on this path"
	if own, ok := strings.CutPrefix(path[0], name+"."); ok && kind != "enum" {
		field = fmt.Sprintf("field '%s'", own)
	}
	hint := fmt.Sprintf("no value of '%s' can be built without one already; wrap %s in `Option`, or hold it in a `List` or in an enum with a variant that does not need one", name, field)
	if kind == "enum" {
		hint = fmt.Sprintf("no value of '%s' can be built without one already, since every variant needs one; add a variant that does not, such as one with no payload", name)
	}
	return TypeError{Line: line, Col: col, Message: msg}.WithHint(hint)
}

// distinctCycleError is the error at a distinct type declaration whose inner
// type contains the type itself.
func distinctCycleError(n *ast.TypeDef, path []string) TypeError {
	msg := fmt.Sprintf("type '%s' is defined in terms of itself", n.Name)
	if len(path) > 2 {
		msg += " (" + strings.Join(path, " → ") + ")"
	}
	if n.Refinement {
		return TypeError{Line: n.Line, Col: n.Col, Message: msg}.WithHint(fmt.Sprintf(
			"a refinement's parents are types its values already are, and their parents in turn, so none of them can be '%s' itself", n.Name))
	}
	return TypeError{
		Line: n.Line, Col: n.Col,
		Message: msg,
	}.WithHint(fmt.Sprintf("a distinct type's inner type is its representation and cannot contain '%s' itself; declare a struct or an enum to make a recursive type", n.Name))
}

// aliasBuilder builds a file's or a block's typealiases. An alias resolves
// to its target when it is built, so a declaration that names an alias
// declared below it needs that alias built first. Each alias is registered
// as a lazy entry under its name (TypeRegistry.RegisterLazy) and built at
// whichever comes first, its declaration in source order or the first
// lookup of its name; building in source order alone made `typealias C D`
// above `typealias D Int` an "unknown type" error.
//
// An alias that names itself, directly or through other aliases, is never
// built: it gets "typealias 'X' refers to itself" at its declaration
// instead, which is also what keeps the lazy builds from recursing.
type aliasBuilder struct {
	fa      *FileAnalysis
	regFor  func(*ast.TypeAlias) (*TypeRegistry, bool)
	pending map[*ast.TypeAlias]bool
	errs    []TypeError
}

// newAliasBuilder prepares the aliases among nodes. regFor answers the
// registry an alias is built against and whether the alias owns its name
// there; only an alias that owns its name is built on lookup.
func newAliasBuilder(fa *FileAnalysis, nodes []ast.Node, regFor func(*ast.TypeAlias) (*TypeRegistry, bool)) *aliasBuilder {
	ab := &aliasBuilder{fa: fa, regFor: regFor, pending: map[*ast.TypeAlias]bool{}}
	inCycle, errs := typeAliasCycles(nodes)
	ab.errs = errs
	for _, node := range nodes {
		n, ok := node.(*ast.TypeAlias)
		if !ok || inCycle[n] {
			continue
		}
		ab.pending[n] = true
		if reg, owns := regFor(n); owns {
			reg.RegisterLazy(n.Name, func() { ab.build(n) })
		}
	}
	return ab
}

// build builds n unless it is built already or is on a cycle.
func (ab *aliasBuilder) build(n *ast.TypeAlias) {
	if !ab.pending[n] {
		return
	}
	delete(ab.pending, n)
	reg, _ := ab.regFor(n)
	ab.errs = append(ab.errs, buildTypeAlias(ab.fa, reg, n)...)
}

// typeAliasCycles returns the aliases among nodes whose target names the
// alias itself, directly or through other aliases among nodes, and an error
// at each.
func typeAliasCycles(nodes []ast.Node) (map[*ast.TypeAlias]bool, []TypeError) {
	byName := map[string]*ast.TypeAlias{}
	var decls []*ast.TypeAlias
	for _, node := range nodes {
		if n, ok := node.(*ast.TypeAlias); ok {
			decls = append(decls, n)
			if _, dup := byName[n.Name]; !dup {
				byName[n.Name] = n
			}
		}
	}
	if len(decls) == 0 {
		return nil, nil
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[*ast.TypeAlias]int{}
	inCycle := map[*ast.TypeAlias]bool{}
	var errs []TypeError
	var stack []*ast.TypeAlias
	var visit func(n *ast.TypeAlias)
	visit = func(n *ast.TypeAlias) {
		switch state[n] {
		case done:
			return
		case visiting:
			start := len(stack) - 1
			for stack[start] != n {
				start--
			}
			cycle := stack[start:]
			names := make([]string, 0, len(cycle)+1)
			for _, m := range cycle {
				names = append(names, m.Name)
			}
			names = append(names, n.Name)
			for _, m := range cycle {
				if inCycle[m] {
					continue
				}
				inCycle[m] = true
				msg := fmt.Sprintf("typealias '%s' refers to itself", m.Name)
				if len(cycle) > 1 {
					msg += " (" + strings.Join(rotateCycle(names, m.Name), " → ") + ")"
				}
				errs = append(errs, TypeError{Line: m.Line, Col: m.Col, Message: msg}.
					WithHint("a typealias is another name for its target, so the target cannot name the alias; a recursive type is a struct or an enum"))
			}
			return
		}
		state[n] = visiting
		stack = append(stack, n)
		forEachTypeAliasTarget(n, func(te ast.TypeExpr) {
			typeExprNames(te, func(name string) {
				if dep := byName[name]; dep != nil {
					visit(dep)
				}
			})
		})
		stack = stack[:len(stack)-1]
		state[n] = done
	}
	for _, n := range decls {
		visit(n)
	}
	return inCycle, errs
}

func forEachTypeAliasTarget(n *ast.TypeAlias, f func(ast.TypeExpr)) {
	if len(n.Bounds) > 0 {
		for _, b := range n.Bounds {
			f(b)
		}
		return
	}
	if n.TargetTypeExpr != nil {
		f(n.TargetTypeExpr)
	}
}

// typeExprNames calls f with every unqualified type name te writes.
func typeExprNames(te ast.TypeExpr, f func(string)) {
	switch t := te.(type) {
	case *ast.SimpleType:
		f(t.Name)
	case *ast.GenericType:
		f(t.Name)
		for _, p := range t.Params {
			typeExprNames(p, f)
		}
	case *ast.FuncType:
		for _, p := range t.Params {
			typeExprNames(p, f)
		}
		if t.Return != nil {
			typeExprNames(t.Return, f)
		}
	case *ast.AnonStructType:
		for _, fld := range t.Fields {
			if fld.TypeAnnotation != nil {
				typeExprNames(fld.TypeAnnotation, f)
			}
		}
	}
}
