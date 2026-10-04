package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// TestPreludeAutoprepend_SuppressedInStdlibFiles pins the
// stdlib-files-opt-out-of-the-auto-prepend branch in
// project_build.go's BuildProjectWithCache. The pre-cutover symmetry
// (every file got prelude as a parent scope) would have made this
// test impossible because stdlib files implicitly inherited every
// prelude name. Post-cutover, stdlib files MUST `import std/'X.{Y}`
// what they consume — including names they themselves re-export
// from prelude. The diagnostic stdlib gets on bare prelude-resident
// references is what guarantees stdlib stays explicit.
//
// Topology (rooted at a temp NOMI_STD_PATH so the real bundled
// stdlib doesn't interfere):
//
//	$NOMI_STD_PATH/prelude.nomi  — re-exports `OrphanProbe` from probe
//	$NOMI_STD_PATH/probe.nomi    — declares `pub type OrphanProbe`
//	$NOMI_STD_PATH/consumer.nomi — bare reference to OrphanProbe
//	                                 (no explicit import) — would
//	                                 resolve via auto-prepend if it
//	                                 received one
//	<project>/main.nomi          — `import std/consumer.{use}`; the
//	                                 import forces discovery to walk
//	                                 std/consumer.nomi and analyze
//	                                 it, surfacing the bare reference
//	                                 as a missing-name error
//
// Assertions:
//
//   - consumer.nomi (stdlib) has an "undefined name 'OrphanProbe'"
//     TypeError — no auto-prepend brought OrphanProbe into scope.
//   - main.nomi (user) has NO such error — its auto-prepend chained
//     through prelude → probe and lifted OrphanProbe.
func TestPreludeAutoprepend_SuppressedInStdlibFiles(t *testing.T) {
	stdRoot := t.TempDir()
	mustWrite := func(rel, content string) {
		full := filepath.Join(stdRoot, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	// Minimal fake stdlib. nomi.toml is required for the resolver to
	// accept this as a real module root.
	mustWrite("nomi.toml", "[module]\nname = \"std\"\nentry_points = []\n")
	mustWrite("prelude.nomi", "import std/probe.{OrphanProbe} export\n")
	mustWrite("probe.nomi", "pub type OrphanProbe\n")
	// Stdlib's consumer.nomi: bare reference to OrphanProbe. The whole
	// point of the test — would resolve via auto-prepend if stdlib
	// files weren't suppressed. The `pub fn use_probe(): OrphanProbe`
	// signature carries the bare reference (no `import std/probe`).
	mustWrite("consumer.nomi", "pub fn use_probe(): OrphanProbe { OrphanProbe }\n")
	t.Setenv("NOMI_STD_PATH", stdRoot)

	// Project setup. main.nomi imports std/consumer.{use_probe} so
	// discovery walks std/consumer.nomi. main.nomi itself uses
	// OrphanProbe bare — that's the user-file half of the assertion.
	projRoot := t.TempDir()
	mainPath := filepath.Join(projRoot, "main.nomi")
	mainSrc := `import std/consumer.{use_probe}

fn main() {
  x: OrphanProbe = OrphanProbe
  Unit
}
`
	if err := os.WriteFile(mainPath, []byte(mainSrc), 0644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}

	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(mainSrc))
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		return nodes, nil
	}

	entryFA, siblingFAs, _ := analysis.BuildProjectWithCache(
		entryNodes, nil, nil, nil, projRoot, loader,
	)
	if entryFA == nil {
		t.Fatal("expected non-nil entry FA")
	}

	// Stdlib consumer.nomi should have observed the bare OrphanProbe
	// reference WITHOUT auto-prepend resolving it.
	consumerFA, ok := siblingFAs["std/consumer"]
	if !ok {
		var keys []string
		for k := range siblingFAs {
			keys = append(keys, k)
		}
		t.Fatalf("expected std/consumer in siblingFAs, got keys: %v", keys)
	}
	consumerErr := false
	for _, e := range consumerFA.TypeErrors {
		if strings.Contains(e.Message, "OrphanProbe") {
			consumerErr = true
			break
		}
	}
	if !consumerErr {
		t.Errorf("expected std/consumer.nomi to error on bare OrphanProbe (auto-prepend should be suppressed for stdlib files), got: %v",
			consumerFA.TypeErrors)
	}

	// entry (user) main.nomi resolved OrphanProbe via auto-prepend
	// chain prelude → probe. No undefined-name diagnostic for it.
	for _, e := range entryFA.TypeErrors {
		if strings.Contains(e.Message, "OrphanProbe") {
			t.Errorf("user main.nomi unexpectedly errored on OrphanProbe (auto-prepend should have lifted it): %s",
				e.Message)
		}
	}
}

