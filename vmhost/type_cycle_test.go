package vmhost_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A type defined in terms of itself is a check error at its declaration on
// the path `nomi check` and `nomi run` take. `type e e` overflowed the Go
// stack in the checker, which no recover catches.
func TestTypeCycleIsACheckError(t *testing.T) {
	rejects(t, "type e e\n\nfn main() {\n}\n",
		"main.nomi:1:6: type 'e' is defined in terms of itself")
	rejects(t, "type A B\n\ntype B List<A>\n\nfn main() {\n}\n",
		"main.nomi:3:6: type 'B' is defined in terms of itself (B → A → B)")
	rejects(t, "typealias X Y\n\ntypealias Y Maybe<X>\n\nfn main() {\n}\n",
		"main.nomi:1:11: typealias 'X' refers to itself (X → Y → X)",
		"main.nomi:3:11: typealias 'Y' refers to itself (Y → X → Y)")
}

// The cycle closes in whichever file is built second, and is reported
// there.
func TestTypeCycleAcrossFilesIsACheckError(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.nomi", "import b\n\npub type A b.B\n\nfn main() {\n}\n")
	write("b.nomi", "import main\n\npub type B List<main.A>\n")
	err := vmhost.Check(filepath.Join(dir, "main.nomi"))
	if err == nil || !strings.Contains(err.Error(), "is defined in terms of itself") {
		t.Fatalf("the cross-file cycle is not reported: %v", err)
	}
}

// A typealias may name one declared after it, and a distinct type may name
// either. Aliases were built in source order, so each of these was an
// "unknown type" error.
func TestTypeAliasDeclaredLaterRuns(t *testing.T) {
	got := runOutput(t, `import std/io

type A Later

typealias Later Earlier

typealias Earlier Int

fn main() {
    A(n) = A(3)
    m: Later = n + 1
    io.print(Int.to_string(m))
}
`)
	if got != "4\n" {
		t.Fatalf("output %q", got)
	}
}
