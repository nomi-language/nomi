package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"strings"
	"testing"
)

func TestImplManifestStoresPair(t *testing.T) {
	fa := &analysis.FileAnalysis{}
	fa.RecordManifest("Int", "Display", analysis.Recording{})
	if fa.ImplManifest == nil {
		t.Fatal("ImplManifest not initialized")
	}
	if len(fa.ImplManifest["Display"]["Int"]) == 0 {
		t.Errorf("expected (Int, Display) in manifest")
	}
}

func TestImplManifestIdempotent(t *testing.T) {
	fa := &analysis.FileAnalysis{}
	fa.RecordManifest("Int", "Display", analysis.Recording{})
	fa.RecordManifest("Int", "Display", analysis.Recording{})
	if len(fa.ImplManifest["Display"]) != 1 {
		t.Errorf("expected 1 entry for Display, got %d", len(fa.ImplManifest["Display"]))
	}
	if len(fa.ImplManifest["Display"]["Int"]) != 1 {
		t.Errorf("expected 1 recording (dedup by struct equality), got %d", len(fa.ImplManifest["Display"]["Int"]))
	}
}

// TestImplManifestStoresRecording asserts a Recording's full payload —
// Pos, Kind — survives a RecordManifest call. Locks in the shape
// DetectMissingImpls reads when it renders demand-site diagnostics.
func TestImplManifestStoresRecording(t *testing.T) {
	fa := &analysis.FileAnalysis{}
	rec := analysis.Recording{
		Pos:  analysis.Pos{File: "a.nomi", Line: 7, Col: 12},
		Kind: analysis.RecordingKindCallSite,
	}
	fa.RecordManifest("Int", "Display", rec)
	recs := fa.ImplManifest["Display"]["Int"]
	if len(recs) != 1 {
		t.Fatalf("expected 1 recording, got %d", len(recs))
	}
	if recs[0] != rec {
		t.Errorf("recording round-trip mismatch: got %+v, want %+v", recs[0], rec)
	}
}

// TestRecordManifestDropsSynthBandRecordings pins the synth-band filter
// in RecordManifest. Recordings whose Pos.Line lies in the derive-synth
// fake-AST band (>= 1<<30) come from the generated bodies' internal call
// sites and would surface a meaningless line number in user diagnostics
// (e.g. "required at .../main.nomi:1074249728:10 via call site"). The
// DeriveSynth/DerivePayload recording emitted at the @derive's field walk
// already covers the same (Iface, T) demand at a real source position, so
// the synth-body record adds no actionable info and is dropped at recording
// time. Real-position recordings pass through unchanged.
func TestRecordManifestDropsSynthBandRecordings(t *testing.T) {
	fa := &analysis.FileAnalysis{}
	synthRec := analysis.Recording{
		Pos:  analysis.Pos{File: "a.nomi", Line: 1 << 30, Col: 5},
		Kind: analysis.RecordingKindCallSite,
	}
	fa.RecordManifest("C", "Display", synthRec)
	if got := len(fa.ImplManifest["Display"]["C"]); got != 0 {
		t.Errorf("synth-band recording leaked into manifest: got %d entries, want 0", got)
	}
	realRec := analysis.Recording{
		Pos:  analysis.Pos{File: "a.nomi", Line: 12, Col: 5},
		Kind: analysis.RecordingKindCallSite,
	}
	fa.RecordManifest("C", "Display", realRec)
	if got := len(fa.ImplManifest["Display"]["C"]); got != 1 {
		t.Errorf("real-position recording dropped or duplicated: got %d entries, want 1", got)
	}
}

// TestImplManifestRecordingIdempotent asserts the slice dedupe rule:
// recording the same Recording twice contributes exactly one entry.
// Two recordings differing in any field (Pos, Kind, Derive pointer)
// land in the slice separately — covered by
// TestImplManifestMergePreservesRecordings.
func TestImplManifestRecordingIdempotent(t *testing.T) {
	fa := &analysis.FileAnalysis{}
	rec := analysis.Recording{
		Pos:  analysis.Pos{File: "a.nomi", Line: 7, Col: 12},
		Kind: analysis.RecordingKindCallSite,
	}
	fa.RecordManifest("Int", "Display", rec)
	fa.RecordManifest("Int", "Display", rec)
	recs := fa.ImplManifest["Display"]["Int"]
	if len(recs) != 1 {
		t.Errorf("expected 1 recording (dedup by struct equality), got %d", len(recs))
	}
}

