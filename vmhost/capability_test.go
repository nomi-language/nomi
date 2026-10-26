package vmhost_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

const filesNotes = `import {
    std/fs
    std/io
}

fn save(text: String) -> Result<Unit, io.Error> {
    fs.write_text("notes.txt", text)
}

fn main() -> Result<Unit, io.Error> {
    text = try fs.read_text("notes.txt")
    try save(text + "!")
    io.print("saved")
    Ok(Unit)
}
`

// A build for a target lacking a class the program reaches is refused with
// each crossing the program reaches, the functions that reach it, and how
// to build anyway. No target lacks `files`, so the refusal is asked for it
// directly; it is what a target lacking `processes` will answer.
func TestRefuse_NamesEachCallSite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.nomi")
	if err := os.WriteFile(path, []byte(filesNotes), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Uses(vmhost.ClassFiles, false) {
		t.Fatal("the program reaches std/fs, and Uses says it does not")
	}
	if refused := p.CheckTarget("nomi build --target js/wasm", "js", "wasm", false, nil); refused != nil {
		t.Fatalf("js/wasm has files, and the build is refused:\n%s", refused.Error())
	}
	refused := p.Refuse("nomi build --target x/y", "x/y", []string{vmhost.ClassFiles}, false)
	if refused == nil {
		t.Fatal("a target lacking files does not refuse a program that reads files")
	}
	var report strings.Builder
	refused.Write(&report)
	got := report.String()
	for _, want := range []string{
		"nomi build --target x/y: this program reads or writes files, which a x/y module cannot do:\n",
		"`fs_host.read_text` (reached from fn main through read_text)\n",
		"`fs_host.write` (reached from fn main through save, write_text)\n",
		"hint: build for your platform without --target, or pass --allow-unsupported files\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report lacks %q:\n%s", want, got)
		}
	}
	if p.Refuse("nomi build", "x/y", nil, false) != nil {
		t.Error("a target that lacks nothing refuses")
	}
}
