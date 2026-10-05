package lsp

import (
	"slices"
	"sort"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// pipeKeywords are the stages a pipe takes besides a call: the unary `dbg`
// and `try`, `if` and `case` over the piped value, and `then`, which applies
// a lambda to it. `assert` and `refute` wrap a pipeline from its head; as a
// stage they are rejected.
var pipeKeywords = []string{"dbg", "try", "if", "case", "then"}

// pipeKeywordCandidates offers the keyword stages after every function
// whose name matches as well: a function that takes the value is the
// likelier stage, and `dbg` is a temporary one. `try` is offered only when
// it can apply to the piped value of type lhs: a Result or a Maybe, or a
// type not known here (nil, Any, or a type variable nothing has solved).
func pipeKeywordCandidates(lhs analysis.Type) []candidate {
	keywords := pipeKeywords
	if !tryApplies(lhs) {
		keywords = make([]string, 0, len(pipeKeywords))
		for _, kw := range pipeKeywords {
			if kw != "try" {
				keywords = append(keywords, kw)
			}
		}
	}
	out := keywordCandidates(keywords)
	for i := range out {
		out[i].locality = 3
	}
	return out
}

// tryApplies reports whether `try` may take a value of type t: t is a
// Result or a Maybe, or t is unknown.
func tryApplies(t analysis.Type) bool {
	t = analysis.ResolveTypeVar(t)
	if t == analysis.TypeAny {
		return true
	}
	switch t := t.(type) {
	case nil, *analysis.TypeVar:
		return true
	case *analysis.EnumType:
		return slices.Contains(analysis.TryOperandTypeNames, t.Name)
	default:
		return false
	}
}

// pipeCandidates offers the stages `value |> ` can continue with: functions
// whose first parameter accepts the piped value's type, inserted as a call
// whose first argument the pipe supplies (`Iter.map(${1:f})`). They are the
// owner functions of the value's type (`String.trim`), the functions of every
// interface the type implements, owner functions on the interface included
// (`Iter.map` for a List, since a List is an Iter), the file's own
// functions, and the public functions of the files it imports (`text.shout`)
// whose first parameter names the type. An owner the document cannot name
// yet comes with its import. When the piped value's type is unknown, every
// function in scope is offered.
func (r *completionRequest) pipeCandidates() []candidate {
	lhs := r.exprType(r.ctx.pipeLHS)
	out := pipeKeywordCandidates(lhs)
	if lhs == nil {
		return append(out, r.scopeCandidates(func(sym *analysis.Symbol) bool {
			return valueSymbol(sym) && realSymbol(sym).Kind == analysis.SymbolFunction
		})...)
	}
	owner := analysis.TypeOwnerName(lhs)
	seen := map[string]bool{}
	add := func(qualifier string, fn *analysis.Symbol, locality int, imp *analysis.MissingImport) {
		label := fn.Name
		if qualifier != "" {
			label = qualifier + "." + fn.Name
		}
		if seen[label] {
			return
		}
		seen[label] = true
		out = append(out, candidate{
			label:      label,
			kind:       protocol.CompletionItemKindFunction,
			sym:        fn,
			filter:     fn.Name,
			locality:   locality,
			importFrom: imp,
		})
	}
	if owner != "" {
		if imp, ok := r.ownerReach(owner); ok {
			for _, fn := range sortedSymbols(r.ownerFunctions(owner)) {
				if r.firstParamAccepts(fn, lhs) {
					add(owner, fn, 1, imp)
				}
			}
		}
		for _, iface := range r.implementedInterfaces(owner) {
			imp, ok := r.ownerReach(iface)
			if !ok {
				continue
			}
			for _, fn := range sortedSymbols(r.ownerFunctions(iface)) {
				if r.firstParamAccepts(fn, lhs) {
					add(iface, fn, 2, imp)
				}
			}
			if sym := r.interfaceSymbol(iface); sym != nil {
				for _, fn := range sortedSymbols(sym.Members) {
					if fn.Kind == analysis.SymbolInterfaceMethod && r.firstParamAccepts(fn, lhs) {
						add(iface, fn, 2, imp)
					}
				}
			}
		}
	}
	for _, sym := range r.fa.ModuleScope.Symbols {
		real := realSymbol(sym)
		switch {
		case sameFileSymbol(sym) && real.Kind == analysis.SymbolFunction && valueSymbol(sym):
			if r.firstParamAccepts(real, lhs) {
				add("", real, 1, nil)
			}
		case real.Kind == analysis.SymbolModule && real.ModuleScope != nil:
			for _, fn := range sortedSymbols(real.ModuleScope.Symbols) {
				if fn.Public && fn.Kind == analysis.SymbolFunction && valueSymbol(fn) && r.firstParamAccepts(fn, lhs) {
					add(sym.Name, fn, 2, nil)
				}
			}
		}
	}
	return out
}

// firstParamAccepts reports whether fn's first parameter takes a value of
// type t. A bare type-parameter first parameter accepts it only in an
// interface's own function, where it is the `self` receiver
// (`Display.to_string(x: self)`); anywhere else it would match every value
// (`Iter.repeat(value: T)` builds an Iter, it does not consume one).
func (r *completionRequest) firstParamAccepts(fn *analysis.Symbol, t analysis.Type) bool {
	real := realSymbol(fn)
	if m, ok := real.Node.(*ast.InterfaceMethod); ok && real.Kind == analysis.SymbolInterfaceMethod {
		// An interface's function takes the implementing value where its
		// declaration writes `self`; the caller already knows t implements
		// the interface.
		if len(m.Params) == 0 {
			return false
		}
		_, self := m.Params[0].TypeAnnotation.(*ast.SelfType)
		return self
	}
	ft, ok := analysis.ResolveTypeVar(real.Type).(*analysis.FuncType)
	if !ok || len(ft.Params) == 0 {
		return false
	}
	p := analysis.ResolveTypeVar(ft.Params[0])
	if _, generic := p.(*analysis.TypeParam_); generic {
		return real.Kind == analysis.SymbolInterfaceMethod
	}
	return r.typeFits(t, p)
}

// implementedInterfaces lists the interfaces the type named owner
// implements, from the file's impl table and the project's, in name order.
func (r *completionRequest) implementedInterfaces(owner string) []string {
	set := map[string]bool{}
	for iface := range r.fa.Impls[owner] {
		set[iface] = true
	}
	if r.fa.ProjectImpls != nil {
		for iface := range r.fa.ProjectImpls.Impls[owner] {
			set[iface] = true
		}
	}
	out := make([]string, 0, len(set))
	for iface := range set {
		out = append(out, iface)
	}
	sort.Strings(out)
	return out
}

// interfaceSymbol finds the interface declaration named name: visible at
// the cursor, or public in a standard-library file.
func (r *completionRequest) interfaceSymbol(name string) *analysis.Symbol {
	if sym := realSymbol(r.scope.Lookup(name)); sym != nil && sym.Kind == analysis.SymbolInterface {
		return sym
	}
	for _, src := range r.importSources() {
		if src.scope == nil {
			continue // a closed file's index entry holds no symbols
		}
		if sym := src.scope.LookupLocal(name); sym != nil && sym.Public && sym.Kind == analysis.SymbolInterface {
			return sym
		}
	}
	return nil
}

// ownerReach reports whether the document can spell an owner-qualified call
// on the type or interface named owner, and the import that makes it
// possible when the name is not visible yet. Only a name that resolves to a
// type, or to a public type of exactly one importable file, is reachable.
func (r *completionRequest) ownerReach(owner string) (*analysis.MissingImport, bool) {
	if sym := r.scope.Lookup(owner); sym != nil {
		return nil, typeSymbol(sym)
	}
	var found *analysis.MissingImport
	for _, src := range r.importSources() {
		if !src.exportsType(owner) {
			continue
		}
		if found != nil {
			return nil, false // ambiguous: two files export the name
		}
		found = &analysis.MissingImport{ModulePath: src.path, Member: owner}
	}
	return found, found != nil
}

// exportsType reports whether the file declares a public type named name:
// in its analyzed scope, or in its index entry when it is closed.
func (src importSource) exportsType(name string) bool {
	if src.scope != nil {
		sym := src.scope.LookupLocal(name)
		return sym != nil && sym.Public && sym.Resolved == nil && typeSymbol(sym)
	}
	for _, d := range src.decls {
		if d.Name == name && d.Public && declIsType(d.Kind) {
			return true
		}
	}
	return false
}

// sortedSymbols returns a symbol table's symbols in name order.
func sortedSymbols(m map[string]*analysis.Symbol) []*analysis.Symbol {
	names := make([]string, 0, len(m))
	for name, sym := range m {
		if sym != nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	out := make([]*analysis.Symbol, len(names))
	for i, n := range names {
		out[i] = m[n]
	}
	return out
}
