package vmhost_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

const reexportLeaf = `pub fn make_box(n: Int): Box {
    Box{value: n}
}

pub fn open_box(b: Box): Int {
    b.value
}

pub struct Box {
    value: Int
}
`

// A facade that re-exports a whole file is a check error at its `export`,
// reported for the facade file, not the entry that imports it.
func TestFileReExport_IsACheckErrorInTheFacade(t *testing.T) {
	path := writeCycleProject(t, map[string]string{
		"facade.nomi": "import {\n    leaf export\n}\n",
		"leaf.nomi":   reexportLeaf,
		"main.nomi":   "import facade.{leaf}\n\nfn main() {\n    _b = leaf.make_box(7)\n}\n",
	})
	err := vmhost.Check(path)
	if err == nil {
		t.Fatal("check admits a facade that re-exports a whole file")
	}
	for _, want := range []string{"facade.nomi", "`leaf` is a file, and a file cannot be re-exported", "import `leaf` directly"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("check error lacks %q:\n%v", want, err)
		}
	}
}

// The same facade listing its items runs, selected or aliased.
func TestFileReExport_ItemFacadeRuns(t *testing.T) {
	path := writeCycleProject(t, map[string]string{
		"facade.nomi": "import {\n    leaf.{Box, make_box, open_box} export\n}\n",
		"leaf.nomi":   reexportLeaf,
		"main.nomi": `import {
    std/io
    facade.{Box, make_box, open_box as open}
}

fn main() {
    b = make_box(7)
    io.inspect(open(b))
    io.inspect(b.value)
    io.inspect(open(Box{value: 8}))
}
`,
	})
	if err := vmhost.Check(path); err != nil {
		t.Fatalf("check: %v", err)
	}
	prog, err := vmhost.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var out bytes.Buffer
	if err := prog.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "7\n7\n8\n"; out.String() != want {
		t.Fatalf("output %q, want %q", out.String(), want)
	}
}
