package vmhost_test

import (
	"strings"
	"testing"
)

// Programs the front end must refuse on the path `nomi check` and `nomi run`
// take (a project build of a real entry file), each with the message the
// reader sees. Every row fails if the source is admitted.

// rejects checks src through both entry paths and asserts every wanted
// fragment appears in the diagnostic.
func rejects(t *testing.T, src string, want ...string) {
	t.Helper()
	for _, path := range []struct {
		name string
		run  func(*testing.T, string) error
	}{{"check", checkEntry}, {"load", loadEntry}} {
		err := path.run(t, src)
		if err == nil {
			t.Fatalf("%s admits this source:\n%s", path.name, src)
		}
		for _, w := range want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: diagnostic lacks %q:\n%s", path.name, w, err)
			}
		}
	}
}

// admits checks src through `nomi check`'s path and fails on any diagnostic.
func admits(t *testing.T, src string) {
	t.Helper()
	if err := checkEntry(t, src); err != nil {
		t.Fatalf("the front end rejects this source: %v\n%s", err, src)
	}
}

func TestUnreachableCodeIsACheckError(t *testing.T) {
	rejects(t, "import std/io\n\nfn early(n: Int): Int {\n  return n\n  io.print(\"never\")\n  n + 1\n}\n\n"+
		"fn main() {\n  io.print(\"${early(1)}\")\n}\n",
		"main.nomi:5:3: unreachable code after return")
}

func TestSkippedNonTrailingDefaultIsACheckError(t *testing.T) {
	rejects(t, "import std/io\n\nfn middle(a: Int, b: Int = 10, c: Int): Int {\n  a + b + c\n}\n\n"+
		"fn main() {\n  io.print(\"${middle(1, 2)}\")\n}\n",
		"main.nomi:8:15: missing argument for parameter 'c'; positional arguments fill parameters in order "+
			"and cannot skip the defaulted 'b', so pass this one by name (c: ...)")
	admits(t, "import std/io\n\nfn middle(a: Int, b: Int = 10, c: Int): Int {\n  a + b + c\n}\n\n"+
		"fn main() {\n  io.print(\"${middle(1, c: 3)}\")\n}\n")
}

func TestCaseWithoutCatchAllIsACheckError(t *testing.T) {
	rejects(t, "import std/io\n\nfn sign(x: Int): String {\n  case {\n    x > 0 -> \"positive\"\n    x == 0 -> \"zero\"\n  }\n}\n\n"+
		"fn word(s: String): Int {\n  case s {\n    \"a\" -> 1\n    \"b\" -> 2\n  }\n}\n\n"+
		"fn main() {\n  io.print(sign(-1))\n  io.print(\"${word(\"c\")}\")\n}\n",
		"main.nomi:4:3: non-exhaustive case: add a `_` arm",
		"main.nomi:11:3: non-exhaustive case: add a `_` arm")
}

func TestDecoratorIsAParseError(t *testing.T) {
	rejects(t, "@uses io\nfn main() {\n  Unit\n}\n",
		"main.nomi:1:1: `@uses` is not supported: Nomi has no decorators")
	admits(t, "fn uses(uses: Int): Int {\n  uses\n}\n\nfn main() {\n  _ = uses(1)\n}\n")
}

func TestRedundantPreludeImportIsACheckError(t *testing.T) {
	rejects(t, "import std/iter.Iter\nimport std/float.Float\nimport std/maybe.Maybe.{self, None}\n\n"+
		"fn main() {\n  _n = Iter.count([1])\n  _s = Float.to_string(1.5)\n  _m: Maybe<Int> = None\n}\n",
		"main.nomi:1:17: imported name 'Iter' is already in scope from the prelude — remove it from the import",
		"main.nomi:2:18: imported name 'Float' is already in scope from the prelude — remove it from the import",
		"main.nomi:3:25: imported name 'Maybe' is already in scope from the prelude — remove it from the import",
		"main.nomi:3:31: imported name 'None' is already in scope from the prelude — remove it from the import")
}

func TestLoadBearingStdImportsStayAdmitted(t *testing.T) {
	admits(t, "import std/iter.Iter as It\nimport std/comparable.Ordering.{Less}\n\n"+
		"fn main() {\n  _n = It.count([1])\n  _o = Less\n}\n")
}
