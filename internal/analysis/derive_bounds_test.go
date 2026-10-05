package analysis_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// Tests for derive's field-type requirements (spec §38.1): field/payload/inner type bound
// checking at the `@derive Iface` site. These need stdlib loaded so the
// check can see Int/String/Bool/etc.'s impls; without stdlib the check is
// a no-op (it can't tell missing-from-table from genuinely-unimplemented).
//
// Lives in package analysis_test (separate from derive_synthesis_test.go,
// which is white-box and can't import stdlib without a circular dep).

// stdlibLoaded ensures std.Load() runs once across all tests in this
// file — every call re-parses and re-analyzes every stdlib module,
// and the rebuild is wasted work when the produced lib is identical.
var stdlibLoaded sync.Once
var stdlibLib *std.StdLib

func loadStdlibOnce() *std.StdLib {
	stdlibLoaded.Do(func() {
		stdlibLib = std.Load()
	})
	return stdlibLib
}

// buildWithStdlibForDerive parses and analyses src with stdlib loaded,
// returning the FileAnalysis. CheckDeriveBounds (called from
// BuildFileWithStdlib's buildModule) reads fa.ProjectImpls to
// discover which stdlib primitives implement which interfaces — the
// single-file path leaves that pointer nil, so the helper calls
// AttachStdlibProjectImpls explicitly before any check fires. The
// stdlib bound check via b.file's @derive sites runs inside
// BuildFileWithStdlib, so the attach must precede the build call.
//
// FinalizeCoherence is invoked at the tail so missing-impl gaps that
// CheckDeriveBounds defers to DetectMissingImpls (the derive-gap
// recordings shipped with the missing-impl-diagnostic followup) reach
// fa.TypeErrors here too — the helper mirrors what internal/frontend's
// Checker.Analyze / DocumentManager.analyze do for the production paths.
func buildWithStdlibForDerive(src string) *analysis.FileAnalysis {
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	// Lower type-body `derive Iface` entries into synthetic
	// `@derive Iface` decorators on the owning declaration —
	// CheckDeriveBounds keys off those decorators, so without lowering it
	// would never see a body-declared derive at all.
	nodes, _ = analysis.LowerDerives(nodes)
	lib := loadStdlibOnce()
	// First build with no stdlib ProjectImpls (CheckDeriveBounds
	// skips), then attach + re-run BuildTypes so the type-builder
	// errors that depend on stdlib conformances coexist with derive-
	// bound errors. derive The-bound errors that need ProjectImpls
	// are produced by a separate CheckDeriveBounds pass below.
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	// Re-run CheckDeriveBounds now that ProjectImpls is attached, so
	// derive-bound errors that depend on knowing stdlib impl coverage
	// (the common case) land on fa.TypeErrors. Idempotent re-walk —
	// duplicate diagnostics from synth-band positions would be a
	// regression, but this codepath is unit-test-only.
	fa.TypeErrors = append(fa.TypeErrors, analysis.CheckDeriveBounds(fa, nodes)...)
	analysis.BuildTypes(fa, nodes)
	// CheckTypes populates fa.ImplManifest with call-site / generic-
	// bound recordings; FinalizeCoherence then consumes the manifest
	// to surface missing-impl diagnostics — including derive-gap cases
	// CheckDeriveBounds intentionally defers when ProjectImpls is set.
	fa.TypeErrors = append(fa.TypeErrors, analysis.CheckTypes(fa, nodes)...)
	fa.TypeErrors = append(fa.TypeErrors, analysis.FinalizeCoherence(fa)...)
	return fa
}

// hasErrSubstring reports whether any error in errs contains substr.
func hasErrSubstring(errs []analysis.TypeError, substr string) bool {
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			return true
		}
	}
	return false
}

// errMsgs joins all error messages into a single newline-separated string
// for use in assertion failure output.
func errMsgs(errs []analysis.TypeError) string {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n  ")
}

