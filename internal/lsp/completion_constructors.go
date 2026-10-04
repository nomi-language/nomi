package lsp

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// COMPLETION OF WAYS TO BUILD THE EXPECTED TYPE.
//
// At an expression position whose expected type T is a nominal type (a
// struct, an enum, a distinct or opaque type, a host type such as Map, Set or
// Vector), completion offers what builds a T besides the names in scope:
//
//   - T's inherent owner functions that return T and take no T, inserted as
//     owner-qualified calls (`Duration.seconds(${1:n})`, `Map.empty()`). A
//     function that takes a T (`Map.put`, `Set.union`) transforms a T the
//     caller already has, which the locals of type T cover.
//   - Where T is `Maybe<X>` or `Result<X, E>`, also X's inherent functions
//     that return that same Maybe or Result (`Date.parse(s)` where
//     `Result<Date, Error>` is expected). A function returning `Result<T, _>`
//     is not offered where a plain T is expected: it does not fit there
//     without a `try` or a match.
//   - When T implements `Literal`, one typed literal written from the first
//     example its docs give (`Date"${1:2026}-${2:05}-${3:04}"`), as
//     completion inside a literal's body offers them.
//
// Each item's filter text is the function's name (the type's name for the
// literal), as an auto-import candidate's is, so `no` finds `Instant.now()`.
// The owner is reached as a pipe stage's owner functions are (ownerReach):
// visible under its own name, where the name must mean the expected type
// itself, or not visible and exported by exactly one importable file, whose
// import the item adds.

// constructorCandidates offers the ways to build the expected type.
func (r *completionRequest) constructorCandidates() []candidate {
	t := analysis.ResolveTypeVar(r.expected)
	if t == nil || r.expectsFunction() || !buildableType(t) {
		return nil
	}
	var out []candidate
	out = append(out, r.ownerConstructors(t, t)...)
	if inner, ok := wrappedType(t); ok && buildableType(inner) {
		out = append(out, r.ownerConstructors(inner, t)...)
	}
	if c, ok := r.literalConstructor(t); ok {
		out = append(out, c)
	}
	return out
}

// buildableType reports whether t is a nominal type whose owner functions
// may build it: not a primitive, a list, a function, a tuple, an interface
// or a type parameter.
func buildableType(t analysis.Type) bool {
	switch v := t.(type) {
	case *analysis.StructType, *analysis.EnumType, *analysis.DistinctType, *analysis.MapType:
		return true
	case *analysis.PrimitiveType:
		// A host type carries its declaring file; Int and String do not.
		return v.Origin != ""
	}
	return false
}

// wrappedType is X of an expected `Maybe<X>` or `Result<X, E>`.
func wrappedType(t analysis.Type) (analysis.Type, bool) {
	et, ok := t.(*analysis.EnumType)
	if !ok || len(et.TypeArgs) == 0 || (et.Name != "Maybe" && et.Name != "Result") {
		return nil, false
	}
	inner := analysis.ResolveTypeVar(et.TypeArgs[0])
	return inner, inner != nil
}

// ownerConstructors offers the inherent functions of owner's type that
// return want and take no value of owner's type, spelled `Owner.name`.
func (r *completionRequest) ownerConstructors(owner, want analysis.Type) []candidate {
	name := analysis.TypeOwnerName(owner)
	if name == "" {
		return nil
	}
	imp, ok := r.constructorReach(owner, name)
	if !ok {
		return nil
	}
	var out []candidate
	for _, sym := range sortedSymbols(r.ownerFunctions(name)) {
		fn := sym.Name
		if sym.ImplInterface != "" || strings.HasPrefix(fn, "__") {
			continue
		}
		ft, ok := analysis.ResolveTypeVar(sym.Type).(*analysis.FuncType)
		if !ok || !r.typeFits(ft.Return, want) || takesOwner(ft, name) {
			continue
		}
		c := candidate{
			label:      name + "." + fn,
			kind:       protocol.CompletionItemKindFunction,
			sym:        sym,
			filter:     fn,
			locality:   2,
			importFrom: imp,
		}
		if imp != nil {
			c.locality = 3
		}
		out = append(out, c)
	}
	return out
}

// takesOwner reports whether a function has a parameter of the owner's type.
func takesOwner(ft *analysis.FuncType, owner string) bool {
	for _, p := range ft.Params {
		if analysis.TypeOwnerName(p) == owner {
			return true
		}
	}
	return false
}

