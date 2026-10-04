package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func (s *Server) textDocumentSignatureHelp(ctx *glsp.Context, params *protocol.SignatureHelpParams) (*protocol.SignatureHelp, error) {
	uri := string(params.TextDocument.URI)
	doc := s.docs.Snapshot(uri)
	if doc == nil || doc.Analysis == nil {
		return nil, nil
	}

	line := int(params.Position.Line)
	char := int(params.Position.Character)

	// Find the function name and active parameter index from the latest
	// text; the symbol is then looked up by name, so an analysis of an
	// older text serves (rpc.go's requestFreshness).
	funcName, activeParam := findCallContext(doc.Text, line, char)
	if funcName == "" {
		return nil, nil
	}

	// Look up the function symbol.
	sym := doc.Analysis.ModuleScope.Lookup(funcName)

	// Try stdlib owners for qualified calls (e.g., "io.inspect").
	if sym == nil && s.std != nil && strings.Contains(funcName, ".") {
		parts := strings.SplitN(funcName, ".", 2)
		if modScope, ok := s.std.Modules[parts[0]]; ok {
			sym = modScope.Lookup(parts[1])
		}
	}

	if sym == nil {
		return nil, nil
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}

	sig, paramInfos, doc_ := buildSignatureInfo(sym)
	if sig == "" {
		return nil, nil
	}

	activeSig := protocol.UInteger(0)
	ap := protocol.UInteger(activeParam)

	result := &protocol.SignatureHelp{
		Signatures: []protocol.SignatureInformation{
			{
				Label:      sig,
				Parameters: paramInfos,
			},
		},
		ActiveSignature: &activeSig,
		ActiveParameter: &ap,
	}

	if doc_ != "" {
		result.Signatures[0].Documentation = protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: doc_,
		}
	}

	return result, nil
}

// buildSignatureInfo returns the signature label, parameter infos, and doc for a symbol.
func buildSignatureInfo(sym *analysis.Symbol) (string, []protocol.ParameterInformation, string) {
	var params []ast.Param
	var typeParams []ast.TypeParam
	var ret ast.TypeExpr
	var doc string

	switch n := sym.Node.(type) {
	case *ast.FuncDef:
		params = n.Params
		typeParams = n.TypeParams
		ret = n.ReturnTypeExpr
		doc = n.Doc
	case *ast.ExternFunc:
		params = n.Params
		ret = n.ReturnTypeExpr
		doc = n.Doc
	case *ast.InterfaceMethod:
		sig := hoverdoc.RenderInterfaceMethodSig(n)
		var paramInfos []protocol.ParameterInformation
		for _, p := range n.Params {
			paramLabel := hoverdoc.ParamDisplayName(p)
			if p.TypeAnnotation != nil {
				paramLabel += ": " + p.TypeAnnotation.TypeString()
			}
			paramInfos = append(paramInfos, protocol.ParameterInformation{
				Label: paramLabel,
			})
		}
		// `n.Doc`, not "": the two arms above return theirs, and an
		// interface method's `///` is written the same way.
		return "fn " + sig, paramInfos, n.Doc
	default:
		return "", nil, ""
	}

	sig := hoverdoc.RenderFuncSig(sym.Name, typeParams, params, ret)

	var paramInfos []protocol.ParameterInformation
	for _, p := range params {
		paramLabel := hoverdoc.ParamDisplayName(p)
		if p.TypeAnnotation != nil {
			paramLabel += ": " + p.TypeAnnotation.TypeString()
		}
		paramInfos = append(paramInfos, protocol.ParameterInformation{
			Label: paramLabel,
		})
	}

	return sig, paramInfos, doc
}

// findCallContext scans backwards from the cursor to find the enclosing function call
// and which argument position the cursor is at. Returns ("", 0) if not in a call.
// line and character are 0-based.
func findCallContext(content string, line, character int) (string, int) {
	lines := strings.Split(content, "\n")
	if line >= len(lines) {
		return "", 0
	}

	// Build flat offset.
	offset := 0
	for i := 0; i < line; i++ {
		offset += len(lines[i]) + 1 // +1 for \n
	}
	col := character
	if col > len(lines[line]) {
		col = len(lines[line])
	}
	offset += col

	// Walk backwards from offset to find the opening '(' that we're inside.
	depth := 0
	i := offset - 1
	for i >= 0 {
		ch := content[i]
		switch ch {
		case ')':
			depth++
		case '(':
			if depth == 0 {
				// Found the opening paren. Extract the function name before it.
				funcName := extractFuncName(content, i)
				// Count commas between '(' and cursor to determine active param.
				activeParam := countCommas(content, i+1, offset)
				return funcName, activeParam
			}
			depth--
		}
		i--
	}

	return "", 0
}

// extractFuncName extracts the function name (possibly qualified) before an opening paren.
func extractFuncName(content string, parenPos int) string {
	end := parenPos
	// Skip whitespace before paren.
	i := end - 1
	for i >= 0 && (content[i] == ' ' || content[i] == '\t') {
		i--
	}
	if i < 0 {
		return ""
	}

	// Collect identifier chars (including '.' for qualified names).
	nameEnd := i + 1
	for i >= 0 && (isIdentByte(content[i]) || content[i] == '.') {
		i--
	}
	name := content[i+1 : nameEnd]
	return name
}

func isIdentByte(c byte) bool {
	// `?` is the trailing predicate marker the lexer absorbs into an
	// identifier (e.g. `empty?`, `contains?`). It is trailing-only in valid
	// source, so including it in a backward char-scan cannot over-match
	// across token boundaries.
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '?'
}

// countCommas counts top-level commas between start and end offsets in content.
func countCommas(content string, start, end int) int {
	count := 0
	depth := 0
	for i := start; i < end && i < len(content); i++ {
		switch content[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				count++
			}
		}
	}
	return count
}
