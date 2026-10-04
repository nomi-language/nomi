package format

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Type shape and impl item syntax — canonical emission.
//
// The formatter emits the canonical type-body form: `field` / `variant` items
// one per line. Functions, once bindings, interface implementations, derives,
// and tests are declarations outside type bodies.
// ---------------------------------------------------------------------------

// roundTrip asserts Format(src) == src (src is already canonical) and that
// formatting is idempotent.
func roundTrip(t *testing.T, src string) {
	t.Helper()
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if got != src {
		t.Errorf("not canonical.\n--- got ---\n%s\n--- want ---\n%s", got, src)
	}
	again, err := Format(got)
	if err != nil {
		t.Fatalf("Format (second pass) error: %v", err)
	}
	if again != got {
		t.Errorf("not idempotent.\n--- once ---\n%s\n--- twice ---\n%s", got, again)
	}
}

// migrates asserts Format(src) == want and that want is itself a fixed
// point (the migration lands directly on canonical output).
func migrates(t *testing.T, src, want string) {
	t.Helper()
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if got != want {
		t.Errorf("migration mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	roundTrip(t, want)
}

// Struct field doc comments stay immediately above their field line.
func TestFormat_StructFieldDocRoundTrips(t *testing.T) {
	src := `struct User {
    /// The user's name.
    name: String
    age: Int
}
`
	roundTrip(t, src)
}

func TestFormat_GoSelectorBindings(t *testing.T) {
	src := `gopkg "example.com/app/ffi"

opaque type RawBox go ffi.Box

fn echo_upper(s: String): String go ffi.EchoUpper
`
	roundTrip(t, src)
}

func TestFormat_InlineGoBindingDedentsMultilineBody(t *testing.T) {
	src := `fn parse(raw: String): Result<ParsedURL, String> go {
    parsed, err := url.Parse(raw)
    if err != nil {
        return nil, err
    }
    return struct {
        Scheme string
        Host string
    }{
        Scheme: parsed.Scheme,
        Host: parsed.Host,
    }, nil
}
`
	roundTrip(t, src)
}

// Every variant payload shape round-trips (positional, tuple, struct,
// embeds).
func TestFormat_EnumPayloadShapesRoundTrip(t *testing.T) {
	src := `enum Shape {
    Circle Float
    Position (Int, Int)
    Rect {width: Float, height: Float}
    embeds Click
}
`
	roundTrip(t, src)
}

// A `//` leading comment on a variant stays above its variant line.
func TestFormat_EnumVariantLeadingCommentRoundTrips(t *testing.T) {
	src := `enum Status {
    // still waiting
    Pending
    Active
}
`
	roundTrip(t, src)
}

func TestFormat_ImplOnceRoundTrips(t *testing.T) {
	src := `struct cache {
    seed: Int
}

pub once Default: Int = 1

`
	roundTrip(t, src)
}

// Variant doc comments round-trip.
func TestFormat_EnumVariantDocPreserved(t *testing.T) {
	src := `enum Status {
    /// Still waiting.
    Pending

    Active
}
`
	roundTrip(t, src)
}

// The reference example (struct/enum shape plus file helpers and interface impls)
// is a fixed point.
func TestFormat_ReferenceExampleRoundTrips(t *testing.T) {
	src := `pub interface Speech {
    fn speak(animal: self): String
}

struct Dog {
    name: String
    breed: String
}

fn rename(d: Dog, new_name: String): Dog {
    Dog{name: new_name, breed: d.breed}
}

impl Speech for Dog {
    fn speak(d: Dog): String {
        "${d.name} says woof"
    }
}

enum Status {
    Active
    Pending Int
}

pub fn is_active(s: Status): Bool {
    case s {
        .Active -> True
        _ -> False
    }
}

impl Speech for Status {
    fn speak(_s: Status): String {
        "status"
    }
}

`
	roundTrip(t, src)
}

// Items written tight against the fields gain a separating blank line.
func TestFormat_StructBodyItems_BlankLineSeparation(t *testing.T) {
	src := `struct Dog {
    name: String
}

fn bark(_d: Dog): String {
    "woof"
}

impl Speech for Dog {
    fn speak(_d: Dog): String {
        "woof"
    }
}

`
	want := `struct Dog {
    name: String
}

fn bark(_d: Dog): String {
    "woof"
}

impl Speech for Dog {
    fn speak(_d: Dog): String {
        "woof"
    }
}

`
	migrates(t, src, want)
}

// A field-only interface conformance is bodyless.
func TestFormat_EmptyNestedImpl(t *testing.T) {
	src := `struct Watcher {
    name: String
}

impl HasName for Watcher

`
	roundTrip(t, src)
}

// A host type with sibling host functions and a sibling interface impl round-trips.
func TestFormat_ExternTypeBody(t *testing.T) {
	src := `pub host type Channel<T>

pub fn label<T>(_c: Channel<T>): String {
    "channel"
}

host fn next<T>(s: Channel<T>): Maybe<(T, Channel<T>)>

impl iter for Channel<T>

`
	roundTrip(t, src)
}

// A bodiless host type stays bodiless; an EMPTY body's braces are
// dropped (canonical: no body).
func TestFormat_ExternTypeEmptyBodyDropsBraces(t *testing.T) {
	roundTrip(t, "pub host type Handle\n")
}

// A comment about an host type stays above the bodiless declaration.
func TestFormat_ExternTypeLeadingCommentRoundTrips(t *testing.T) {
	src := `// host-provided; ops arrive in a later phase
pub host type Handle
`
	roundTrip(t, src)
}

// A zero-sized distinct type with a typed-literal helper round-trips.
func TestFormat_TypeDefBody_ZeroSizedWithImpl(t *testing.T) {
	src := `type Sql

fn from_fragments(_fragments: List<Fragment<String>>): String {
    "q"
}

impl Literal for Sql {
    fn from_fragments(fragments: List<Fragment<String>>): String {
        from_fragments(fragments)
    }
}

`
	roundTrip(t, src)
}

// An opaque distinct type with sibling constructors round-trips.
func TestFormat_TypeDefBody_OpaqueDistinct(t *testing.T) {
	src := `pub opaque type Id Int

pub fn zero(): Id {
    Id(0)
}
`
	roundTrip(t, src)
}

// Interface with all four member kinds — field requirement, required fn,
// All interface member kinds with docs: field requirement, required method,
// open default, final default, host-backed default (host fn) — each carrying
// a doc comment, blank-line separated.
func TestFormat_InterfaceAllMemberKindsWithDocs(t *testing.T) {
	src := `pub interface Greeter {
    /// The name.
    field name: String
    /// Required.
    fn greet(g: self): String

    /// Overridable default.
    open fn hello(_g: self): String {
        "hello"
    }

    /// Final default.
    fn sealed(_g: self): String {
        "sealed"
    }
    /// Host-backed default.
    host fn fast_path(g: self): String
}
`
	roundTrip(t, src)
}

// `open host fn` (overridable host-backed default) round-trips with both
// modifiers preserved.
func TestFormat_InterfaceOpenExternDefault(t *testing.T) {
	src := `interface Seq {
    fn next(s: self): Int
    open host fn sort(s: self): Int
}
`
	roundTrip(t, src)
}

// A method-level `where` clause on an interface default method round-trips —
// emitted inline after the return type, before the body brace.
func TestFormat_InterfaceMethodWhereClause(t *testing.T) {
	src := `interface Sortable<T> {
    fn sort_by_key<K>(
        source: self,
        _key_of_each_element: (T) -> K,
    ): List<T> where T: Comparable, K: Hashable {
        source
    }
}
`
	roundTrip(t, src)
}

func TestFormat_InterfaceRequiredMethodWhereClause(t *testing.T) {
	src := `interface Sortable<T> {
    fn sort_by_key<K>(
        source: self,
        key_of_each_element: (T) -> K,
    ): List<T> where T: Comparable, K: Hashable
}
`
	roundTrip(t, src)
}

func TestFormat_FuncWhereClause(t *testing.T) {
	src := `fn step_by<T, S>(
    range_to_step_through: Range<T>,
    _by: S,
): Iter<T> where T: Comparable and Steppable<S> {
    r
}
`
	roundTrip(t, src)
}

func TestFormat_ExternFuncWhereClause(t *testing.T) {
	src := `host fn sorted<T>(items: List<T>): List<T> where T: Comparable
`
	roundTrip(t, src)
}

// A derived struct keeps each derive entry as its own line, alongside the
// method block for the hand-written conformance.
func TestFormat_DerivedStructWithBody(t *testing.T) {
	src := `pub struct Tag {
    name: String
}

derive Equatable for Tag
derive Hashable for Tag

impl Speech for Tag {
    fn speak(t: Tag): String {
        t.name
    }
}

`
	roundTrip(t, src)
}

func TestFormat_ConformanceCommentsPreserveGroupingBoundaries(t *testing.T) {
	cases := []string{
		`pub opaque struct Date {
    year: Int
    month: Int
    day: Int
}

derive Equatable for Date

// some comment
derive Hashable for Date
derive Comparable for Date

impl Literal for Date

impl Display for Date

impl Debug for Date

impl DateParts for Date

impl Shift for Date

`,
		`pub opaque struct Date {
    year: Int
    month: Int
    day: Int
}

// a comment
derive Equatable for Date
derive Hashable for Date
derive Comparable for Date

impl Literal for Date

impl Display for Date

impl Debug for Date

impl DateParts for Date

impl Shift for Date

`,
	}
	for _, src := range cases {
		got, err := Format(src)
		if err != nil {
			t.Fatalf("Format error: %v", err)
		}
		if got != src {
			t.Fatalf("first format changed commented conformance groups:\nwant:\n%sgot:\n%s", src, got)
		}
		got2, err := Format(got)
		if err != nil {
			t.Fatalf("second Format error: %v", err)
		}
		if got2 != got {
			t.Fatalf("second format was not idempotent:\nfirst:\n%ssecond:\n%s", got, got2)
		}
	}
}

// Doc comments above interface contract members (required fn, open fn
// default, field requirement) round-trip through the formatter. Before the
// InterfaceMethod/InterfaceField Doc slots existed, `nomi fmt -w` deleted
// these lines outright.
func TestFormat_InterfaceContractMemberDocsPreserved(t *testing.T) {
	src := `interface Speech {
    /// The speaker's name.
    field name: String

    /// The required noise.
    fn speak(s: self): String

    /// Overridable politeness.
    open fn greet(_s: self): String {
        "hi"
    }
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	for _, want := range []string{
		"/// The speaker's name.",
		"/// The required noise.",
		"/// Overridable politeness.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted output lost doc comment %q:\n%s", want, got)
		}
	}
	// Doc lines must sit immediately above their member.
	if !strings.Contains(got, "/// The required noise.\n    fn speak(s: self): String") {
		t.Errorf("doc comment not attached above required method:\n%s", got)
	}
	if !strings.Contains(got, "/// Overridable politeness.\n    open fn greet(_s: self): String") {
		t.Errorf("doc comment not attached above open default method:\n%s", got)
	}
	if !strings.Contains(got, "/// The speaker's name.\n    field name: String") {
		t.Errorf("doc comment not attached above field requirement:\n%s", got)
	}
	roundTrip(t, got)
}

// Doc comments on struct fields survive formatting.
func TestFormat_StructFieldDocPreserved(t *testing.T) {
	src := `struct Dog {
  /// The dog's name.
  name: String
}
`
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	if !strings.Contains(got, "/// The dog's name.") {
		t.Errorf("formatted output lost struct-field doc comment:\n%s", got)
	}
	roundTrip(t, got)
}
