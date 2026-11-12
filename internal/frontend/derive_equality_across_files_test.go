package frontend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A declared type with equality and no Equatable or Hashable impl of its
// own meets either structurally (spec §5), wherever it is declared. A
// derive over a field of another file's enum whose `embeds` variants name
// that file's types reads those names in that file: read in the deriving
// file, which imports only the enum, they named nothing, and the derive was
// rejected with "no impl of `Equatable` for `Shape`" where the same enum in
// the deriving file checked.
func TestDeriveEqualityOfAnotherFilesEmbedsEnum(t *testing.T) {
	lib := "pub struct Spot\n    n: Int\nend\n\npub type Tag String\n\npub enum Shape\n    embeds Spot\n    embeds Tag\n    Dot\nend\n"
	main := "import lib.Shape\n\nstruct Drv\n    v: Shape\nend\n\nderive Equatable for Drv\nderive Hashable for Drv\n\n" +
		"fn main()\n    d = Drv{v: Shape.Dot}\n    _ = d == d and Hashable.hash(d) == Hashable.hash(d)\nend\n"
	check := func(lib string) error {
		dir := t.TempDir()
		for name, src := range map[string]string{"main.nomi": main, "lib.nomi": lib} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		_, err := New(Config{}).CheckFile(filepath.Join(dir, "main.nomi"), Mode{})
		return err
	}
	if err := check(lib); err != nil {
		t.Fatalf("the derive over another file's embeds enum is rejected: %v", err)
	}
	// The same enum with a function in a variant has no equality.
	const want = "no impl of `Equatable` for `Shape` (required via `derive Equatable for Drv`, field `v`)"
	err := check(strings.Replace(lib, "    Dot\n", "    Dot\n    Fn {f: (Int) -> Int}\n", 1))
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("the derive over an enum holding a function checks as %v; want %q", err, want)
	}
}
