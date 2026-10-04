package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// TestCheckReservedTypeName_BroadenedSetFromPrelude pins the
// post-stdlib-as-package set of prelude-shadowed names that
// checkReservedTypeName rejects. The Task 7 cleanup retired the
// curated 22-name `reservedTypeNames` constant and rewired the check
// to "look up in parent (prelude) scope". The new set is strictly
// broader than the curated one — every name prelude re-exports
// participates, including enum variants and control-flow names.
//
// The test serves two purposes:
//
//   - As a visible inventory: future contributors editing
//     std/prelude.nomi see this table diff alongside their re-export
//     change, rather than having user files mysteriously start
//     erroring across the codebase.
//   - As a regression guard for the broadening itself: if someone
//     re-introduces a curated list (or any other mechanism that
//     narrows the check), the affected names below stop erroring
//     and the test fails.
//
// Naming style: each name is checked as both `struct X` and
// `enum X { ... }` (when applicable) — the diagnostic is keyed by
// the parent-scope Lookup, not by the kind, so both forms should
// fire identically. The kind verbiage in the error message is
// asserted separately.
func TestCheckReservedTypeName_BroadenedSetFromPrelude(t *testing.T) {
	// Names this test asserts should error when redeclared. Splitting
	// by category aids future maintainers in correlating with
	// std/prelude.nomi's import groups.
	cases := []struct {
		category string
		names    []string
	}{
		{
			category: "primitive types",
			names:    []string{"Int", "Float", "String", "Bool", "Unit", "Infallible"},
		},
		{
			category: "container types",
			names:    []string{"List", "Vector", "Map", "Set"},
		},
		{
			category: "sum types",
			names:    []string{"Maybe", "Result"},
		},
		{
			category: "iterator machinery",
			names:    []string{"Range"},
		},
		{
			category: "interfaces",
			names:    []string{"Display", "Debug", "Equatable", "Comparable", "Hashable", "Ordering"},
		},
		// Enum-variant re-exports. Pre-cutover these were NOT in
		// reservedTypeNames so declaring `struct Some` etc. silently
		// passed; post-cutover the parent-scope lookup catches them
		// because prelude re-exports them via drill-through.
		{
			category: "Maybe variants",
			names:    []string{"Some", "None"},
		},
		{
			category: "Result variants",
			names:    []string{"Ok", "Err"},
		},
		{
			category: "Bool variants",
			names:    []string{"True", "False"},
		},
		// Ordering's Equal/Greater/Less variants are NOT here: they are
		// not prelude-exported (only the `Ordering` type is), so user code
		// may freely declare types with those names — pinned by the
		// NonPreludeNamesAllowed test below. `@derive Comparable`'s
		// synthesized body names them qualified (`Ordering.Less`), so it
		// doesn't depend on the bare names either.
		// Control and the old variant names (Break, BreakWith, Continue,
		// Return, ReturnWith) are ordinary user names — the runtime's
		// break/continue/return callback signal is an internal Go type
		// with no Nomi declaration at all. User code is free to declare
		// types with those names — pinned by the NonPreludeNamesAllowed
		// test below.
		//
		// The whole std/literals cluster (Literal, Fragment, and the
		// Fragment Static/Dynamic variants) is NOT prelude-exported —
		// typed-literal tag modules import it explicitly, so none of those
		// names are reserved.
	}

	for _, group := range cases {
		group := group
		for _, name := range group.names {
			name := name
			t.Run(group.category+"/"+name, func(t *testing.T) {
				// Use struct as the declaration kind. The check is kind-
				// agnostic, so struct alone is sufficient — testing every
				// (kind, name) combination would multiply the test count
				// without adding signal.
				src := "pub struct " + name + " { x: Int }\n\nfn main() { Unit }\n"
				if errs := buildAndCollectErrs(t, src); !containsErrAbout(errs, name) {
					t.Errorf("expected reserved-name diagnostic for `struct %s`, got: %v", name, errs)
				}
			})
		}
	}
}