// TestDeriveEquatableOnFieldWithoutImplErrors pins spec §38.1's field-type requirement: a
// `@derive Equatable struct Wrapper { id: User }` where `User` has no
// Equatable impl errors at the @derive site, naming the field and the
// missing interface. Without this check the failure surfaces deep in
// runtime dispatch with an unhelpful synth-band position.
//
// CheckDeriveBounds records the derive-gap into ImplManifest and
// defers the diagnostic to DetectMissingImpls (via FinalizeCoherence)
// — the richer message names the interface, the receiver type, and
// the offending field via DeriveCtx provenance.
func TestDeriveEquatableOnFieldWithoutImplErrors(t *testing.T) {
	src := `struct User { id: Int }

struct Wrapper {
  id: User
}
derive Equatable for Wrapper`

	fa := buildWithStdlibForDerive(src)
	if !hasErrSubstring(fa.TypeErrors, "no impl of `Equatable` for `User`") {
		t.Fatalf("expected missing-impl error for Equatable/User, got:\n  %s", errMsgs(fa.TypeErrors))
	}
	if !hasErrSubstring(fa.TypeErrors, "@derive(Equatable) on Wrapper field id") {
		t.Errorf("error must carry DeriveCtx provenance (Wrapper field id), got:\n  %s", errMsgs(fa.TypeErrors))
	}
}

// TestDeriveOnEnumPayloadWithoutImplErrors pins the enum-payload variant:
// `@derive Equatable enum E { V(NoImpl) }` reports the missing impl on
// the variant payload's type. The DeriveCtx FieldName carries the
// variant name so the user knows which variant to fix.
func TestDeriveOnEnumPayloadWithoutImplErrors(t *testing.T) {
	src := `struct NoImpl { x: Int }

enum E {
  V NoImpl
  Empty
}
derive Equatable for E`

	fa := buildWithStdlibForDerive(src)
	if !hasErrSubstring(fa.TypeErrors, "no impl of `Equatable` for `NoImpl`") {
		t.Fatalf("expected missing-impl error for Equatable/NoImpl, got:\n  %s", errMsgs(fa.TypeErrors))
	}
	// DeriveCtx FieldName for enum-positional payloads is the variant
	// name (see checkDeriveComponentTypes EnumDef branch).
	if !hasErrSubstring(fa.TypeErrors, "@derive(Equatable) on E field V") {
		t.Errorf("error must carry DeriveCtx provenance (E variant V), got:\n  %s", errMsgs(fa.TypeErrors))
	}
}

// TestDeriveOnDistinctInnerWithoutImplErrors pins the distinct-type
// case: `@derive Equatable type W NoImpl` where NoImpl has no
// Equatable impl errors at the @derive site, naming the inner type
// via DeriveCtx provenance on the missing-impl diagnostic.
func TestDeriveOnDistinctInnerWithoutImplErrors(t *testing.T) {
	src := `struct NoImpl { x: Int }

type W NoImpl
derive Equatable for W`

	fa := buildWithStdlibForDerive(src)
	if !hasErrSubstring(fa.TypeErrors, "no impl of `Equatable` for `NoImpl`") {
		t.Fatalf("expected missing-impl error for Equatable/NoImpl, got:\n  %s", errMsgs(fa.TypeErrors))
	}
	// DeriveCtx FieldName for distinct-type inner is the literal
	// "inner" sentinel set by checkDeriveComponentTypes (TypeDef branch).
	if !hasErrSubstring(fa.TypeErrors, "@derive(Equatable) on W field inner") {
		t.Errorf("error must carry DeriveCtx provenance (W field inner), got:\n  %s", errMsgs(fa.TypeErrors))
	}
}

// TestDeriveOnStructWithImplFieldClean pins the positive case: a struct
// whose fields are primitive-typed (Int, String) — both of which ship
// stdlib impls — produces no @derive bound error.
func TestDeriveOnStructWithImplFieldClean(t *testing.T) {
	src := `struct Point {
  x: Int
  y: Int
}
derive Equatable for Point
derive Hashable for Point
derive Comparable for Point
derive Debug for Point`

	fa := buildWithStdlibForDerive(src)
	for _, e := range fa.TypeErrors {
		if strings.Contains(e.Message, "@derive") {
			t.Errorf("unexpected @derive error: %s", e.Error())
		}
	}
}

