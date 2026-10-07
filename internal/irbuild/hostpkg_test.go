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
		{"an impl item", &ast.ExternFunc{Name: "f", ForeignName: "ffi.F", ImplFunction: true}},
		{"an inline go body", &ast.ExternFunc{Name: "f", ForeignName: "ffi.F", GoBody: "return 1"}},
	} {
		if hostBoundFn(bad.fn) {
			t.Errorf("%s was claimed as Go-bound", bad.what)
		}
	}
	// The POSITIVE case, so the four doors above are not passing for some
	// unrelated reason — the discipline stdcontextanchor_test.go states.
	if !hostBoundFn(&ast.ExternFunc{Name: "echo_upper", Public: true, ForeignName: "ffi.EchoUpper"}) {
		t.Fatal("the shape the corpus actually declares was rejected, so every door above is " +
			"vacuous")
	}
	if !hostBoundType(&ast.ExternType{Name: "RawBox", Opaque: true, ForeignName: "ffi.Box"}) {
		t.Fatal("the `opaque type X go pkg.Y` shape the corpus declares was rejected")
	}
}

// TestHostModuleOfSubPackageReplacesTheModuleRoot pins the one thing `dir` is
// for, in the one case the corpus cannot reach.
//
// `dir` has exactly one consumer: the `replace <modPath> => <dir>` renderGoMod
// writes into a generated artifact's go.mod. A `replace` names a module
// directory, so a `gopkg "mod/sub"` whose `dir` pointed at `<root>/sub` would
// produce an artifact Go refuses to load:
//
//	reading <root>/sub/go.mod: no such file or directory
//
// The corpus cannot see this: every `tests/18-ffi-and-dynamic/*_app/`
// fixture binds its module's root package (`gopkg "callbackffiapp"` inside
// `module callbackffiapp`), where the package directory and the module root
// are the same path, so the wrong value and the right one are
// indistinguishable.
//
// Both arms are asserted, so returning the root for the sub-package case by
// breaking the root-package case fails here.
func TestHostModuleOfSubPackageReplacesTheModuleRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module testproject\n\ngo 1.26.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nomiPath := filepath.Join(dir, "main.nomi")

	for _, tc := range []struct {
		name       string
		importPath string
	}{
		{"module root package", "testproject"},
		{"sub-package", "testproject/echobinding"},
		{"nested sub-package", "testproject/a/b/c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotDir, gotMod, why := hostModuleOf(nomiPath, tc.importPath)
			if why != "" {
				t.Fatalf("%s refused: %s", tc.importPath, why)
			}
			if gotMod != "testproject" {
				t.Errorf("module path: got %q, want %q", gotMod, "testproject")
			}
			if gotDir != dir {
				t.Errorf("replace directory: got %q, want the MODULE ROOT %q — a `replace` "+
					"pointing at a package subdirectory names a directory with no go.mod in it",
					gotDir, dir)
			}
		})
	}

	// The negative half, so the three assertions above are not passing because
	// hostModuleOf answers unconditionally.
	if _, _, why := hostModuleOf(nomiPath, "example.com/elsewhere"); why == "" {
		t.Fatal("a `gopkg` outside the project's own module was accepted, so the refusal this " +
			"resolution rule exists to produce is gone")
	}
}

// TestHostModuleOfARequiredModule: a `gopkg` naming a module the project's
// go.mod requires resolves, at its local `replace` directory when there is
// one. The FFI wrapper builds against that go.mod, so the VM can call it.
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
	gotDir, gotMod, why := hostModuleOf(nomiPath, "echobinding")
	if why != "" || gotMod != "echobinding" || gotDir != filepath.Join(dir, "echobinding") {
		t.Errorf("replaced module: dir %q, module %q, refusal %q", gotDir, gotMod, why)
	}
	gotDir, gotMod, why = hostModuleOf(nomiPath, "example.com/cached/sub")
	if why != "" || gotMod != "example.com/cached" || gotDir != "" {
		t.Errorf("required module: dir %q, module %q, refusal %q", gotDir, gotMod, why)
	}
}

// TestHostModuleOfTheStandardLibrary: a Go standard library package resolves
// with no go.mod above the declaring file, as the module "std" with no
// directory, and one beside a go.mod resolves the same way. A path that only
// looks like one (no dot in its first element) still needs a module.
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
			gotDir, gotMod, why := hostModuleOf(nomiPath, importPath)
			if why != "" || gotMod != "std" || gotDir != "" {
				t.Errorf("%s from %s: dir %q, module %q, refusal %q", importPath, root, gotDir, gotMod, why)
			}
		}
	}
	if _, _, why := hostModuleOf(filepath.Join(withMod, "main.nomi"), "nosuchstdpackage"); why == "" {
		t.Error("a dotless path the standard library does not hold was accepted")
	}
}
