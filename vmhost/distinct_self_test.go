package vmhost_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// Every distinct type `type X T` is a `Distinct<T>`. An interface default
// bounded by `where self: Distinct<T>` takes `self` apart with `self(p)` and
// builds one with `self(x)`; it is lowered once per implementor, so each is
// that implementor's own destructuring and constructor.

const distinctTemperature = `type Celsius Decimal
type Fahrenheit Decimal

interface Temperature {
    fn unit(t: self) -> String

    fn label(self(n) as t) -> String where self: Distinct<Decimal> {
        "${Decimal.round(n, 1, .HalfUp)} ${Temperature.unit(t)}"
    }

    fn scale(self(n), by: Decimal) -> self where self: Distinct<Decimal> {
        self(n * by)
    }

    fn all(_t: self, ns: List<Decimal>) -> String where self: Distinct<Decimal> {
        ns |> Iter.map(self) |> Iter.to_list() |> Debug.inspect()
    }

    fn piped(_t: self, n: Decimal) -> self where self: Distinct<Decimal> {
        n |> self()
    }

    fn warmer(a: self, b: self) -> self where self: Distinct<Decimal> {
        self(m) = b
        case a {
            self(n) when n > m -> a
            _ -> self(m)
        }
    }

    fn sign(t: self) -> String where self: Distinct<Decimal> {
        case t {
            self(0d) -> "zero"
            self(n) when n < 0d -> "below"
            self(_) -> "above"
        }
    }
}

impl Temperature for Celsius {
    fn unit(_t: Celsius) -> String {
        "C"
    }
}

impl Temperature for Fahrenheit {
    fn unit(_t: Fahrenheit) -> String {
        "F"
    }
}
`

