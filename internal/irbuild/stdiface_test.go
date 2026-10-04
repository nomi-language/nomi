package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// --- the differential fixtures ----------------------------------------------

// --- identity ----------------------------------------------------------------

func TestStdIface_ShadowingDeclarationIsNotTheSharedDef(t *testing.T) {
	g := stdIfaceGen(t, `import std/io

pub interface ToJson {
  fn to_string(value: self): String
}

struct Mine {
  n: Int
}

impl ToJson for Mine {
  fn to_string(m: Mine): String {
    "mine(${m.n})"
  }
}

fn show(d: ToJson): String {
  ToJson.to_string(d)
}

fn main() {
  io.print(show(Mine{n: 1}))
}
`)
	if _, anchored := g.stdIfaceNamed("ToJson"); anchored {
		t.Fatal("stdIfaceNamed anchored a name the module declares itself: the anchor is name-keyed")
	}
	for _, d := range stdIfaceDefs() {
		if d.nomi != "Display" {
			continue
		}
		if _, isStd := stdIfaceOf(d); !isStd {
			t.Fatal("stdIfaceOf does not recognise its own def")
		}
	}
}

// TestStdIface_SpecNamesAreUnshadowableBySource is the fence, pinned so that
// removing it fires here rather than in a wrong answer.
//
// Every name with a spec row is prelude-injected, and prelude interface names
// are reserved, so no source program can declare its own interface under a
// spec'd name. That is what makes `stdIfaceByName` — a NAME-keyed map —
// safe today: not that a collision resolves correctly, but that a collision
// cannot be written.
//
// If this test starts failing, the analyzer began accepting the shadow again
// and the requirement it was fencing comes back: `stdIfaceNamed` must return
// not-anchored for a name the module declares itself, and the `Display`
// spelling of testdata/std_iface_shadow.nomi is the fixture to restore. The
// real fix in that world is analysis.InterfaceType gaining an Origin, so a
// user's `Display` and std's can be told apart at all.
func TestStdIface_SpecNamesAreUnshadowableBySource(t *testing.T) {
	for _, d := range stdIfaceDefs() {
		name := d.nomi
		t.Run(name, func(t *testing.T) {
			src := "pub interface " + name + " {\n  fn probe(value: self): String\n}\n\nfn main() {\n  io.print(\"x\")\n}\n"
			if _, err := AnalyzeSource("stdiface_fence", src); err == nil {
				t.Fatalf("the analyzer accepted `pub interface %s`; stdIfaceByName is name-keyed, so a "+
					"declarable spec name is a reachable wrong answer rather than a coverage gap", name)
			}
		})
	}
}

// A module that reaches the name through something other than an import of the
// DECLARING std module gets no anchor. This is the whole identity check, driven
// directly rather than inferred from a fixture that happens to pass.
func TestStdIface_AnchorRequiresTheDeclaringModule(t *testing.T) {
	g := stdIfaceGen(t, stdIfaceProbeSource)
	sym := g.fa.ModuleScope.Lookup("Display")
	if sym == nil {
		t.Fatal("Display is not in the probe's module scope")
	}
	mod, imported := stdImportModuleOf(sym, g.fa.Origin)
	if !imported {
		t.Fatal("Display did not resolve through an import statement, so there is no declaring module to check")
	}
	if mod != "std/display" {
		t.Fatalf("Display was reached through %q, want std/display — the prelude re-export must report the ORIGIN module", mod)
	}
	// A symbol with no Resolved link is a LOCAL declaration and can never be
	// mistaken for an import, which is what keeps a user's own interface out.
	local := &analysis.Symbol{Name: "Display", Node: &ast.InterfaceDef{Name: "Display"}}
	if _, isImport := stdImportModuleOf(local, g.fa.Origin); isImport {
		t.Fatal("a local declaration was read as an import")
	}
	// And an import statement with no Resolved link is not one either: the
	// chain has to REACH a declaration, or there is nothing to validate.
	dangling := &analysis.Symbol{Name: "Display", Node: &ast.ImportStmt{ModulePath: []ast.Node{&ast.Ident{Name: "std"}}}}
	if _, isImport := stdImportModuleOf(dangling, g.fa.Origin); isImport {
		t.Fatal("an unresolved import was read as reaching a declaration")
	}
	// The IMPORTER decides the spelling. A stdlib file writes `import
	// display.Display` because the stdlib is one module, and that has to
	// report `std/display` — while the SAME bare path in a user file must
	// report `display`, because a user's own display.nomi anchors nothing.
	bare := &analysis.Symbol{
		Name:     "Display",
		Node:     &ast.ImportStmt{ModulePath: []ast.Node{&ast.Ident{Name: "display"}}},
		Resolved: &analysis.Symbol{Name: "Display", Node: &ast.InterfaceDef{Name: "Display"}},
	}
	if mod, _ := stdImportModuleOf(bare, "std/decimal"); mod != "std/display" {
		t.Errorf("a stdlib file's bare sibling import reported %q, want std/display", mod)
	}
	if mod, _ := stdImportModuleOf(bare, ""); mod != "display" {
		t.Errorf("a user file's bare import reported %q, want display — qualifying it would let a "+
			"user's own display.nomi anchor against std's Display", mod)
	}
}

