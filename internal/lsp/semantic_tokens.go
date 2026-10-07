package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/semtokens"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// tokenTypes lists the semantic token types in legend order (the index is what
// the protocol encodes, and server.go advertises this legend).
var tokenTypes = []string{
	string(protocol.SemanticTokenTypeFunction),   // 0
	string(protocol.SemanticTokenTypeStruct),     // 1
	string(protocol.SemanticTokenTypeEnum),       // 2
	string(protocol.SemanticTokenTypeEnumMember), // 3
	string(protocol.SemanticTokenTypeType),       // 4
	string(protocol.SemanticTokenTypeInterface),  // 5
	string(protocol.SemanticTokenTypeParameter),  // 6
	string(protocol.SemanticTokenTypeVariable),   // 7
	string(protocol.SemanticTokenTypeProperty),   // 8
	string(protocol.SemanticTokenTypeNamespace),  // 9
	string(protocol.SemanticTokenTypeComment),    // 10
}

// tokenModifiers lists the semantic token modifiers in legend order (bit
// position matters).
var tokenModifiers = []string{
	string(protocol.SemanticTokenModifierReadonly), // bit 0
	"attachedTest", // bit 1
}

const (
	modReadonly = 1 << 0
	// modAttachedTest marks a token on a `//!` attached-test line. Editors
	// map it to draw attached tests dimmed: Zed through the extension's
	// semantic_token_rules.json; Neovim dims the lines itself and leaves the
	// modifier's group (@lsp.mod.attachedTest.nomi) unset.
	modAttachedTest = 1 << 1
)

// encodeSemanticTokens builds the LSP semantic-tokens data array from a
// FileAnalysis and the nodes it was built from. Identifier classification is
// shared with the language-tour highlighter via the semtokens package; this
// function maps the shared token-type names into this legend's indices, sets
// the modifiers, and delta-encodes them.
func encodeSemanticTokens(fa *analysis.FileAnalysis, nodes []ast.Node) []protocol.UInteger {
	toks := semtokens.Collect(fa)
	if len(toks) == 0 {
		// Never nil: it marshals as `"data": null`, which Neovim's
		// semantic-tokens handler crashes on when a file is empty.
		return []protocol.UInteger{}
	}
	attached := semtokens.AttachedTestLines(nodes)

	typeIndex := make(map[string]uint32, len(tokenTypes))
	for i, name := range tokenTypes {
		typeIndex[name] = uint32(i)
	}

	// semtokens.Collect already returns tokens sorted by (line, col), which is
	// what the delta encoding requires.
	data := make([]protocol.UInteger, 0, len(toks)*5)
	prevLine := 1
	prevCol := 1
	for _, tok := range toks {
		tt, ok := typeIndex[tok.Type]
		if !ok {
			continue
		}
		var mod uint32
		if tok.Readonly {
			mod |= modReadonly
		}
		if attached[tok.Line] {
			mod |= modAttachedTest
		}
		deltaLine := tok.Line - prevLine
		deltaStartChar := tok.Col - 1 // 1-based to 0-based on a new line
		if deltaLine == 0 {
			deltaStartChar = tok.Col - prevCol
		}
		data = append(data,
			protocol.UInteger(deltaLine),
			protocol.UInteger(deltaStartChar),
			protocol.UInteger(tok.Length),
			protocol.UInteger(tt),
			protocol.UInteger(mod),
		)
		prevLine = tok.Line
		prevCol = tok.Col
	}
	return data
}

func (s *Server) textDocumentSemanticTokensFull(ctx *glsp.Context, params *protocol.SemanticTokensParams) (*protocol.SemanticTokens, error) {
	uri := string(params.TextDocument.URI)
	doc := s.docs.Snapshot(uri)
	if doc == nil {
		return nil, nil
	}
	data := encodeSemanticTokens(doc.Analysis, doc.Nodes)
	return &protocol.SemanticTokens{Data: data}, nil
}