// TestImplManifestMergePreservesRecordings asserts that merging two FAs
// with distinct Recordings for the same (Iface, Type) pair unions both
// recordings into the destination slice — not collapsing them to one.
// The missing-impl diagnostic needs every demand site to render the full list,
// so the merge must preserve provenance even for duplicate pairs.
func TestImplManifestMergePreservesRecordings(t *testing.T) {
	dst := &analysis.FileAnalysis{}
	srcA := &analysis.FileAnalysis{}
	srcB := &analysis.FileAnalysis{}
	recA := analysis.Recording{Pos: analysis.Pos{File: "a.nomi", Line: 7, Col: 12}, Kind: analysis.RecordingKindCallSite}
	recB := analysis.Recording{Pos: analysis.Pos{File: "b.nomi", Line: 3, Col: 5}, Kind: analysis.RecordingKindTypedLiteralSlot}
	srcA.RecordManifest("Int", "Display", recA)
	srcB.RecordManifest("Int", "Display", recB)
	analysis.MergeImplManifestForTest(dst, srcA)
	analysis.MergeImplManifestForTest(dst, srcB)
	recs := dst.ImplManifest["Display"]["Int"]
	if len(recs) != 2 {
		t.Fatalf("expected 2 recordings (union), got %d: %+v", len(recs), recs)
	}
	gotA, gotB := false, false
	for _, r := range recs {
		if r == recA {
			gotA = true
		}
		if r == recB {
			gotB = true
		}
	}
	if !gotA || !gotB {
		t.Errorf("missing recordings after merge: gotA=%v gotB=%v, recs=%+v", gotA, gotB, recs)
	}
}

// buildProjectManifest type-checks a multi-file project (entry source plus
// optional siblings) using the standard project builder, returns the entry
// FileAnalysis after CheckTypes has run, and asserts no diagnostics fired.
// Mirrors checkTypedLiteralProject's shape so the manifest assertions can
// inspect post-check state directly. siblings keys are module-path strings
// (e.g. "joiner") that the loader maps to source.
func buildProjectManifest(t *testing.T, entrySrc string, siblings map[string]string) *analysis.FileAnalysis {
	t.Helper()
	lib := std.Load()
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		key := strings.Join(modulePath, "/")
		src, ok := siblings[key]
		if !ok {
			return nil, nil
		}
		tokens := lexer.Lex(src)
		nodes, _ := parser.ParseWithRecovery(tokens)
		return nodes, nil
	}
	tokens := lexer.Lex(entrySrc)
	entryNodes, _ := parser.ParseWithRecovery(tokens)
	entryFA, cache, siblingNodes := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, "/project", loader,
	)
	if entryFA == nil {
		t.Fatalf("BuildProject returned nil")
	}
	// Mirror internal/frontend's Checker.Analyze: run
	// CheckTypes over each sibling FA + nodes pair so conformance recordings
	// inside sibling function bodies (e.g. `${name}` interpolation) reach
	// the entry's ImplManifest. Sibling diagnostics are discarded — the
	// entry's CheckTypes is the authoritative diagnostics path here.
	for key, sibFA := range cache {
		nodes := siblingNodes[key]
		_ = analysis.CheckTypes(sibFA, nodes)
		analysis.MergeImplManifestForTest(entryFA, sibFA)
	}
	checkErrs := analysis.CheckTypes(entryFA, entryNodes)
	all := append([]analysis.TypeError{}, entryFA.TypeErrors...)
	all = append(all, checkErrs...)
	if len(all) > 0 {
		msgs := make([]string, len(all))
		for i, e := range all {
			msgs[i] = e.Error()
		}
		t.Fatalf("expected clean check, got %d errors:\n  %s", len(all), strings.Join(msgs, "\n  "))
	}
	return entryFA
}

