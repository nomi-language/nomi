package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// preludeImportsCache memoizes the per-stdlib-path enumeration of
// prelude.nomi's `import` statements so one process (LSP server, test
// binary, runtime invocation) parses prelude.nomi once. Keyed by the
// stdlib root path so a test that points NOMI_STD_PATH at a different
// directory doesn't see a stale list from a previous test.
//
// preludeImportsCacheErr is the sibling sticky-error cache: when
// prelude.nomi exists but fails to parse, the error is recorded here
// (and a nil slice in preludeImportsCache) so subsequent calls
// short-circuit instead of re-stat'ing + re-lexing + re-parsing the
// same broken file on every analyze pass — a long-lived LSP would
// otherwise pay that cost on every keystroke until the file is fixed.
// Process restarts clear both caches; in-process callers that
// patch prelude.nomi in place still need to restart the LSP, which
// is also true of the success-path cache today. No invalidation hook
// is needed: the key is the stdlibPath, so a test that points
// NOMI_STD_PATH elsewhere bypasses the sticky cache naturally.
var (
	preludeImportsCache    = map[string][]*ast.ImportStmt{}
	preludeImportsCacheErr = map[string]error{}
	preludeImportsCacheMu  sync.Mutex
)

// preludeImports returns the chain of `import` statements the auto-
// prepend pass should prepend to every non-stdlib user file. The
// statements are derived from std/prelude.nomi: each top-level
// `import std/X.{...} export` entry there becomes an equivalent
// `import std/X.{...}` (export flag stripped) in the returned slice.
// ImportBlocks are flattened to their constituent ImportStmts.
//
// Why mirror prelude.nomi's structure instead of synthesizing one
// big `import std/prelude.{NAMES}`: the legacy pre-cutover path made
// prelude's ModuleScope the parent scope of every user file, so bare
// lookups for `Some`, `None`, `Equal`, etc. walked through prelude's
// Maybe / Ordering enums and found them as Members. A flat
// `import std/prelude.{None, Some, ...}` would trip the analyzer's
// `cannot import variant 'X' directly` check (variants must be
// drill-imported via `Enum.{Variant}` or fully qualified). Mirroring
// prelude.nomi's actual structure — including its drill-through
// `std/maybe.Maybe.{self, None, Some}` lines — sidesteps the check
// the same way prelude.nomi itself does.
//
// prelude.nomi is read from stdlibPath when the stdlib source tree is on
// disk. A binary built with -trimpath and installed without that tree (a
// release) has no stdlibPath; it reads prelude.nomi
// through loader, which answers `std/...` from the stdlib embedded in the
// binary, as it does for every other stdlib file. Both read the same file,
// so a program resolves the same names whether or not the tree is present.
// Returning nothing in the second case left a user file with the prelude
// only as its parent scope, where any file-scope name shadows it: an enum
// variant named `Debug` hid the `Debug` interface from the file's own
// `Debug.inspect` calls and from the synthesized `impl Debug` headers
// ("impl block: 'Debug' is not an interface").
//
// Returns nil + nil-error when prelude.nomi cannot be found either way.
func preludeImports(stdlibPath string, loader FileLoader) ([]*ast.ImportStmt, error) {
	preludeImportsCacheMu.Lock()
	defer preludeImportsCacheMu.Unlock()
	// Error cache is checked FIRST: the parse-error branch below writes
	// to both maps (nil into the success cache as a sentinel, the wrapped
	// error into the error cache), so a success-cache-first probe would
	// match the nil sentinel and incorrectly return (nil, nil) — the
	// "missing prelude" shape — instead of the sticky parse error. See
	// TestPreludeImports_StickyParseErrorCache.
	if cachedErr, ok := preludeImportsCacheErr[stdlibPath]; ok {
		// Sticky-cached parse error from a prior call: re-return without
		// re-stat'ing or re-parsing. See the doc-block on
		// preludeImportsCacheErr above for why this exists.
		return nil, cachedErr
	}
	if cached, ok := preludeImportsCache[stdlibPath]; ok {
		return cached, nil
	}

	var nodes []ast.Node
	var parseErrs []parser.ParseError
	if stdlibPath != "" {
		data, err := os.ReadFile(filepath.Join(stdlibPath, "prelude.nomi"))
		if err != nil {
			// Missing prelude.nomi is non-fatal: cache nil so repeated
			// lookups don't keep stat-ing the disk.
			preludeImportsCache[stdlibPath] = nil
			return nil, nil
		}
		nodes, parseErrs = parser.ParseWithRecovery(lexer.Lex(string(data)))
	} else {
		if loader == nil {
			return nil, nil
		}
		loaded, err := loader("", []string{"std", "prelude"})
		if err != nil {
			// Not cached: a later call may pass a loader that has it.
			return nil, nil
		}
		nodes = loaded
	}
	if len(parseErrs) > 0 {
		// Surface the first parse error so a malformed prelude.nomi
		// fails with a pointer at the cause rather than producing a
		// silent empty re-export list that would yield mass
		// "undefined name" errors across every user file. Sticky-cache
		// the wrapped error (and nil in the success-side cache) so
		// subsequent calls short-circuit: a long-lived LSP analyzing
		// every keystroke would otherwise re-stat + re-lex + re-parse
		// the same broken file on every pass. The cache is keyed by
		// stdlibPath so a test that points NOMI_STD_PATH elsewhere
		// bypasses the sticky cache naturally.
		first := parseErrs[0]
		wrapped := fmt.Errorf("std/prelude.nomi: parse error at line %d:%d: %s",
			first.Line, first.Col, first.Message)
		preludeImportsCacheErr[stdlibPath] = wrapped
		preludeImportsCache[stdlibPath] = nil
		return nil, wrapped
	}

	var out []*ast.ImportStmt
	// index counts every CANDIDATE statement, not every accepted clone, so a
	// clone's synth slot is tied to its ordinal in prelude.nomi rather than
	// to which of its siblings happened to be re-exports.
	index := 0
	for _, n := range nodes {
		switch stmt := n.(type) {
		case *ast.ImportStmt:
			if cloned := cloneImportStmtForInject(stmt, index); cloned != nil {
				out = append(out, cloned)
			}
			index++
		case *ast.ImportBlock:
			for _, entry := range stmt.Entries {
				if cloned := cloneImportStmtForInject(entry, index); cloned != nil {
					out = append(out, cloned)
				}
				index++
			}
		}
	}

	preludeImportsCache[stdlibPath] = out
	return out, nil
}

