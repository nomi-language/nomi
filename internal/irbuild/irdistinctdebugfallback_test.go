package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An opaque type's universal Debug is name-only. `dbg` over one calls its
// synthesized body, so the VM prints `<opaque Secret>` and never the payload
// a structural rendering would show.
func TestIRDistinctDebug_OpaqueDefaultIsNameOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `opaque type Secret Int

fn main() {
  _s = dbg Secret(7)
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	obs := vmReference(path)
	if obs.exit != 0 {
		t.Fatalf("exit %d, stderr:\n%s", obs.exit, obs.stderr)
	}
	all := obs.stdout + obs.stderr
	if !strings.Contains(all, "Secret(7) = <opaque Secret>") {
		t.Fatalf("dbg of an opaque value did not render name-only:\n%s", all)
	}
	if strings.Contains(all, "= Secret(7)") {
		t.Fatalf("dbg of an opaque value rendered its payload:\n%s", all)
	}
}
