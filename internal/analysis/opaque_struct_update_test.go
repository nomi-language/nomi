package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// `Struct.update` on an opaque struct from outside its defining file must not
// rewrite a private field. The corpus witness's own doc comment
// (`tests/10-generics-and-type-wrappers/opaque_types/counter.nomi`)
// promises that outside callers "cannot rewrite the `value` field
// independently of `increments`, and cannot otherwise desync the two
// numbers"; `Struct.update(c, {value: -999})` would do exactly that.
//
// The rule is on the PATCH, not on `Struct.update`'s receiver: a patch may not
// name a field of an opaque struct from outside that struct's defining file.
// See spec §7 ("Opacity restricts the operation, not the conformance") and
// §15.3's table.
//
// Every negative below is paired with a positive that an over-broad rule would
// break. A fix that refuses `Struct.update` outright, or that refuses any
// opaque receiver, passes all five negatives and fails the six positives.

// counterModule is the corpus witness's shape: an opaque struct, exported
// constructor / updater / accessors, plus one in-file `Struct.update` so the
// same-file positive has something to call.
const counterModule = `pub opaque struct Counter {
  value: Int
  increments: Int
}

pub fn new(): Counter {
  Counter{value: 0, increments: 0}
}

pub fn bump(c: Counter): Counter {
  Counter{value: c.value + 1, increments: c.increments + 1}
}

pub fn value(c: Counter): Int { c.value }
pub fn increments(c: Counter): Int { c.increments }

pub fn set_value_inside(c: Counter, v: Int): Counter {
  Struct.update(c, {value: v})
}
`

// openModule is the negative control: byte-for-byte the same shape with
// `pub opaque struct` replaced by `pub struct`.
const openModule = `pub struct Open {
  a: Int
  b: Int
}

pub fn mk(): Open { Open{a: 1, b: 2} }
`

func opaquePatchFiles(main string) map[string]string {
	return map[string]string{
		"counter.nomi": counterModule,
		"open.nomi":    openModule,
		"main.nomi":    main,
	}
}

// expectOpaquePatchRefused asserts the opacity diagnostic fired, naming the
// field. Matching on the message rather than on "some error exists" is what
// keeps the test from passing on an unrelated failure — the shape that made
// this defect invisible was a guard nobody consulted, and a test satisfied by
// any diagnostic is the same blind spot one level up.
func expectOpaquePatchRefused(t *testing.T, errs []analysis.TypeError, field string) {
	t.Helper()
	want := "cannot update field '" + field + "' of opaque type 'Counter' outside its defining module"
	for _, e := range errs {
		if strings.Contains(e.Message, want) {
			return
		}
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message
	}
	t.Fatalf("expected the opaque-patch diagnostic for field %q, got %d errors:\n  %s",
		field, len(errs), strings.Join(msgs, "\n  "))
}

// --- negatives: every route to an opaque struct's fields through a patch ---

func TestOpaqueUpdate_DirectPatchFromOutsideIsRefused(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{self, Counter}

fn wreck(c: Counter): Counter {
  Struct.update(c, {value: -999})
}
`))
	expectOpaquePatchRefused(t, errs, "value")
}

// The pipe form reaches `Struct.update` through checkPipe rather than through
// the direct-call path, so it is a separate admission site and was separately
// open.
func TestOpaqueUpdate_PipedPatchFromOutsideIsRefused(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{self, Counter}

fn wreck(c: Counter): Counter {
  c |> Struct.update({value: -999})
}
`))
	expectOpaquePatchRefused(t, errs, "value")
}