func TestDistinctSelf_TemperatureRuns(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": "import std/io\n\n" + distinctTemperature + `
fn show<T>(t: T) -> String where T: Temperature {
    T.label(t)
}

fn main() {
    io.print(Temperature.label(Celsius(21.456d)))
    io.print(Temperature.label(Fahrenheit(70d)))
    io.inspect(Celsius.scale(Celsius(10d), 2d))
    io.inspect(Temperature.scale(Fahrenheit(1.5d), 2d))
    io.print(Temperature.all(Celsius(0d), [1d, 2d]))
    io.inspect(Fahrenheit.piped(Fahrenheit(0d), 4d))
    io.inspect(Temperature.warmer(Celsius(1d), Celsius(3d)))
    io.inspect(Temperature.warmer(Celsius(5d), Celsius(3d)))
    io.print(Temperature.sign(Celsius(0d)))
    io.print(Temperature.sign(Celsius(-1d)))
    io.print(Temperature.sign(Fahrenheit(3d)))
    io.print(show(Fahrenheit(3d)))
    temps: List<Temperature> = [Celsius(1d), Fahrenheit(2d)]
    temps |> Iter.map(fn t -> Temperature.label(t)) |> String.join(", ") |> io.print()
}
`})
	want := "21.5 C\n70.0 F\nCelsius(20d)\nFahrenheit(3.0d)\n[Celsius(1d), Celsius(2d)]\nFahrenheit(4d)\n" +
		"Celsius(3d)\nCelsius(5d)\nzero\nbelow\nabove\n3.0 F\n1.0 C, 2.0 F\n"
	if got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// `T` in `where self: Distinct<T>` may be the interface's own type
// parameter, so one interface serves wrappers of different types.
func TestDistinctSelf_GenericInnerRuns(t *testing.T) {
	got := runSiblingProgram(t, map[string]string{"main.nomi": `import std/io

type UserId Int
type Name String

interface Wrapper<T> {
    fn unwrap(self(x)) -> T where self: Distinct<T> {
        x
    }

    fn rewrap(w: self, f: (T) -> T) -> self where self: Distinct<T> {
        self(x) = w
        self(f(x))
    }

    fn show(self(x)) -> String where self: Distinct<T> {
        "<${Debug.inspect(x)}>"
    }
}

impl Wrapper<Int> for UserId {}

impl Wrapper<String> for Name {}

fn main() {
    io.print(Wrapper.unwrap(UserId(7)) + 1)
    io.print(Wrapper.unwrap(Name("ada")))
    io.inspect(UserId.rewrap(UserId(1), fn n -> n * 10))
    io.inspect(Name.rewrap(Name("a"), fn s -> "${s}b"))
    io.print(Wrapper.show(Name("x")))
    io.print(Wrapper.show(UserId(3)))
}
`})
	if want := "8\nada\nUserId(10)\nName(\"ab\")\n<\"x\">\n<3>\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

// `Distinct<T>` is a bound like any other: a generic function bounded by it
// takes a distinct type wrapping T.
func TestDistinctSelf_BoundOnAFunction(t *testing.T) {
	src := `import std/io

type UserId Int
type Name String

fn tag<D>(d: D) -> String where D: Distinct<Int> {
    "#${Debug.inspect(d)}"
}

fn main() {
    io.print(tag(UserId(4)))
}
`
	if got := runSiblingProgram(t, map[string]string{"main.nomi": src}); got != "#UserId(4)\n" {
		t.Fatalf("output %q", got)
	}
	rejects(t, strings.Replace(src, "tag(UserId(4))", "tag(Name(\"x\"))", 1),
		"Name does not implement Distinct")
	rejects(t, `type UserId Int

impl Distinct<Int> for UserId {}

fn main() {
    Unit
}
`,
		"main.nomi:3:1: `Distinct` is a built-in marker and cannot be implemented")
}

const distinctScaled = `pub interface Scaled {
    fn describe(s: self) -> String

    fn scale(self(n), by: Int) -> self where self: Distinct<Int> {
        self(n * by)
    }
}
`

const distinctMeters = `pub opaque type Meters Int

pub fn of(n: Int) -> Meters {
    Meters(n)
}

pub fn raw(Meters(n)) -> Int {
    n
}
`

// An opaque type implemented in its own file gets a default that takes it
// apart and builds it. Implemented outside that file it does not: the impl
// is an error until it writes the function, which a final default then
// allows.
func TestDistinctSelf_OpaqueImplementors(t *testing.T) {
	positive := `import scaled.Scaled

pub opaque type PositiveInt Int

pub fn of(n: Int) -> PositiveInt {
    PositiveInt(n)
}

impl Scaled for PositiveInt {
    fn describe(PositiveInt(n)) -> String {
        "+${n}"
    }
}
`
	written := `import {
    std/io
    meters.{self, Meters}
    positive
    scaled.Scaled
}

impl Scaled for Meters {
    fn describe(m: Meters) -> String {
        "${meters.raw(m)} m"
    }

    fn scale(m: Meters, by: Int) -> Meters {
        meters.of(meters.raw(m) * by)
    }
}

fn main() {
    io.print(Scaled.describe(Scaled.scale(positive.of(3), 2)))
    io.print(Scaled.describe(Scaled.scale(meters.of(4), 3)))
}
`
	got := runSiblingProgram(t, map[string]string{
		"main.nomi": written, "scaled.nomi": distinctScaled, "positive.nomi": positive, "meters.nomi": distinctMeters,
	})
	if want := "+6\n12 m\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}

	unwritten := strings.Replace(written, `
    fn scale(m: Meters, by: Int) -> Meters {
        meters.of(meters.raw(m) * by)
    }
`, "", 1)
	err := checkSiblingFiles(t, map[string]string{
		"main.nomi": unwritten, "scaled.nomi": distinctScaled, "positive.nomi": positive, "meters.nomi": distinctMeters,
	})
	want := "main.nomi:8:1: `Meters` is opaque here, so it can't use `Scaled.scale`'s default, which builds and takes apart `self`; write `fn scale` in this impl"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("diagnostic lacks %q:\n%v", want, err)
	}
}

// checkSiblingFiles checks a module whose entry is main.nomi, as `nomi
// check` does, and returns its diagnostics.
func checkSiblingFiles(t *testing.T, files map[string]string) error {
	t.Helper()
	dir := t.TempDir()
	files["nomi.toml"] = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return vmhost.Check(filepath.Join(dir, "main.nomi"))
}

func TestDistinctSelf_NeedsTheBound(t *testing.T) {
	src := `interface Shown {
    fn label(self(n)) -> String where self: Display {
        "${n}"
    }
}

fn main() {
    Unit
}
`
	rejects(t, src,
		"main.nomi:2:14: `self(...)` needs `where self: Distinct<T>` on this function",
		"with T the type every implementor wraps")
	rejects(t, strings.Replace(src, `"${n}"`, `_ = self(n)
        "x"`, 1)+"\n",
		"main.nomi:2:14: `self(...)` needs `where self: Distinct<T>`")
	admits(t, strings.Replace(src, "where self: Display", "where self: Distinct<Int>", 1))
}

func TestDistinctSelf_OnlyInADefault(t *testing.T) {
	rejects(t, `type Celsius Decimal

impl Celsius {
    fn make(n: Decimal) -> Celsius {
        self(n)
    }
}

fn free(n: Decimal) -> Int {
    _ = self(n)
    1
}

fn main() {
    _ = free(1d)
    _ = Celsius.make(1d)
}
`,
		"main.nomi:5:9: `self(...)` only appears in an interface's default function",
		"an impl names its own type: write `Celsius(...)`",
		"main.nomi:10:9: `self(...)` only appears in an interface's default function")
	admits(t, `type Celsius Decimal

impl Celsius {
    fn make(n: Decimal) -> Celsius {
        Celsius(n)
    }
}

fn main() {
    _ = Celsius.make(1d)
}
`)
}

// The pattern's payload and the constructor's argument are checked against
// what `self` wraps.
func TestDistinctSelf_InnerTypeMismatch(t *testing.T) {
	src := `interface Temperature {
    fn bad(self(s)) -> String where self: Distinct<Decimal> {
        case s {
            "x" -> "y"
            _ -> "z"
        }
    }

    fn build(_t: self) -> self where self: Distinct<Decimal> {
        self("no")
    }
}

fn main() {
    Unit
}
`
	rejects(t, src,
		"main.nomi:4:13: pattern \"x\" is a String, but the value is a Decimal",
		"main.nomi:10:14: `self` wraps Decimal, so self(...) takes a Decimal; got String")
	admits(t, strings.NewReplacer(`"x" -> "y"`, `0d -> "y"`, `self("no")`, `self(1d)`).Replace(src))
}

// An implementor that is not a distinct type, or wraps another type, does
// not meet the bound, as any `where self` bound is reported at the impl.
func TestDistinctSelf_ImplementorMustWrapT(t *testing.T) {
	src := `type Count Int

struct Point {
    x: Int
}

interface Temperature {
    fn label(self(n)) -> String where self: Distinct<Decimal> {
        "${n}"
    }
}

impl Temperature for Count {}

impl Temperature for Point {}

fn main() {
    Unit
}
`
	rejects(t, src,
		"main.nomi:13:1: `Count` does not implement Distinct<Decimal>, which `Temperature.label`'s default requires (`where self: Distinct<Decimal>`)",
		"`Count` wraps Int, so it is not a Distinct<Decimal>; write `fn label` in this impl",
		"main.nomi:15:1: `Point` does not implement Distinct<Decimal>",
		"`Point` is not a distinct type, so it is not a Distinct<Decimal>; write `fn label` in this impl")
	admits(t, strings.NewReplacer("impl Temperature for Count {}", `impl Temperature for Count {
    fn label(_c: Count) -> String {
        "count"
    }
}`, "impl Temperature for Point {}", `impl Temperature for Point {
    fn label(_p: Point) -> String {
        "point"
    }
}`).Replace(src))
}

// `self(p)` takes apart a `self` value only: an argument typed with the
// interface's name is any implementor, and a literal payload is refutable in
// a parameter. A `case` over `self` patterns must cover the wrapped value.
func TestDistinctSelf_PatternPlaces(t *testing.T) {
	rejects(t, `interface Temperature {
    fn other(_t: self, x: Temperature) -> Int where self: Distinct<Int> {
        self(n) = x
        n
    }
}

fn main() {
    Unit
}
`,
		"main.nomi:3:9: `self(...)` takes apart a value of type `self`, got Temperature")
	rejects(t, `interface Temperature {
    fn zero(self(0)) -> Int where self: Distinct<Int> {
        0
    }
}

fn main() {
    Unit
}
`,
		"main.nomi:2: refutable pattern in parameter")
	src := `interface Temperature {
    fn sign(t: self) -> String where self: Distinct<Int> {
        case t {
            self(0) -> "zero"
            self(n) when n < 0 -> "below"
        }
    }
}

fn main() {
    Unit
}
`
	rejects(t, src, "main.nomi:3:9: non-exhaustive case: add a `_` arm")
	admits(t, strings.Replace(src, `self(n) when n < 0 -> "below"`, `self(n) when n < 0 -> "below"
            self(_) -> "above"`, 1))
}
