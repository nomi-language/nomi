package analysis_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// An import whose path names no file is an error at the path, saying which
// file was looked for and where. The forms below all slipped through the
// front end once, leaving `nomi run` to stop at a BLOCKED line from the
// lowering.
func TestMissingImport_IsAnErrorAtThePath(t *testing.T) {
	utils := "pub fn greet(): Int {\n    1\n}\n"
	cases := []struct {
		name     string
		entry    string
		siblings map[string]string
		want     string // "line:col-endcol: message[; hint]"
	}{
		{
			name:  "file API import",
			entry: "import lib\n\nfn main() {\n    lib.greet()\n}\n",
			want:  "1:8-11: no module `lib`: no file lib.nomi in this file's directory",
		},
		{
			name:  "unused file API import",
			entry: "import lib\n\nfn main() {\n}\n",
			want:  "1:8-11: no module `lib`: no file lib.nomi in this file's directory",
		},
		{
			name:  "selected item",
			entry: "import lib.Thing\n\nfn main() {\n    _ = Thing.new()\n}\n",
			want:  "1:8-11: no module `lib`: no file lib.nomi in this file's directory",
		},
		{
			name:  "brace selector",
			entry: "import lib.{a, b}\n\nfn main() {\n    a()\n    b()\n}\n",
			want:  "1:8-11: no module `lib`: no file lib.nomi in this file's directory",
		},
		{
			name:  "owner selector",
			entry: "import shape.Shape.{Circle}\n\nfn main() {\n    _ = Circle(1.0)\n}\n",
			want:  "1:8-13: no module `shape`: no file shape.nomi in this file's directory",
		},
		{
			name:  "nested path",
			entry: "import tools/lib\n\nfn main() {\n    lib.greet()\n}\n",
			want:  "1:8-17: no module `tools/lib`: no file lib.nomi in tools/",
		},
		{
			// utils.nomi exists, but the `/` makes `inner` a file, not
			// an owner inside utils.nomi.
			name:     "missing file under an existing one",
			entry:    "import utils/inner.Thing\n\nfn main() {\n    _ = Thing.new()\n}\n",
			siblings: map[string]string{"utils": utils},
			want:     "1:8-19: no module `utils/inner`: no file inner.nomi in utils/",
		},
		{
			name:     "misspelled sibling",
			entry:    "import utlis\n\nfn main() {\n    utlis.greet()\n}\n",
			siblings: map[string]string{"utils": utils},
			want:     "1:8-13: no module `utlis`: no file utlis.nomi in this file's directory; did you mean 'utils'?",
		},
		{
			name:     "misspelled sibling in a selector",
			entry:    "import utlis.greet\n\nfn main() {\n    greet()\n}\n",
			siblings: map[string]string{"utils": utils},
			want:     "1:8-13: no module `utlis`: no file utlis.nomi in this file's directory; did you mean 'utils'?",
		},
		{
			name:     "directory",
			entry:    "import sub\n\nfn main() {\n    sub.greet()\n}\n",
			siblings: map[string]string{"sub/helper": utils},
			want:     "1:8-11: no module `sub`: no file sub.nomi in this file's directory; `sub` is a directory; import a file in it, such as `sub/helper`",
		},
		{
			name:  "nested import",
			entry: "fn main() {\n    import lib.greet\n\n    greet()\n}\n",
			want:  "2:12-15: no module `lib`: no file lib.nomi in this file's directory",
		},
		{
			name:  "standard library module",
			entry: "import std/nosuchmodule\n\nfn main() {\n    nosuchmodule.x()\n}\n",
			want:  "1:8-24: no module `std/nosuchmodule`: the standard library has no nosuchmodule.nomi",
		},
		{
			name:  "misspelled standard library module",
			entry: "import std/lsits\n\nfn main() {\n    lsits.x()\n}\n",
			want:  "1:8-17: no module `std/lsits`: the standard library has no lsits.nomi; did you mean 'std/lists'?",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := buildProjectExpectingErrors(t, tc.entry, tc.siblings)
			var got []string
			for _, e := range errs {
				if !strings.HasPrefix(e.Message, "no module") {
					continue
				}
				s := fmt.Sprintf("%d:%d-%d: %s", e.Line, e.Col, e.EndCol, e.Message)
				if len(e.Hints) > 0 {
					s += "; " + strings.Join(e.Hints, "; ")
				}
				got = append(got, s)
			}
			if len(got) == 0 {
				t.Fatalf("the front end admits the import; errors: %v", errs)
			}
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), tc.want)
			}
		})
	}
}