// cloneImportStmtForInject returns a copy of `src` with re-export
// flags stripped (the user file isn't re-exporting; it's consuming)
// AND every nested *Ident/*TypeIdent position rewritten into the
// synth-band sentinel range.
//
// Why the deep rewrite: defineImport derives the per-imported-name
// `Pos` it writes into `fa.Definitions[Pos]` from each `Names[i]`
// node's `Line`/`Col` (see builder.go, the selective-import branch).
// Without the rewrite, every user file in the build ends up with
// ~35 `Definitions` (and `References`) entries at lines 36-58 — the
// source range of std/prelude.nomi's import block. Those phantom
// positions surface in LSP rename/refs (which iterate by Pos and have
// no file-tag), so a user-file with any binding at those line/col
// coordinates sees rename emit a TextEdit on the
// user file at positions that aren't theirs.
//
// The fix routes the cloned positions into the synth-band reserved
// for derive-synthesis (analysis/derive_synthesis.go: synthPreludeBase,
// IsSynthesizedLine) — into the region reserved for prelude clones
// specifically, which is disjoint from every file's own declaration
// slots because the cache shares these pointers across files. LSP rename/refs already filter out synth-band
// positions (lsp/references.go:126, 139), so synth-tagging the cloned
// idents lets the existing guards do their job without piecemeal
// changes inside every consumer.
//
// Other fields (IncludeParent, etc.) are passed through unchanged
// because we want defineImport to see the exact shape it would see
// from a user-typed import — including drill-through `Enum.{Variant}`
// and `self` markers.
//
// Clones must be free of source-range positions so LSP rename/refs
// filter them (via IsSynthesizedLine). The cache shares cloned
// pointers across files and BuildProject calls, which is safe
// because defineImport reads from the AST but never mutates. The
// synth-band positions are assigned once per cloned statement (one
// synth base per clone) so two user files sharing the same cloned
// ident still resolve their rename/refs into the synth band rather
// than into each other.
//
// `index` is the clone's ordinal in prelude.nomi's own re-export list,
// which is what makes the assigned positions a pure function of the
// prelude source: they no longer depend on when this cache was filled
// relative to any file's derive synthesis.
func cloneImportStmtForInject(src *ast.ImportStmt, index int) *ast.ImportStmt {
	if src == nil {
		return nil
	}
	// Only re-export statements participate in the prelude chain. The
	// auto-prepend would otherwise leak any sibling `import` line a
	// future prelude.nomi adds for its own purposes (e.g., a helper
	// that prelude uses internally but never re-exports).
	if !stmtIsReExport(src) {
		return nil
	}
	base := synthPreludeBase(index)
	col := 0
	nextSynthPos := func() (int, int) {
		col++
		return base, col
	}
	// Claim the first synth-col slot for the ImportStmt itself so its
	// top-level Line/Col point into the synth band rather than the
	// (0, 0) zero default. No consumer reads ImportStmt.Line/Col today,
	// but a future diagnostic anchored at the statement-level position
	// would otherwise surface as `(0, 0)`. Subsequent nextSynthPos()
	// calls (for each Names/Aliases/ModulePath ident and the SelfLine/
	// SelfCol slot) take cols 2, 3, … in the same synth-line band.
	stmtLine, stmtCol := nextSynthPos()
	// ModulePath gains a leading `std` segment. prelude.nomi is a stdlib
	// file, so it writes its re-exports bare (`maybe.Maybe.{self, None,
	// Some} export`) like any intra-module import; the clone is prepended to
	// files OUTSIDE the stdlib, where the same module is `std/maybe`. Without
	// the segment every user file's prelude chain would resolve against the
	// user's own project root and every prelude name would be undefined.
	//
	// The segment is synthesized here rather than in prelude.nomi because the
	// file's own siblings must stay bare — the stdlib is one module, and a
	// `std/`-spelled sibling would be a second key for one file.
	dst := &ast.ImportStmt{
		Line:          stmtLine,
		Col:           stmtCol,
		ModulePath:    prependStdSegment(cloneIdentSliceSynth(src.ModulePath, nextSynthPos), nextSynthPos),
		Names:         cloneIdentSliceSynth(src.Names, nextSynthPos),
		Aliases:       cloneIdentSliceSynth(src.Aliases, nextSynthPos),
		ModuleAlias:   cloneIdentNodeSynth(src.ModuleAlias, nextSynthPos),
		IncludeParent: src.IncludeParent,
		// Line / Col on the statement itself are assigned above to
		// synth-band positions, matching SelfLine / SelfCol below.
		// SelfLine / SelfCol record the source position of the `self`
		// token. defineImport writes Definitions/References at this
		// position when IncludeParent is true, so they must also live
		// in the synth band rather than pointing into prelude.nomi.
		// Zero out when IncludeParent is false — no consumer reads
		// SelfLine/SelfCol in that case, and zeros document intent.
		// ExportAll, ExportFlags, ExportAliases all left
		// at their zero values: the user file is consuming names, not
		// re-exporting them.
	}
	if src.IncludeParent {
		selfLine, selfCol := nextSynthPos()
		dst.SelfLine = selfLine
		dst.SelfCol = selfCol
	}
	return dst
}

