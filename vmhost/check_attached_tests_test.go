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
	const used = "import helper.four\n\n/// Doubles.\n//! assert double(2) == four()\n//\npub fn double(x: Int) -> Int\n    x * 2\nend\n"
	const unused = "import helper.{four, five}\n\n/// Doubles.\n//! assert double(2) == four()\n//\npub fn double(x: Int) -> Int\n    x * 2\nend\n"
	for _, tc := range []struct {
		name, main, wantErr string
	}{
		{name: "used by an attached test", main: used},
		{name: "used by nothing", main: unused, wantErr: "imported name 'five' is unused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range map[string]string{
				"helper.nomi": "pub fn four() -> Int\n    4\nend\n\npub fn five() -> Int\n    5\nend\n",
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
// (TestStdlib_AnalyzesWithoutErrors). The two must agree: a Check that strips
// `//!` tests reads an import only the tests use as unused.
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

// Every file of std's test-only `_fixtures` tree checks clean on its own, as
// `nomi check std` checks it, in a process whose stdlib does not hold the
// fixtures. A fixture is its std module (`std/_fixtures/conn`), so a bare
// sibling import is std's: read as a user file, `import iter.Iter` loaded
// std/iter.nomi again under the key `iter`, and `String.join` refused the
// fixture's `Iter` as another type.
func TestCheck_EveryStdFixtureChecksClean(t *testing.T) {
	var files []string
	err := filepath.WalkDir(filepath.Join("..", "std", "_fixtures"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".nomi") {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sawConn := false
	for _, f := range files {
		sawConn = sawConn || filepath.Base(f) == "conn.nomi"
		if err := vmhost.Check(f); err != nil {
			t.Errorf("%s: %v", filepath.ToSlash(f), err)
		}
	}
	if !sawConn {
		t.Fatalf("std/_fixtures/conn.nomi is not among the %d fixtures: %v", len(files), files)
	}
}
