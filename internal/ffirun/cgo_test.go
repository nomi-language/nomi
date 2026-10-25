package ffirun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Go binding builds with CGO_ENABLED=0, so a `gopkg` naming a package that
// imports "C", or that imports one that does (here, through a module in the
// module cache), is refused at the `gopkg`, naming the chain. A pure-Go
// package is not.
func TestGoImportProblem_APackageThatNeedsCgo(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "modcache")
	t.Setenv("GOMODCACHE", cache)
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.22\n\nrequire example.com/cdriver v1.0.0\n")
	write(filepath.Join(root, "ffi.nomi"), "")
	// Pure Go.
	write(filepath.Join(root, "plain", "plain.go"), "package plain\n\nimport \"strings\"\n\nfunc Up(s string) string { return strings.ToUpper(s) }\n")
	// Calls C itself.
	write(filepath.Join(root, "direct", "direct.go"), "package direct\n\n// int two() { return 2; }\nimport \"C\"\n\nfunc Two() int { return int(C.two()) }\n")
	// Imports a module-cache package that calls C.
	write(filepath.Join(root, "wrap", "wrap.go"), "package wrap\n\nimport \"example.com/cdriver/sqlite\"\n\nfunc Open() int { return sqlite.Open() }\n")
	dep := filepath.Join(cache, "example.com", "cdriver@v1.0.0")
	write(filepath.Join(dep, "go.mod"), "module example.com/cdriver\n\ngo 1.22\n")
	write(filepath.Join(dep, "sqlite", "open.go"), "package sqlite\n\nimport \"example.com/cdriver/internal/csql\"\n\nfunc Open() int { return csql.Open() }\n")
	write(filepath.Join(dep, "internal", "csql", "csql.go"), "package csql\n\n// int open_db() { return 1; }\nimport \"C\"\n\nfunc Open() int { return int(C.open_db()) }\n")
	write(filepath.Join(cache, "cache", "download", "example.com", "cdriver", "@v", "v1.0.0.ziphash"), "h1:x\n")

	nomiPath := filepath.Join(root, "ffi.nomi")
	if got := GoImportProblem(nomiPath, "example.com/app/plain"); got != "" {
		t.Errorf("a pure-Go package: %q", got)
	}
	if got, want := GoImportProblem(nomiPath, "example.com/app/direct"),
		"needs cgo: its files import \"C\", and Go bindings build with CGO_ENABLED=0"; got != want {
		t.Errorf("a package that imports C:\n got %q\nwant %q", got, want)
	}
	want := "needs cgo: it imports example.com/cdriver/internal/csql (example.com/app/wrap → example.com/cdriver/sqlite → " +
		"example.com/cdriver/internal/csql), whose files import \"C\", and Go bindings build with CGO_ENABLED=0"
	if got := GoImportProblem(nomiPath, "example.com/app/wrap"); got != want {
		t.Errorf("a package that reaches C through the module cache:\n got %q\nwant %q", got, want)
	}
	if got := GoImportProblem(nomiPath, "example.com/cdriver/sqlite"); !strings.Contains(got, "needs cgo") {
		t.Errorf("a module-cache package that reaches C: %q", got)
	}
}
