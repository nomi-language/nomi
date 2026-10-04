package analysis_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// A non-generic `host type` is identified by (declaring file, name), as a
// struct is. Each analysis builds its own object for the declaration, so two
// analyses of one file must still produce one type: a stdlib module checked
// afresh from source next to the shared stdlib, an LSP rebuild, a second
// vmhost load.

// hostTypeOf is the Type a module scope binds name to.
func hostTypeOf(t *testing.T, scope *analysis.Scope, name string) *analysis.PrimitiveType {
	t.Helper()
	if scope == nil {
		t.Fatalf("no scope to look %s up in", name)
	}
	sym := scope.Lookup(name)
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil {
		t.Fatalf("%s is not bound", name)
	}
	p, ok := sym.Type.(*analysis.PrimitiveType)
	if !ok {
		t.Fatalf("%s is a %T, want a *PrimitiveType", name, sym.Type)
	}
	return p
}

func assertOneType(t *testing.T, what string, a, b *analysis.PrimitiveType) {
	t.Helper()
	if a == b {
		t.Fatalf("%s: both analyses returned one object, so this does not test identity across analyses", what)
	}
	if !analysis.TypesEqual(a, b) || !analysis.TypesEqual(b, a) {
		t.Errorf("%s: TypesEqual(%s@%q, %s@%q) is false", what, a, a.Origin, b, b.Origin)
	}
	if err := analysis.Unify(a, b); err != nil {
		t.Errorf("%s: Unify: %v", what, err)
	}
	if !analysis.SameScopedType(a, b) {
		t.Errorf("%s: SameScopedType is false", what)
	}
}

func TestHostTypeIdentity_StdHostTypeAcrossTwoLoads(t *testing.T) {
	first, second := std.Load(), std.Load()
	for _, c := range []struct{ module, name string }{
		{"regex", "Regex"},
		{"context", "Context"},
		{"supervisors", "Supervisor"},
	} {
		a := hostTypeOf(t, first.Modules[c.module], c.name)
		b := hostTypeOf(t, second.Modules[c.module], c.name)
		if a.Origin != "std/"+c.module {
			t.Errorf("%s: Origin = %q, want %q", c.name, a.Origin, "std/"+c.module)
		}
		assertOneType(t, c.name, a, b)
	}
	// A built-in stays the one process-wide singleton, with no Origin.
	if p := hostTypeOf(t, first.Modules["bytes"], "Bytes"); p != analysis.TypeBytes || p.Origin != "" {
		t.Errorf("Bytes = %p (Origin %q), want the TypeBytes singleton", p, p.Origin)
	}
}

const handlesSrc = "pub host type Handle\n\npub host fn open(): Handle\n"

// buildHostProject runs BuildProject over main.nomi plus siblings and answers
// the per-file analyses and the entry's errors.
func buildHostProject(t *testing.T, entry string, siblings map[string]string) (map[string]*analysis.FileAnalysis, []analysis.TypeError) {
	t.Helper()
	tmp := t.TempDir()
	for name, content := range siblings {
		if err := os.WriteFile(filepath.Join(tmp, name+".nomi"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(entry))
	loader := func(root string, path []string) ([]ast.Node, error) {
		data, err := os.ReadFile(filepath.Join(root, filepath.Join(path...)) + ".nomi")
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		return nodes, nil
	}
	lib := std.Load()
	fa, cache, _ := analysis.BuildProjectWithCache(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("BuildProject returned no analysis")
	}
	errs := append([]analysis.TypeError{}, fa.TypeErrors...)
	errs = append(errs, analysis.CheckTypes(fa, entryNodes)...)
	return cache, errs
}

func TestHostTypeIdentity_UserHostTypeAcrossTwoFilesAndTwoBuilds(t *testing.T) {
	entry := "import handles.{Handle, open}\n\nfn keep(h: Handle): Handle {\n  h\n}\n\nfn main() {\n  h = keep(open())\n}\n"
	siblings := map[string]string{"handles": handlesSrc}
	builds := make([]*analysis.PrimitiveType, 2)
	for i := range builds {
		cache, errs := buildHostProject(t, entry, siblings)
		for _, e := range errs {
			if e.Code != analysis.UnusedBindingCode {
				t.Errorf("build %d: %s", i+1, e.Message)
			}
		}
		if cache["handles"] == nil {
			t.Fatalf("build %d analyzed no handles.nomi", i+1)
		}
		builds[i] = hostTypeOf(t, cache["handles"].ModuleScope, "Handle")
	}
	if builds[0].Origin != "handles" {
		t.Errorf("Handle's Origin = %q, want %q", builds[0].Origin, "handles")
	}
	assertOneType(t, "Handle", builds[0], builds[1])
}

// The mirror: two files each declaring `host type Handle` are two types.
func TestHostTypeIdentity_SameNameInTwoFilesIsTwoTypes(t *testing.T) {
	_, errs := buildHostProject(t,
		"import alpha\nimport beta\n\nfn take(_h: alpha.Handle): Unit {\n  Unit\n}\n\nfn main() {\n  take(beta.open())\n}\n",
		map[string]string{"alpha": handlesSrc, "beta": handlesSrc})
	assertErrorContains(t, errs, "argument 1: expected alpha.Handle, got beta.Handle")
}
