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

// A file that declares a prelude name (`enum Ordering`) is rejected at the
// declaration, and the prelude's synthesized import keeps the module-scope
// slot. The name then has to mean ONE declaration everywhere in the file.
// It used to mean two: a type annotation resolved through the type registry,
// which the local declaration had written itself into, while `Ordering.Equal`
// resolved through the scope slot to std/comparable's enum. The program got
// `return type mismatch: expected main.Ordering, got std/comparable.Ordering`
// on top of the reserved-name error, and an impl-signature mismatch against
// Comparable besides. These tests pin the one lookup: whatever the scope slot
// holds is what a type annotation and an expression both resolve to.

// reservedDiagnosticFor returns the reserved-name diagnostic for name, and
// every other error. It also checks the rejection's wording, so a rejection
// for some other reason does not read as the reservation holding.
func reservedDiagnosticFor(t *testing.T, errs []analysis.TypeError, name string) (analysis.TypeError, []analysis.TypeError) {
	t.Helper()
	var reserved *analysis.TypeError
	var rest []analysis.TypeError
	for i, e := range errs {
		if strings.HasPrefix(e.Message, "type name '"+name+"' is reserved by the language and cannot be redeclared") {
			if reserved == nil {
				reserved = &errs[i]
			}
			continue
		}
		rest = append(rest, e)
	}
	if reserved == nil {
		t.Fatalf("the front end no longer rejects a declaration of prelude name %q, so what these tests assert about its follow-on errors is untested; errors: %v", name, errs)
	}
	return *reserved, rest
}

// withoutUnreadParams drops the unread-parameter warnings the fixtures'
// minimal bodies produce; they say nothing about name resolution.
func withoutUnreadParams(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if strings.Contains(e.Message, "is never read") {
			continue
		}
		out = append(out, e)
	}
	return out
}

func TestReservedName_OrderingRedeclaredHasNoFollowOnMismatch(t *testing.T) {
	src := `enum Ordering {
  Less
  Equal
  Greater
}

struct T {
  a: Int
}

impl Comparable for T {
  fn compare(_a: T, _b: T): Ordering {
    Ordering.Equal
  }
}
`
	errs := reservedNamesCheckAll(t, src)
	reserved, rest := reservedDiagnosticFor(t, errs, "Ordering")
	if reserved.Line != 1 || reserved.Col != 6 {
		t.Errorf("reserved-name diagnostic at %d:%d, want 1:6: %s", reserved.Line, reserved.Col, reserved.Message)
	}
	if rest = withoutUnreadParams(rest); len(rest) != 0 {
		t.Errorf("a redeclared prelude name produced follow-on errors from resolving the name two ways: %v", rest)
	}
}

// The declared return type and the `Ordering.Equal` in the body resolve to
// the same declaration, and it is the one the scope slot holds: std's.
func TestReservedName_TypeAndExpressionPositionsResolveToOneDeclaration(t *testing.T) {
	src := `enum Ordering {
  Less
  Equal
  Greater
}

fn f(): Ordering {
  Ordering.Equal
}
`
	fa, _ := reservedNamesFA(t, src)
	slot := fa.ModuleScope.Lookup("Ordering")
	if slot == nil {
		t.Fatal("no `Ordering` in the entry's scope")
	}
	for slot.Resolved != nil {
		slot = slot.Resolved
	}
	slotType, ok := slot.Type.(*analysis.EnumType)
	if !ok || slotType.Origin != "std/comparable" {
		t.Fatalf("`Ordering` in the entry's scope should be std/comparable's enum (the prelude keeps the slot), got %#v", slot.Type)
	}

	fsym := fa.ModuleScope.Lookup("f")
	ft, ok := fsym.Type.(*analysis.FuncType)
	if !ok {
		t.Fatalf("f has no function type: %#v", fsym.Type)
	}
	if !analysis.TypesEqual(ft.Return, slotType) {
		t.Errorf("the type annotation `Ordering` resolved to %s (origin %q), not the declaration the scope names",
			ft.Return, originOf(ft.Return))
	}

	// `Ordering.Equal` is on line 8; the variant reference sits at column 12.
	variant := fa.References[analysis.Pos{Line: 8, Col: 12}]
	if variant == nil {
		t.Fatal("no reference recorded for `Equal` in `Ordering.Equal`")
	}
	if !analysis.TypesEqual(variant.Type, slotType) {
		t.Errorf("`Ordering.Equal` resolved to %s (origin %q), the annotation to %s (origin %q)",
			variant.Type, originOf(variant.Type), ft.Return, originOf(ft.Return))
	}
}

// The local declaration's shape must not be written onto the prelude's
// type. Pass 2 of BuildTypes once looked the type up by NAME, which for a
// declaration that lost its slot is std's own type.
func TestReservedName_RedeclarationDoesNotReshapeStdType(t *testing.T) {
	src := `enum Maybe<T> {
  Some T
  None
  Extra
}
`
	fa, _ := reservedNamesFA(t, src)
	slot := fa.ModuleScope.Lookup("Maybe")
	for slot != nil && slot.Resolved != nil {
		slot = slot.Resolved
	}
	et, ok := slot.Type.(*analysis.EnumType)
	if !ok {
		t.Fatalf("`Maybe` in scope is not an enum: %#v", slot.Type)
	}
	var names []string
	for _, v := range et.Variants {
		names = append(names, v.Name)
	}
	if strings.Join(names, ",") != "Some,None" {
		t.Errorf("std's Maybe now has variants %v; a local redeclaration rewrote it", names)
	}
}