// TestCheckReservedTypeName_NonPreludeNamesAllowed sanity-checks the
// other direction: names NOT in the prelude should be free to
// declare. Without this, a future regression that mass-reserves
// every stdlib name (rather than just prelude re-exports) would go
// undetected.
//
// `Date` is the canonical non-prelude stdlib name (the same one
// the orphan_violator_nonprelude fixture uses to prove the
// Stage 2 coverage gap closed). User files MUST be able to
// declare their own `struct Date` — otherwise the entire stdlib's
// non-prelude surface becomes off-limits to users, which is
// neither documented nor intended.
func TestCheckReservedTypeName_NonPreludeNamesAllowed(t *testing.T) {
	for _, name := range []string{"Date", "Duration", "App", "Error", "io.Error", "MyType", "Static", "Dynamic", "Less", "Greater", "Equal", "Control", "Break", "BreakWith", "Continue", "Return", "ReturnWith", "Task"} {
		name := name
		t.Run(name, func(t *testing.T) {
			src := "pub struct " + name + " { x: Int }\n\nfn main() { Unit }\n"
			errs := buildAndCollectErrs(t, src)
			for _, e := range errs {
				if strings.Contains(e, "reserved") && strings.Contains(e, name) {
					t.Errorf("unexpected reserved-name diagnostic for `struct %s` (not in prelude): %s", name, e)
				}
			}
		})
	}
}

// TestCheckReservedTypeName_PreludeInterfaceNames covers the declaring kind
// the check never reached. checkReservedTypeName was wired into
// defineStructStub / defineEnumStub / defineTypeDefStub /
// defineTypeAliasStub and NOT defineInterfaceStub, so every prelude-injected
// interface name was freely redeclarable while the four sibling kinds were
// not — a skipped node kind, invisible from every angle except the one it
// skipped.
//
// The listed names are the measured prelude-injected interfaces, taken from
// the entry file's own module scope rather than from a curated constant. If
// std/prelude.nomi stops re-exporting one, its subtest stops failing and
// TestCheckReservedTypeName_PreludeInterfaceSetIsExactlyWhatPreludeInjects
// below reports the drift.
func TestCheckReservedTypeName_PreludeInterfaceNames(t *testing.T) {
	for _, name := range preludeInterfaceNames {
		name := name
		t.Run(name, func(t *testing.T) {
			src := "pub interface " + name + " {\n  fn probe(value: self): String\n}\n"
			if errs := buildAndCollectErrs(t, src); !containsErrAbout(errs, name) {
				t.Errorf("expected reserved-name diagnostic for `interface %s`, got: %v", name, errs)
			}
		})
	}
}

// preludeInterfaceNames is the set TestCheckReservedTypeName_PreludeInterfaceNames
// asserts over, spelled out so a prelude change shows up as a diff here rather
// than as a silently smaller test.
var preludeInterfaceNames = []string{
	"Add", "Comparable", "Debug", "Discrete", "Display", "Divide",
	"Equatable", "Hashable", "Iter", "Multiply", "Steppable", "Struct",
	"Subtract",
}

// TestCheckReservedTypeName_PreludeInterfaceSetIsExactlyWhatPreludeInjects
// keeps the list above honest against the prelude itself. Derived rather than
// curated: the previous curated `reservedTypeNames` constant was lossy by
// accident, and a list that drifts silently would make the interface check
// look complete while leaving a name redeclarable.
func TestCheckReservedTypeName_PreludeInterfaceSetIsExactlyWhatPreludeInjects(t *testing.T) {
	fa, _ := reservedNamesFA(t, "struct Anchor {\n  x: Int\n}\n")
	injected := map[string]bool{}
	for name, sym := range fa.ModuleScope.Symbols {
		if sym.Kind == analysis.SymbolInterface && name != "Anchor" {
			injected[name] = true
		}
	}
	asserted := map[string]bool{}
	for _, name := range preludeInterfaceNames {
		asserted[name] = true
		if !injected[name] {
			t.Errorf("preludeInterfaceNames lists %q but the prelude no longer injects it", name)
		}
	}
	for name := range injected {
		if !asserted[name] {
			t.Errorf("the prelude injects interface %q but preludeInterfaceNames does not assert it — add it, or the name is redeclarable and nothing notices", name)
		}
	}
}

