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
	"github.com/nomi-language/nomi/std"
)

// buildAndCollectTypeErrors is buildAndCollectErrs with the POSITIONS kept.
// The sibling helper returns messages only, which is the half that cannot see
// this defect at all.
func buildAndCollectTypeErrors(t *testing.T, src string) []analysis.TypeError {
	t.Helper()
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "main.nomi")
	if err := os.WriteFile(mainPath, []byte(src), 0644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	// SYNTHESIS RUNS ON THE CALLER'S SLICE, and skipping it is how a first
	// draft of this test measured zero diagnostics. buildProjectWithCache
	// synthesizes into its OWN local copy (`entryNodes = extendedEntry`), so
	// a CheckTypes call handed the raw parse never walks a synthesized body.
	// internal/frontend's Checker.Prepare does exactly this before analysing.
	entryNodes, lowerErrs := analysis.LowerDerives(entryNodes)
	if len(lowerErrs) > 0 {
		t.Fatalf("LowerDerives: %v", lowerErrs)
	}
	entryNodes, _ = analysis.SynthesizeDerives(entryNodes)
	entryNodes = analysis.SynthesizeUniversalDebug(entryNodes)
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		return nodes, nil
	}
	lib := std.Load()
	fa, _, _ := analysis.BuildProjectWithCache(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	// BOTH PHASES. The build phase populates fa.TypeErrors; the check phase
	// is a separate call, and it is the one that reaches a synthesized
	// BODY. A helper that returned only the first half would report zero
	// diagnostics for every row below. Mirrors internal/frontend's
	// Checker.Analyze.
	errs := append([]analysis.TypeError(nil), fa.TypeErrors...)
	return append(errs, analysis.CheckTypes(fa, entryNodes)...)
}

// TestSynthDiagnostics_NeverCarryASynthBandPosition pins the rule that a
// diagnostic about a SYNTHESIZED impl body reports a position a programmer can
// navigate to.
//
// Both rows are reproductions; without the fix they read:
//
//	derive FromJson  ->  line 1432010752, col 84: unknown type "Json.ShapeError"
//	derive ToJson    ->  line 1431994368, col 7:  undefined type Json.Obj
//
// 1432010752 is not an uninitialized field. It decodes exactly as
// synthSlot(synthOriginConformance, index 2, synthKindFromJson) —
// 1<<30 + 21867*16384 — a legitimate slot from derive_synthesis.go's band. The
// defect is that a band position reached a user-facing message, which
// ast.ImplBlock.SynthOriginLine's own doc-comment already forbids ("a
// diagnostic about one must report HERE instead") and which implDiagPos
// already fixed for the diagnostics checkImplBlock raises itself. These rows
// come from its CALLEES, checking the synthesized body.
//
// Both rows were measured with a file that derives without importing `Json`.
// That file now checks: derived code reaches std's `Json` through the
// compiler-known route (synthSupportScope). The rows now declare the file's
// own `Json`, which the route leaves bound, so the synthesized body still
// names a type that has no `Obj` and the diagnostics still come from checking
// a synthesized body.
func TestSynthDiagnostics_NeverCarryASynthBandPosition(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "derive FromJson beside a file's own Json",
			src: `import { std/io; std/json.FromJson }

struct Json { a: Int }

struct P { a: Int }

derive FromJson for P

fn main() { io.print("x") }
`,
		},
		{
			name: "derive ToJson beside a file's own Json",
			src: `import { std/io; std/json.ToJson }

struct Json { a: Int }

struct P { a: Int }

derive ToJson for P

fn main() { io.print("x") }
`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			errs := buildAndCollectTypeErrors(t, tc.src)
			// Non-vacuity. If derived code ever stops resolving `Json` to
			// the file's own declaration these rows stop producing a
			// diagnostic, and a test that then asserts
			// "nothing is in the band" would assert nothing at all.
			if len(errs) == 0 {
				t.Fatal("no diagnostics: this row no longer exercises a synthesized-body diagnostic, so it proves nothing — repoint it at a shape that does, or delete it")
			}
			lineCount := strings.Count(tc.src, "\n") + 1
			for _, e := range errs {
				if analysis.IsSynthesizedLine(e.Line) {
					t.Errorf("diagnostic reports a synthesized-band position: %v", e)
					continue
				}
				if e.Line < 1 || e.Line > lineCount {
					t.Errorf("diagnostic reports line %d, outside the source's 1..%d: %v", e.Line, lineCount, e)
				}
			}
		})
	}
}

// TestSynthDiagnostics_AHandWrittenImplKeepsItsOwnPosition is the control the
// rewrite needs: it must move band positions and nothing else.
//
// A hand-written `impl` records no SynthOriginLine ("Zero on every
// hand-written block"), so the rewrite cannot reach it. Without this row a
// version that stamped every impl diagnostic onto the receiver declaration
// would pass the test above.
func TestSynthDiagnostics_AHandWrittenImplKeepsItsOwnPosition(t *testing.T) {
	src := `import { std/io; std/display.Display }

struct P { a: Int }

impl Display for P {
  fn to_string(_value: P): Nope { "x" }
}

fn main() { io.print("x") }
`
	errs := buildAndCollectTypeErrors(t, src)
	var found bool
	for _, e := range errs {
		if !strings.Contains(e.Message, "Nope") {
			continue
		}
		found = true
		if e.Line != 6 {
			t.Errorf("hand-written impl diagnostic moved: got line %d, want 6: %v", e.Line, e)
		}
	}
	if !found {
		t.Fatalf("expected a diagnostic naming the unresolvable return type `Nope`; got %v", errs)
	}
}
