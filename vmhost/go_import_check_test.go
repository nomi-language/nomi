package vmhost_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// TestCheck_AGopkgPathNoModuleProvidesIsRejected: `nomi check` on an entry
// file whose `gopkg` path no Go module of the project provides reports it
// at the path. The checker accepted these, and the IR builder then declined
// every body bound through the declaration ("go import: … outside the
// project's own Go module"); a fuzz run found `gopkg "callback:= f(s"`.
func TestCheck_AGopkgPathNoModuleProvidesIsRejected(t *testing.T) {
	for _, tc := range []struct {
		path, want string
	}{
		{"callback:= f(s", "main.nomi:1:8: gopkg \"callback:= f(s\": not a valid Go import path: invalid char ':'"},
		{"example.com/other", "main.nomi:1:8: gopkg \"example.com/other\": outside the project's own Go module app, the Go standard library, and every module its go.mod requires or replaces"},
		{"app/ffi", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module app\n\ngo 1.26.3\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(dir, "main.nomi")
			src := "gopkg \"" + tc.path + "\" as go_ffi\n\nfn main() {\n}\n"
			if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			err := vmhost.Check(entry)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("the front end rejects a path in the project's module: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("check admits `gopkg %q`", tc.path)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("diagnostic lacks %q:\n%s", tc.want, err)
			}
		})
	}
}