// An import into another module names that module and the file it lacks,
// with the module's file the path misspells.
func TestMissingImport_CrossModule(t *testing.T) {
	todoMain := "import stringkit/pda.pad_left\n\nfn main() {\n  _ = pad_left(\"hi\")\n}\n"
	todoRoot, _, entryPath := writeCratePackagingExample(t, todoMain, "pub fn pad_left(s: String): String {\n  s\n}\n", nil)
	data, _ := os.ReadFile(entryPath)
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
	lib := std.Load()
	fa := analysis.BuildProject(entryNodes, lib.Primitives, lib.Modules, lib.Files, todoRoot, std.MakeLoader())
	want := "1:8-21: no module `stringkit/pda`: module `stringkit` has no file pda.nomi; did you mean 'stringkit/pad'?"
	var got []string
	for _, e := range fa.TypeErrors {
		s := fmt.Sprintf("%d:%d-%d: %s", e.Line, e.Col, e.EndCol, e.Message)
		if len(e.Hints) > 0 {
			s += "; " + strings.Join(e.Hints, "; ")
		}
		got = append(got, s)
	}
	if len(got) == 0 {
		t.Fatal("the front end admits the import")
	}
	if strings.Join(got, "\n") != want {
		t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), want)
	}
}

// A missing import's error stands alone: the names it would have bound are
// still bound, so their uses report nothing more.
func TestMissingImport_ReportsNothingElse(t *testing.T) {
	errs := buildProjectExpectingErrors(t, "import lib.{a, b}\n\nfn main() {\n    a()\n    b()\n}\n", nil)
	if len(errs) != 1 {
		t.Fatalf("want the one missing-module error, got %d: %v", len(errs), errs)
	}
}

// A name missing from a module that exists is an error at the name, and the
// module is spelled as it is imported.
func TestMissingImport_NameInAnExistingModule(t *testing.T) {
	cases := []struct {
		entry, want string
	}{
		{"import std/io.nosuchname\n\nfn main() {\n    nosuchname()\n}\n", "1:15: file 'std/io' has no exported name 'nosuchname'"},
		{"import utils.gret\n\nfn main() {\n    gret()\n}\n", "1:14: file 'utils' has no exported name 'gret'"},
	}
	for _, tc := range cases {
		errs := buildProjectExpectingErrors(t, tc.entry, map[string]string{"utils": "pub fn greet() {\n}\n"})
		found := false
		for _, e := range errs {
			if strings.HasPrefix(e.Message, "no module") {
				t.Errorf("%q: the module exists, but: %s", tc.entry, e.Message)
			}
			if fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Message) == tc.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: want %q, got %v", tc.entry, tc.want, errs)
		}
	}
}

// Imports of files that exist stay accepted, in every form, including an
// import cycle and a nested path.
func TestMissingImport_PresentModulesStayAccepted(t *testing.T) {
	siblings := map[string]string{
		"utils":       "import tools/shape\n\npub fn greet(): Int {\n    shape.area()\n}\n",
		"tools/shape": "import utils\n\npub enum Shape {\n    Circle(Float)\n}\n\npub fn area(): Int {\n    1\n}\n\npub fn twice(): Int {\n    utils.greet() + 1\n}\n",
	}
	entry := "import {\n    std/io\n    tools/shape.Shape.{Circle}\n    utils\n    utils.greet\n}\n\nfn main() {\n    _ = Circle(1.0)\n    io.print(Int.to_string(utils.greet() + greet()))\n}\n"
	errs := buildProjectExpectingErrors(t, entry, siblings)
	for _, e := range errs {
		if e.Code == analysis.UnusedBindingCode {
			continue
		}
		t.Errorf("unexpected error: %d:%d: %s", e.Line, e.Col, e.Message)
	}
}
