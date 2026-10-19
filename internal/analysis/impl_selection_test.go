package analysis_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// A generic interface implemented for one receiver at several type
// arguments: the checker's selection, ambiguity, overlap, duplicate and
// orphan rules (impl_selection.go, impl_coherence.go).

// holdsBox is a user interface with three instantiations for one user type.
const holdsBox = `
pub interface Holds<T> {
    fn holds?(b: self, x: T) -> Bool
}

pub enum Color {
    Red
    Green
}

pub struct Box {
    n: Int
    s: String
}

impl Holds<Int> for Box {
    fn holds?(b: Box, x: Int) -> Bool {
        b.n == x
    }
}

impl Holds<String> for Box {
    fn holds?(b: Box, x: String) -> Bool {
        b.s == x
    }
}

impl Holds<Color> for Box {
    fn holds?(b: Box, x: Color) -> Bool {
        b.n == 0 and x == .Red
    }
}

fn via<C, T>(c: C, x: T) -> Bool where C: Holds<T> {
    C.holds?(c, x)
}
`

// errorsBesidesUnused drops the unused-binding diagnostics a fixture's
// helpers draw.
func errorsBesidesUnused(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if e.Code != analysis.UnusedBindingCode {
			out = append(out, e)
		}
	}
	return out
}

func expectSelectionClean(t *testing.T, errs []analysis.TypeError) {
	t.Helper()
	for _, e := range errorsBesidesUnused(errs) {
		t.Errorf("unexpected diagnostic at %d:%d: %s", e.Line, e.Col, e.Message)
	}
}

// expectOnly requires the fixture to be rejected, every diagnostic to hold
// want, and at least one to: a program that starts being accepted fails.
func expectRejected(t *testing.T, errs []analysis.TypeError, want ...string) {
	t.Helper()
	errs = errorsBesidesUnused(errs)
	if len(errs) == 0 {
		t.Fatalf("the program was accepted; want an error containing %q", want)
	}
	for _, w := range want {
		assertErrorContains(t, errs, w)
	}
}

func TestImplSelection_EveryCallFormChoosesByTheArgument(t *testing.T) {
	errs := buildProjectExpectingErrors(t, holdsBox+`
fn main() {
    b = #Box{n: 1, s: "a"}
    direct: Bool = Box.holds?(b, 1)
    qualified: Bool = Holds.holds?(b, "a")
    variant: Bool = Box.holds?(b, Color.Red)
    piped: Bool = b |> Box.holds?("b")
    bounded: Bool = via(b, 2)
    partial: (String) -> Bool = &Holds.holds?(b, &1)
    _ok = direct and qualified and !variant and !piped and !bounded and !partial("z")
}
`, nil)
	expectSelectionClean(t, errs)
}

func TestImplSelection_TwoInstantiationsAndAnOperator(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `
pub type Meters Int
pub type Feet Int

fn meters(m: Meters) -> Int {
    Meters(n) = m
    n
}

impl Add<Meters, Meters> for Meters {
    fn add(lhs: Meters, rhs: Meters) -> Meters {
        Meters(meters(lhs) + meters(rhs))
    }
}

impl Add<Feet, Meters> for Meters {
    fn add(lhs: Meters, rhs: Feet) -> Meters {
        Feet(f) = rhs
        Meters(meters(lhs) + f)
    }
}

fn main() {
    a = Meters(1) + Meters(2)
    b = Meters(1) + Feet(3)
    c = Add.add(Meters(1), Feet(3))
    _ok = meters(a) + meters(b) + meters(c) == 11
}
`, nil)
	expectSelectionClean(t, errs)
}

func TestImplSelection_AnUndeterminedArgumentIsAnError(t *testing.T) {
	for name, call := range map[string]string{
		"variant":     "Box.holds?(b, .Red)",
		"placeholder": "&Holds.holds?(b, &1)",
	} {
		t.Run(name, func(t *testing.T) {
			errs := buildProjectExpectingErrors(t, holdsBox+`
fn main() {
    b = #Box{n: 1, s: "a"}
    _x = `+call+`
}
`, nil)
			expectRejected(t, errs, "`Holds.holds?` for `Box` has impls at `Int`, `String` and `Color`; the argument's type does not choose one: annotate it")
		})
	}
}

