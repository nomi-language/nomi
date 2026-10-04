package rt_test

import (
	"os/exec"
	"strings"
	"testing"
)

// rtImportPath is this package's import path; every package under it is part
// of the runtime library.
const rtImportPath = "github.com/nomi-language/nomi/rt"

// allowedRuntimeModules are the third-party modules the runtime may link. A
// dependency here is a dependency of everything that links the runtime,
// including a VM runner, so adding one is a decision, not a bump.
//
// github.com/rivo/uniseg is there because `String.length`, `String.slice` and
// `String.reverse` are grapheme-cluster operations per UAX #29, which is
// table-driven Unicode work with no answer over `unicode/utf8`. See
// grapheme.go.
var allowedRuntimeModules = []string{
	"github.com/rivo/uniseg",
}

// TestRuntimeImportsOnlyItsAllowlist holds every non-test package under rt/ to
// the standard library, the modules in allowedRuntimeModules, and rt's own
// packages, across the whole transitive dependency set. rt shares a module
// with the compiler, so nothing but this test stops it from importing the
// front end or picking up a new dependency.
func TestRuntimeImportsOnlyItsAllowlist(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps",
		"-f", "{{.ImportPath}}\t{{.Standard}}\t{{join .Imports \" \"}}",
		rtImportPath+"/...")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s/...: %v\n%s", rtImportPath, err, out)
	}
	type pkg struct {
		standard bool
		imports  []string
	}
	pkgs := map[string]pkg{}
	var order []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			t.Fatalf("unexpected go list line %q", line)
		}
		pkgs[fields[0]] = pkg{standard: fields[1] == "true", imports: strings.Fields(fields[2])}
		order = append(order, fields[0])
	}
	if _, ok := pkgs[rtImportPath]; !ok {
		t.Fatalf("go list did not report %s; this test is checking nothing", rtImportPath)
	}
	for _, path := range order {
		p := pkgs[path]
		// Only edges leaving an allowed package are reported: a forbidden
		// package's own imports would bury the edge that pulled it in.
		if p.standard || !allowedInRuntime(path) {
			continue
		}
		for _, imp := range p.imports {
			if pkgs[imp].standard || allowedInRuntime(imp) {
				continue
			}
			t.Errorf("%s imports %s, which the runtime library may not link.\n"+
				"rt may import only the standard library, %s, and packages under %s. "+
				"Move what it needs down into rt rather than widening this list.",
				path, imp, strings.Join(allowedRuntimeModules, ", "), rtImportPath)
		}
	}
}

func allowedInRuntime(path string) bool {
	for _, prefix := range append([]string{rtImportPath}, allowedRuntimeModules...) {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}