// THE RECEIVER IS TRANSPARENT HERE. `Box` is an ordinary `struct`, so any rule
// keyed on `Struct.update`'s receiver being opaque — including the "opaque
// structs do not satisfy `Struct`" reading — admits this call and still lets
// the patch rewrite `Counter.value`. It is the reason the rule is on the patch.
func TestOpaqueUpdate_NestedThroughATransparentWrapperIsRefused(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{self, Counter}

struct Box {
  inner: counter.Counter
  label: String
}

fn wreck(b: Box): Box {
  Struct.update(b, {inner: {value: -999}})
}
`))
	expectOpaquePatchRefused(t, errs, "value")
}

// `Partial<T>` is a compiler-known type operator a USER may annotate, so
// `Struct.update` is not the only function that takes a patch. Routing the
// guard through both `Partial<T>` admission sites rather than through
// `Struct.update` specifically is what closes this one.
func TestOpaqueUpdate_ThroughAUserPartialParameterIsRefused(t *testing.T) {
	files := opaquePatchFiles(`import counter.{self, Counter}
import patcher

fn wreck(c: Counter): Counter {
  patcher.apply(c, {value: -999})
}
`)
	files["patcher.nomi"] = `pub fn apply<T>(x: T, p: Partial<T>): T where T: Struct {
  Struct.update(x, p)
}
`
	_, errs := opaqueProject(t, files)
	expectOpaquePatchRefused(t, errs, "value")
}

// The struct-literal guard EXISTS (checker.go, checkStructLit) and was simply
// never reached under a module-only import: `ResolveTypeExpr` failed on the
// bare name, `checkStructLit` returned nil with NO diagnostic, and execution
// then resolved the bare name and built a real `Counter` — so
// `Counter{value: -999, increments: 1}` type-checked clean and ran, rendering
// as `<opaque Counter>`. Reporting the resolution failure is what makes the
// guard reachable. The selective-import spelling (`import counter.{Counter}`)
// was always refused; this is the spelling that was not.
func TestOpaqueUpdate_BareStructLiteralUnderAModuleImportIsRefused(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter

fn wreck(): Int {
  counter.value(Counter{value: -999, increments: 1})
}
`))
	// Under a module-only import the guard is `checkStructLit`'s existing
	// opacity branch; it only needed the resolution failure reported so the
	// branch is reached. Either diagnostic refuses the program, so accept
	// whichever this spelling produces.
	for _, e := range errs {
		if strings.Contains(e.Message, `unknown type "Counter"`) ||
			strings.Contains(e.Message, "constructor of opaque type 'Counter' is private") {
			return
		}
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message
	}
	t.Fatalf("expected the bare `Counter{...}` literal to be reported, got %d errors:\n  %s",
		len(errs), strings.Join(msgs, "\n  "))
}

// --- planted positives: each one an over-broad rule would break ---

// A non-opaque struct declared in ANOTHER file. Refusing this means the rule
// keyed on "the patch crosses a file boundary" rather than on opacity.
func TestOpaqueUpdate_NonOpaqueStructFromAnotherFileStillWorks(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import open.{Open}

fn ok(o: Open): Open {
  Struct.update(o, {a: 42})
}
`))
	opaqueExpectNoErrors(t, errs)
}

// The owner's own right. `counter.set_value_inside` patches `Counter` from
// inside `counter.nomi`; refusing it means the rule keyed on opacity alone and
// dropped the positional half.
func TestOpaqueUpdate_OpaqueStructFromInsideItsOwnFileStillWorks(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{self, Counter}

fn ok(c: Counter): Counter {
  counter.set_value_inside(c, 7)
}
`))
	opaqueExpectNoErrors(t, errs)
}

// Whole-value replacement on an OPAQUE receiver from outside. The patch names
// no field, so nothing is exposed. Refusing it means the rule keyed on the
// receiver being opaque instead of on the patch naming a field.
func TestOpaqueUpdate_WholeValueReplacementOnAnOpaqueReceiverStillWorks(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{self, Counter}

fn ok(c: Counter): Counter {
  Struct.update(c, counter.new())
}
`))
	opaqueExpectNoErrors(t, errs)
}

// Replacing an opaque FIELD wholesale through a transparent wrapper. The patch
// names `Box.inner` — `Box`'s own field, not `Counter`'s — so the recursion
// must stop at the non-anon-struct value rather than firing on the field's
// opaque TYPE.
func TestOpaqueUpdate_ReplacingAnOpaqueFieldWholesaleStillWorks(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{self, Counter}

struct Box {
  inner: counter.Counter
  label: String
}

fn ok(b: Box): Box {
  Struct.update(b, {inner: counter.new()})
}
`))
	opaqueExpectNoErrors(t, errs)
}