// TestCheckReservedTypeName_NonPreludeInterfacesAllowed is the other
// direction, and it is what makes the interface reservation a line rather
// than a blanket. A stdlib interface the user must IMPORT is a name the user
// chose, so shadowing it stays legal; only the prelude's unasked injection is
// reserved. All eight names are real `pub interface` declarations under std/.
func TestCheckReservedTypeName_NonPreludeInterfacesAllowed(t *testing.T) {
	for _, name := range []string{"App", "Assertable", "DateParts", "TimeParts", "Anchored", "ToJson", "FromJson", "Literal"} {
		name := name
		t.Run(name, func(t *testing.T) {
			src := "pub interface " + name + " {\n  fn probe(value: self): String\n}\n"
			for _, e := range buildAndCollectErrs(t, src) {
				if strings.Contains(e, "reserved") && strings.Contains(e, name) {
					t.Errorf("unexpected reserved-name diagnostic for `interface %s` (stdlib but not prelude-injected): %s", name, e)
				}
			}
		})
	}
}

// TestCheckReservedTypeName_InterfaceRemedyIsFollowable pins the wording,
// because the data-kind advice cannot be followed for an interface:
// `type MyDisplay Display` does not wrap a contract. A diagnostic whose only
// suggestion is impossible is worse than a terse one.
func TestCheckReservedTypeName_InterfaceRemedyIsFollowable(t *testing.T) {
	errs := buildAndCollectErrs(t, "pub interface Display {\n  fn probe(value: self): String\n}\n")
	var msg string
	for _, e := range errs {
		if strings.Contains(e, "reserved") && strings.Contains(e, "'Display'") {
			msg = e
		}
	}
	if msg == "" {
		t.Fatalf("no reserved-name diagnostic for `interface Display`: %v", errs)
	}
	if strings.Contains(msg, "distinct type") {
		t.Errorf("interface diagnostic offers the distinct-type remedy, which cannot wrap a contract: %q", msg)
	}
	if !strings.Contains(msg, "as an interface") {
		t.Errorf("interface diagnostic should read \"as an interface\": %q", msg)
	}
	if !strings.Contains(msg, "MyDisplay") {
		t.Errorf("interface diagnostic should suggest a concrete alternative name: %q", msg)
	}
}

