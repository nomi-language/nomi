package analysis

import "testing"

// `embeds T` makes T a subtype of the enum: T values flow into
// enum-typed positions without explicit qualified construction.

func TestEmbed_SubtypeCoercion_DistinctType_Assignment(t *testing.T) {
	src := `type UserId String
enum Identifier { embeds UserId; Anonymous }
fn main(): Identifier {
  uid: UserId = UserId("alice")
  uid
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestEmbed_SubtypeCoercion_DistinctType_FunctionArg(t *testing.T) {
	src := `type UserId String
enum Identifier { embeds UserId; Anonymous }
fn render(_id: Identifier): String { "x" }
fn main(): String {
  uid: UserId = UserId("alice")
  render(uid)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestEmbed_SubtypeCoercion_DistinctType_ListElement(t *testing.T) {
	src := `type UserId String
enum Identifier { embeds UserId; Anonymous }
fn main(): List<Identifier> {
  uid: UserId = UserId("alice")
  [uid, UserId("bob"), Identifier.Anonymous]
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestEmbed_SubtypeCoercion_StructEmbed_Assignment(t *testing.T) {
	src := `struct Circle { radius: Float }
enum Shape { embeds Circle; Point }
fn main(): Shape {
  c: Circle = Circle{radius: 5.0}
  c
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestEmbed_SubtypeCoercion_StructEmbed_ListElement(t *testing.T) {
	src := `struct Circle { radius: Float }
struct Rectangle { width: Float; height: Float }
enum Shape { embeds Circle; embeds Rectangle; Point }
fn main(): List<Shape> {
  c: Circle = Circle{radius: 5.0}
  [c, Rectangle{width: 3.0, height: 4.0}, Shape.Point]
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Wrapping-distinct embed payload is the inner type, not the wrapped
// distinct. Pattern destructure binds the inner type directly. This
// matches how a standalone distinct destructure works (`case uid {
// UserId(name) -> name }` where name: String) — embed shouldn't add
// a layer of nesting just because the type now lives inside a variant.
func TestEmbed_WrappingDistinct_PatternBindsInner(t *testing.T) {
	src := `type UserId String
enum Identifier { embeds UserId; Anonymous }
fn label(id: Identifier): String {
  case id {
    Identifier.UserId(name) -> name
    Identifier.Anonymous -> "anonymous"
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Wrapping-distinct embed: qualified construction takes the inner
// type directly. `Identifier.UserId("alice")` is one-step; the
// previous `Identifier.UserId(UserId("alice"))` two-step form is
// no longer valid (it would pass UserId where String is expected).
func TestEmbed_WrappingDistinct_QualifiedConstructionTakesInner(t *testing.T) {
	src := `type UserId String
enum Identifier { embeds UserId; Anonymous }
fn make(): Identifier {
  Identifier.UserId("alice")
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Subtype coercion still works: a bare UserId value flows into an
// Identifier-typed slot. The variant payload change doesn't affect
// the subtype rule because subtyping operates on the wrapping type
// (UserId ≤ Identifier), not on the variant's payload type.
func TestEmbed_WrappingDistinct_SubtypeCoercionStillWorks(t *testing.T) {
	src := `type UserId String
enum Identifier { embeds UserId; Anonymous }
fn make(): Identifier {
  uid: UserId = UserId("alice")
  uid
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// `embeds Iface` is not allowed — interfaces aren't structs or distincts,
// so there's no meaningful subtyping to install (that would be open
// polymorphism / dynamic interface dispatch, rejected by §13.7). Without this
// check the declaration silently no-ops: parses fine, but no constructor is created
// and no widening occurs. Diagnostic must point the reader at the
// single-payload variant form, which is the right shape for an
// interface-typed payload.
func TestEmbed_InterfaceTypeRejected(t *testing.T) {
	src := `interface Renderable { fn render(value: self): String }
enum Wrapper { embeds Renderable; Empty }`
	_, errs := checkSource(src)
	expectError(t, errs, "embeds requires a struct or distinct type, got interface Renderable")
}
