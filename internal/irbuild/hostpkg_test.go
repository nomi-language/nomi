package irbuild

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// TestHostPkg_ABareHostDeclarationKeepsItsRefusal is the reachability witness
// for the OTHER half, and the half that decides whether this family collides
// with stdhost.go's.
//
// A `host fn` or `host type` with NO `go alias.Symbol` binding names no Go
// symbol. Its implementation has to come from an embedder's host table, so
// this family does not bind it, and this is what keeps `hostBoundFn` /
// `hostBoundType` from being a name-keyed test that std's hundreds of `host fn`
// declarations could satisfy.
//
// Analysis is not asked to accept this file: `Analyze` passes
// WithSourceBoundExternsProvided, which exempts SOURCE-BOUND declarations only,
// so a bare `host fn` still reports unmet and the file is a front-end error.
// That is the documented narrowness of the option — "a bare `host fn` with no
// `go` binding is a typo and STILL reports unmet" — so the witness is the
// front-end refusal, and the builder's arm below is asserted directly on the
// predicate instead.
func TestHostPkg_ABareHostDeclarationKeepsItsRefusal(t *testing.T) {
	if hostBoundFn(&ast.ExternFunc{Name: "echo", Public: true}) {
		t.Error("a `host fn` with no ForeignName was claimed as Go-bound; every std `host fn` " +
			"has that shape, so the family would adopt the whole standard library")
	}
	if hostBoundType(&ast.ExternType{Name: "Bytes", Public: true}) {
		t.Error("a `host type` with no ForeignName was claimed as Go-bound; std/bytes declares " +
			"exactly that shape and stdhost.go owns its representation")
	}
	// And the shapes this family declines even WITH a binding, each because
	// something else in the package owns it or nothing does.
	for _, bad := range []struct {
		what string
		fn   *ast.ExternFunc
	}{
		{"a generic host fn", &ast.ExternFunc{Name: "f", ForeignName: "ffi.F",
			TypeParams: []ast.TypeParam{{Name: "T"}}}},
		{"a where-bounded host fn", &ast.ExternFunc{Name: "f", ForeignName: "ffi.F",
			WhereClauses: []ast.WhereConstraint{{Name: "T"}}}},
	} {
		if hostBoundFn(bad.fn) {
			t.Errorf("%s was claimed as Go-bound", bad.what)
		}
	}
	// The POSITIVE case, so the three doors above are not passing for some
	// unrelated reason — the discipline stdcontextanchor_test.go states.
	if !hostBoundFn(&ast.ExternFunc{Name: "echo_upper", Public: true, ForeignName: "ffi.EchoUpper"}) {
		t.Fatal("the shape the corpus actually declares was rejected, so every door above is " +
			"vacuous")
	}
	if !hostBoundType(&ast.ExternType{Name: "RawBox", Opaque: true, ForeignName: "ffi.Box"}) {
		t.Fatal("the `opaque type X go pkg.Y` shape the corpus declares was rejected")
	}
}

// TestHostModuleOfTheProjectsOwnModule: the project's module provides its
// root package and every sub-package, and a path outside it and outside what
// its go.mod requires is refused.
func TestHostModuleOfTheProjectsOwnModule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module testproject\n\ngo 1.26.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nomiPath := filepath.Join(dir, "main.nomi")

	for _, importPath := range []string{"testproject", "testproject/echobinding", "testproject/a/b/c"} {
		if why := hostModuleOf(nomiPath, importPath); why != "" {
			t.Errorf("%s refused: %s", importPath, why)
		}
	}

	// The negative half, so the assertions above are not passing because
	// hostModuleOf answers unconditionally.
	if why := hostModuleOf(nomiPath, "example.com/elsewhere"); why == "" {
		t.Fatal("a `gopkg` outside the project's own module was accepted, so the refusal this " +
			"resolution rule exists to produce is gone")
	}
}

// TestHostModuleOfARequiredModule: a `gopkg` naming a module the project's
// go.mod requires resolves, with a local `replace` or without one. The FFI
// wrapper builds against that go.mod, so the VM can call it.
func TestHostModuleOfARequiredModule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(`module testproject

go 1.26.3

require echobinding v0.0.0

require example.com/cached v1.2.3

replace echobinding => ./echobinding
`), 0o644); err != nil {
		t.Fatal(err)
	}
	nomiPath := filepath.Join(dir, "main.nomi")
	for _, importPath := range []string{"echobinding", "example.com/cached/sub"} {
		if why := hostModuleOf(nomiPath, importPath); why != "" {
			t.Errorf("%s refused: %s", importPath, why)
		}
	}
}

// TestHostModuleOfTheStandardLibrary: a Go standard library package resolves
// with no go.mod above the declaring file, and one beside a go.mod resolves
// the same way. A path that only looks like one (no dot in its first
// element) still needs a module.
func TestHostModuleOfTheStandardLibrary(t *testing.T) {
	bare := t.TempDir()
	withMod := t.TempDir()
	if err := os.WriteFile(filepath.Join(withMod, "go.mod"),
		[]byte("module testproject\n\ngo 1.26.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{bare, withMod} {
		nomiPath := filepath.Join(root, "main.nomi")
		for _, importPath := range []string{"strings", "math", "net/http"} {
			if why := hostModuleOf(nomiPath, importPath); why != "" {
				t.Errorf("%s from %s: refusal %q", importPath, root, why)
			}
		}
	}
	if why := hostModuleOf(filepath.Join(withMod, "main.nomi"), "nosuchstdpackage"); why == "" {
		t.Error("a dotless path the standard library does not hold was accepted")
	}
}
