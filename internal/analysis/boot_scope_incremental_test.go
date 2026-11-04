package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// An edit that makes a function boot-only rechecks the open file that calls
// it from outside boot: the language server propagates the change to the
// file's importers, and their boot-only check reads the program as it is now
// (analysis.CheckBootScope), not as it was when they were last checked.
func TestBootOnly_ACalleeThatBecomesBootOnlyRechecksItsCaller(t *testing.T) {
	main := `import lib

fn main() {
    _ = lib.make()
}
`
	plain := `pub fn make() -> Int {
    1
}
`
	bootOnly := `import std::supervisors.Supervisor

pub fn make() -> Int {
    _ = Supervisor.new(max_running: 1)
    1
}
`
	dir := t.TempDir()
	for name, text := range map[string]string{"main.nomi": main, "lib.nomi": plain} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mainURI := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	libURI := fileuri.FromPath(filepath.Join(dir, "lib.nomi"))
	dm := newManager()
	dm.Open(mainURI, main)
	dm.Open(libURI, plain)
	// As the server does once it has analyzed a document.
	dm.UpdateImportEdges(mainURI)
	dm.UpdateImportEdges(libURI)
	if errs := bootErrors(dm, mainURI); len(errs) != 0 {
		t.Fatalf("before the edit main reports %q", errs)
	}

	dm.Update(libURI, bootOnly)
	dm.PropagateChange(libURI)
	errs := bootErrors(dm, mainURI)
	want := "`make` creates a supervisor (`Supervisor.new` at lib.nomi:4), so it can only be called while `boot` builds the app value"
	if len(errs) != 1 || !strings.HasPrefix(errs[0], want) {
		t.Fatalf("after the edit main reports %q; want one error starting %q", errs, want)
	}

	dm.Update(libURI, plain)
	dm.PropagateChange(libURI)
	if errs := bootErrors(dm, mainURI); len(errs) != 0 {
		t.Fatalf("after the edit is undone main reports %q", errs)
	}
}

// bootErrors is the boot-only diagnostics uri's installed analysis reports.
func bootErrors(dm *analysis.DocumentManager, uri string) []string {
	var out []string
	for _, e := range analysis.InstalledForTest(dm, uri).TypeErrors {
		if strings.Contains(e.Message, "creates a supervisor") || strings.Contains(e.Message, "a supervisor here") {
			out = append(out, e.Message)
		}
	}
	return out
}