// TestReservedInterfaceName_ClosesAStaticToRuntimeSoundnessGap is the reason
// the check was wired in, stated as the consequence rather than as the
// omission.
//
// Before the fix this program analyzed CLEAN: the user's own `Display`
// declares `render`, `impl Display for Point` provides `render`, and
// `where T: Display` then passed the static bound check — because interface
// identity is the bare NAME, so the impl registered against std's `Display`.
// The program died at runtime with
// `Display.to_string: no implementation for type 'Point'`. The control below
// shows what the language does when the shadow is absent: it rejects the
// bound at ANALYSIS time. Turning a static type error into a runtime crash is
// the gap; the declaration-site diagnostic is what closes it.
//
// Delete the checkReservedTypeName call in defineInterfaceStub and this test
// fails while the control keeps passing, which is the shape that says the
// reservation is load-bearing rather than cosmetic.
func TestReservedInterfaceName_ClosesAStaticToRuntimeSoundnessGap(t *testing.T) {
	witness := `pub interface Display {
  fn render(value: self): String
}

struct Point {
  x: Int
}

impl Display for Point {
  fn render(_p: Point): String {
    "pt"
  }
}

fn show<T>(x: T): String where T: Display {
  Display.to_string(x)
}
`
	if errs := buildAndCollectErrs(t, witness); !containsErrAbout(errs, "Display") {
		t.Errorf("the shadowing witness analyzed without a reserved-name diagnostic; "+
			"`where T: Display` then passes statically for a Point that has no to_string, "+
			"and the program crashes at runtime instead. Errors were: %v", errs)
	}

	// The control runs the FULL pipeline, because the bound diagnostic comes
	// from CheckTypes rather than from the build sweep — BuildProjectWithCache
	// alone reports nothing here, which is part of why the gap was invisible.
	// The `show(...)` call matters: the bound is checked at the instantiation
	// site, so a generic function nobody calls is never checked against it.
	control := `struct Point {
  x: Int
}

fn show<T>(x: T): String where T: Display {
  Display.to_string(x)
}

fn main() {
  _s = show(Point { x: 1 })
}
`
	errs := reservedNamesCheckAll(t, control)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "Point") && strings.Contains(e.Message, "Display") {
			found = true
		}
	}
	if !found {
		t.Errorf("control lost its static bound diagnostic — without it the witness above "+
			"proves nothing, because there would be no static error to convert. Errors were: %v", errs)
	}
}

// reservedNamesFA is buildAndCollectErrs's sibling for the cases that need the
// FileAnalysis itself rather than its messages. Returns the parsed entry nodes
// too, so callers can run the checker passes over the same build.
func reservedNamesFA(t *testing.T, src string) (*analysis.FileAnalysis, []ast.Node) {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "main.nomi"), []byte(src), 0644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		data, err := os.ReadFile(filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi")
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		return nodes, nil
	}
	lib := std.Load()
	fa, _, _ := analysis.BuildProjectWithCache(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	return fa, entryNodes
}

// reservedNamesCheckAll is the whole analyzer: build sweep, then CheckTypes,
// then FinalizeCoherence. The interface-bound and missing-impl diagnostics
// only exist once a project impl index has been built, so a test about what
// the analyzer catches statically has to run all three.
func reservedNamesCheckAll(t *testing.T, src string) []analysis.TypeError {
	t.Helper()
	fa, entryNodes := reservedNamesFA(t, src)
	errs := append([]analysis.TypeError(nil), fa.TypeErrors...)
	errs = append(errs, analysis.CheckTypes(fa, entryNodes)...)
	return append(errs, analysis.FinalizeCoherence(fa)...)
}

// buildAndCollectErrs runs the source through BuildProjectWithCache
// (which wires the auto-prepended prelude) and returns the entry
// FA's TypeError messages. helper centralises the disk-loader setup
// so each table-driven case stays a one-liner.
func buildAndCollectErrs(t *testing.T, src string) []string {
	t.Helper()
	tmp := t.TempDir()
	mainPath := filepath.Join(tmp, "main.nomi")
	if err := os.WriteFile(mainPath, []byte(src), 0644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		return nodes, nil
	}
	// std.Load() provides the `primitives` scope that becomes the
	// non-stdlib file's parent — checkReservedTypeName's parent-
	// scope lookup is what the test exercises, so this must be
	// populated (passing nil short-circuits the check).
	lib := std.Load()
	fa, _, _ := analysis.BuildProjectWithCache(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	var msgs []string
	for _, e := range fa.TypeErrors {
		msgs = append(msgs, e.Message)
	}
	return msgs
}

// containsErrAbout reports whether any message both names `name`
// and references the "reserved" keyword. Distinguishes the
// reserved-name diagnostic from other type errors that might also
// mention the same name (e.g. orphan/duplicate-impl messages).
func containsErrAbout(msgs []string, name string) bool {
	for _, m := range msgs {
		if strings.Contains(m, "reserved") && strings.Contains(m, "'"+name+"'") {
			return true
		}
	}
	return false
}
