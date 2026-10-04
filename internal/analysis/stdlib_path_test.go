package analysis_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// TestStdlibPath verifies the fallback chain — env-var override wins,
// runtime.Caller fallback finds the bundled std/ in the repo layout, and
// the function returns a non-empty absolute path that points at a real
// directory containing prelude.nomi.
func TestStdlibPath(t *testing.T) {
	t.Run("env-var override", func(t *testing.T) {
		tmp := t.TempDir()
		// Drop a marker file so we can confirm the returned path is the
		// one we set, not a fallback.
		_ = os.WriteFile(filepath.Join(tmp, "prelude.nomi"), []byte("// marker"), 0644)
		t.Setenv("NOMI_STD_PATH", tmp)

		got, err := analysis.StdlibPath()
		if err != nil {
			t.Fatalf("StdlibPath() error = %v", err)
		}
		if got != tmp {
			t.Fatalf("StdlibPath() = %q, want %q (env override should win)", got, tmp)
		}
	})

	t.Run("fallback finds bundled stdlib", func(t *testing.T) {
		t.Setenv("NOMI_STD_PATH", "")

		got, err := analysis.StdlibPath()
		if err != nil {
			t.Fatalf("StdlibPath() error = %v", err)
		}
		if got == "" {
			t.Fatal("StdlibPath() returned empty path with no error")
		}
		// Bundled stdlib must contain prelude.nomi.
		if _, err := os.Stat(filepath.Join(got, "prelude.nomi")); err != nil {
			t.Fatalf("StdlibPath() = %q but prelude.nomi missing: %v", got, err)
		}
	})

	t.Run("env-var to nonexistent dir falls through to bundled", func(t *testing.T) {
		t.Setenv("NOMI_STD_PATH", "/does/not/exist/at/all")
		got, err := analysis.StdlibPath()
		if err != nil {
			t.Fatalf("StdlibPath() error = %v", err)
		}
		if got == "/does/not/exist/at/all" {
			t.Fatal("StdlibPath() returned the nonexistent env-var path; should have fallen through")
		}
	})
}
