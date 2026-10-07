package analysis_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// findRedundantWithStdlib builds a lone document the way the LSP does — the
// prelude as the parent scope — and returns the structured query results.
func findRedundantWithStdlib(t *testing.T, src string) ([]analysis.UnusedImport, *analysis.FileAnalysis, []ast.Node) {
	t.Helper()
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	return analysis.FindRedundantPreludeImports(fa, nodes), fa, nodes
}

func redundantNames(items []analysis.UnusedImport) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}

// A used-but-prelude-provided import is the whole point of the rule: the
// unused-import check stays silent because `Iter.loop` references the binding,
// yet the import establishes a binding the file already had.
func TestRedundantPreludeImport_UsedNameStillReported(t *testing.T) {
	src := "import std/iter.Iter\n\nfn demo(): Int {\n  Iter.loop(|n = 0| { break n })\n}\n"
	got, _, _ := findRedundantWithStdlib(t, src)
	if len(got) != 1 {
		t.Fatalf("expected 1 redundant import, got %v", redundantNames(got))
	}
	if got[0].Name != "Iter" {
		t.Errorf("name: got %q, want Iter", got[0].Name)
	}
	if got[0].Pos.Line != 1 {
		t.Errorf("position: got line %d, want 1", got[0].Pos.Line)
	}
	if !strings.Contains(got[0].Message, "already in scope from the prelude") {
		t.Errorf("message: got %q", got[0].Message)
	}
}

func TestRedundantPreludeImport_LifecycleTypes(t *testing.T) {
	for _, tc := range []struct{ path, name string }{
		{"std/startup.Startup", "Startup"},
		{"std/context.Context", "Context"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _ := findRedundantWithStdlib(t, "import "+tc.path+"\nfn pass(x: "+tc.name+"): "+tc.name+" { x }")
			if len(got) != 1 || got[0].Name != tc.name {
				t.Fatalf("redundant imports: %v", redundantNames(got))
			}
			aliased, _, _ := findRedundantWithStdlib(t, "import "+tc.path+" as Input\nfn pass(x: Input): Input { x }")
			if len(aliased) != 0 {
				t.Fatalf("alias must remain an explicit import: %v", redundantNames(aliased))
			}
		})
	}
}

// A brace list reports per item and carries the index the surgical fix needs,
// so `{Comparable, Ordering}` yields two independently-removable entries.
func TestRedundantPreludeImport_BraceItemsReportedIndividually(t *testing.T) {
	src := "import std/comparable.{Comparable, Ordering}\n\n" +
		"fn cmp(a: Int, b: Int): Ordering {\n  Comparable.compare(a, b)\n}\n"
	got, _, _ := findRedundantWithStdlib(t, src)
	if len(got) != 2 {
		t.Fatalf("expected 2 redundant items, got %v", redundantNames(got))
	}
	for _, it := range got {
		if it.ItemKind != analysis.UnusedBraceItem {
			t.Errorf("%s: item kind = %v, want UnusedBraceItem", it.Name, it.ItemKind)
		}
		if it.NameIdx < 0 {
			t.Errorf("%s: NameIdx = %d, want a brace index", it.Name, it.NameIdx)
		}
	}
}

// A `self` item under a prelude type binds that type, so it is as redundant as
// the plain item, and it carries the self-marker kind the surgical fix removes.
func TestRedundantPreludeImport_SelfItemUnderPreludeType(t *testing.T) {
	src := "import std/maybe.Maybe.{self, None}\n\nfn f(): Maybe<Int> {\n  None\n}\n"
	got, _, _ := findRedundantWithStdlib(t, src)
	var self *analysis.UnusedImport
	for i := range got {
		if got[i].Name == "Maybe" {
			self = &got[i]
		}
	}
	if self == nil {
		t.Fatalf("the `self` item was not reported: %v", redundantNames(got))
	}
	if self.ItemKind != analysis.UnusedSelfMarker {
		t.Errorf("item kind = %v, want UnusedSelfMarker", self.ItemKind)
	}
}

