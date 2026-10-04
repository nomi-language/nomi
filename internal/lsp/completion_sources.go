package lsp

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Keywords offered by position. A statement position offers everything an
// operand position does plus the statement-only forms.
var (
	topLevelKeywords = []string{
		"fn", "pub", "import", "struct", "enum", "impl", "interface", "type",
		"typealias", "tests", "test", "once", "derive", "opaque", "host",
	}
	expressionKeywords = []string{"if", "case", "try", "dbg", "todo", "concurrent"}
	statementKeywords  = append([]string{
		"return", "defer", "with", "assert", "refute", "import", "fn", "once",
		"break", "continue",
	}, expressionKeywords...)
)

func keywordCandidates(words []string) []candidate {
	out := make([]candidate, 0, len(words))
	for _, w := range words {
		out = append(out, candidate{label: w, kind: protocol.CompletionItemKindKeyword, locality: 2})
	}
	return out
}

// realSymbol follows a selective import's binding to the declaration.
func realSymbol(sym *analysis.Symbol) *analysis.Symbol {
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	return sym
}

// valueSymbol reports whether a bare name for sym can stand in an expression.
// Synthetic hover markers, fields and interface methods cannot, and neither
// can a function declared in an impl block: it is called through its owner
// (`Int.to_string(n)`), never bare.
func valueSymbol(sym *analysis.Symbol) bool {
	if sym == nil || sym.Name == "" || sym.Name == "self" || strings.HasPrefix(sym.Name, "__") {
		return false
	}
	switch sym.Kind {
	case analysis.SymbolField, analysis.SymbolInterfaceMethod, analysis.SymbolArgHint,
		analysis.SymbolAssertion, analysis.SymbolTestSetup, analysis.SymbolTestDecl,
		analysis.SymbolTryOp, analysis.SymbolControlFlow, analysis.SymbolImplKeyword,
		analysis.SymbolLiteral:
		return false
	}
	real := realSymbol(sym)
	return real.OwningType == "" && !real.IsImplMethod
}

// typeSymbol reports whether sym names a type.
func typeSymbol(sym *analysis.Symbol) bool {
	switch realSymbol(sym).Kind {
	case analysis.SymbolStruct, analysis.SymbolEnum, analysis.SymbolType,
		analysis.SymbolTypeAlias, analysis.SymbolInterface:
		return true
	}
	return false
}

// typeQualifierModule reports whether sym is a module that can qualify a
// type, `calendar` in `calendar.Date`: one the file imports. The file's own
// module name and the prelude's are module symbols too, with no import
// behind them, and `main.Place` or `prelude.Int` names no type.
func typeQualifierModule(sym *analysis.Symbol) bool {
	return sym.Kind == analysis.SymbolModule && sym.Node != nil
}

// scopeCandidates offers every name visible at the cursor that keep accepts,
// nearest scope first so a shadowing local wins.
func (r *completionRequest) scopeCandidates(keep func(*analysis.Symbol) bool) []candidate {
	var out []candidate
	seen := map[string]bool{}
	// Scopes inside the module scope hold locals; the module scope holds
	// the file's own names and its imports; the scopes above it hold the
	// prelude.
	phase := 0
	for scope := r.scope; scope != nil; scope = scope.Parent {
		if scope == r.fa.ModuleScope {
			phase = 1
		} else if phase == 1 {
			phase = 2
		}
		for name, sym := range scope.Symbols {
			if seen[name] {
				continue
			}
			seen[name] = true
			if !keep(sym) {
				continue
			}
			l := phase
			if phase == 1 && !sameFileSymbol(sym) {
				l = 2
			}
			out = append(out, candidate{
				label:    name,
				kind:     symbolKindToCompletionKind(realSymbol(sym).Kind),
				sym:      sym,
				locality: l,
			})
		}
	}
	return out
}

// sameFileSymbol reports whether a module-scope symbol is declared in the
// file itself rather than imported or copied from the prelude.
func sameFileSymbol(sym *analysis.Symbol) bool {
	return sym.Resolved == nil && sym.SourceFile == "" && sym.Node != nil &&
		sym.Pos.Line > 0 && !analysis.IsSynthesizedLine(sym.Pos.Line) && sym.Kind != analysis.SymbolModule
}