// A Dynamic slot in a typed literal triggers an interface-conformance check
// against the tag's per-slot interface (here Display). The manifest must
// record the slot's static type → interface pair so the runtime can pre-
// register the dispatch entry.
func TestManifestRecordedAtTypedLiteralSlot(t *testing.T) {
	siblings := map[string]string{
		"joiner": `import {
  std/display: Display
  std/iter.Iter
  std/literals: Fragment, Literal
  std/literals.Fragment.{Static, Dynamic}
}

pub type Joiner

impl Literal for Joiner {
  fn from_fragments(fragments: List<Fragment<Display>>): String {
    Iter.reduce(fragments, |acc = "", frag|
      case frag {
        Static(s) -> acc + s
        Dynamic(v) -> acc + Display.to_string(v)
      })
  }
}`,
	}
	entry := `import {
  std/io
  joiner: Joiner
}

fn main() {
  io.print(Joiner"hello-${42}")
}`
	fa := buildProjectManifest(t, entry, siblings)
	if len(fa.ImplManifest["Display"]["Int"]) == 0 {
		t.Errorf("expected (Int, Display) recorded from Joiner\"hello-${42}\" slot; got manifest=%v",
			fa.ImplManifest)
	}
}

func TestImplManifestMerges(t *testing.T) {
	dst := &analysis.FileAnalysis{}
	src := &analysis.FileAnalysis{}
	src.RecordManifest("Int", "Display", analysis.Recording{})
	src.RecordManifest("String", "Display", analysis.Recording{})
	src.RecordManifest("Int", "Hashable", analysis.Recording{})

	analysis.MergeImplManifestForTest(dst, src)

	if len(dst.ImplManifest["Display"]["Int"]) == 0 {
		t.Errorf("expected (Int, Display) merged into dst")
	}
	if len(dst.ImplManifest["Display"]["String"]) == 0 {
		t.Errorf("expected (String, Display) merged into dst")
	}
	if len(dst.ImplManifest["Hashable"]["Int"]) == 0 {
		t.Errorf("expected (Int, Hashable) merged into dst")
	}
}