// TestDeriveOnGenericFieldClean pins how the field-type check and the
// implicit generic bound interact: a generic field type (`value: T`) is
// recognized as a type-parameter reference and skipped — the implicit
// `T: Equatable` bound on the
// synthesized fn handles the requirement at the call site, so the
// derive-bound check at the decl site must not flag it.
func TestDeriveOnGenericFieldClean(t *testing.T) {
	src := `struct Box<T> {
  value: T
}
derive Equatable for Box`

	fa := buildWithStdlibForDerive(src)
	for _, e := range fa.TypeErrors {
		if strings.Contains(e.Message, "@derive") {
			t.Errorf("unexpected @derive error on generic Box<T>: %s", e.Error())
		}
	}
}

// TestDeriveRecursiveTypeIsClean pins the self-recursive type case:
// `struct List { head: Int; tail: List }` with `@derive Equatable` must
// not flag the `tail` field (whose type is List itself). The synthesizer
// runs first and registers Impls[List][Equatable]; by the time the
// bound-check runs in defineTopLevel's wake, that entry exists, so the
// recursive reference passes the impl lookup naturally.
func TestDeriveRecursiveTypeIsClean(t *testing.T) {
	src := `struct Cell {
  value: Int
  next: Cell
}
derive Equatable for Cell`

	fa := buildWithStdlibForDerive(src)
	for _, e := range fa.TypeErrors {
		if strings.Contains(e.Message, "@derive") {
			t.Errorf("unexpected @derive error on self-recursive Cell: %s", e.Error())
		}
	}
}

// TestDeriveMutuallyRecursiveTypesAreClean pins the mutual-recursion
// case: A.b: B and B.a: A with both deriving Equatable. Each type's
// impl entry is registered during defineTopLevel's FuncDef pass for the
// synthesized FuncDefs; when the bound-check runs after that pass, both
// (A, Equatable) and (B, Equatable) are in fa.Impls.
func TestDeriveMutuallyRecursiveTypesAreClean(t *testing.T) {
	src := `struct A {
  b: B
}
derive Equatable for A

struct B {
  a: A
}
derive Equatable for B`

	fa := buildWithStdlibForDerive(src)
	for _, e := range fa.TypeErrors {
		if strings.Contains(e.Message, "@derive") {
			t.Errorf("unexpected @derive error on mutually recursive A/B: %s", e.Error())
		}
	}
}

// TestDeriveMultiArgChecksEachInterface pins multi-arg @derive: every
// listed interface gets a separate field-impl check, so a struct whose
// field type impl only one of the two listed interfaces fails for
// the missing one and not the other.
func TestDeriveMultiArgChecksEachInterface(t *testing.T) {
	// HalfImpl impl only Equatable manually. The struct derives
	// both Equatable and Comparable; the derive Comparable should fail
	// because HalfImpl has no Comparable impl, but the derive Equatable
	// should succeed.
	src := `struct HalfImpl { x: Int }

impl Equatable for HalfImpl {
  fn equal?(a: HalfImpl, b: HalfImpl): Bool {
    Equatable.equal?(a.x, b.x)
  }
}

struct Wrap {
  h: HalfImpl
}
derive Equatable for Wrap
derive Comparable for Wrap`

	fa := buildWithStdlibForDerive(src)
	if !hasErrSubstring(fa.TypeErrors, "no impl of `Comparable` for `HalfImpl`") {
		t.Errorf("expected missing-impl error for Comparable/HalfImpl, got:\n  %s", errMsgs(fa.TypeErrors))
	}
	if !hasErrSubstring(fa.TypeErrors, "@derive(Comparable) on Wrap field h") {
		t.Errorf("error must carry DeriveCtx provenance (Wrap field h), got:\n  %s", errMsgs(fa.TypeErrors))
	}
	for _, e := range fa.TypeErrors {
		// The `derive Equatable` entry should NOT error — HalfImpl has a manual
		// impl Equatable block.
		if strings.Contains(e.Message, "no impl of `Equatable` for `HalfImpl`") {
			t.Errorf("unexpected missing-impl error for Equatable/HalfImpl (manual impl exists): %s", e.Error())
		}
	}
}

