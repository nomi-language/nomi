package lsp

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/token"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The interface of a `derive` declaration (`derive ‸`, `derive ‸ for T`) and
// its type (`derive Equatable for ‸`). Their values sit apart from
// completion_syntax.go's list so that list need not change for them.
const (
	ctxDeriveIface ctxKind = 110
	ctxDeriveType  ctxKind = 111
)

// classifyDerive recognizes the interface or the type of a `derive`
// declaration the tree holds.
func classifyDerive(ctx *completionContext) bool {
	h := ctx.hit
	if h.field != "Name" {
		return false
	}
	if _, ok := firstOf(h.at(0)).(*ast.SimpleType); !ok {
		return false
	}
	conf, ok := firstOf(h.at(1)).(*ast.ImplConformance)
	if !ok || !conf.Derive {
		return false
	}
	switch _, field := h.at(0); field {
	case "Interface":
		ctx.kind = ctxDeriveIface
	case "Receiver":
		ctx.kind = ctxDeriveType
	default:
		return false
	}
	return true
}

// classifyDeriveTokens recognizes `derive ‸` and `derive Eq‸`, which do not
// parse until the `for` is written.
func classifyDeriveTokens(ctx *completionContext, tokens []token.Token, idx int) bool {
	prev := prevToken(tokens, idx)
	if prev < 0 || tokens[prev].Type != token.IDENT || tokens[prev].Lexeme != "derive" || tokens[prev].Col != 1 {
		return false
	}
	ctx.kind = ctxDeriveIface
	return true
}

// deriveConformance is the derive declaration the cursor sits in, nil when
// the tree does not hold it yet.
func (r *completionRequest) deriveConformance() *ast.ImplConformance {
	if r.ctx.hit == nil {
		return nil
	}
	conf, _ := firstOf(r.ctx.hit.at(1)).(*ast.ImplConformance)
	return conf
}

// deriveIfaceCandidates offers the interfaces `derive` can synthesize. With
// the type already written (`derive ‸ for Point`) it leaves out the ones
// the checker would reject there: one the type already derives or
// implements by hand, and `FromJson` for an enum. `Debug` ranks last: every
// type has the structural Debug a derive would write. An interface the file
// does not have in scope (`ToJson`) adds its import.
func (r *completionRequest) deriveIfaceCandidates() []candidate {
	var recv string
	if conf := r.deriveConformance(); conf != nil && conf.Receiver != nil {
		recv = analysis.TypeExprBaseName(conf.Receiver)
	}
	decls, has := deriveFacts(r.deriveNodes(), r.pos.Line)
	var out []candidate
	for i, iface := range analysis.DerivableInterfaces() {
		if recv != "" {
			if has[recv][iface] {
				continue
			}
			if decl := decls[recv]; decl != nil && !analysis.DeriveTargetAllowed(iface, decl) {
				continue
			}
		}
		c := candidate{label: iface, kind: protocol.CompletionItemKindInterface, order: i, locality: 2}
		if sym := r.scope.Lookup(iface); sym != nil {
			c.sym = sym
		} else if home := analysis.DeriveHomeModule(iface); home != "" {
			c.locality = 3
			c.importFrom = &analysis.MissingImport{ModulePath: home, Member: iface}
		}
		if iface == "Debug" {
			c.locality = 4
			c.sym = nil
			c.detail = "redundant: every type has a structural Debug"
		}
		out = append(out, c)
	}
	return out
}

// deriveTypeCandidates offers the types `derive Iface for ‸` can name: only
// the file's own top-level types, since a derive in another file is
// rejected, in declaration order, without the ones that already derive or
// implement the interface or that it cannot take.
func (r *completionRequest) deriveTypeCandidates() []candidate {
	var iface string
	if conf := r.deriveConformance(); conf != nil && conf.Interface != nil {
		iface = analysis.TypeExprBaseName(conf.Interface)
	}
	nodes := r.deriveNodes()
	decls, has := deriveFacts(nodes, r.pos.Line)
	var out []candidate
	for i, n := range nodes {
		name := typeDeclNameOf(n)
		if name == "" || decls[name] != n || strings.Contains(name, completionSentinel) {
			continue
		}
		if iface != "" && (has[name][iface] || !analysis.DeriveTargetAllowed(iface, n)) {
			continue
		}
		c := candidate{label: name, kind: protocol.CompletionItemKindStruct, order: i, locality: 1}
		if sym := r.fa.ModuleScope.LookupLocal(name); sym != nil {
			c.sym = sym
			c.kind = symbolKindToCompletionKind(realSymbol(sym).Kind)
		} else if _, ok := n.(*ast.EnumDef); ok {
			c.kind = protocol.CompletionItemKindEnum
		}
		out = append(out, c)
	}
	return out
}

// deriveNodes is the latest text's top-level tree.
func (r *completionRequest) deriveNodes() []ast.Node {
	nodes, _, _ := parser.ParseResilient(lexer.Lex(r.doc.Content))
	return nodes
}

// deriveFacts reads top-level declarations: each type's first declaration
// by name (a later one is a redeclaration the derive does not reach), and
// the interfaces each type derives or implements by hand, leaving out the
// derive on the cursor's line, which is being written.
func deriveFacts(nodes []ast.Node, cursorLine int) (map[string]ast.Node, map[string]map[string]bool) {
	decls := map[string]ast.Node{}
	has := map[string]map[string]bool{}
	add := func(recv, iface ast.TypeExpr) {
		t, i := analysis.TypeExprBaseName(recv), analysis.TypeExprBaseName(iface)
		if t == "" || i == "" {
			return
		}
		if has[t] == nil {
			has[t] = map[string]bool{}
		}
		has[t][i] = true
	}
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ImplConformance:
			if v.Derive && v.Line != cursorLine && v.Receiver != nil && v.Interface != nil {
				add(v.Receiver, v.Interface)
			}
		case *ast.ImplBlock:
			if v.Interface != nil && v.Receiver != nil {
				add(v.Receiver, v.Interface)
			}
		default:
			if name := typeDeclNameOf(n); name != "" && decls[name] == nil {
				decls[name] = n
			}
		}
	}
	return decls, has
}

// typeDeclNameOf is the name a top-level type declaration declares, or "".
func typeDeclNameOf(n ast.Node) string {
	switch v := n.(type) {
	case *ast.StructDef:
		return v.Name
	case *ast.EnumDef:
		return v.Name
	case *ast.TypeDef:
		return v.Name
	case *ast.ExternType:
		return v.Name
	}
	return ""
}
