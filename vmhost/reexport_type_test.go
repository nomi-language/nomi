package vmhost_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A type another file re-exports with `pub import` names that type in an
// importer's annotations, a parameter's and a binding's alike. Both used to
// be `unknown type "Helper"`: the registries followed one import link, and a
// re-export is an import of an import.
func TestReExport_ATypeReExportedByAnotherFileNamesTheType(t *testing.T) {
	files := map[string]string{
		"other":  "pub struct Helper {\n    n: Int\n}\n\npub fn make(n: Int) -> Helper {\n    Helper{n}\n}\n",
		"facade": "pub import other.{Helper, make}\n",
	}
	main := "import std::io\nimport facade.{Helper, make}\n\nfn double(h: Helper) -> Int {\n    h.n * 2\n}\n\nfn main() {\n    h: Helper = make(4)\n    io.print(\"${double(h)}\")\n}\n"
	var out bytes.Buffer
	p, err := vmhost.LoadSource("main", main, vmhost.WithVirtualFiles(files), vmhost.WithOutput(&out))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if out.String() != "8\n" {
		t.Errorf("printed %q; want %q", out.String(), "8\n")
	}
}
