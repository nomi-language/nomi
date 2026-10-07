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

// Two declarations of one short name print the same, so a mismatch between
// them must name each one's declaring file (type_diagnostic.go).

var samePointSiblings = map[string]string{
	"a": "pub struct Point {\n  x: Int\n}\n",
	"b": "pub struct Point {\n  x: Int\n}\n\npub fn show(p: Point): Int {\n  p.x\n}\n\npub fn show_all(ps: List<Point>): Int {\n  Iter.count(ps)\n}\n",
}

func assertErrorExact(t *testing.T, errs []analysis.TypeError, want string) {
	t.Helper()
	for _, e := range errs {
		if e.Message == want {
			return
		}
	}
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Message)
	}
	t.Fatalf("no error reads exactly\n  %s\ngot:\n  %s", want, strings.Join(msgs, "\n  "))
}

func TestTypeDiagnostic_SameNamedStructsNameTheirFiles(t *testing.T) {
	errs := buildProjectExpectingErrors(t,
		"import a\nimport b\n\nfn demo(): Int {\n  b.show(a.Point{x: 1})\n}\n",
		samePointSiblings)
	assertErrorExact(t, errs, "argument 1: expected b.Point, got a.Point")
}

func TestTypeDiagnostic_SameNamedTypeInsideAContainerIsQualified(t *testing.T) {
	errs := buildProjectExpectingErrors(t,
		"import a\nimport b\n\nfn demo(): Int {\n  xs: List<a.Point> = [a.Point{x: 1}]\n  b.show_all(xs)\n}\n",
		samePointSiblings)
	assertErrorExact(t, errs, "argument 1: expected List<b.Point>, got List<a.Point>")
}

// buildEntryExpectingErrors builds an entry file the way `nomi check` does,
// with its path known, so the entry's own types are spelled by its file name
// (`main.Mode`). Siblings are written beside it, keyed by file name without
// the extension.
func buildEntryExpectingErrors(t *testing.T, src string, siblings map[string]string) []analysis.TypeError {
	t.Helper()
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "main.nomi")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range siblings {
		if err := os.WriteFile(filepath.Join(tmp, name+".nomi"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		data, err := os.ReadFile(filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi")
		if err != nil {
			return nil, err
		}
		n, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		return n, nil
	}
	lib := std.Load()
	fa, _, _ := analysis.BuildProjectFromEntry(mainPath, nodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	errs := append([]analysis.TypeError{}, fa.TypeErrors...)
	return append(errs, analysis.CheckTypes(fa, nodes)...)
}

// An impl whose signature names the file's own `Mode` against an interface
// declared with another file's `Mode` prints both, each by its file. (A local
// redeclaration of a prelude name such as `Ordering` no longer reaches this
// diagnostic: the prelude keeps the name, so the signature names std's type
// too. See reserved_name_one_lookup_test.go.)
func TestTypeDiagnostic_LocalTypeAgainstAnInterfacesSameNamedType(t *testing.T) {
	errs := buildEntryExpectingErrors(t, `import modes.{Toggle}

enum Mode {
  On
  Off
}

struct Box {
  n: Int
}

impl Toggle for Box {
  fn mode(_b: Box): Mode {
    Mode.On
  }
}

fn main() {
}
`, map[string]string{"modes": `pub enum Mode {
  On
  Off
}

pub interface Toggle {
  fn mode(value: self): Mode
}
`})
	assertErrorExact(t, errs,
		"impl function 'mode': return type main.Mode does not match interface 'Toggle' return type modes.Mode")
	for _, e := range errs {
		if strings.Contains(e.Message, "Mode does not match interface 'Toggle' return type Mode") {
			t.Errorf("a message still names both declarations the same: %s", e.Message)
		}
	}
}

// A mismatch whose two types already print differently is left exactly as it
// was: only a collision is qualified.
func TestTypeDiagnostic_OrdinaryMismatchIsUnchanged(t *testing.T) {
	errs := buildProjectExpectingErrors(t,
		"import a\n\nfn take(n: Int): Int {\n  n\n}\n\nfn bad(): Int {\n  \"nope\"\n}\n\nfn demo(): Int {\n  p = a.Point{x: 1}\n  take(\"s\") + p\n}\n",
		samePointSiblings)
	assertErrorExact(t, errs, "argument 1: expected Int, got String")
	assertErrorExact(t, errs, "return type mismatch: expected Int, got String")
	assertErrorExact(t, errs, "binary + type mismatch: Int vs Point")
}