// The same holds for every reserved prelude type and for variant-qualified
// access: the program reports the reservation, and a use written the way the
// prelude type is written raises nothing more.
func TestReservedName_PreludeTypesResolveOneWay(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"Maybe", `enum Maybe<T> {
  Some T
  None
}

fn some(): Maybe<Int> {
  Maybe.Some(1)
}

fn none(): Maybe<Int> {
  Maybe.None
}
`},
		{"Result", `enum Result<T, E> {
  Ok T
  Err E
}

fn ok(): Result<Int, String> {
  Result.Ok(1)
}
`},
		{"List", `struct List {
  x: Int
}

fn f(xs: List<Int>): List<Int> {
  xs
}
`},
		{"Ordering", `struct Ordering {
  x: Int
}

fn f(): Ordering {
  Ordering.Less
}
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, rest := reservedDiagnosticFor(t, reservedNamesCheckAll(t, tc.src), tc.name)
			if rest = withoutUnreadParams(rest); len(rest) != 0 {
				t.Errorf("follow-on errors after the reserved-name diagnostic: %v", rest)
			}
		})
	}
}

// A declaration of a prelude name keeps no auto-synthesized Debug impl
// either: typed against the type the name does mean, it reported an arity
// error at `struct List` about a declaration the user never wrote.
func TestReservedName_DeclarationSiteHasOnlyTheReservation(t *testing.T) {
	errs := reservedNamesCheckAll(t, "struct List {\n  x: Int\n}\n")
	_, rest := reservedDiagnosticFor(t, errs, "List")
	if len(rest) != 0 {
		t.Errorf("a declaration of a prelude name reported more than the reservation: %v", rest)
	}
}

// Legitimate shadowing is untouched: a file may declare a type whose name
// another file also declares, and reach the other one through its file API
// object. The two names are two declarations, each named one way.
func TestReservedName_OrdinaryShadowingOfAnImportedFileStillWorks(t *testing.T) {
	shapes := `pub enum Level {
  Low
  High
}

pub fn low(): Level {
  Level.Low
}
`
	src := `import shapes

enum Level {
  Low
  Mid
  High
}

fn mine(): Level {
  Level.Mid
}

fn theirs(): shapes.Level {
  shapes.low()
}
`
	fa, errs := checkProjectWithFiles(t, src, map[string]string{"shapes.nomi": shapes})
	if len(errs) != 0 {
		t.Fatalf("shadowing an imported file's type name should analyze clean, got %v", errs)
	}
	mine := fa.ModuleScope.Lookup("mine").Type.(*analysis.FuncType).Return
	theirs := fa.ModuleScope.Lookup("theirs").Type.(*analysis.FuncType).Return
	if originOf(mine) != analysis.OriginEntry {
		t.Errorf("local `Level` resolved to origin %q, want the entry's", originOf(mine))
	}
	if originOf(theirs) != "shapes" {
		t.Errorf("`shapes.Level` resolved to origin %q, want shapes", originOf(theirs))
	}
}

// A selective import of a name the file also declares is still rejected, and
// with the redeclaration message rather than anything about resolution.
func TestReservedName_SelectiveImportCollisionStillRejected(t *testing.T) {
	shapes := "pub enum Level {\n  Low\n  High\n}\n"
	src := `import shapes.{Level}

enum Level {
  Low
  Mid
}

fn mine(): Level {
  Level.Mid
}
`
	_, errs := checkProjectWithFiles(t, src, map[string]string{"shapes.nomi": shapes})
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Message, "'Level' is already defined in this scope") {
			found = true
		} else {
			t.Errorf("unexpected error beside the redeclaration: %s", e.Message)
		}
	}
	if !found {
		t.Fatalf("the front end now ADMITS a selective import colliding with a local declaration; errors: %v", errs)
	}
}

func originOf(t analysis.Type) string {
	switch v := t.(type) {
	case *analysis.EnumType:
		return v.Origin
	case *analysis.StructType:
		return v.Origin
	case *analysis.DistinctType:
		return v.Origin
	}
	return "<not nominal>"
}

// checkProjectWithFiles runs the whole analyzer over an entry file plus
// sibling files in one project directory.
func checkProjectWithFiles(t *testing.T, src string, files map[string]string) (*analysis.FileAnalysis, []analysis.TypeError) {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "main.nomi"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
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
	errs := append([]analysis.TypeError(nil), fa.TypeErrors...)
	errs = append(errs, analysis.CheckTypes(fa, entryNodes)...)
	errs = append(errs, analysis.FinalizeCoherence(fa)...)
	return fa, withoutUnreadParams(errs)
}
