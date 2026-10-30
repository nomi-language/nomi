package vmhost_test

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A tool runs in the project's root: it reads the project's files and
// writes one, which comes back as that section's new text, and the
// process's working directory is as it was.
func TestToolMarked_WritesTheProjectsFiles(t *testing.T) {
	src := `// FILE: main.nomi
import {
    std/fs
    std/io
}

fn main() {
    io.print(fs.read_text("count.txt") |> Result.with_default("none"))
}

// FILE: count.txt
1

// FILE: tools/bump.nomi
import {
    std/fs
    std/io
}

fn main() -> Result<Unit, String> {
    text = fs.read_text("count.txt") |> Result.map_err(fn e -> "${e}")
    n = String.to_int(String.trim(text?)) |> Maybe.with_default(0)
    _ = fs.write_text("count.txt", "${n + 1}\n") |> Result.map_err(fn e -> "${e}")
    io.print("bumped to ${n + 1}")
    Ok(Unit)
}

// FILE: nomi.toml
[module]
name = "demo"
entry_points = ["main", "tools/bump"]

// FILE: go.mod
module demo
`
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	written, err := vmhost.ToolMarked(src, t.TempDir(), "demo", "bump", &out)
	if err != nil {
		t.Fatalf("the tool failed: %v\n%s", err, out.String())
	}
	if out.String() != "bumped to 2\n" {
		t.Errorf("the tool printed %q", out.String())
	}
	if len(written) != 1 || written["count.txt"] != "2\n" {
		t.Fatalf("the tool wrote %q", written)
	}
	if now, _ := os.Getwd(); now != wd {
		t.Errorf("the working directory is %s after the tool, not %s", now, wd)
	}
	next := vmhost.ReplaceSections(src, written)
	want := "// FILE: count.txt\n2\n\n// FILE: tools/bump.nomi\n"
	if !bytes.Contains([]byte(next), []byte(want)) || bytes.Contains([]byte(next), []byte("count.txt\n1\n")) {
		t.Fatalf("the source after the tool:\n%s", next)
	}
	if _, err := vmhost.ToolMarked(src, t.TempDir(), "demo", "nope", &out); err == nil {
		t.Error("a tool the module does not have ran")
	}
	if _, err := vmhost.ToolMarked(src, t.TempDir(), "other", "bump", &out); err == nil {
		t.Error("a tool of a module the project does not require ran")
	}
}

// In a block of sibling projects, a tool of the project's dependency runs
// in the project's directory, as `nomi tool csvdata generate` in rates/
// does: it reads and writes rates/'s files, which come back by their paths
// from the block's root, and the block's test then passes.
func TestToolMarked_RunsInTheSiblingProjectsDirectory(t *testing.T) {
	src := siblingsBlock(t, func(s string) string { return strings.Replace(s, "9200", "9300", 1) })
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	written, err := vmhost.ToolMarked(src, t.TempDir(), "csvdata", "generate", &out)
	if err != nil {
		t.Fatalf("the tool failed: %v\n%s", err, out.String())
	}
	if out.String() != "wrote tables.nomi, tables_test.nomi\n" {
		t.Errorf("the tool printed %q", out.String())
	}
	var names []string
	for name := range written {
		names = append(names, name)
	}
	slices.Sort(names)
	if strings.Join(names, " ") != "rates/tables.nomi rates/tables_test.nomi" ||
		!strings.Contains(written["rates/tables.nomi"], "Some(9300)") {
		t.Fatalf("the tool wrote %q", written)
	}
	if now, _ := os.Getwd(); now != wd {
		t.Errorf("the working directory is %s after the tool, not %s", now, wd)
	}
	p, _, err := vmhost.LoadMarked(vmhost.ReplaceSections(src, written), t.TempDir())
	if err != nil {
		t.Fatalf("LoadMarked after the tool: %v", err)
	}
	if cases := p.Cases(&out, vmhost.TestOptions{}); len(cases) != 1 || cases[0].Err != nil {
		t.Fatalf("the digest test fails after the tool: %+v\n%s", cases, out.String())
	}
	if _, err := vmhost.ToolMarked(src, t.TempDir(), "rates", "generate", &out); err == nil {
		t.Error("the project ran a tool it does not have")
	}
}