// TestPreludeAutoprepend_NoPhantomPositionsInUserFile checks that the
// injected prelude imports leave no definitions at real positions in the
// user's file.
//
// If cloneImportStmtForInject in prelude_inject.go shared AST node pointers
// for ModulePath/Names/Aliases with prelude.nomi, it would keep their
// Line/Col positions, and defineImport would write
// Definitions[Pos{preludeLine, preludeCol}] = sym into every user file's FA.
// LSP rename/refs iterate Definitions/References by Pos with no file tag, so
// those would surface as edits inside the user's own source.
//
// cloneImportStmtForInject deep-clones the position-carrying nodes into the
// synth-band reserved by derive_synthesis, where LSP rename/refs already
// filter them out.
//
// Assertion shape: count the file's actual source lines, then
// verify every Definitions/References entry is at a position
// either inside that line range OR in the synth band. Anything
// in between (e.g. line 36 in a 5-line file) is a phantom.
func TestPreludeAutoprepend_NoPhantomPositionsInUserFile(t *testing.T) {
	projRoot := t.TempDir()
	// Minimal single-line-of-real-content user file. The auto-prepend
	// fires at BuildProjectWithCache time with the real bundled
	// prelude.nomi, so the test exercises the production code path.
	mainSrc := "fn main() { Unit }\n"
	if err := os.WriteFile(filepath.Join(projRoot, "main.nomi"), []byte(mainSrc), 0644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}
	lineCount := strings.Count(mainSrc, "\n")
	if lineCount == 0 {
		lineCount = 1
	}

	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(mainSrc))
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		return nodes, nil
	}

	entryFA, _, _ := analysis.BuildProjectWithCache(
		entryNodes, nil, nil, nil, projRoot, loader,
	)
	if entryFA == nil {
		t.Fatal("expected non-nil entry FA")
	}

	// Real-source positions: line 1..lineCount.
	// Synth-band positions: line >= synthLineBase (filtered by
	// LSP rename/refs via analysis.IsSynthesizedLine).
	// Anything else is a phantom from cloned prelude imports.
	var phantomDefs []analysis.Pos
	for pos := range entryFA.Definitions {
		if pos.Line >= 1 && pos.Line <= lineCount {
			continue
		}
		if analysis.IsSynthesizedLine(pos.Line) {
			continue
		}
		phantomDefs = append(phantomDefs, pos)
	}
	if len(phantomDefs) > 0 {
		t.Errorf("user main.nomi Definitions contains %d phantom positions outside source range 1..%d and outside synth-band: %v",
			len(phantomDefs), lineCount, phantomDefs)
	}

	var phantomRefs []analysis.Pos
	for pos := range entryFA.References {
		if pos.Line >= 1 && pos.Line <= lineCount {
			continue
		}
		if analysis.IsSynthesizedLine(pos.Line) {
			continue
		}
		phantomRefs = append(phantomRefs, pos)
	}
	if len(phantomRefs) > 0 {
		t.Errorf("user main.nomi References contains %d phantom positions outside source range 1..%d and outside synth-band: %v",
			len(phantomRefs), lineCount, phantomRefs)
	}
}