// TestDeriveDebugOnInterfaceTypedPayloadIsClean pins the existential
// payload case: an enum variant payload whose type is an interface
// (e.g. `Render Renderable`) is exempt from the derive-bound check.
// The runtime value at that slot is some concrete type implementing
// Renderable; calling Debug.to_string(value) at runtime dispatches on
// the concrete type's Debug impl. The static check can't verify this,
// so it must not surface a "Renderable does not implement Debug"
// error (which would be true but unactionable).
func TestDeriveDebugOnInterfaceTypedPayloadIsClean(t *testing.T) {
	src := `pub interface Speech {
  fn say(value: self): String
}

struct Holder {
  what: Speech
}
derive Debug for Holder`

	fa := buildWithStdlibForDerive(src)
	for _, e := range fa.TypeErrors {
		if strings.Contains(e.Message, "@derive Debug") && strings.Contains(e.Message, "Speech") {
			t.Errorf("unexpected @derive Debug error on interface-typed field: %s", e.Error())
		}
	}
}

// TestDeriveGapRoutedThroughDetectMissingImpls pins the missing-impl-
// diagnostic followup: when CheckDeriveBounds detects a derive gap,
// it MUST NOT emit the terse "@derive Iface on T: field 'f' (type 'X')
// does not implement Iface" error so long as the caller will run
// FinalizeCoherence (signaled by fa.ProjectImpls != nil). Instead, the
// DeriveSynth Recording lands in the manifest and DetectMissingImpls
// surfaces the richer "no impl of `Iface` for `X` ... via @derive(Iface)
// on T field f" message with DeriveCtx provenance. Producing both
// would double-report the same fix.
func TestDeriveGapRoutedThroughDetectMissingImpls(t *testing.T) {
	src := `struct C { n: Int }

struct B {
  c: C
}
derive Display for B`

	fa := buildWithStdlibForDerive(src)
	// New (DetectMissingImpls) message MUST appear.
	if !hasErrSubstring(fa.TypeErrors, "no impl of `Display` for `C`") {
		t.Fatalf("expected missing-impl diagnostic for Display/C, got:\n  %s", errMsgs(fa.TypeErrors))
	}
	if !hasErrSubstring(fa.TypeErrors, "@derive(Display) on B field c") {
		t.Errorf("expected DeriveCtx provenance (B field c) in message, got:\n  %s", errMsgs(fa.TypeErrors))
	}
	// Old (CheckDeriveBounds) terse message MUST NOT appear — the
	// missing-impl-diagnostic followup suppresses it when
	// FinalizeCoherence will run, so the user sees one diagnostic
	// per fix, not two.
	for _, e := range fa.TypeErrors {
		if strings.Contains(e.Message, "@derive Display on B: field 'c'") {
			t.Errorf("CheckDeriveBounds terse error must be suppressed when FinalizeCoherence runs; got: %s", e.Error())
		}
	}
}

// TestDeriveExternTypeAcceptsVacuously — `@derive` on an host type has
// no component types (fields / payloads / inner) for the bound check to
// demand impls for, so the full pipeline (CheckDeriveBounds + CheckTypes +
// FinalizeCoherence) accepts it with zero diagnostics: the synthesized
// constant bodies type-check and produce no manifest gaps.
func TestDeriveExternTypeAcceptsVacuously(t *testing.T) {
	src := `pub host type Tok
derive Equatable for Tok
derive Comparable for Tok
derive Hashable for Tok`

	fa := buildWithStdlibForDerive(src)
	if len(fa.TypeErrors) != 0 {
		t.Errorf("expected no diagnostics for @derive on host type, got:\n  %s", errMsgs(fa.TypeErrors))
	}
}
