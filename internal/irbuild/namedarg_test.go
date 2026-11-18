package irbuild

import (
	"strings"
	"testing"
)

// TestNamedArg_ExplicitZeroIsNotAbsence is the mechanism's central claim, held
// at the unit level as well as through the VM, because it is the one a future
// reader is most likely to "simplify".
//
// Presence is decided by the `filled []string` slot vector, and a lowered
// expression's code text is never the empty string. So `opts(2, factor: 0)` fills its slot
// with the text `0` and the default is not applied. A zero-value test cannot
// distinguish "absent" from "explicitly the type's zero", and `0` / `""` /
// `False` / `0.0` are all reachable Nomi literals.
func TestNamedArg_ExplicitZeroIsNotAbsence(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	out := vmReference(fixture("named_args.nomi"))
	if !strings.Contains(out.stdout, "L0") {
		t.Fatalf("the VM no longer answers L0 for `opts(2, factor: 0)`, so the "+
			"fixture has stopped discriminating a presence mask from a zero-value "+
			"test. stdout=%q", out.stdout)
	}
	if strings.Contains(out.stdout, "L20") {
		t.Fatalf("`opts(2, factor: 0)` answered as though `factor` had DEFAULTED to 10. "+
			"That is the zero-value-test defect. stdout=%q", out.stdout)
	}
}

// A named call to an interface function that leaves a defaulted slot empty
// before a supplied one (`Sum.f(x, 1, c: 9)`) fills it from the declaration
// the call reaches, as a call omitting trailing slots does: an inherited
// default's or an override's own, through the interface, the type, a
// bounded type parameter and an erased receiver, whose impl's is chosen
// when the call runs.
func TestNamedArg_InterfaceCallFillsAnEarlierDefault(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

interface Sum
    fn base(value: self) -> Int

    open fn f(value: self, a: Int, b: Int = 2, c: Int = 3) -> Int
        Sum.base(value) * 1000 + a * 100 + b * 10 + c
    end
end

struct Box
    n: Int
end

impl Sum for Box
    fn base(value: Box) -> Int
        value.n
    end
end

struct Own
    n: Int
end

impl Sum for Own
    fn base(value: Own) -> Int
        value.n
    end

    fn f(value: Own, a: Int, b: Int = 5, c: Int = 3) -> Int
        -(value.n * 1000 + a * 100 + b * 10 + c)
    end
end

fn bounded<T>(x: T) -> Int where T: Sum
    T.f(x, 1, c: 9)
end

fn erased(s: Sum) -> Int
    Sum.f(s, 1, c: 9)
end

fn main()
    x = Box{n: 4}
    o = Own{n: 5}
    io.inspect(Sum.f(x, 1, c: 9))
    io.inspect(Box.f(x, 1, c: 9))
    io.inspect(Sum.f(o, 1, c: 9))
    io.inspect(Own.f(o, 1, c: 9))
    io.inspect(Sum.f(o, a: 1, c: 9))
    io.inspect(bounded(x))
    io.inspect(bounded(o))
    io.inspect(erased(x))
    io.inspect(erased(o))
end
`, "4129\n4129\n-5159\n-5159\n-5159\n4129\n-5159\n4129\n-5159\n")
}

// A nested function that calls itself with named arguments: the call goes
// through the function's value, which carries no parameter names, and its
// arguments reach the slots the checker recorded.
func TestNamedArg_ANestedFunctionCallsItselfByName(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn main()
    fn fact(n: Int, acc: Int = 1) -> Int
        if n <= 1 then acc else fact(acc: acc * n, n: n - 1)
    end
    io.print(fact(n: 5))
    io.print(fact(acc: 2, n: 3))
end
`, "120\n12\n")
}

// Arguments are evaluated left to right as written, a named argument where
// it is written (spec §8 *Evaluation order*): a callee that is an
// expression first, then a pipe's head, then the written arguments, then
// the defaults the call leaves out. A partial capture evaluates its written
// arguments when the function it makes is called.
func TestNamedArg_ArgumentsAreEvaluatedAsWritten(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn p(tag: String) -> String
    io.print("eval ${tag}")
    tag
end

fn f(a: String, b: String, c: String = p("default")) -> String
    "${a}/${b}/${c}"
end

fn pick(tag: String) -> (String, String) -> String
    io.print("callee ${tag}")
    fn a, b -> "${a}+${b}"
end

fn main()
    io.print(f(b: p("B"), p("A")))
    io.print(f(c: p("C"), b: p("B"), a: p("A")))
    io.print(p("head") |> f(b: p("B")))
    io.print(pick("K")(p("A"), p("B")))
    io.print(p("head") |> pick("K")(p("B")))
    g = &f(b: p("PB"), &1)
    io.print(g(p("PA")))
end
`, "eval B\neval A\neval default\nA/B/default\neval C\neval B\neval A\nA/B/C\neval head\neval B\neval default\nhead/B/default\ncallee K\neval A\neval B\nA+B\ncallee K\neval head\neval B\nhead+B\neval PA\neval PB\neval default\nPA/PB/default\n")
}

// `Struct.update` takes a patch that is a value of the base's own struct
// type, written as a literal that names the type or spreads another value:
// it names every field, so the update is the patch.
func TestStructUpdate_APatchOfTheBasesOwnType(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

struct Point
    x: Int
    y: Int
end

fn main()
    p = Point{x: 1, y: 2}
    q = Struct.update(p, {..p, x: 3})
    io.print(q.x)
    io.print(q.y)
    r = Struct.update(p, Point{x: 3, y: 9})
    io.print(r.y)
    s = Struct.update(updates: {..p, y: 7}, original: p)
    io.print(s.x)
    io.print(s.y)
end
`, "3\n2\n9\n1\n7\n")
}

// A partial application over a function value the builder holds no lambda
// for takes its callee from the checker's record: a local copy of a declared
// function is that function's partial, and a nested function named inside
// its own body has its declaration's parameters. A value with no function
// behind it (a call's result, a parameter) has its type's parameters.
func TestPartial_OverAFunctionValueTheRecordNames(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn sub(a: Int, b: Int = 1) -> Int
    a - b
end

fn mk() -> (Int, Int) -> Int
    fn a, b -> a - b
end

fn apply(f: (Int, Int) -> Int) -> Int
    h = &f(&1, 2)
    h(10)
end

fn main()
    g = sub
    q = &g(&1, 3)
    io.print(q(9))
    r = &g(&1)
    io.print(r(9))
    fn walk(n: Int, acc: Int = 0) -> Int
        step = &walk(acc: &1, n: n - 1)
        if n <= 0 then acc else step(acc + n)
    end
    io.print(walk(3))
    k = mk()
    p = &k(&1, 3)
    io.print(p(9))
    io.print(apply(k))
end
`, "6\n8\n6\n6\n8\n")
}

// A program's impl for a std type is called through the type's name as
// through the interface's: the checker's record names the program's
// function, so the route for std's own Range functions leaves it alone.
func TestQualCall_AProgramsImplOverARange(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

interface Describe
    fn describe(value: self) -> String
end

impl Describe for Range<Int>
    fn describe(r: Range<Int>) -> String
        "range from ${Range.start(r)}"
    end
end

fn main()
    io.print(Range.describe(1..3))
    io.print(Describe.describe(1..3))
    io.print(1..3 |> Range.describe())
    io.print(Range.to_string(1..3))
end
`, "range from 1\nrange from 1\nrange from 1\n1..3\n")
}