// prependStdSegment returns `segs` with a synth-band `std` *Ident in front,
// turning a stdlib file's intra-module import path into the cross-module
// spelling a file outside the stdlib needs. The segment takes a synth-band
// position from the same counter as the cloned idents, so hover on it lands
// in the band LSP rename/refs already filter rather than on a real position
// in the user's file.
func prependStdSegment(segs []ast.Node, nextPos func() (int, int)) []ast.Node {
	line, col := nextPos()
	out := make([]ast.Node, 0, len(segs)+1)
	out = append(out, &ast.Ident{Name: "std", Line: line, Col: col})
	return append(out, segs...)
}

// cloneIdentSliceSynth produces a fresh slice of nodes with each
// *Ident/*TypeIdent replaced by a copy whose Line/Col point into the
// synth-band. Nil entries pass through as nil (Aliases has nil slots
// for unaliased names).
func cloneIdentSliceSynth(src []ast.Node, nextPos func() (int, int)) []ast.Node {
	if src == nil {
		return nil
	}
	out := make([]ast.Node, len(src))
	for i, n := range src {
		out[i] = cloneIdentNodeSynth(n, nextPos)
	}
	return out
}

// cloneIdentNodeSynth returns a fresh *Ident / *TypeIdent with synth-
// band Line/Col. Returns nil unchanged (Aliases slots, legacy
// ModuleAlias) and returns the original for any other node type
// (defensive; ModulePath/Names/Aliases only ever carry *Ident or
// *TypeIdent per ast.go's documented contract).
func cloneIdentNodeSynth(n ast.Node, nextPos func() (int, int)) ast.Node {
	if n == nil {
		return nil
	}
	line, col := nextPos()
	switch v := n.(type) {
	case *ast.Ident:
		return &ast.Ident{Name: v.Name, Line: line, Col: col}
	case *ast.TypeIdent:
		return &ast.TypeIdent{Name: v.Name, Line: line, Col: col}
	default:
		return n
	}
}

// stmtIsReExport reports whether the statement carries any re-export
// modifier — line-level `export` on a selective import, or any per-item
// ExportFlags entry being true.
func stmtIsReExport(s *ast.ImportStmt) bool {
	if s.ExportAll {
		return true
	}
	for _, f := range s.ExportFlags {
		if f {
			return true
		}
	}
	return false
}

// prependPreludeImports returns a node slice with every prelude
// import placed before the file's existing nodes. The originals are
// left untouched (we return a fresh slice to keep the synth-band
// invariant that no caller-visible AST node identity changes
// underneath them).
func prependPreludeImports(nodes []ast.Node, preludeStmts []*ast.ImportStmt) []ast.Node {
	if len(preludeStmts) == 0 {
		return nodes
	}
	out := make([]ast.Node, 0, len(nodes)+len(preludeStmts))
	for _, s := range preludeStmts {
		out = append(out, s)
	}
	out = append(out, nodes...)
	return out
}