// --- the spec table and its agreement with rt --------------------------------

func TestStdIfaceTableNamesResolve(t *testing.T) {
	wantGap := map[string]string{
		"Display.to_string":  "",
		"Comparable.compare": gapMultiSelf,
		// Empty, like Display's: one self position in argument zero, so the
		// erased-receiver route is available and nothing is refused.
		"Debug.inspect": "",
	}
	for i, d := range stdIfaceDefs() {
		s := &stdIfaceSpecs[i]
		for _, m := range d.order {
			if !m.hasTable() {
				t.Fatalf("%s.%s has no table: rtMethodName could not name its rt variable", s.nomi, m.name)
			}
			// `why` is derived by the SAME function a declared interface's
			// methods go through, so a spec row cannot claim a dispatch route
			// the builder refuses for an identically-shaped declaration. The
			// expectation is pinned per row rather than asserted uniformly,
			// because `Comparable.compare` genuinely has no erased-receiver
			// route and a blanket `dispatchable()` would have to be relaxed to
			// nothing to accommodate it.
			key := s.nomi + "." + m.name
			if got, want := m.why, wantGap[key]; got != want {
				t.Fatalf("%s erased-receiver gap is %q, want %q", key, got, want)
			}
		}
	}
	if rtMethodName(struct{}{}) != "" || rtMethodName(nil) != "" {
		t.Fatal("rtMethodName named something that is not an rt table")
	}
}

// The precondition for sharing one *ifaceDef across every gen, asserted rather
// than claimed. A kind interned in one package's tables renders as a Go name
// that is correct there and undefined everywhere else; a shared def may hold
// none.
func TestStdIfaceDefsArePackageNeutral(t *testing.T) {
	for _, d := range stdIfaceDefs() {
		if d.foreign != "" || d.pkg != "" || d.unit != -1 {
			t.Fatalf("%s carries a package: foreign=%q pkg=%q unit=%d", d.nomi, d.foreign, d.pkg, d.unit)
		}
		for _, m := range d.order {
			for i, k := range m.params {
				if m.shape.selfTyped(i) {
					if k != kindInvalid {
						t.Fatalf("%s.%s self position %d has kind %s, want none until an implementor supplies one",
							d.nomi, m.name, i, k.nomi())
					}
					continue
				}
				if !k.packageNeutral() {
					t.Fatalf("%s.%s parameter %d is %s, which is not package-neutral", d.nomi, m.name, i, k.nomi())
				}
			}
			if !m.result.packageNeutral() {
				t.Fatalf("%s.%s returns %s, which is not package-neutral", d.nomi, m.name, m.result.nomi())
			}
		}
	}
}

// A row for an interface nothing can name is scaffolding. stdenum.go's
// equivalent guard already earned its place by failing on a wrong
// unreachability claim.
func TestStdIfaceSpecsAreReachable(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	_, programs := corpusAnalysis(t)
	seen := map[string]bool{}
	for _, prog := range programs {
		p := prog.Prog
		if p == nil {
			continue
		}
		for i := range p.Modules {
			byDecl, _ := stdIfaceAnchors(p.Modules[i].FA)
			for decl := range byDecl {
				seen[decl.Name] = true
			}
		}
		if len(seen) == len(stdIfaceSpecs) {
			return
		}
	}
	for i := range stdIfaceSpecs {
		if !seen[stdIfaceSpecs[i].nomi] {
			t.Fatalf("no corpus program anchors %s: the row is a registry entry for an interface nothing reaches",
				stdIfaceSpecs[i].nomi)
		}
	}
}

// --- the shape check ---------------------------------------------------------

// --- the refusals that stay ---------------------------------------------------

// --- helpers ------------------------------------------------------------------

const stdIfaceProbeSource = `import std/io

fn show(d: Display): String {
  Display.to_string(d)
}

fn main() {
  io.print("${1}")
}
`

func stdIfaceGen(t *testing.T, src string) *gen {
	t.Helper()
	p, err := AnalyzeSource("stdiface_probe", src)
	if err != nil {
		t.Fatalf("analyzing the probe program: %v", err)
	}
	g := &gen{fa: p.Modules[0].FA, types: map[string]*typeDef{}, ifaces: map[string]*ifaceDef{}}
	// The file's OWN interface declarations, which is what `stdIfaceNamed` has
	// to prefer. Without this the probe would assert against a gen that has
	// not read the file it was built from.
	g.declareIfaces(p.Modules[0].Nodes)
	g.loadStdIfaces()
	return g
}