// constructorReach is ownerReach for the type t named name: a visible name
// must mean t itself, not another type of the same name.
func (r *completionRequest) constructorReach(t analysis.Type, name string) (*analysis.MissingImport, bool) {
	if sym := realSymbol(r.scope.Lookup(name)); sym != nil && !sameNominal(sym.Type, t) {
		return nil, false
	}
	return r.ownerReach(name)
}

// sameNominal reports whether a and b are the same declared type, whatever
// their type arguments: the same owner name from the same file.
func sameNominal(a, b analysis.Type) bool {
	a, b = analysis.ResolveTypeVar(a), analysis.ResolveTypeVar(b)
	name := analysis.TypeOwnerName(a)
	if name == "" || name != analysis.TypeOwnerName(b) {
		return false
	}
	oa, okA := nominalOrigin(a)
	ob, okB := nominalOrigin(b)
	return !okA || !okB || oa == ob
}

// nominalOrigin is the declaring file of a declared type, when the type
// records one. A List or Map spelled with arguments records none.
func nominalOrigin(t analysis.Type) (string, bool) {
	switch v := t.(type) {
	case *analysis.StructType:
		return v.Origin, true
	case *analysis.EnumType:
		return v.Origin, true
	case *analysis.DistinctType:
		return v.Origin, true
	case *analysis.PrimitiveType:
		return v.Origin, v.Origin != ""
	}
	return "", false
}

// literalConstructor is the typed literal of t's first documented example,
// when t implements `Literal` and its docs write one.
func (r *completionRequest) literalConstructor(t analysis.Type) (candidate, bool) {
	name := analysis.TypeOwnerName(t)
	if name == "" {
		return candidate{}, false
	}
	imp, ok := r.constructorReach(t, name)
	if !ok {
		return candidate{}, false
	}
	examples := r.s.literalExamplesOf(r.doc, name)
	if len(examples) == 0 {
		return candidate{}, false
	}
	body := examples[0]
	open, close, ok := literalDelims(body)
	if !ok {
		return candidate{}, false
	}
	text := name + open + body + close
	c := candidate{
		label:      text,
		kind:       protocol.CompletionItemKindValue,
		detail:     "example from " + name + " docs",
		typ:        t,
		insert:     text,
		filter:     name,
		snippet:    snippetEscape(name+open) + exampleSnippet(body) + snippetEscape(close),
		locality:   2,
		importFrom: imp,
	}
	if imp != nil {
		c.locality = 3
	}
	return c, true
}

// literalDelims picks the quotes a one-line literal body can be written
// between: double quotes, unless the body would need an escape or would
// read as an interpolation there, then backticks.
func literalDelims(body string) (string, string, bool) {
	if strings.ContainsAny(body, "\n\r") {
		return "", "", false
	}
	if !strings.ContainsAny(body, `"\`) && !strings.Contains(body, "${") {
		return `"`, `"`, true
	}
	if !strings.Contains(body, "`") {
		return "`", "`", true
	}
	return "", "", false
}

// instantiatedGeneric is a written generic type with its arguments, resolved
// through the names visible at the cursor: `Map<String, Int>`,
// `Result<Date, Error>`. decl is the generic declaration's type. When an
// argument does not resolve, decl itself is the answer.
func (r *completionRequest) instantiatedGeneric(g *ast.GenericType, decl analysis.Type) analysis.Type {
	if decl == nil {
		return nil
	}
	args := make([]analysis.Type, len(g.Params))
	for i, p := range g.Params {
		if args[i] = r.typeOfTypeExpr(p); args[i] == nil {
			return decl
		}
	}
	switch d := decl.(type) {
	case *analysis.EnumType:
		if len(d.TypeParams) != len(args) {
			return decl
		}
		inst := *d
		inst.TypeArgs = args
		return &inst
	case *analysis.StructType:
		if len(d.TypeParams) != len(args) {
			return decl
		}
		inst := *d
		inst.TypeArgs = args
		return &inst
	case *analysis.DistinctType:
		if d.Name == "Map" && len(args) == 2 {
			return &analysis.MapType{Key: args[0], Val: args[1]}
		}
		if d.Name == "List" && len(args) == 1 {
			return &analysis.ListType{Elem: args[0]}
		}
		if len(d.TypeParams) != len(args) {
			return decl
		}
		inst := *d
		inst.TypeArgs = args
		return &inst
	}
	return decl
}
