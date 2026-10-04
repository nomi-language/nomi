// Package highlight provides the analyzer-derived semantic tokens used to
// highlight a Nomi snippet (via internal/semtokens, the same classifier the LSP
// uses). It is exposed to the browser as nomiSemTokens (cmd/nomi-wasm); the
// shared JS highlighter (tour/src/lib/highlight.mjs) overlays these
// tokens onto the tree-sitter grammar's syntactic captures to render HTML, the
// same two-layer model Zed uses. Both the static tour and the live playground
// go through that one renderer, so there is no separate highlight definition.
package highlight

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/semtokens"
	"github.com/nomi-language/nomi/std"
)

// Analyze runs the PROJECT analysis pipeline (parse + derive/Debug synthesis +
// BuildProjectWithCache + CheckTypes, with stdlib) on a snippet and returns the
// entry FileAnalysis — the shared input for semantic tokens AND hover
// (internal/hoverdoc). Returns nil if lib is nil. May panic on malformed input;
// callers recover (see SemanticTokens and cmd/nomi-wasm).
//
// Project analysis establishes the boot contract for scoped field checking.
// The resulting references feed both semantic tokens and hover rendering.
//
// BuildProjectWithCache rather than BuildProjectFromEntry because a tour
// snippet HAS NO PATH. The entry-path variant exists to populate
// FileAnalysis.FilePath for the internal/-access and entry-placement checks,
// which derive a module-relative path from it; inventing a path to satisfy
// them would make those checks judge a file that does not exist. This is the
// same reason document.go's header gives for the LSP per-document path.
//
// projectRoot "" and a nil loader because there is no filesystem: the tour runs
// in wasm with one buffer and no siblings. DiscoverProject returns an empty
// project for that pair (analysis/discovery.go:118), so the build walks no
// imports and touches no disk, and the entry is the whole project.
//
// No AttachStdlibProjectImpls: it is the single-file path's stdlib-only
// substitute for ProjectImpls, and its own doc says callers that went through
// BuildProject have the richer index already. No explicit BuildTypes either —
// Sweep C runs inside the project build.
func Analyze(code string, lib *std.StdLib) *analysis.FileAnalysis {
	if lib == nil {
		return nil
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(code))
	nodes, _ = analysis.LowerDerives(nodes)
	nodes, _ = analysis.SynthesizeDerives(nodes)
	nodes = analysis.SynthesizeUniversalDebug(nodes)
	fa, _, _ := analysis.BuildProjectWithCache(nodes, lib.Primitives, lib.Modules, lib.Files, "", nil)
	if fa == nil {
		return nil
	}
	_ = analysis.CheckTypes(fa, nodes)
	return fa
}

// SemanticTokens analyzes a snippet and returns its analyzer-derived semantic
// tokens (the overlay layer). Best-effort: a snippet that fails to
// parse/analyze (e.g. a fragment, or lib == nil) yields none.
func SemanticTokens(code string, lib *std.StdLib) (toks []semtokens.Token) {
	defer func() {
		if recover() != nil {
			toks = nil
		}
	}()
	fa := Analyze(code, lib)
	if fa == nil {
		return nil
	}
	return semtokens.Collect(fa)
}