// Every shape that must survive the rule. Each of these binds a name the
// prelude does NOT provide, so reporting any of them would be a false positive
// that breaks the file when acted on.
func TestRedundantPreludeImport_LoadBearingImportsExempt(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{{
		// The prelude exports no file API objects.
		name: "whole-file module",
		src:  "import std/strings\n\nfn f(s: String): String {\n  String.normalize(s, strings.NormalForm.NFC)\n}\n",
	}, {
		// An alias is a new name, not a duplicate binding.
		name: "aliased item",
		src:  "import std/iter.Iter as It\n\nfn demo(): Int {\n  It.loop(|n = 0| { break n })\n}\n",
	}, {
		// The prelude exports `Iter`, never bare `loop`.
		name: "drill-through past the prelude's stopping point",
		src:  "import std/iter.Iter.{loop}\n\nfn demo(): Int {\n  loop(|n = 0| { break n })\n}\n",
	}, {
		// Ordering's variants are deliberately not preluded.
		name: "variant drill-through",
		src: "import std/comparable.Ordering.{Less}\n\n" +
			"fn f(o: Ordering): Bool {\n  case o {\n    Less -> True\n    _ -> False\n  }\n}\n",
	}, {
		// Literal/Fragment are deliberately excluded from the prelude.
		name: "deliberately non-preluded types",
		src: "import std/literals.{Fragment, Literal}\n\n" +
			"struct Tag {\n  body: String\n}\n\n" +
			"impl Literal for Tag {\n" +
			"  fn from_fragments(fragments: List<Fragment<String>>): Tag {\n" +
			"    Tag{body: Iter.reduce(fragments, |acc = \"\", f|\n" +
			"      case f {\n        .Static(s) -> acc + s\n        .Dynamic(v) -> acc + v\n      }\n    )}\n" +
			"  }\n}\n",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _ := findRedundantWithStdlib(t, tc.src)
			if len(got) != 0 {
				t.Fatalf("expected no redundant imports, got %v", redundantNames(got))
			}
		})
	}
}

// Identity is compared through the resolved declaration, not the spelling. A
// module of the user's own exporting `Result` shadows the prelude deliberately.
func TestRedundantPreludeImport_SameNameDifferentModuleExempt(t *testing.T) {
	src := "import mine.Result\n\nfn f(_r: Result): Int {\n  0\n}\n"
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	lib := std.Load()
	loader := func(_ string, modulePath []string) ([]ast.Node, error) {
		if len(modulePath) == 1 && modulePath[0] == "mine" {
			mine, _ := parser.ParseWithRecovery(lexer.Lex("pub struct Result {\n  code: Int\n}\n"))
			return mine, nil
		}
		return nil, fmt.Errorf("no module %v", modulePath)
	}
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", loader)
	if got := analysis.FindRedundantPreludeImports(fa, nodes); len(got) != 0 {
		t.Fatalf("a different module's Result must not be reported: %v", redundantNames(got))
	}
}

// Stdlib modules get no prelude (std.Load passes nil primitives), so their
// explicit imports are load-bearing and must never be reported. Without the
// parent-scope guard this rule would demand deleting imports that stdlib files
// genuinely need.
func TestRedundantPreludeImport_StdlibExempt(t *testing.T) {
	src := "import std/iter.Iter\n\nfn f(xs: List<Int>): Int {\n  Iter.count(xs)\n}\n"
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, nil, lib.Modules, "", nil)
	if got := analysis.FindRedundantPreludeImports(fa, nodes); len(got) != 0 {
		t.Fatalf("prelude-less file must be exempt, got %v", redundantNames(got))
	}
}

// The diagnostic carries the code the LSP filters on, and fires through the
// ordinary builder pipeline rather than needing a separate call.
func TestRedundantPreludeImport_DiagnosticCarriesCode(t *testing.T) {
	src := "import std/strings.String\n\nfn f(s: String): Int {\n  String.length(s)\n}\n"
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	found := false
	for _, e := range fa.TypeErrors {
		if e.Code == analysis.RedundantPreludeImportCode {
			found = true
			if !strings.Contains(e.Message, "'String'") {
				t.Errorf("message should name the item: %q", e.Message)
			}
		}
	}
	if !found {
		t.Fatalf("no %s diagnostic in %v", analysis.RedundantPreludeImportCode, fa.TypeErrors)
	}
}
