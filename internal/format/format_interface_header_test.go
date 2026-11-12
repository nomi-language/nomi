package format

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Interface-header surface syntax — formatter emission.
//
// `derive A for T` asks the compiler to synthesize impls.
// Manual interface impls are top-level `impl Iface for Type { ... }` blocks
// with plain functions.
//
// The formatter keeps derives near the type shape and formats
// impl blocks like ordinary top-level declarations.
// ---------------------------------------------------------------------------

// A struct with sibling file functions, `derive`, and interface impl blocks
// round-trips.
func TestFormat_StructBodyConformanceAndMethods(t *testing.T) {
	src := `struct Money
    cents: Int
end

fn doubled(m: Money) -> Money
    Money{cents: m.cents * 2}
end

derive Equatable for Money

impl Display for Money
    fn to_string(_m: Money) -> String
        "money"
    end
end

`
	roundTrip(t, src)
}

// An host type with a derive and many impl blocks renders them as ordinary
// top-level declarations.
func TestFormat_TightConformanceBlock(t *testing.T) {
	src := `pub host type List<T>

derive Equatable for List

impl Display for List<T> end

impl Debug for List<T> end

impl Hashable for List<T> end

impl Comparable for List<T> end

impl iter for List<T> end

`
	roundTrip(t, src)
}

// `fmt` keeps sibling derive declarations near the type and leaves impl blocks
// as sibling top-level declarations.
func TestFormat_OrdersConformancesAndNestedImplBlocks(t *testing.T) {
	src := `struct Dog
    name: String
end

fn rename(d: Dog) -> Dog
    d
end

derive Equatable for Dog

impl Speech for Dog
    fn speak(_d: Dog) -> String
        "woof"
    end
end

`
	want := `struct Dog
    name: String
end

fn rename(d: Dog) -> Dog
    d
end

derive Equatable for Dog

impl Speech for Dog
    fn speak(_d: Dog) -> String
        "woof"
    end
end

`
	migrates(t, src, want)
}

// Sibling derives stay as ordinary top-level declarations.
func TestFormat_HoistConsolidatesScatteredConformances(t *testing.T) {
	src := `struct Point
    x: Int
end

pub once origin: Point = Point{x: 0}

derive Equatable for Point

derive Hashable for Point


`
	want := `struct Point
    x: Int
end

pub once origin: Point = Point{x: 0}

derive Equatable for Point
derive Hashable for Point

`
	migrates(t, src, want)
}

// An already-ordered body is unchanged — the reorder is a no-op when fields,
// derives, and value items are already in house order.
func TestFormat_HoistIdempotentWhenOrdered(t *testing.T) {
	src := `struct Dog
    name: String
end

fn rename(d: Dog) -> Dog
    d
end

derive Equatable for Dog

impl Speech for Dog
    fn speak(_d: Dog) -> String
        "woof"
    end
end

`
	roundTrip(t, src)
}

// A single standalone derive entry stays standalone.
func TestFormat_SingleConformanceStandalone(t *testing.T) {
	src := `struct Point
    x: Int
end

derive Equatable for Point

`
	roundTrip(t, src)
}

// Adjacent derive declarations stay as repeated sibling declarations.
func TestFormat_ConformanceRunTight(t *testing.T) {
	src := `struct Point
    x: Int
end

derive Equatable for Point
derive Hashable for Point

impl Debug for Point end

impl Display for Point
    fn to_string(_p: self) -> String
        "pt"
    end
end

`
	roundTrip(t, src)
}

func TestFormat_LongConformanceListWrapsPacked(t *testing.T) {
	src := `struct Stamp
    year: Int
end

impl Literal for Stamp end

impl Display for Stamp end

impl Debug for Stamp end

impl DateParts for Stamp end

impl TimeParts for Stamp end

impl Anchored for Stamp end

impl Advance for Stamp end

impl Shift for Stamp end

impl TimeShift for Stamp end

impl Equatable for Stamp end

impl Hashable for Stamp end

impl Comparable for Stamp end

`
	want := `struct Stamp
    year: Int
end

impl Literal for Stamp end

impl Display for Stamp end

impl Debug for Stamp end

impl DateParts for Stamp end

impl TimeParts for Stamp end

impl Anchored for Stamp end

impl Advance for Stamp end

impl Shift for Stamp end

impl TimeShift for Stamp end

impl Equatable for Stamp end

impl Hashable for Stamp end

impl Comparable for Stamp end

`
	migrates(t, src, want)
}

func TestFormat_BlankLineKeepsSameKindConformanceGroupsSeparate(t *testing.T) {
	src := `struct Stamp
    year: Int
end

impl Literal for Stamp end

impl Display for Stamp end

impl Debug for Stamp end

impl DateParts for Stamp end

`
	roundTrip(t, src)
}

// A foreign type implementing two interfaces is two separate top-level
// `impl ... for` blocks, blank-line separated.
func TestFormat_MultipleImplementsForBlocks(t *testing.T) {
	src := `impl Display for Money
    fn to_string(_m: self) -> String
        "money"
    end
end

impl Debug for Money
    fn inspect(_m: Money) -> String
        "money"
    end
end
`
	roundTrip(t, src)
}

// An extern-type body with an interface host fn round-trips.
func TestFormat_ExternTypeBodyImplMethod(t *testing.T) {
	src := `host type Widget

impl Display for Widget
    host fn to_string(w: self) -> String
end

`
	roundTrip(t, src)
}

// A top-level `impl Iface for Type { ... }` block round-trips.
func TestFormat_InterfaceHeaderImplBlock(t *testing.T) {
	src := `impl Display for Point
    fn to_string(_p: self) -> String
        "point"
    end
end
`
	roundTrip(t, src)
}

