package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// memberCandidates offers what can follow `X.`. X names a file object, an
// owner type, an interface, a bounded type parameter, or a value; Nomi has no
// method syntax, so a value offers its fields and nothing else.
func (r *completionRequest) memberCandidates() []candidate {
	obj := r.ctx.object
	if sym := r.symbolOf(obj); sym != nil {
		switch {
		case sym.Kind == analysis.SymbolModule:
			return scopeMemberCandidates(sym.ModuleScope, valueSymbol)
		case sym.Kind == analysis.SymbolInterface:
			// An interface's own functions, and the owner functions an
			// `impl Iter<T> { ... }` block declares on it (`Iter.map`).
			out := interfaceFunctionCandidates(sym)
			for name, fn := range r.ownerFunctions(sym.Name) {
				out = append(out, candidate{label: name, kind: protocol.CompletionItemKindMethod, sym: fn, locality: 2})
			}
			return out
		case isTypeParam(sym):
			return r.boundFunctionCandidates(sym)
		case typeSymbol(sym):
			return r.ownerCandidates(sym)
		}
	} else if id, ok := obj.(*ast.Ident); ok && r.s.std != nil && r.scope.Lookup(id.Name) == nil {
		// A stdlib file the document has not imported yet (`io.` with no
		// `import std/io`): its members, so the name being typed can be
		// found before the import is added.
		if scope := r.s.std.Modules[id.Name]; scope != nil {
			return scopeMemberCandidates(scope, valueSymbol)
		}
	}
	t := r.exprType(obj)
	return fieldCandidates(fieldsOf(t), r.declOf(t))
}

// declOf returns the symbol declaring a struct type when it is visible at
// the cursor under its own name.
func (r *completionRequest) declOf(t analysis.Type) *analysis.Symbol {
	st, ok := analysis.ResolveTypeVar(t).(*analysis.StructType)
	if !ok {
		return nil
	}
	sym := realSymbol(r.scope.Lookup(st.Name))
	if sym == nil {
		return nil
	}
	if decl, ok := sym.Type.(*analysis.StructType); ok && decl.Name == st.Name && decl.Origin == st.Origin {
		return sym
	}
	return nil
}

// scopeMemberCandidates offers the public members of a file object's scope
// that keep accepts. A function declared in one of the file's impl blocks is
// not a member: it is reached through its owner, and `duration.seconds` is
// an error.
func scopeMemberCandidates(scope *analysis.Scope, keep func(*analysis.Symbol) bool) []candidate {
	if scope == nil {
		return nil
	}
	var out []candidate
	for name, sym := range scope.Symbols {
		if !sym.Public || !keep(sym) {
			continue
		}
		out = append(out, candidate{
			label:    name,
			kind:     symbolKindToCompletionKind(realSymbol(sym).Kind),
			sym:      sym,
			locality: 2,
		})
	}
	return out
}

func isTypeParam(sym *analysis.Symbol) bool {
	_, ok := sym.Type.(*analysis.TypeParam_)
	return ok && sym.Kind == analysis.SymbolType
}

// interfaceFunctionCandidates offers an interface's functions, called
// interface-qualified (`Display.to_string(x)`).
func interfaceFunctionCandidates(iface *analysis.Symbol) []candidate {
	var out []candidate
	for name, m := range iface.Members {
		if m == nil || m.Kind != analysis.SymbolInterfaceMethod {
			continue
		}
		out = append(out, candidate{label: name, kind: protocol.CompletionItemKindMethod, sym: m, locality: 2})
	}
	return out
}

// boundFunctionCandidates offers the functions of a type parameter's bounds:
// `T.to_string(x)` inside `fn show<T>(x: T) where T: Display`.
func (r *completionRequest) boundFunctionCandidates(tp *analysis.Symbol) []candidate {
	var out []candidate
	for _, bound := range tp.TypeParamBounds {
		if iface := realSymbol(r.scope.Lookup(bound)); iface != nil && iface.Kind == analysis.SymbolInterface {
			out = append(out, interfaceFunctionCandidates(iface)...)
		}
	}
	return out
}