// An interface-bound generic call with a concrete argument type triggers the
// constraint check against the interface. The manifest must record the
// argument type → interface pair the same way typed-literal slots do.
func TestManifestRecordedAtGenericInstantiation(t *testing.T) {
	src := `import std/io

pub fn show<T>(x: T): String where T: Display {
  Display.to_string(x)
}

fn main() {
  io.print(show(42))
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	if len(fa.ImplManifest["Display"]["Int"]) == 0 {
		t.Errorf("expected (Int, Display) recorded from show(42); got manifest=%v",
			fa.ImplManifest)
	}
}

// Interface-method calls on interfaces with multiple type parameters
// must record only the self binding as a conformance fact, not every
// type-param binding in subs. Pusher<T> binds self → Bag and T → Int;
// only (Bag, Pusher) is a real conformance — Int does NOT implement
// Pusher just because it appears as the T arg.
func TestManifestSelfBindingOnly(t *testing.T) {
	src := `pub interface Pusher<T> {
  fn push(c: self, v: T): self
}

pub struct Bag { count: Int }

impl Pusher for Bag {
  fn push(c: Bag, v: Int): Bag { Bag{count: c.count + v} }
}

fn main(): Pusher<Int> {
  b = Bag{count: 0}
  Pusher.push(b, 42)
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	if len(fa.ImplManifest["Pusher"]["Bag"]) == 0 {
		t.Errorf("expected (Bag, Pusher) recorded; got manifest=%v", fa.ImplManifest)
	}
	if len(fa.ImplManifest["Pusher"]["Int"]) > 0 {
		t.Errorf("did NOT expect (Int, Pusher) recorded — Int doesn't implement Pusher; got manifest=%v",
			fa.ImplManifest)
	}
}

// Same contract as TestManifestSelfBindingOnly, broadened beyond the
// single-typeparam Pusher<T> shape: an interface with multiple non-self
// type params (Foo<A, B>) must still record only the self binding as a
// conformance fact, not every type-param binding in subs. Bag impl
// Foo<Int, String>; only (Bag, Foo) is a real conformance — Int and
// String do NOT implement Foo just because they appear as A and B args.
func TestManifestSelfBindingOnly_MultipleNonSelfTypeParams(t *testing.T) {
	src := `pub interface Foo<A, B> {
  fn baz(x: self, a: A, b: B): self
}

pub struct Bag { count: Int }

impl Foo for Bag {
  fn baz(x: Bag, a: Int, b: String): Bag { _ = b; Bag{count: x.count + a} }
}

fn main(): Foo<Int, String> {
  b = Bag{count: 0}
  Foo.baz(b, 42, "hello")
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	if len(fa.ImplManifest["Foo"]["Bag"]) == 0 {
		t.Errorf("expected (Bag, Foo) recorded; got manifest=%v", fa.ImplManifest)
	}
	if len(fa.ImplManifest["Foo"]["Int"]) > 0 || len(fa.ImplManifest["Foo"]["String"]) > 0 {
		t.Errorf("did NOT expect Int or String recorded for Foo (only the receiver impl it); got manifest=%v",
			fa.ImplManifest)
	}
}

// When an interface bound is satisfied by a generic-container type
// (List<E>, Map<K, V>, Result<T, E>, Maybe<T>, etc.), the analyzer
// must record (typearg, iface) for each TypeArg the container's
// stdlib impl will dispatch through at runtime — not just the
// container itself.
// Uses io.print (Display) as the vehicle: universal default Debug is satisfied
// by every type and records NO manifest demand, so the generic-container
// TypeArg recursion is now exercised through Display, which is still recorded.
func TestManifestRecursesIntoGenericContainerTypeArgs(t *testing.T) {
	src := `import { std/io }

fn main() {
  ages = {"alice" => 30, "bob" => 25}
  io.print(ages)
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	if len(fa.ImplManifest["Display"]["Map"]) == 0 {
		t.Errorf("expected (Map, Display); got %v", fa.ImplManifest)
	}
	if len(fa.ImplManifest["Display"]["String"]) == 0 {
		t.Errorf("expected (String, Display) recursed from Map<String, _>; got %v", fa.ImplManifest)
	}
	if len(fa.ImplManifest["Display"]["Int"]) == 0 {
		t.Errorf("expected (Int, Display) recursed from Map<_, Int>; got %v", fa.ImplManifest)
	}
}

// Same recursion contract through a piped generic call: checkPipe's
// generic branch must run the interface-bound enforcement loop and record
// the demand. Vehicle is io.print (Display);
// universal Debug records no manifest demand (see the recursion test above).
func TestManifestRecordsInterfaceBoundsThroughPipe(t *testing.T) {
	src := `import { std/io }

fn main() {
  ages = {"alice" => 30, "bob" => 25}
  ages |> io.print()
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	for _, want := range []string{"Map", "String", "Int"} {
		if len(fa.ImplManifest["Display"][want]) == 0 {
			t.Errorf("expected (%s, Display); got %v", want, fa.ImplManifest)
		}
	}
}

// @derive proves every component type satisfies the derived interface
// (CheckDeriveBounds → hasInterfaceImpl). The synthesized impl invokes
// Iface.method on those components at runtime, so each (component, iface)
// must land in the manifest.
func TestManifestRecordsDerivePayloadConformance(t *testing.T) {
	src := `pub enum Shape {
  Circle Float
  Square Int

}
derive Comparable for Shape

fn main() {
  c1 = Shape.Circle(1.0)
  c2 = Shape.Circle(2.0)
  case Comparable.compare(c1, c2) {
    .Less -> {}
    .Equal -> {}
    .Greater -> {}
  }
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	if len(fa.ImplManifest["Comparable"]["Shape"]) == 0 {
		t.Errorf("expected (Shape, Comparable); got %v", fa.ImplManifest)
	}
	if len(fa.ImplManifest["Comparable"]["Float"]) == 0 {
		t.Errorf("expected (Float, Comparable) recorded from @derive payload; got %v", fa.ImplManifest)
	}
	if len(fa.ImplManifest["Comparable"]["Int"]) == 0 {
		t.Errorf("expected (Int, Comparable) recorded from @derive payload; got %v", fa.ImplManifest)
	}
}

// @derive Display/Debug synth bodies emit StringInterp literals like
// "Some(${Iface.to_string(_v0)})". At runtime, evalStringInterp
// re-dispatches Display.to_string on the inner String parts (the
// to_string return values), so (String, Display) must be in the
// dispatch table whenever the program uses any @derive Display/Debug
// type — regardless of the type's payloads.
func TestManifestRecordsStringDisplayForDeriveDebugStringInterp(t *testing.T) {
	src := `import { std/io }

pub enum Maybe2<T> {
  Some2 T
  None2

}
derive Debug for Maybe2

fn main(): Maybe2<Int> {
  m = Maybe2.Some2(42)
  io.inspect(m)
  m
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	if len(fa.ImplManifest["Display"]["String"]) == 0 {
		t.Errorf("expected (String, Display) recorded from @derive Debug synth body's StringInterp; got manifest=%v",
			fa.ImplManifest)
	}
}

// A `${...}` interpolation inside a sibling module's function body
// triggers a Display recording on that sibling's FileAnalysis. The
// recording must reach the entry's ImplManifest so a program whose
// entry imports the sibling and calls into it doesn't need to also
// import std/strings explicitly. Without per-sibling CheckTypes, the
// recording stays trapped on the sibling's FA and never propagates.
func TestManifestRecordsSiblingModuleBodyConformance(t *testing.T) {
	siblings := map[string]string{
		"greeter": `pub fn greet(name: String): String {
  "hello, ${name}"
}`,
	}
	entry := `import {
  std/io
  greeter
}

fn main() {
  io.print(greeter.greet("Alice"))
}`
	fa := buildProjectManifest(t, entry, siblings)
	if len(fa.ImplManifest["Display"]["String"]) == 0 {
		t.Errorf("expected (String, Display) recorded from sibling greeter's ${name}; got manifest=%v",
			fa.ImplManifest)
	}
}

// Chained module access through a re-exporting facade
// (`facade.parser_lib.greet(...)`) must reach the same generic-call /
// interface-bound recording paths as the non-chained shape. Without the
// builder resolving the inner *ast.FieldAccess as a module reference,
// `n.Field.Name` on the outer FieldAccess never lands in
// fa.References, the checker sees nil types at the call site, and
// the manifest stays empty for whatever conformance the call would
// have triggered. Here `parser_lib.greet` returns a `String` that
// the entry's `io.print` then forces a Display recording on. (Was io.inspect
// / Debug before universal default Debug stopped recording Debug demands.)
func TestManifestRecordsThroughReExportChain(t *testing.T) {
	siblings := map[string]string{
		"facade":     `import parser_lib export`,
		"parser_lib": `pub fn greet(name: String): String { "hello, " + name }`,
	}
	entry := `import {
  std/io
  facade.{parser_lib}
}

fn main() {
  io.print(parser_lib.greet("Alice"))
}`
	fa := buildProjectManifest(t, entry, siblings)
	if len(fa.ImplManifest["Display"]["String"]) == 0 {
		t.Errorf("expected (String, Display) recorded for chained re-export call facade.parser_lib.greet(...); got manifest=%v",
			fa.ImplManifest)
	}
}

// TestBareNameImplCall_Rejected pins the rejection of a bare-name call to a
// CONCRETE impl method (`speak(dog)`): it must be rejected, with the
// diagnostic pointing at the qualified spellings. bareNameDispatchInterface
// (Shape 1) recognizes the method via implMethodIfacesFor, which detects
// block-impl membership through the project impl index — so a block-form
// `impl Speech for Dog { fn speak… }` method is rejected just like the old
// decorator form was.
func TestBareNameImplCall_Rejected(t *testing.T) {
	src := `import std/io

pub interface Speech { fn speak(s: self): String }

pub struct Dog { name: String }

impl Speech for Dog {
  fn speak(d: Dog): String {
    d.name + " says woof"
  }
}

fn main() {
  io.print(speak(Dog{name: "Rex"}))
}`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "bare-name dispatch is not supported")
}

// TestBareNameImplCall_ShadowedByLocalFn_NotRecorded asserts that a local
// block-scoped binding shadowing an impl method name does NOT get a
// conformance pair recorded — the bare call resolves to the local lambda,
// not the impl method, so the analyzer leaves it alone.
func TestBareNameImplCall_ShadowedByLocalFn_NotRecorded(t *testing.T) {
	src := `import std/io

pub interface Speech { fn speak(s: self): String }

pub struct Dog { name: String }

impl Speech for Dog {
  fn speak(d: Dog): String {
    d.name + " says woof"
  }
}

fn main() {
  speak = |n: Int| "shadowed: ${n}"
  io.print(speak(42))
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	// AttachStdlibProjectImpls — see checker_stdlib_test.go's
	// checkSourceWithStdlib for the rationale.
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	_ = analysis.BuildTypes(fa, nodes)
	_ = analysis.CheckTypes(fa, nodes)

	if len(fa.ImplManifest["Speech"]["Int"]) > 0 {
		t.Errorf("expected (Int, Speech) NOT recorded (speak shadowed by a local binding); got manifest %v", fa.ImplManifest)
	}
}

// TestBareNameDefaultMethodCall_Rejected pins bare-name rejection for the
// default-method shape: a bare-name call to an interface DEFAULT method (declared in the
// interface block, not via an impl block) is rejected too — exercises
// shape 2 of bareNameDispatchInterface (the pointer-identity scan) on the
// rejection path. (Note the default body itself uses the qualified
// `Identity.name(value)`, which is fine.)
func TestBareNameDefaultMethodCall_Rejected(t *testing.T) {
	src := `import std/io

pub interface Identity {
  fn name(value: self): String
  fn describe(value: self): String {
    "I am " + Identity.name(value)
  }
}

pub struct Robot { model: String }

impl Identity for Robot {
  fn name(robot: Robot): String {
    robot.model
  }
}

fn main() {
  io.print(describe(Robot{model: "T-800"}))
}`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "bare-name dispatch is not supported")
}

// TestAmbiguousBareNameImplCall_Rejected pins that a method name implemented
// by two interfaces (Greeter+Farewell, both `greet` for Person) is rejected as
// a bare call. Requiring a qualifier makes a separate cross-interface
// ambiguity diagnostic unnecessary, so the bare call is rejected like any other.
// Block-form impls reach this via implMethodIfacesFor / the project impl index.
func TestAmbiguousBareNameImplCall_Rejected(t *testing.T) {
	src := `import std/io

pub interface Greeter { fn greet(g: self): String }
pub interface Farewell { fn greet(f: self): String }

pub struct Person { name: String }

impl Greeter for Person {
  fn greet(p: Person): String {
    "hello " + p.name
  }
}

impl Farewell for Person {
  fn greet(p: Person): String {
    "bye " + p.name
  }
}

fn main() {
  io.print(greet(Person{name: "Ada"}))
}`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "bare-name dispatch is not supported")
}

// TestMultiReceiverSameInterfaceImplCall_NotAmbiguous is the contrast case:
// two `impl Speech for T` methods named `speak` for different receiver types are
// NOT a collision — both register under the same `Speech.speak` key, so the
// interface-qualified call dispatches on the runtime type. Coherence must not
// false-positive on the multi-receiver polymorphic-dispatch shape.
func TestMultiReceiverSameInterfaceImplCall_NotAmbiguous(t *testing.T) {
	src := `import std/io

pub interface Speech { fn speak(s: self): String }

pub struct Dog { name: String }

pub struct Cat { name: String }

impl Speech for Dog {
  fn speak(d: Dog): String {
    d.name + " says woof"
  }
}

impl Speech for Cat {
  fn speak(c: Cat): String {
    c.name + " says meow"
  }
}

fn main() {
  io.print(Speech.speak(Dog{name: "Rex"}))
  io.print(Speech.speak(Cat{name: "Whiskers"}))
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// Simplest possible program (single file, no siblings, no tag module) that
// exercises the BuildProject pipeline. It checks mergeImplManifest's
// wiring: the entry FileAnalysis returned by BuildProject must carry the
// program-wide manifest.
func TestImplManifestVisibleViaBuildProject(t *testing.T) {
	src := `import std/io

fn main() {
  io.print(Display.to_string(42))
}`
	fa := buildProjectManifest(t, src, nil)
	if len(fa.ImplManifest["Display"]["Int"]) == 0 {
		t.Errorf("ImplManifest not propagated via BuildProject; got %v", fa.ImplManifest)
	}
}

// TestDeriveSynthRecordsDeriveCtx pins the derive-synth Recording shape:
// a single-level `@derive` on a struct produces a Recording per component
// type with Kind=RecordingKindDeriveSynth and a populated DeriveCtx
// naming the type, the @derive position, and the offending field.
// The missing-impl diagnostic renders the @derive position + field name when
// the demand site is a derive synthesizer.
func TestDeriveSynthRecordsDeriveCtx(t *testing.T) {
	src := `import { std/io }

pub struct Box {
  n: Int

}
derive Display for Box

fn main() {
  b = Box{n: 42}
  io.print(Display.to_string(b))
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	recs := fa.ImplManifest["Display"]["Int"]
	if len(recs) == 0 {
		t.Fatalf("expected at least one (Int, Display) recording from @derive Display struct Box { n: Int }; got %v", fa.ImplManifest)
	}
	var derivedRec *analysis.Recording
	for i := range recs {
		if recs[i].Kind == analysis.RecordingKindDeriveSynth {
			derivedRec = &recs[i]
			break
		}
	}
	if derivedRec == nil {
		t.Fatalf("expected at least one Recording with Kind=RecordingKindDeriveSynth; got recs=%+v", recs)
	}
	if derivedRec.Derive == nil {
		t.Fatalf("derive-synth Recording.Derive is nil: %+v", derivedRec)
	}
	if derivedRec.Derive.TypeName != "Box" {
		t.Errorf("DeriveCtx.TypeName: got %q, want %q", derivedRec.Derive.TypeName, "Box")
	}
	if derivedRec.Derive.Iface != "Display" {
		t.Errorf("DeriveCtx.Iface: got %q, want %q", derivedRec.Derive.Iface, "Display")
	}
	if derivedRec.Derive.FieldName != "n" {
		t.Errorf("DeriveCtx.FieldName: got %q, want %q", derivedRec.Derive.FieldName, "n")
	}
	// `derive Display` is on line 7 in the source. DerivePos.Line should
	// match the conformance line the synthetic @derive decorator was stamped from.
	if derivedRec.Derive.DerivePos.Line != 7 {
		t.Errorf("DeriveCtx.DerivePos.Line: got %d, want 7 (the `derive` line)", derivedRec.Derive.DerivePos.Line)
	}
}

// TestGenericInstantiationAttribution pins the load-bearing generic-
// instantiation recursion: when one @derive'd type embeds another @derive'd
// generic type (`@derive Display struct Holder { w: Wrapper<C> }`), the
// inner type-arg's (Display, C) recording must attribute to *Holder's* w
// field, not Wrapper's synth body. Without this, the actionable fix
// pointer ("add impl Display for C") lands on a synthesizer-band
// position the user can't act on.
func TestGenericInstantiationAttribution(t *testing.T) {
	src := `import { std/io }

pub struct C { x: Int }

impl Display for C {
  fn to_string(c: C): String { "C(" + Display.to_string(c.x) + ")" }
}

pub struct Wrapper<T> {
  value: T

}
derive Display for Wrapper

pub struct Holder {
  w: Wrapper<C>

}
derive Display for Holder

fn main() {
  h = Holder{w: Wrapper{value: C{x: 7}}}
  io.print(Display.to_string(h))
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	recs := fa.ImplManifest["Display"]["C"]
	if len(recs) == 0 {
		t.Fatalf("expected at least one (C, Display) recording; got manifest %v", fa.ImplManifest)
	}
	var fromHolder *analysis.Recording
	for i := range recs {
		if recs[i].Kind != analysis.RecordingKindDeriveSynth || recs[i].Derive == nil {
			continue
		}
		if recs[i].Derive.TypeName == "Holder" {
			fromHolder = &recs[i]
			break
		}
	}
	if fromHolder == nil {
		t.Fatalf("expected at least one (C, Display) recording attributed to Holder (not Wrapper); got recs=%+v", recs)
	}
	if fromHolder.Derive.FieldName != "w" {
		t.Errorf("DeriveCtx.FieldName: got %q, want %q (Holder's w field, not Wrapper's value)",
			fromHolder.Derive.FieldName, "w")
	}
}

// TestManifestRecordedForDestructuredImport checks that the
// destructured-import form (`import std/dynamic.{Dynamic}` with
// `d: Dynamic`) records the (Debug, Dynamic) manifest entry. The
// module-qualified spelling `dynamic.Dynamic` after a bare
// `import std/dynamic` names the same type and must demand the same
// impl: the TypeRegistry holds a `<modAlias>.<TypeName>` key for every
// type member of each imported SymbolModule's ModuleScope, so the
// QualifiedType branch of ResolveTypeExpr resolves to the same
// `*PrimitiveType{Name_: "Dynamic"}` the bare-name path produces, and
// the recording sites in checker.go consume resolved Types rather than
// raw ast.TypeExprs.
func TestManifestRecordedForDestructuredImport(t *testing.T) {
	src := `import std/dynamic.{Dynamic}

pub fn show(d: Dynamic): String {
  Debug.inspect(d)
}

fn main() { Unit }`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	if len(fa.ImplManifest["Debug"]["Dynamic"]) == 0 {
		t.Errorf("expected (Debug, Dynamic) recorded from destructured-import form; got manifest=%v",
			fa.ImplManifest)
	}
}
