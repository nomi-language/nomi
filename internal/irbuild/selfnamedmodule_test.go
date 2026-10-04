package irbuild

import (
	"strings"
	"testing"
)

// A file named after its own module (testdata/self_named_module, module
// "thing", file thing.nomi) is reached by `import thing`, by an alias of it,
// and by `import thing/thing`. The bare name used to be read as the module
// itself with nothing left to load, so the import bound no file and every
// call through it declined.
func TestSelfNamedModule_ImportByModuleNameReachesTheFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "42\n" +
		"42\n" +
		"42\n" +
		"hello, ada\n" +
		"hi, ada\n"
	got := vmReference(fixture("self_named_module/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}

// The same import from a test file, which is the shape a library's own test
// file has (cmd/nomi/testdata/ffi_modules/sqlite/sqlite_test.nomi).
func TestSelfNamedModule_TestFileImportByModuleName(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	got := vmReference(fixture("self_named_module/thing_test.nomi"))
	if got.exit != 0 || !strings.Contains(got.stdout, "1 passed, 0 failed") {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s", got.exit, got.stdout, got.stderr)
	}
}