// A generic receiver `impl Iface for Box<T> { ... }` block round-trips.
func TestFormat_GenericImplBlock(t *testing.T) {
	src := `impl iter for Box<T>
    fn next(_b: self) -> Maybe<(T, Box<T>)>
        .None
    end
end
`
	roundTrip(t, src)
}

func TestFormat_ImplBlockWhereClause(t *testing.T) {
	src := `impl iter for Box<T> where T: Display and Debug
    fn next(_b: self) -> Maybe<(T, Box<T>)>
        .None
    end
end
`
	roundTrip(t, src)
}

func TestFormat_EmptyImplBlockWhereClause(t *testing.T) {
	src := `impl Marker for Box<T> where T: Hashable end
`
	roundTrip(t, src)
}

func TestFormat_DeriveWhereClause(t *testing.T) {
	src := `derive Display for Box<T> where T: Display
`
	roundTrip(t, src)
}

func TestFormat_DeriveOptions(t *testing.T) {
	src := `derive ToJson for User with ToJson.Options{rename_all: Json.Case.Camel}
`
	roundTrip(t, src)
}

func TestFormat_DeriveOptionsBeforeWhereClause(t *testing.T) {
	src := `derive ToJson for Box<T> with ToJson.Options{
    rename_all: Json.Case.Camel,
    omit_empty_fields: True,
} where T: ToJson
`
	roundTrip(t, src)
}

func TestFormat_TypeDeclarationWhereClauses(t *testing.T) {
	src := `struct Box<T> where T: Display
    value: T
end

enum MaybeBox<T> where T: Display
    Empty
    Full Box<T>
end

interface Renderable<T> where T: Display
    fn render(value: self) -> String
end

host type Handle<T> where T: Display
`
	roundTrip(t, src)
}

func TestFormat_ConstrainedTypeBodyImpl(t *testing.T) {
	src := `struct Range<T> where T: Comparable
    start: T
end

impl Display for Range<T> end

impl Iter for Range<T> where T: Discrete
    fn next(_r: Range<T>) -> Maybe<(T, Range<T>)>
        none
    end
end

`
	roundTrip(t, src)
}

// A module-qualified interface in an `impl ... for` header
// (`impl json.Encode for Money`) round-trips.
func TestFormat_QualifiedInterfaceImplBlock(t *testing.T) {
	src := `impl json.Encode for Money
    fn encode(_m: self) -> String
        "json"
    end
end
`
	roundTrip(t, src)
}

// An empty `impl ... for` body is one line, `impl Iface for Type end`.
func TestFormat_EmptyImplBlock(t *testing.T) {
	src := `impl Display for Point end
`
	roundTrip(t, src)
	migrates(t, "impl Display for Point\nend\n", src)
	src = `impl Display for Point
    // later
end
`
	roundTrip(t, src)
}

// Unformatted input (no blank line between methods) migrates to the canonical
// blank-separated form and lands on a fixed point.
func TestFormat_ImplBlockBlankLineMigration(t *testing.T) {
	src := `impl Display for Point
    fn to_string(_p: self) -> String
        "point"
    end
    fn other(_p: self) -> String
        "x"
    end
end
`
	want := `impl Display for Point
    fn to_string(_p: self) -> String
        "point"
    end

    fn other(_p: self) -> String
        "x"
    end
end
`
	migrates(t, src, want)
}

// Consecutive derive entries stay as consecutive lines, with a blank line
// before the next top-level impl block.
func TestFormat_TightConformanceLinesStable(t *testing.T) {
	src := `struct Money
    cents: Int
end

derive Equatable for Money

impl Display for Money
    fn to_string(_m: self) -> String
        "money"
    end
end

`
	roundTrip(t, src)
}

func TestFormat_GroupedConformanceBlockRejected(t *testing.T) {
	src := `struct Point
  x: Int
  impl
    derive Equatable
  end
end
`
	got, err := Format(src)
	if err == nil {
		t.Fatal("expected parse error for grouped conformance block, got nil")
	}
	if !strings.Contains(err.Error(), "interface implementations are written as `impl Iface for Type` blocks outside the type body") {
		t.Errorf("expected grouped-block rejection, got %q", err.Error())
	}
	if got != src {
		t.Errorf("expected original source returned on parse error, got %q", got)
	}
}

func TestFormat_QualifiedImplMethodRoundTrips(t *testing.T) {
	src := `struct Money
    cents: Int
end

impl Display for Money
    fn to_string(_m: self) -> String
        "money"
    end
end

impl Debug for Money end

`
	roundTrip(t, src)
}

// The old `derive impl` spelling is rejected; the error points at the current
// `derive Iface` syntax.
func TestFormat_OldDeriveConformanceKeywordRejected(t *testing.T) {
	src := `struct Money
  cents: Int

  derive impl Equatable
end
`
	got, err := Format(src)
	if err == nil {
		t.Fatal("expected parse error for old `derive impl` conformance spelling, got nil")
	}
	if !strings.Contains(err.Error(), "derive Iface") {
		t.Errorf("expected error pointing at `derive Iface`, got %q", err.Error())
	}
	if got != src {
		t.Errorf("expected original source returned on parse error, got %q", got)
	}
}

// The `derives` keyword is rejected; the error points at the `derive Iface`
// syntax.
func TestFormat_DerivesKeywordRejected(t *testing.T) {
	src := `struct Money
  cents: Int

  derives Equatable
end
`
	got, err := Format(src)
	if err == nil {
		t.Fatal("expected parse error for the `derives` keyword, got nil")
	}
	if !strings.Contains(err.Error(), "derive Iface") {
		t.Errorf("expected error pointing at `derive Iface`, got %q", err.Error())
	}
	if got != src {
		t.Errorf("expected original source returned on parse error, got %q", got)
	}
}
