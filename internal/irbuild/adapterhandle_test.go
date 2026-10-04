package irbuild

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// The co-located adapter path, lowered.
//
// std/regex is a stdlib module whose implementation is Go. Its facade declares
// a bare `pub host type Regex` and bare `host fn`s, and the adapter's
// FFI-shaped signatures are projected onto rt's shapes.
//
// # The two halves
//
//   - The HANDLE TYPE is `rt.Regex`, an `rt.Dynamic`-shaped one-field struct
//     over `any`. rt/regexhandle.go argues the placement: a FUNCTION is called
//     from one place that can import what it names, while a VALUE is stored in
//     generated packages that never call the adapter, and rt is what every
//     generated package can name.
//   - The IMPLEMENTATION is `nomi/stdregex`'s FFI-shaped functions, which
//     internal/stdlibbindings binds and the generated adapters call.

// TestAdapterWall_TheCoLocatedAdapterLowersEndToEnd checks that every
// declaration in std/regex is lowerable: the seven host externs and the three
// Nomi-bodied impl methods over the handle. The Nomi bodies matter because the
// index checks a declaration's signature before anything else, so a handle
// type the builder cannot represent refuses bodies that have nothing
// host-shaped in them.
func TestAdapterWall_TheCoLocatedAdapterLowersEndToEnd(t *testing.T) {
	idx := stdlibLowering()
	// Named individually rather than by prefix scan, so a declaration DELETED
	// from std/regex fails here instead of shrinking the assertion to nothing.
	for _, name := range []string{
		// The `go`-bound externs.
		"regex.Regex.compile",
		"regex.Regex.pattern",
		"regex.Regex.match?",
		"regex.Regex.find",
		"regex.Regex.find_all",
		"regex.Regex.replace_all",
		"regex.Regex.split",
		// The Nomi bodies over the handle.
		"regex.Regex.from_fragments",
		"regex.Regex.to_string",
		"regex.Regex.inspect",
	} {
		f, known := idx.byKey[name]
		if !known {
			t.Errorf("%s is not in the stdlib index at all; std/regex does not declare it "+
				"and this row is measuring nothing", name)
			continue
		}
		if !f.lowerable() {
			t.Errorf("%s refuses %q. The adapter path regressed: check whether the "+
				"stdHostSpecs `std/regex` row still anchors and whether nomi/stdregex "+
				"still satisfies hostFnFor", name, f.why)
		}
	}
	// The paired negative, so this test cannot pass by the index having become
	// permissive: random.Generator.step is refused, for reasons that are not
	// the adapter's.
	if f, known := idx.byKey["random.Generator.step"]; !known || f.lowerable() {
		t.Fatal("random.Generator.step now lowers, so `lowerable()` is not the " +
			"discriminator this test claims. Repoint the control")
	}
}

// TestAdapterWall_HandleRowsRequireAGoBindingBothWays is the shape check the
// `handle` field buys, asserted in BOTH directions because each direction is a
// different wrong anchor.
//
// A `handle` row over an UNBOUND `pub host type` would claim a type an
// embedder's host table supplies. A
// non-handle row over a BOUND one would send a Go-backed handle down the
// rt-implemented path. Neither is caught by anything else: `declaredHostType`'s
// other clauses are all satisfied by both shapes.
func TestAdapterWall_HandleRowsRequireAGoBindingBothWays(t *testing.T) {
	handles := 0
	for i := range stdHostSpecs {
		if stdHostSpecs[i].handle {
			handles++
		}
	}
	if handles == 0 {
		t.Fatal("no row sets `handle`, so the check below is vacuous")
	}
	// The FIRING, per row, against the declaration std actually carries.
	for _, tc := range []struct {
		nomi       string
		entry      string
		wantAnchor bool
	}{
		// The handle row, anchored through the file that imports it.
		{"Regex", "17-typed-literals/typed_literals/typed_literals_test.nomi", true},
		// A NON-handle row in the same table, anchored through a file that
		// reaches it, so the loop is not passing because only one row exists.
		{"Dynamic", "18-ffi-and-dynamic/json_test.nomi", true},
	} {
		root, err := filepath.Abs(filepath.Join("..", "..", "tests"))
		if err != nil {
			t.Fatal(err)
		}
		p, err := Analyze(filepath.Join(root, tc.entry))
		if err != nil {
			t.Fatalf("%s: %v", tc.entry, err)
		}
		anchored := false
		for i := range p.Modules {
			if _, ok := stdHostAnchors(p.Modules[i].FA)[tc.nomi]; ok {
				anchored = true
			}
		}
		if anchored != tc.wantAnchor {
			t.Errorf("%s anchored=%v through %s, want %v", tc.nomi, anchored, tc.entry, tc.wantAnchor)
		}
	}
	// The counterfactual. Every std declaration is a bare `host type`, so both
	// settings of the row's `handle` bit match std's own declaration; the bit's
	// correctness is checked against the bindings type table in
	// TestStdHostHandleRowsMatchTheBindingsTypeTable.
	//
	// What declaredHostType decides here is that a declaration carrying a
	// `go` binding must not anchor, whatever
	// the row says, because such a declaration is discovered as a co-located
	// adapter and reached through the FFI projection instead. Both settings of
	// the bit are exercised, so this cannot pass by the row happening to be a
	// handle.
	var row stdHostSpec
	for i := range stdHostSpecs {
		if stdHostSpecs[i].handle {
			row = stdHostSpecs[i]
			break
		}
	}
	decl := regexDecl(t)
	if !row.declaredHostType(decl) {
		t.Fatal("the handle row does not match std/regex's own declaration")
	}
	bound := *decl
	bound.ForeignAlias, bound.ForeignName = "go_regex", "Regex"
	for _, handle := range []bool{true, false} {
		row.handle = handle
		if row.declaredHostType(&bound) {
			t.Errorf("a row with handle=%v matches a `go`-bound declaration. Nothing under "+
				"`std/` may name a Go symbol: such a declaration is discovered as a "+
				"co-located adapter, which puts every program importing the module "+
				"behind a Go toolchain.", handle)
		}
	}
}