// ownerNameOf is the name owner-qualified calls on a type are keyed by:
// `String`, `List`, `Point`, a type alias's target.
func (r *completionRequest) ownerNameOf(sym *analysis.Symbol) string {
	if name := analysis.TypeOwnerName(sym.Type); name != "" {
		return name
	}
	return sym.Name
}

// ownerFunctions returns the functions callable as `Owner.name`: the type's
// inherent functions and the functions of the interfaces it implements, from
// this file's impl blocks and the project's (the standard library's
// included). Another file's inherent function must be public; an interface
// implementation is reachable wherever the interface is.
func (r *completionRequest) ownerFunctions(owner string) map[string]*analysis.Symbol {
	out := map[string]*analysis.Symbol{}
	if r.fa.ProjectImpls != nil {
		for name, sym := range r.fa.ProjectImpls.TypeMethods[owner] {
			if sym != nil && (sym.Public || sym.ImplInterface != "") {
				out[name] = sym
			}
		}
	}
	for name, sym := range r.fa.TypeMethods[owner] {
		if sym != nil {
			out[name] = sym
		}
	}
	return out
}

func (r *completionRequest) ownerFunction(owner, name string) *analysis.Symbol {
	return r.ownerFunctions(owner)[name]
}

// ownerCandidates offers what follows a type's name: its owner functions, an
// enum's variants, its public owner-level `once` bindings, and, when the type
// is an application type, its application fields. Inside `with App.` only
// the fields fit.
func (r *completionRequest) ownerCandidates(owner *analysis.Symbol) []candidate {
	var out []candidate
	if app := r.appStruct(owner); app != nil {
		out = append(out, fieldCandidates(app.Fields, owner)...)
	}
	if r.ctx.withTarget {
		return out
	}
	for name, fn := range r.ownerFunctions(r.ownerNameOf(owner)) {
		out = append(out, candidate{label: name, kind: protocol.CompletionItemKindMethod, sym: fn, locality: 2})
	}
	for name, m := range owner.Members {
		m = realSymbol(m)
		if m == nil {
			continue
		}
		switch m.Kind {
		case analysis.SymbolEnumVariant:
			out = append(out, candidate{label: name, kind: protocol.CompletionItemKindEnumMember, sym: m, locality: 2})
		case analysis.SymbolOnce:
			out = append(out, candidate{label: name, kind: protocol.CompletionItemKindConstant, sym: m, locality: 2})
		}
	}
	return out
}

// appStruct returns the application struct owner names, when owner is a
// type an entry boot returns.
func (r *completionRequest) appStruct(owner *analysis.Symbol) *analysis.StructType {
	st, ok := analysis.ResolveTypeVar(owner.Type).(*analysis.StructType)
	if !ok {
		return nil
	}
	for _, app := range r.fa.AppTypes {
		if app == st || (app.Name == st.Name && app.Origin == st.Origin) {
			return app
		}
	}
	return nil
}

// fieldCandidates offers fields, with their types as detail. decl, when set,
// is the struct's symbol, whose declaration carries the fields' docs.
func fieldCandidates(fields []analysis.FieldDef, decl *analysis.Symbol) []candidate {
	docs := map[string]string{}
	if decl != nil {
		if sd, ok := decl.Node.(*ast.StructDef); ok {
			for _, f := range sd.Fields {
				docs[f.Name] = f.Doc
			}
		}
	}
	out := make([]candidate, 0, len(fields))
	for _, f := range fields {
		detail := f.Name
		if f.Type != nil {
			detail += ": " + typeDisplay(f.Type)
		}
		out = append(out, candidate{
			label:    f.Name,
			kind:     protocol.CompletionItemKindField,
			detail:   detail,
			doc:      docs[f.Name],
			locality: 0,
			typ:      f.Type,
		})
	}
	return out
}

// typeDisplay renders a type for an item's detail.
func typeDisplay(t analysis.Type) string {
	if t == nil {
		return "_"
	}
	return analysis.ResolveTypeVar(t).String()
}
