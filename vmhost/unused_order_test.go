package vmhost_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// Unused-binding diagnostics come in source order. They were collected from a
// map, so their order changed from run to run; ten runs make a pass by luck
// improbable.
func TestUnusedBindings_ReportInSourceOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := "fn main() {\n  a = 1\n  b = 2\n  c = 3\n  d = 4\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		err := vmhost.Check(path)
		var ds vmhost.Diagnostics
		if !errors.As(err, &ds) || len(ds) != 4 {
			t.Fatalf("want four unused-binding diagnostics, got: %v", err)
		}
		for i, d := range ds {
			if d.Line != i+2 {
				t.Fatalf("diagnostics out of source order:\n%s", err)
			}
		}
	}
}
