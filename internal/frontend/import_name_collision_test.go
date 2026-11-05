package frontend_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// A top-level declaration named like a file import's short name keeps the
// name, and the import is an error that says so (spec §14 *Short names*);
// the file is reached through its directory instead. Checked with the file
// as the entry and as a file the entry imports, since a file built on its
// own defines its imports before its declarations and a project build the
// other way round.
func TestDeclarationNamedLikeAFileImportTakesTheName(t *testing.T) {
	const taken = "'b' is already defined in this scope as a function on line 3, so "
	for _, c := range []struct {
		name, src string
		// want is the one diagnostic, `line:col message`, or "" for none.
		want string
	}{
		{"the directory imported instead", "import jobs\n\npub fn b() -> Int {\n    jobs::b.x()\n}\n", ""},
		{"nested path", "import jobs::b\n\npub fn b() -> Int {\n    1\n}\n", "1:14 " + taken + "`import jobs::b` cannot bind it"},
		{"alias", "import jobs::notifications as b\n\npub fn b() -> Int {\n    1\n}\n", "1:31 " + taken + "`import jobs::notifications` cannot bind it"},
		{"one segment", "import b\n\npub fn b() -> Int {\n    1\n}\n", "1:8 " + taken + "`import b` cannot bind it"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"a.nomi":                  c.src,
				"b.nomi":                  "pub fn x() -> Int {\n    2\n}\n",
				"jobs/b.nomi":             "pub fn x() -> Int {\n    2\n}\n",
				"jobs/notifications.nomi": "pub fn x() -> Int {\n    2\n}\n",
				"main.nomi":               "import a\n\nfn main() {\n    dbg a.b()\n}\n",
			}
			for name, src := range files {
				path := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, entry := range []string{"main.nomi", "a.nomi"} {
				t.Run(entry, func(t *testing.T) {
					path := filepath.Join(dir, entry)
					_, err := frontend.New(frontend.Config{}).CheckFileSource(path, files[entry], frontend.Mode{})
					if c.want == "" {
						if err != nil {
							t.Fatalf("the front end rejects a declaration named like an import: %v", err)
						}
						return
					}
					ds, ok := err.(frontend.Diagnostics)
					if !ok {
						t.Fatalf("the front end admits an import whose name a declaration holds: %v", err)
					}
					var got []string
					for _, d := range ds {
						got = append(got, fmt.Sprintf("%s %d:%d %s", filepath.Base(d.Path), d.Line, d.Col, d.Message))
					}
					if want := "a.nomi " + c.want; strings.Join(got, "\n") != want {
						t.Fatalf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), want)
					}
				})
			}
		})
	}
}
