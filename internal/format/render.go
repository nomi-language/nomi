package format

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// RenderImports renders a set of import statements/blocks in canonical layout
// (the same sorting + emission `nomi fmt` applies to a file's import section):
// sorted by origin group then path, selective items alphabetized with `self`
// first, block form preserved. The result has NO trailing newline, so callers
// splicing it into a document control their own line terminators.
//
// Every node must be an *ast.ImportStmt or *ast.ImportBlock; any other node is
// an error. This is the standalone import renderer the auto-import code actions
// use so their inserted/merged imports match the formatter exactly instead of
// hand-built strings.
func RenderImports(nodes []ast.Node) (string, error) {
	if len(nodes) == 0 {
		return "", nil
	}
	// Clone first: sortImports sorts selective lists in place, which would
	// otherwise mutate the caller's (often cached) AST nodes.
	cloned := make([]ast.Node, 0, len(nodes))
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ImportStmt:
			cloned = append(cloned, cloneImportStmt(v))
		case *ast.ImportBlock:
			b := *v
			b.Entries = make([]*ast.ImportStmt, len(v.Entries))
			for j, e := range v.Entries {
				b.Entries[j] = cloneImportStmt(e)
			}
			cloned = append(cloned, &b)
		default:
			return "", fmt.Errorf("RenderImports: non-import node %T", n)
		}
	}
	sorted := collapseSingleEntryBlocks(sortImports(cloned))
	parts := make([]Doc, 0, len(sorted)*2)
	for i, n := range sorted {
		if i > 0 {
			parts = append(parts, HardLine())
		}
		switch v := n.(type) {
		case *ast.ImportStmt:
			parts = append(parts, emitImport(v))
		case *ast.ImportBlock:
			parts = append(parts, emitImportBlock(v))
		default:
			return "", fmt.Errorf("RenderImports: non-import node %T", n)
		}
	}
	return Render(Concat(parts...), defaultWidth), nil
}

// RenderNode renders a single AST node in canonical layout. It is intended for
// diagnostics and tooling that need source-shaped text for a parsed node.
func RenderNode(n ast.Node) string {
	return Render(emit(n), defaultWidth)
}

// RenderPattern renders a pattern as a `case` arm writes it.
func RenderPattern(n ast.Node) string {
	return Render(emitPattern(n), defaultWidth)
}

// RenderAttachedTestBody renders the code a reader edits for an attached test:
// `//! ...` attached tests become the body text without the surrounding prompt
// markers.
func RenderAttachedTestBody(test ast.AttachedTest) string {
	if test.Body == nil {
		return ""
	}
	if test.Kind == "test" || !test.Inline {
		return renderBlockBody(test.Body)
	}
	if len(test.Body.Stmts) == 0 {
		return ""
	}
	return strings.TrimSuffix(Render(emit(test.Body.Stmts[0]), defaultWidth), "\n")
}

// FormatAttachedTestBody formats body, the text a reference editor holds for
// an attached test, as `nomi fmt` formats it in its `//!` prompt: written as
// that prompt above a declaration, formatted, and read back out. The result is
// what RenderAttachedTestBody shows for the formatted prompt, so an editor
// that formats its body with this agrees with the page that showed it. An
// error is the parse error of body, at body's own line numbers.
func FormatAttachedTestBody(body string) (string, error) {
	body = strings.TrimRight(body, "\n")
	if strings.TrimSpace(body) == "" {
		return "", nil
	}
	var src strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			src.WriteString("//!\n")
		} else {
			src.WriteString("//! " + line + "\n")
		}
	}
	src.WriteString("fn reference() {}\n")
	nodes, _, err := parser.ParseFile(lexer.Lex(src.String()))
	if err != nil {
		return body, err
	}
	normalizeBodies(nodes)
	stripSelfTypingAnnotations(nodes)
	gatherLambdaHeaderComments(nodes)
	var parts []string
	for _, n := range nodes {
		for _, test := range ast.AttachedTestsOf(n) {
			if text := renderBlockBody(attachedTestCanonicalBody(test)); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func renderBlockBody(block *ast.Block) string {
	if block == nil || len(block.Stmts) == 0 {
		return ""
	}
	return strings.TrimSuffix(Render(Concat(emitStatementDocs(block.Stmts)...), defaultWidth), "\n")
}

// cloneImportStmt copies an import statement and its parallel slices so the
// in-place selective-list sort can't reach the caller's node.
func cloneImportStmt(s *ast.ImportStmt) *ast.ImportStmt {
	c := *s
	c.ModulePath = append([]ast.Node(nil), s.ModulePath...)
	c.Names = append([]ast.Node(nil), s.Names...)
	c.Aliases = append([]ast.Node(nil), s.Aliases...)
	return &c
}

// RenderImportBody renders a single import statement WITHOUT the leading
// `import ` keyword — the path, drill-through type, and selector list
// (`self` first when present, names alphabetized). This is what a caller splices in when
// replacing the span that begins at an import's module path: a top-level
// statement keeps its existing `import ` prefix, and a block entry (which has
// no per-entry keyword at all) gets just its body. The selective list is sorted
// in place, so pass a copy if the input node is shared.
func RenderImportBody(stmt *ast.ImportStmt) string {
	sortSelectiveList(stmt)
	return Render(emitImportBody(stmt), defaultWidth)
}
