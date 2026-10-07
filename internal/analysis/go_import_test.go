package analysis_test

import (
	"strings"
	"testing"
)

// A `gopkg` path no Go module of the project provides is a check error at
// the path. Each rejected declaration below passed the checker, and the IR
// builder then declined every body bound through it ("go import: … outside
// the project's own Go module"). The declarations are in a sibling file,
// which the project build gives its path; vmhost's
// TestCheck_AGopkgPathNoModuleProvidesIsRejected covers the entry file.
func TestGoImport_APathNoModuleProvidesIsRejected(t *testing.T) {
	for _, tc := range []struct {
		path, want string
	}{
		{"callback:= f(s", "gopkg \"callback:= f(s\": not a valid Go import path: invalid char ':'"},
		{"example.com/other", "gopkg \"example.com/other\": outside the project's own Go module app, the Go standard library, and every module its go.mod requires or replaces\n" +
			"help: add it with `go get example.com/other`, or add a require or replace for its module to go.mod"},
		{"appx", "gopkg \"appx\": outside the project's own Go module app"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			_, errs := opaqueProject(t, goImportProject(tc.path))
			for _, e := range errs {
				if strings.Contains(diagText(e), tc.want) {
					if e.Line != 1 || e.Col != 8 {
						t.Errorf("the error is at %d:%d, want the path at 1:8", e.Line, e.Col)
					}
					return
				}
			}
			t.Fatalf("want an error containing %q, got %v", tc.want, errs)
		})
	}
}

// The mirror: the project's own module, a package in it, a required
// module's package and a Go standard library package stay accepted.
func TestGoImport_TheProjectsModulesAreAccepted(t *testing.T) {
	for _, path := range []string{"app", "app/ffi", "example.com/dep/sub", "strings"} {
		if _, errs := opaqueProject(t, goImportProject(path)); len(errs) != 0 {
			t.Errorf("%s: want accepted, got %v", path, errs)
		}
	}
}

// A source with no file has no go.mod to read, so only the path itself is
// checked.
func TestGoImport_ASourceWithNoFileChecksThePathOnly(t *testing.T) {
	_, errs := checkSourceWithStdlib("gopkg \"callback:= f(s\" as ffi\n\nfn main() {\n}\n")
	expectStdlibError(t, errs, "gopkg \"callback:= f(s\": not a valid Go import path: invalid char ':'")
	_, errs = checkSourceWithStdlib("gopkg \"example.com/other\" as ffi\n\nfn main() {\n}\n")
	expectNoStdlibErrors(t, errs)
}

// goImportProject is a project whose sibling file `ffi.nomi` declares
// `gopkg "<path>"`, in a Go module `app` that requires example.com/dep.
func goImportProject(path string) map[string]string {
	return map[string]string{
		"go.mod":    "module app\n\ngo 1.26.3\n\nrequire example.com/dep v1.0.0\n",
		"ffi.nomi":  "gopkg \"" + path + "\" as go_ffi\n\npub fn one(): Int {\n  1\n}\n",
		"main.nomi": "import ffi\n\nfn main() {\n  _ = ffi.one()\n}\n",
	}
}

// A `go alias.Symbol` binding whose alias no `gopkg` of the file declares is
// a check error at the alias. The checker accepted it, and the IR builder
// then declined the declaration ("no `gopkg` declares ffs"), which `nomi
// run` reported as a gap in Nomi.
func TestGoImport_AnUndeclaredAliasIsRejected(t *testing.T) {
	const src = "gopkg \"strings\" as ffi\n\n" +
		"pub fn up(s: String): String go ffs.ToUpper\n\n" +
		"fn main() {\n  _ = up(\"a\")\n}\n"
	_, errs := checkSourceWithStdlib(src)
	const want = "`go ffs.ToUpper`: no `gopkg` in this file declares the alias ffs\n" +
		"help: declare the package with `gopkg \"<import path>\" as ffs`"
	for _, e := range errs {
		if strings.Contains(diagText(e), want) {
			if e.Line != 3 || e.Col != 33 {
				t.Errorf("the error is at %d:%d, want the alias at 3:33", e.Line, e.Col)
			}
			return
		}
	}
	t.Fatalf("want an error containing %q, got %v", want, errs)
}

// The mirror: the alias the file's `gopkg` declares is accepted.
func TestGoImport_ADeclaredAliasIsAccepted(t *testing.T) {
	const src = "gopkg \"strings\" as ffi\n\n" +
		"pub fn up(s: String): String go ffi.ToUpper\n\n" +
		"fn main() {\n  _ = up(\"a\")\n}\n"
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}
