package lsp

import (
	"github.com/nomi-language/nomi/internal/ast"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Code lens commands. The client defines them; the server only names them.
//
//   - nomi.runTest, arguments [uri, line]: run `nomi test <file> --line
//     <line>`, where line is the 1-based first line of a `test`, a `tests`
//     group or a `//!` attached test.
//   - nomi.runMain, arguments [uri]: run `nomi run <file>`.
//   - nomi.debugTest and nomi.debugMain, the same arguments: start a debug
//     session (`nomi dap`) that runs the same test or program.
//
// Each run lens has a debug lens beside it. The VS Code extension
// (editors/vscode) and the Neovim plugin (editors/nvim) register all four.
const (
	commandRunTest   = "nomi.runTest"
	commandRunMain   = "nomi.runMain"
	commandDebugTest = "nomi.debugTest"
	commandDebugMain = "nomi.debugMain"
)

// textDocumentCodeLens puts a lens above each `test`, `tests` group (and
// each test in it), `//!` attached test and top-level `fn main` of the
// file, from its parse; no analysis is needed.
func (s *Server) textDocumentCodeLens(_ *glsp.Context, params *protocol.CodeLensParams) ([]protocol.CodeLens, error) {
	uri := string(params.TextDocument.URI)
	doc := s.docs.Snapshot(uri)
	if doc == nil {
		return nil, nil
	}
	lenses := codeLenses(uri, doc.Nodes)
	lines := s.lines.get(uri, doc.Content)
	for i := range lenses {
		lenses[i].Range = lines.utf16Range(lenses[i].Range)
	}
	return lenses, nil
}

// codeLenses builds the lenses of a file's top-level nodes, in source
// order, with byte-column ranges.
func codeLenses(uri string, nodes []ast.Node) []protocol.CodeLens {
	var out []protocol.CodeLens
	runTest := func(what string, line, col, width int) {
		r := nameRange(line, col, width)
		out = append(out,
			protocol.CodeLens{Range: r, Command: &protocol.Command{Title: "Run " + what, Command: commandRunTest, Arguments: []any{uri, line}}},
			protocol.CodeLens{Range: r, Command: &protocol.Command{Title: "Debug " + what, Command: commandDebugTest, Arguments: []any{uri, line}}})
	}
	attached := func(n ast.Node) {
		for _, at := range ast.AttachedTestsOf(n) {
			runTest("attached test", at.Line, at.Col, len("//!"))
		}
	}
	var test func(t *ast.TestDecl)
	test = func(t *ast.TestDecl) {
		if !t.Group {
			runTest("test", t.Line, t.Col, len("test"))
			return
		}
		runTest("tests", t.Line, t.Col, len("tests"))
		if t.Body != nil {
			for _, st := range t.Body.Stmts {
				if child, ok := st.(*ast.TestDecl); ok {
					test(child)
				}
			}
		}
	}
	for _, n := range nodes {
		attached(n)
		switch v := n.(type) {
		case *ast.TestDecl:
			test(v)
		case *ast.FuncDef:
			if v.Name == "main" {
				r := nameRange(v.Line, v.Col, len(v.Name))
				out = append(out,
					protocol.CodeLens{Range: r, Command: &protocol.Command{Title: "Run", Command: commandRunMain, Arguments: []any{uri}}},
					protocol.CodeLens{Range: r, Command: &protocol.Command{Title: "Debug", Command: commandDebugMain, Arguments: []any{uri}}})
			}
		case *ast.ImplBlock:
			for _, item := range v.Items {
				attached(item)
			}
		}
	}
	return out
}
