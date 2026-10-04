package vmhost_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A type declared twice is reported as the redeclaration, and nothing derived
// from the second declaration adds to it: universal Debug synthesizes one impl
// per name, and a `derive` builds its body for the first declaration, the one
// the name denotes. Before, the second declaration got a second `Debug` impl
// (a duplicate-impl error at a synthesized line), and a derive was built for
// the last declaration ("enum pattern requires an enum type").
func TestCheck_RedeclaredTypeReportsOnlyTheRedeclaration(t *testing.T) {
	src := strings.Join([]string{
		"derive Debug for A",
		"",
		"struct A {",
		"    x: Int",
		"}",
		"",
		"derive Equatable for B",
		"",
		"struct B {",
		"    x: Int",
		"}",
		"",
		"enum B {",
		"    One",
		"}",
		"",
		"enum A {",
		"    One",
		"}",
		"",
		"struct C {",
		"    x: Int",
		"}",
		"",
		"enum C {",
		"    One",
		"}",
		"",
		"fn main() {",
		"}",
		"",
	}, "\n")
	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	err := vmhost.Check(path)
	if err == nil {
		t.Fatal("a type declared twice passed the check")
	}
	want := strings.Join([]string{
		path + ":13:6: 'B' is already defined in this scope as a type",
		path + ":13:6: help: pick a different name",
		path + ":9:8: note: 'B' is first defined here",
		path + ":17:6: 'A' is already defined in this scope as a type",
		path + ":17:6: help: pick a different name",
		path + ":3:8: note: 'A' is first defined here",
		path + ":25:6: 'C' is already defined in this scope as a type",
		path + ":25:6: help: pick a different name",
		path + ":21:8: note: 'C' is first defined here",
	}, "\n")
	if got := strings.TrimRight(err.Error(), "\n"); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// An imported file's build-phase error fails vmhost.Check and vmhost.Load at
// that file's position, once, though two files import it.
func TestCheckAndLoad_ReportAnImportedFilesBuildErrorOnce(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{
		"nomi.toml":   "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"main.nomi":   "import std/io\nimport left\nimport right\n\nfn main() {\n  io.print(\"${left.f()}${right.f()}\")\n}\n",
		"left.nomi":   "import shared\n\npub fn f(): String {\n  shared.s()\n}\n",
		"right.nomi":  "import shared\n\npub fn f(): String {\n  shared.s()\n}\n",
		"shared.nomi": "import std/regex\n\npub fn s(): String {\n  \"h\"\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	main := filepath.Join(dir, "main.nomi")
	want := filepath.Join(dir, "shared.nomi") + ":1:12: imported module 'regex' is unused — remove the import"
	checkErr := vmhost.Check(main)
	_, loadErr := vmhost.Load(main)
	for name, err := range map[string]error{"Check": checkErr, "Load": loadErr} {
		if err == nil {
			t.Errorf("vmhost.%s accepts an unused import in an imported file", name)
			continue
		}
		if got := strings.TrimRight(err.Error(), "\n"); got != want {
			t.Errorf("vmhost.%s:\n%s\nwant:\n%s", name, got, want)
		}
	}
}