// variantCandidates offers enum variants after a leading dot. The label is
// the bare variant name: the dot is already in the source. When the position
// expects an enum, only its variants resolve there, so only they are
// offered; otherwise every variant of every enum in scope is. Where the
// position expects a function over a struct, the dot starts a field
// accessor, and the struct's fields are offered instead.
func (r *completionRequest) variantCandidates() []candidate {
	if fields, ok := r.accessorCandidates(); ok {
		return fields
	}
	if et, ok := analysis.ResolveTypeVar(r.expected).(*analysis.EnumType); ok {
		return r.enumVariantCandidates(et)
	}
	var out []candidate
	seen := map[string]bool{}
	for _, sym := range r.scope.AllVisible() {
		enum := realSymbol(sym)
		if enum.Kind != analysis.SymbolEnum {
			continue
		}
		for name, member := range enum.Members {
			member = realSymbol(member)
			if member == nil || member.Kind != analysis.SymbolEnumVariant || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, candidate{label: name, kind: protocol.CompletionItemKindEnumMember, sym: member, locality: 2})
		}
	}
	return out
}

// enumVariantCandidates offers one enum's variants, with their declaration
// symbols when the enum's declaration is visible at the cursor.
func (r *completionRequest) enumVariantCandidates(et *analysis.EnumType) []candidate {
	var members map[string]*analysis.Symbol
	if sym := r.enumSymbol(et); sym != nil {
		members = sym.Members
	}
	out := make([]candidate, 0, len(et.Variants))
	for _, v := range et.Variants {
		c := candidate{label: v.Name, kind: protocol.CompletionItemKindEnumMember, locality: 2, typ: et}
		if m := realSymbol(members[v.Name]); m != nil {
			c.sym = m
		} else {
			c.detail = "variant of " + et.Name
		}
		out = append(out, c)
	}
	return out
}

// enumSymbol finds the declaration of an enum type among the names visible
// at the cursor.
func (r *completionRequest) enumSymbol(et *analysis.EnumType) *analysis.Symbol {
	for _, sym := range r.scope.AllVisible() {
		real := realSymbol(sym)
		if real.Kind != analysis.SymbolEnum {
			continue
		}
		if decl, ok := real.Type.(*analysis.EnumType); ok && decl.Name == et.Name && decl.Origin == et.Origin {
			return real
		}
	}
	return nil
}

// moduleMemberCandidates offers the public members of the file object bound
// to name at the cursor (`io` after `import std/io`), filtered by keep.
func (r *completionRequest) moduleMemberCandidates(name string, keep func(*analysis.Symbol) bool) []candidate {
	return scopeMemberCandidates(r.moduleScopeOf(name), keep)
}

// moduleScopeOf returns the member scope of the file object bound to name at
// the cursor, or of the stdlib module of that name.
func (r *completionRequest) moduleScopeOf(name string) *analysis.Scope {
	if sym := r.scope.Lookup(name); sym != nil {
		if sym.Kind == analysis.SymbolModule && sym.ModuleScope != nil {
			return sym.ModuleScope
		}
		if real := realSymbol(sym); real.Kind == analysis.SymbolModule && real.ModuleScope != nil {
			return real.ModuleScope
		}
		return nil
	}
	if r.s.std != nil {
		return r.s.std.Modules[name]
	}
	return nil
}

// testGroupKeywords are a `tests` group's lines, in the order they run and
// `nomi fmt` places them.
var testGroupKeywords = []string{"clock", "boot", "setup", "test"}

// testGroupCandidates offers the group lines that may still be written: each
// of clock, boot and setup until the group has one, anywhere in the group,
// and test always.
func testGroupCandidates(has map[string]bool, indent string) []candidate {
	var out []candidate
	for i, kw := range testGroupKeywords {
		if kw != "test" && has[kw] {
			continue
		}
		snippet, plain := testGroupLine(kw, indent)
		out = append(out, candidate{
			label:    kw,
			kind:     protocol.CompletionItemKindKeyword,
			locality: 2,
			order:    i,
			snippet:  snippet,
			insert:   plain,
			asIs:     true,
		})
	}
	return out
}
