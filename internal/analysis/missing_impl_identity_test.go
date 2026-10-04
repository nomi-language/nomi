package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// implIdentityErrs is the whole analyzer: build sweep, CheckTypes, then
// FinalizeCoherence. The missing-impl diagnostic exists only once a project
// impl index has been built and every recording has fired, so a test about
// what the analyzer catches statically has to run all three.
func implIdentityErrs(t *testing.T, src string) []string {
	t.Helper()
	fa, nodes := implIdentityFA(t, src)
	var msgs []string
	for _, e := range fa.TypeErrors {
		msgs = append(msgs, e.Message)
	}
	for _, e := range analysis.CheckTypes(fa, nodes) {
		msgs = append(msgs, e.Message)
	}
	for _, e := range analysis.FinalizeCoherence(fa) {
		msgs = append(msgs, e.Message)
	}
	return msgs
}

func implIdentityFA(t *testing.T, src string) (*analysis.FileAnalysis, []ast.Node) {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "main.nomi"), []byte(src), 0644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	loader := func(root string, mod []string) ([]ast.Node, error) {
		data, err := os.ReadFile(filepath.Join(root, filepath.Join(mod...)) + ".nomi")
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

func hasMissingImpl(msgs []string, iface, typeName string) bool {
	want := "no impl of `" + iface + "` for `" + typeName + "`"
	for _, m := range msgs {
		if strings.Contains(m, want) {
			return true
		}
	}
	return false
}

// TestMissingImpl_TheOnlyVariableIsTheTypesName is the instrument that found
// the defect, kept verbatim because it isolates the cause to one character
// class: the two programs are identical except for the name of the enum.
//
// `std/calendar.nomi` carries `derive Equatable for Error`. With `Impls` keyed by
// BARE type name, a user enum named `Error` borrowed that conformance and the
// program was ACCEPTED with no impl of its own. An enum named `Zonk` was
// correctly refused.
//
// Both rows must produce the diagnostic. A row that stops producing one is
// a regression whichever direction it moves in, so neither is written as
// "expect no error".
func TestMissingImpl_TheOnlyVariableIsTheTypesName(t *testing.T) {
	const tmpl = `import { std/io }

pub enum %[1]s {
  Bad String
}

fn mk(s: String): Result<Int, %[1]s> { Err(%[1]s.Bad(s)) }

fn main() {
  io.print("equal?: ${Equatable.equal?(mk("a"), mk("b"))}")
}
`
	for _, name := range []string{"Error", "Zonk"} {
		src := strings.ReplaceAll(tmpl, "%[1]s", name)
		msgs := implIdentityErrs(t, src)
		if !hasMissingImpl(msgs, "Equatable", name) {
			t.Errorf("enum %s with no Equatable impl was ACCEPTED; diagnostics: %v", name, msgs)
		}
	}
}

// TestMissingImpl_AnEntryImplIsNotMistakenForAnotherModules is the fail-safe
// direction, and it is the one a too-eager identity check breaks. The user's
// own `impl Equatable for Error` is a real conformance; rejecting it because
// std/calendar also declares an `Error` would turn a correct program into an
// error, which is strictly worse than the bug being fixed.
func TestMissingImpl_AnEntryImplIsNotMistakenForAnotherModules(t *testing.T) {
	const src = `import { std/io }

pub enum Error {
  Bad String
}

impl Equatable for Error {
  fn equal?(_a: Error, _b: Error): Bool { True }
}

fn main() {
  io.print("equal?: ${Equatable.equal?(Error.Bad("a"), Error.Bad("b"))}")
}
`
	for _, m := range implIdentityErrs(t, src) {
		if strings.Contains(m, "no impl of `Equatable`") {
			t.Fatalf("an entry-declared impl was read as another module's: %s", m)
		}
	}
}

// TestMissingImpl_StdlibsOwnDemandsAreNotBlamedOnTheUser pins the reason the
// check filters per RECORDING rather than judging the bucket.
//
// `ImplManifest["Equatable"]["Error"]` holds the demands the stdlib raised
// about its own `Error` types, which are supplied, and the one the user raised
// about theirs, which is not. A whole-bucket rule has no answer — the origins
// disagree — so it either accepts the borrow or reports the stdlib's own
// correct demands against the user. The diagnostic must name
// the user's position and nothing in std.
func TestMissingImpl_StdlibsOwnDemandsAreNotBlamedOnTheUser(t *testing.T) {
	const src = `import {
  std/io
  std/calendar.{DateTime}
}

pub enum Error {
  Bad String
}

fn mk(s: String): Result<Int, Error> { Err(Error.Bad(s)) }

fn main() {
  d = DateTime"2026-01-01T12:00:00[America/New_York]"
  io.print("x: ${d}")
  io.print("equal?: ${Equatable.equal?(mk("a"), mk("b"))}")
}
`
	msgs := implIdentityErrs(t, src)
	if !hasMissingImpl(msgs, "Equatable", "Error") {
		t.Fatalf("the user's un-implemented Error was accepted; diagnostics: %v", msgs)
	}
	for _, m := range msgs {
		if strings.Contains(m, "calendar.nomi") || strings.Contains(m, "random.nomi") {
			t.Errorf("a stdlib position was reported to the user: %s", m)
		}
	}
}

// TestImplIdentity_EveryKeyCarriesAnOrigin is the mutation net. Keying with
// `Origin: ""` — the lattice bottom — would make `implSuppliedByIdentity`
// answer "cannot judge" for everything and silently restore the old
// behaviour while every behavioural test above still passed, because those
// tests also pass under a build that never populated the index at all.
func TestImplIdentity_EveryKeyCarriesAnOrigin(t *testing.T) {
	fa, _ := implIdentityFA(t, "fn main() {}\n")
	idx := fa.ProjectImpls
	if idx == nil {
		t.Fatal("nil ProjectImpls")
	}
	if len(idx.ImplsByIdentity) == 0 {
		t.Fatal("ImplsByIdentity is EMPTY, so the identity check cannot ever reject")
	}
	for k := range idx.ImplsByIdentity {
		if k.Origin == analysis.OriginUnresolved {
			t.Errorf("key %+v carries no origin, which reads as the lattice bottom", k)
		}
		if k.Type == "" || k.Iface == "" {
			t.Errorf("key %+v is incomplete", k)
		}
	}
}

// TestImplIdentity_TheStdlibErrorsAreSeparateConformances is the defect
// stated as data rather than as behaviour: under a bare-name key the std
// types called `Error` share one `Equatable` slot, and under an identity key
// each is its own entry.
func TestImplIdentity_TheStdlibErrorsAreSeparateConformances(t *testing.T) {
	fa, _ := implIdentityFA(t, "fn main() {}\n")
	idx := fa.ProjectImpls
	if idx == nil {
		t.Fatal("nil ProjectImpls")
	}
	if !idx.Impls["Error"]["Equatable"] {
		t.Fatal("the bare-name table no longer claims Equatable for `Error`; " +
			"if the stdlib Errors dropped their derives this test is measuring nothing")
	}
	var with, without []string
	for _, origin := range []string{"std/calendar", "std/random"} {
		if idx.ImplsByIdentity[analysis.ImplIdentityKey{Origin: origin, Type: "Error", Iface: "Equatable"}] {
			with = append(with, origin)
		} else {
			without = append(without, origin)
		}
	}
	sort.Strings(with)
	// Both derive Equatable, because the diagnostic this check produces
	// names a stdlib type the user cannot add a `derive` to. The property
	// under test is that each one is its OWN entry, so the count is two and
	// not "one, shared".
	if len(with) != 2 {
		t.Errorf("Equatable for `Error` resolved per-declaration = %v, missing %v; want both as separate entries", with, without)
	}
}

// TestImplIdentity_TheFallbackPopulationIsClosed asserts the set of
// (type, iface) pairs that still fall back to the bare-name answer, EXACTLY.
//
// An upper bound would not do. A new name here is either a receiver whose
// origin genuinely cannot be established — a generic `host type` resolving to
// a shared PrimitiveType singleton, an inherent-block method with no
// ImplFiles entry — or a real type that stopped resolving, and only an exact
// assertion tells those apart. Growth in this set silently shrinks the check.
func TestImplIdentity_TheFallbackPopulationIsClosed(t *testing.T) {
	fa, _ := implIdentityFA(t, "fn main() {}\n")
	idx := fa.ProjectImpls
	if idx == nil {
		t.Fatal("nil ProjectImpls")
	}
	// Every (T, Iface) the bare table claims, that the identity index does
	// not cover.
	var fellBack []string
	for ty, ifaces := range idx.Impls {
		for iface := range ifaces {
			if !idx.ImplIdentityCovers(ty, iface) {
				fellBack = append(fellBack, ty+"/"+iface)
			}
		}
	}
	sort.Strings(fellBack)
	got := strings.Join(fellBack, " ")
	if got != implIdentityFallbackPopulation {
		t.Errorf("the fallback population moved.\n got: %s\nwant: %s", got, implIdentityFallbackPopulation)
	}
}

// implIdentityFallbackPopulation is every (T, Iface) pair the bare table
// claims and the identity index cannot judge, measured over the stdlib.
//
// All 35 are OPERATOR interfaces, and one mechanism explains the whole set:
// `ImplBlockInterfaceKey` spells an operator impl with its type arguments
// (`Add<Days, Date>`), while `Impls` and `ImplManifest` both key the bare
// name (`Add`), so the two spellings never meet and no operator conformance
// enters the covered set. `DispatchImplKey` (dispatch_key.go) is the existing
// translation between them.
//
// NOT widened here, deliberately. Every non-operator conformance — the
// Equatable / Display / Debug / Hashable / Comparable family, which is what
// the defect was about — is covered. Reaching the operator family means
// judging std/calendar's `Add`/`Subtract` ladder by identity, and that ladder
// is measured to be the one place the RUNTIME's qualified/bare tolerance is
// load-bearing: 13 of its keys ask qualified and are served bare, and a
// too-wide rule there costs 13 corpus files and 3 std tests. It is a named
// follow-on with a known mechanism, not an oversight.
const implIdentityFallbackPopulation = "Bytes/Add Date/Add Date/Subtract " +
	"DateTime/Add DateTime/Subtract Decimal/Add Decimal/Divide " +
	"Decimal/Multiply Decimal/Subtract Duration/Add Duration/Divide " +
	"Duration/Multiply Duration/Subtract Float/Add Float/Divide " +
	"Float/Multiply Float/Subtract Instant/Add Instant/Subtract Int/Add Int/Divide " +
	"Int/Multiply Int/Subtract List/Add Map/Add NaiveDateTime/Add " +
	"NaiveDateTime/Subtract OffsetDateTime/Add OffsetDateTime/Subtract " +
	"Set/Add Set/Subtract String/Add Time/Add Time/Subtract Vector/Add"
