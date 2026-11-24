package analysis

import (
	"strings"
	"testing"
)

const returnedSelfSrc = `interface Make
    fn make(n: Int) -> self
end

struct A
    n: Int
end

struct B
    n: Int
end

impl Make for A
    fn make(n: Int) -> A
        A{n:}
    end
end

impl Make for B
    fn make(n: Int) -> B
        B{n:}
    end
end

interface Parse<T>
    fn parse(x: T) -> self
end

struct Num
    n: Int
end

impl Parse<Int> for Num
    fn parse(x: Int) -> Num
        Num{n: x}
    end
end

impl Parse<String> for Num
    fn parse(_x: String) -> Num
        Num{n: 0}
    end
end

fn takes_a(a: A) -> Int
    a.n
end

fn give() -> B
    Make.make(3)
end

fn run() -> Int
    a: A = Make.make(1)
    n: Num = Parse.parse(5)
    b = Make.make<B>(4)
    a.n + takes_a(Make.make(2)) + n.n + b.n + give().n
end
`

// TestCallTypeArgs_AReturnedSelfIsInferred: a function whose signature
// names `self` only in its result, called through its interface with no
// type argument, takes its implementor from where the result goes: an
// annotation, a parameter's type at a call, a return position, and an
// annotation that also chooses among one type's impls of a generic
// interface (Parse<Int> and Parse<String> for Num). A type argument names
// it too. Each call records the type it is for. Treating such a function as
// one whose signature names no `self` reported each call as naming no
// implementor.
func TestCallTypeArgs_AReturnedSelfIsInferred(t *testing.T) {
	fa, nodes := checkNodes(t, returnedSelfSrc)
	var got []string
	for _, name := range []string{"Make.make", "Parse.parse"} {
		for _, call := range elabCalls(nodes, name) {
			got = append(got, name+" for "+forName(fa.CalleeOf(call.Func).For))
		}
	}
	want := []string{
		"Make.make for B", // give's tail
		"Make.make for A", // the annotation
		"Make.make for B", // the type argument
		"Make.make for A", // takes_a's parameter
		"Parse.parse for Num",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls are for:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func forName(t Type) string {
	if t == nil {
		return "nothing"
	}
	return t.String()
}

const typeArgFormsDecls = `interface Unit2
    fn symbol() -> String
    fn times(n: Int) -> String
end

interface Conv
    fn conv<U>(u: U) -> String
end

struct Meters
end

impl Unit2 for Meters
    fn symbol() -> String
        "m"
    end

    fn times(_n: Int) -> String
        "m"
    end
end

impl Meters
    fn half(n: Int) -> Int
        n / 2
    end
end

impl Conv for Meters
    fn conv<U>(_u: U) -> String
        "m"
    end
end

fn g() -> Int
    1
end

fn inc(n: Int) -> Int
    n + 1
end

fn app<T>(f: (T) -> T, x: T) -> T
    f(x)
end

fn run()
`

// TestCallTypeArgs_EveryCallFormChecksItsTypeArguments: a call writes one
// type argument per type parameter its callee takes, and each names a type,
// whatever the call's form: a free function, a type-qualified function, an
// operator interface's function by name, an interface function through its
// interface (the implementor first, then the function's own), a lambda
// bound to a name, a function value whose type names the enclosing
// function's type parameter, and each as a pipe stage. Each count error
// says what the callee takes, and each name that is no type is the unknown
// type error, each reported once. Every one of these was admitted: a
// function with no type parameters ignored its type arguments, a pipe stage
// ignored them all, and a function value's rebound the enclosing
// function's type parameter.
func TestCallTypeArgs_EveryCallFormChecksItsTypeArguments(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		count      string // the count error, "" for none
		unknown    bool   // whether `Nope` is the unknown type error
	}{
		{"free function, a type", "_ = g<Int>()", "expected 0 type argument(s), got 1", false},
		{"free function, no type", "_ = g<Nope>()", "expected 0 type argument(s), got 1", true},
		{"type-qualified", "_ = Meters.half<Nope>(3)", "expected 0 type argument(s), got 1", true},
		{"through the interface, too many", "_ = Unit2.symbol<Meters, Int>()", "expected 1 type argument(s), got 2", false},
		{"through the interface, no type", "_ = Unit2.symbol<Nope>()", "", true},
		{"own type parameters, too many", "_ = Conv.conv<Meters, Int, Int>(5)", "expected 1 or 2 type argument(s), got 3", false},
		{"lambda bound to a name", "h = fn n: Int -> n\n    _ = h<Int>(1)", "expected 0 type argument(s), got 1", false},
		{"pipe stage", "_ = 1 |> inc<Int>()", "expected 0 type argument(s), got 1", false},
		{"pipe stage, no type", "_ = 1 |> inc<Nope>()", "expected 0 type argument(s), got 1", true},
		{"pipe stage through the interface", "_ = 4 |> Unit2.times<Meters, Int>()", "expected 1 type argument(s), got 2", false},
		{"pipe stage, type-qualified", "_ = 1 |> Meters.half<Int>()", "expected 0 type argument(s), got 1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := typeArgFormsDecls + "    " + tc.body + "\nend\n"
			_, errs := checkSource(src)
			if len(errs) == 0 {
				t.Fatalf("the checker admits this source:\n%s", src)
			}
			counts, unknowns := 0, 0
			for _, e := range errs {
				switch {
				case tc.count != "" && e.Message == tc.count:
					counts++
				case e.Code == UnknownTypeCode && len(e.Args) > 0 && e.Args[0] == "Nope":
					unknowns++
				default:
					t.Errorf("an error the call should not draw: %v", e)
				}
			}
			if want := btoi(tc.count != ""); counts != want {
				t.Errorf("%q reported %d times, want %d: %v", tc.count, counts, want, errs)
			}
			if want := btoi(tc.unknown); unknowns != want {
				t.Errorf("unknown type reported %d times, want %d: %v", unknowns, want, errs)
			}
		})
	}
	// A function value's type parameters are the enclosing function's.
	src := strings.Replace(typeArgFormsDecls, "    f(x)\n", "    f<Int>(x)\n", 1) + "    _ = app(inc, 1)\nend\n"
	_, errs := checkSource(src)
	found := false
	for _, e := range errs {
		if e.Message == "expected 0 type argument(s), got 1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a function value's type argument rebinds the enclosing function's: %v", errs)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