func TestImplSelection_NoImplTakesTheArgument(t *testing.T) {
	errs := buildProjectExpectingErrors(t, holdsBox+`
fn main() {
    b = #Box{n: 1, s: "a"}
    _x = Box.holds?(b, 1.5)
}
`, nil)
	expectRejected(t, errs, "`Holds.holds?` for `Box` has impls at `Int`, `String` and `Color`, and none takes `Float`")
}

func TestImplSelection_ABoundTheCallLeavesOpenIsAnError(t *testing.T) {
	errs := buildProjectExpectingErrors(t, holdsBox+`
fn main() {
    b = #Box{n: 1, s: "a"}
    _x = via(b, .Red)
}
`, nil)
	expectRejected(t, errs, "`Box` implements `Holds` at `Int`, `String` and `Color`, and the call's arguments do not choose one")
}

// A type argument no parameter names is chosen by the expected type, as a
// generic call's result-only type parameter is.
func TestImplSelection_TheExpectedTypeChoosesAResultOnlyArgument(t *testing.T) {
	const factory = `
pub interface Make<T> {
    fn make(seed: self) -> T
}

pub struct Factory {
    n: Int
}

impl Make<Int> for Factory {
    fn make(seed: Factory) -> Int {
        seed.n
    }
}

impl Make<String> for Factory {
    fn make(seed: Factory) -> String {
        Int.to_string(seed.n)
    }
}

fn made<C, T>(c: C) -> T where C: Make<T> {
    C.make(c)
}
`
	t.Run("annotated", func(t *testing.T) {
		errs := buildProjectExpectingErrors(t, factory+`
fn main() {
    f = #Factory{n: 7}
    i: Int = Factory.make(f)
    s: String = Make.make(f)
    j: Int = made(f)
    _ok = i + j == 14 and s == "7"
}
`, nil)
		expectSelectionClean(t, errs)
	})
	t.Run("unannotated", func(t *testing.T) {
		errs := buildProjectExpectingErrors(t, factory+`
fn main() {
    _x = Factory.make(#Factory{n: 7})
}
`, nil)
		expectRejected(t, errs, "`Make.make` for `Factory` has impls at `Int` and `String`; only the result's type tells them apart, and nothing here expects one: annotate the result")
	})
	t.Run("operator", func(t *testing.T) {
		errs := buildProjectExpectingErrors(t, `
pub type Day Int
pub type Days Int

fn day(d: Day) -> Int {
    Day(n) = d
    n
}

impl Add<Days, Day> for Day {
    fn add(lhs: Day, rhs: Days) -> Day {
        Days(n) = rhs
        Day(day(lhs) + n)
    }
}

impl Add<Days, Days> for Day {
    fn add(lhs: Day, rhs: Days) -> Days {
        Days(n) = rhs
        Days(day(lhs) + n)
    }
}

fn main() {
    later: Day = Day(1) + Days(2)
    gap: Days = Day(1) + Days(2)
    _x = Day(1) + Days(2)
}
`, nil)
		expectRejected(t, errs, "`Add.add` for `Day` has impls at `Days, Day` and `Days, Days`; only the result's type tells them apart")
		if n := len(errorsBesidesUnused(errs)); n != 1 {
			t.Errorf("want only the unannotated sum rejected, got %d errors: %v", n, errs)
		}
	})
}

func TestImplSelection_AGenericImplBesideAConcreteOneOverlaps(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `
pub interface Holds<T> {
    fn holds?(b: self, x: T) -> Bool
}

pub struct Wrap<T> {
    v: T
}

impl Holds<T> for Wrap<T> where T: Equatable {
    fn holds?(b: Wrap<T>, x: T) -> Bool {
        b.v == x
    }
}

impl Holds<Int> for Wrap<T> {
    fn holds?(b: Wrap<T>, x: Int) -> Bool {
        x == 0
    }
}

fn main() {
}
`, nil)
	expectRejected(t, errs, "overlapping impls: `Holds<T>` for `Wrap` is generic in its type arguments, so it covers `Holds<Int>`, which `Wrap` also implements")
}

func TestImplSelection_TheSameArgumentsTwiceAreADuplicate(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `
pub interface Holds<T> {
    fn holds?(b: self, x: T) -> Bool
}

pub struct Box {
    n: Int
}

impl Holds<Int> for Box {
    fn holds?(b: Box, x: Int) -> Bool {
        b.n == x
    }
}

impl Holds<Int> for Box {
    fn holds?(b: Box, x: Int) -> Bool {
        b.n != x
    }
}

fn main() {
}
`, nil)
	expectRejected(t, errs, "duplicate impl: `Holds<Int>` is implemented for `Box`", "once at each set of type arguments")
}

