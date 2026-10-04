package vmhost_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// Check analyzes a file's `//!` tests, as run and test do: an import only an
// attached test uses is used, and one nothing uses is still reported.
func TestCheck_AttachedTestUsesCountForImports(t *testing.T) {
	const used = "import helper.four\n\n/// Doubles.\n//! assert double(2) == four()\n//\npub fn double(x: Int): Int {\n    x * 2\n}\n"
	const unused = "import helper.{four, five}\n\n/// Doubles.\n//! assert double(2) == four()\n//\npub fn double(x: Int): Int {\n    x * 2\n}\n"
	for _, tc := range []struct {
		name, main, wantErr string
	}{
		{name: "used by an attached test", main: used},
		{name: "used by nothing", main: unused, wantErr: "imported name 'five' is unused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range map[string]string{
				"helper.nomi": "pub fn four(): Int {\n    4\n}\n\npub fn five(): Int {\n    5\n}\n",
				"main.nomi":   tc.main,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err := vmhost.Check(filepath.Join(dir, "main.nomi"))
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Check: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("Check: %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

// Every stdlib source file checks clean on its own, as std.Load analyzes it
// (TestStdlib_AnalyzesWithoutErrors). The two used to disagree: Check stripped
// `//!` tests, so an import only the tests used read as unused.
func TestCheck_EveryStdlibFileChecksClean(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "std", "*.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no stdlib files found")
	}
	for _, f := range files {
		if err := vmhost.Check(f); err != nil {
			t.Errorf("%s: %v", filepath.Base(f), err)
		}
	}
}
