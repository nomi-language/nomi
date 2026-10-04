package vmhost_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// An unknown name's diagnostic carries the hint "did you mean" when exactly
// one name in scope is close to it, and no hint when none is.
func TestUnknownNames_SuggestTheClosestName(t *testing.T) {
	helper := "pub fn greet(): String {\n  \"hi\"\n}\n"
	for _, tc := range []struct {
		kind, body, want string
	}{
		{"file member", `  io.prnt("hi")`, "file 'io' has no member 'prnt'; did you mean 'print'?"},
		{"variable", "  total = 3\n  io.print(Int.to_string(totl + total))",
			"undefined variable 'totl'; did you mean 'total'?"},
		{"function", "  io.print(Int.to_string(count_itemz(1)))",
			"undefined variable 'count_itemz'; did you mean 'count_items'?"},
		{"type", "  p: Pointt = Point{x: 1, y: 2}\n  io.print(Int.to_string(p.x))",
			`unknown type "Pointt"; did you mean 'Point'?`},
		{"field", "  p = Point{x: 1, y: 2}\n  io.print(Int.to_string(p.yy))",
			"struct 'Point' has no field 'yy'; did you mean 'y'?"},
		{"variant", "  c = Color.Gren\n  io.print(Debug.inspect(c))",
			"type 'Color' has no member 'Gren'; did you mean 'Green'?"},
		{"variant pattern", "  c = Color.Red\n  s = case c {\n    Color.Red -> \"r\"\n    Color.Gren -> \"g\"\n  }\n  io.print(s)",
			"variant Gren not found in enum Color; did you mean 'Green'?"},
		{"dot variant", "  c: Color = .Gren\n  io.print(Debug.inspect(c))",
			"no variant 'Gren' on enum Color; did you mean 'Green'?"},
		{"import item", "  io.print(greett())",
			"file 'helper' has no exported name 'greett'; did you mean 'greet'?"},
		// No name in scope is close: the message stops at the name.
		{"no suggestion", "  io.print(Int.to_string(zzzzzz))", "undefined variable 'zzzzzz'\n"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			dir := t.TempDir()
			imports := "import std/io\n"
			if tc.kind == "import item" {
				imports += "import helper.{greett}\n"
			}
			src := imports + "\nstruct Point {\n  x: Int\n  y: Int\n}\n\nenum Color {\n  Red\n  Green\n}\n\n" +
				"fn count_items(n: Int): Int {\n  n\n}\n\nfn main() {\n" + tc.body + "\n}\n"
			for name, text := range map[string]string{"main.nomi": src, "helper.nomi": helper} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err := vmhost.Check(filepath.Join(dir, "main.nomi"))
			var ds vmhost.Diagnostics
			if !errors.As(err, &ds) {
				t.Fatalf("the front end accepts this program, so there is no diagnostic to suggest in: %v", err)
			}
			// The suggestion is the diagnostic's hint, not part of its message.
			msg, hint, _ := strings.Cut(strings.TrimSuffix(tc.want, "\n"), "; ")
			found := false
			for _, d := range ds {
				if d.Message == msg {
					found = true
					if got := strings.Join(d.Hints, "|"); got != hint {
						t.Fatalf("%s: hints %q, want %q", msg, got, hint)
					}
				}
			}
			if !found {
				t.Fatalf("want %q in:\n%s", msg, err)
			}
			if tc.kind == "import item" && strings.Contains(err.Error(), "is private") {
				t.Fatalf("a name the file does not declare is reported as private too:\n%s", err)
			}
		})
	}
}