func TestImplSelection_SeveralImplsEachWriteTheirArguments(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `
pub interface Holds<T> {
    fn holds?(b: self, x: T) -> Bool
}

pub struct Box {
    n: Int
}

impl Holds for Box {
    fn holds?(b: Box, x: Int) -> Bool {
        b.n == x
    }
}

impl Holds<String> for Box {
    fn holds?(b: Box, x: String) -> Bool {
        x == ""
    }
}

fn main() {
}
`, nil)
	expectRejected(t, errs, "`Box` implements `Holds` more than once, so each impl writes its type arguments in its header", "write `impl Holds<Int> for Box`")
}

// The orphan rule holds each instantiation to what it held an impl to: the
// interface or the receiver is the program's own. A type argument the
// program declares is neither.
func TestImplSelection_OrphanRulePerInstantiation(t *testing.T) {
	t.Run("own interface at a std receiver", func(t *testing.T) {
		errs := buildProjectExpectingErrors(t, `
pub interface Holds<T> {
    fn holds?(b: self, x: T) -> Bool
}

impl Holds<Int> for String {
    fn holds?(b: String, x: Int) -> Bool {
        String.length(b) == x
    }
}

impl Holds<Bool> for String {
    fn holds?(b: String, x: Bool) -> Bool {
        x
    }
}

fn main() {
    _ok = String.holds?("ab", 2) and Holds.holds?("", True)
}
`, nil)
		expectSelectionClean(t, errs)
	})
	t.Run("std interface at a std receiver", func(t *testing.T) {
		// The orphan rule is a module's, so the program is a module on disk.
		root := t.TempDir()
		files := map[string]string{
			"nomi.toml": "[module]\nname = \"tags\"\nentry_points = [\"main\"]\n",
			"go.mod":    "module tags\n\ngo 1.27.0\n",
			"main.nomi": `
pub struct Tag {
    name: String
}

impl Contains<Tag> for String {
    fn contains?(c: String, x: Tag) -> Bool {
        c == x.name
    }
}

fn main() {
}
`,
		}
		for name, text := range files {
			if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		entry, _ := parser.ParseWithRecovery(lexer.Lex(files["main.nomi"]))
		loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
			data, err := os.ReadFile(filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi")
			if err != nil {
				return nil, err
			}
			nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
			return nodes, nil
		}
		lib := std.Load()
		fa, _, _ := analysis.BuildProjectWithCache(entry, lib.Primitives, lib.Modules, lib.Files, root, loader)
		if fa == nil {
			t.Fatal("no analysis")
		}
		expectRejected(t, entryErrors(fa), "orphan impl: `Contains`", "String")
	})
}

func TestImplSelection_AnArgumentFromAnotherFile(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `
import shapes.{Circle, Square}

pub interface Fits<T> {
    fn fits?(b: self, x: T) -> Bool
}

pub struct Hole {
    size: Int
}

impl Fits<Circle> for Hole {
    fn fits?(b: Hole, x: Circle) -> Bool {
        x.r * 2 <= b.size
    }
}

impl Fits<Square> for Hole {
    fn fits?(b: Hole, x: Square) -> Bool {
        x.side <= b.size
    }
}

fn main() {
    h = #Hole{size: 4}
    _ok = Hole.fits?(h, #Circle{r: 2}) and !Fits.fits?(h, #Square{side: 5})
}
`, map[string]string{"shapes": `
pub struct Circle {
    r: Int
}

pub struct Square {
    side: Int
}
`})
	expectSelectionClean(t, errs)
}

// A value held as an instantiation of a generic interface takes any type
// implementing that instantiation, and a type implementing two takes either.
func TestImplSelection_AValueHeldAsAnInstantiation(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `
pub interface Gets<T> {
    fn got(h: self) -> T
}

pub struct Bag {
    s: String
    n: Int
}

impl Gets<String> for Bag {
    fn got(h: Bag) -> String {
        h.s
    }
}

impl Gets<Int> for Bag {
    fn got(h: Bag) -> Int {
        h.n
    }
}

fn text(h: Gets<String>) -> String {
    Gets.got(h)
}

fn main() {
    b = #Bag{s: "a", n: 1}
    numbers: List<Gets<Int>> = [b]
    _ok = text(b) == "a" and Iter.count(numbers) == 1
}
`, nil)
	expectSelectionClean(t, errs)
}