// Patching the wrapper's OWN non-opaque field while an opaque field sits
// beside it untouched. Refusing this means the rule fired on the target's
// field SET rather than on the fields the patch actually names.
func TestOpaqueUpdate_PatchingATransparentFieldBesideAnOpaqueOneStillWorks(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{Counter}

struct Box {
  inner: Counter
  label: String
}

fn ok(b: Box): Box {
  Struct.update(b, {label: "y"})
}
`))
	opaqueExpectNoErrors(t, errs)
}

// `Struct` STAYS UNIVERSAL. This is the positive that distinguishes the two
// readings of the spec contradiction: under "opaque structs do not satisfy
// `Struct` from outside their file" both of these are rejected, and neither
// names a field or exposes anything. Spec §7 keeps its unqualified "every
// struct value satisfies `Struct`" because of this test.
func TestOpaqueUpdate_OpaqueStructStillSatisfiesStructAtABoundAndAnExistential(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{self, Counter}

fn dump(_s: Struct): Int { 1 }

fn widen<T>(x: T): T where T: Struct { x }

fn ok(c: Counter): Int {
  dump(c) + counter.value(widen(c))
}
`))
	opaqueExpectNoErrors(t, errs)
}

// --- the other newly-guarded route: a field REQUIREMENT ---

// An interface `field` requirement is a STORAGE obligation (checker.go's
// validateImplBlockFieldRequirements: "only a struct declares storage"), so an
// impl claiming one for an opaque struct re-exports its private field set.
// From outside the defining file this was open, and it is a field READ rather
// than a write: `interface HasValue { field value: Int }` +
// `impl HasValue for Counter {}` in a sibling module, then `x.value` on a
// `HasValue` existential, returned `Counter.value` with no diagnostic.
//
// It cannot be closed where `fieldTypeFromObject` closes direct field access,
// because at the read the receiver is the INTERFACE and the opaque struct is
// not in scope. The conformance is the only place both are in hand.
func TestOpaqueUpdate_FieldRequirementOnAnOpaqueReceiverFromOutsideIsRefused(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{self, Counter}

interface HasValue {
  field value: Int
}

impl HasValue for Counter {}

fn read(x: HasValue): Int { x.value }

fn leak(): Int { read(counter.new()) }
`))
	want := "cannot satisfy a field requirement with opaque type 'Counter' outside its defining module"
	for _, e := range errs {
		if strings.Contains(e.Message, want) {
			return
		}
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message
	}
	t.Fatalf("expected the opaque field-requirement diagnostic, got %d errors:\n  %s",
		len(errs), strings.Join(msgs, "\n  "))
}

// A NON-OPAQUE struct satisfying a cross-file field requirement. Refusing this
// means the rule keyed on the impl crossing a file boundary rather than on
// opacity — `std/app.nomi`'s `App { field context: Context }` is the shape that
// would break, since every app struct in the tree claims it.
func TestOpaqueUpdate_FieldRequirementOnANonOpaqueReceiverStillWorks(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import open.{Open}

interface HasA {
  field a: Int
}

impl HasA for Open {}

fn read(x: HasA): Int { x.a }

fn ok(o: Open): Int { read(o) }
`))
	opaqueExpectNoErrors(t, errs)
}

// The OWNER exposing its own representation through a field requirement, from
// inside the defining file. Refusing this means the rule dropped the positional
// half and made `opaque` mean "no field requirement ever", which no other entry
// in §15.3's table does.
func TestOpaqueUpdate_FieldRequirementInsideTheDefiningFileStillWorks(t *testing.T) {
	files := opaquePatchFiles(`import inner

fn ok(): Int { inner.read(inner.new()) }
`)
	files["inner.nomi"] = `pub interface HasValue {
  field value: Int
}

pub opaque struct Counted {
  value: Int
}

impl HasValue for Counted {}

pub fn new(): Counted { Counted{value: 3} }

pub fn read(x: HasValue): Int { x.value }
`
	_, errs := opaqueProject(t, files)
	opaqueExpectNoErrors(t, errs)
}