// regexDecl is std/regex's own `pub host type Regex` node.
func regexDecl(t *testing.T) *ast.ExternType {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "tests"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(filepath.Join(root, "17-typed-literals/typed_literals/typed_literals_test.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range p.Modules {
		fa := p.Modules[i].FA
		if fa == nil || fa.ModuleScope == nil {
			continue
		}
		sym := fa.ModuleScope.Lookup("Regex")
		if sym == nil {
			continue
		}
		if sym.Resolved != nil {
			sym = sym.Resolved
		}
		if et, ok := sym.Node.(*ast.ExternType); ok {
			return et
		}
	}
	t.Fatal("std/regex's Regex declaration is not reachable as an *ast.ExternType; " +
		"stdHostSpecs' origin channel cannot anchor it")
	return nil
}

// TestAdapterWall_EveryHostPackageIsArtifactSafe checks that no host package
// outside the two compiler ones links the front end.
//
// There are six `hostPackages` rows. Two of them reach the front end BY DESIGN — their
// implementation IS the compiler — and the rest must not, because the whole
// argument for putting an implementation outside rt is that the cost falls on
// the artifacts that call it. A co-located adapter's shim reaching
// `nomi/analysis` through some helper would be silent: no cycle, no failing
// build, and every artifact that compiles one regex grows by a front end.
func TestAdapterWall_EveryHostPackageIsArtifactSafe(t *testing.T) {
	// The two that legitimately reach the front end, by path, with the reason.
	byDesign := map[string]string{
		compilerHostPath:    "its implementation IS the analyzer",
		compilerRunHostPath: "its implementation IS the front end and the VM",
	}
	forbidden := []string{"github.com/nomi-language/nomi/internal/analysis", "github.com/nomi-language/nomi/internal/parser", "github.com/nomi-language/nomi/internal/lsp"}
	checked := 0
	for _, hp := range hostPackages {
		if hp.path == rtModulePath {
			// Already guarded, more strictly, by rt's own
			// TestRuntimeImportsOnlyItsAllowlist.
			continue
		}
		if _, ok := byDesign[hp.path]; ok {
			continue
		}
		checked++
		deps := goListDeps(t, hp.path)
		if len(deps) == 0 {
			t.Fatalf("%s has no dependencies at all, so this test asserts nothing", hp.path)
		}
		for _, bad := range forbidden {
			if deps[bad] {
				t.Errorf("host package %s transitively links %s. Every artifact that calls "+
					"into it would link the front end; move the offending helper rather "+
					"than relaxing this test.", hp.path, bad)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no host package was checked, so the loop above is vacuous")
	}
	// The PAIRED POSITIVE: the two excluded rows really do reach what the
	// exclusion claims, so `byDesign` is describing the tree rather than hiding
	// a clean package behind an excuse.
	for path, why := range byDesign {
		if !goListDeps(t, path)["github.com/nomi-language/nomi/internal/analysis"] {
			t.Errorf("%s does NOT reach nomi/analysis, so its exclusion (%q) is stale", path, why)
		}
	}
}

// goListDeps is the transitive import set of one package, as `go list` sees it.
func goListDeps(t *testing.T, pkg string) map[string]bool {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	cmd.Dir = repoNomiLangDir(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", pkg, err, out)
	}
	deps := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			deps[line] = true
		}
	}
	return deps
}

func repoNomiLangDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
