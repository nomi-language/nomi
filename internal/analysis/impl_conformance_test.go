package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

const dayDecls = `type Day Int

fn day_number(day: Day): Int {
    Day(n) = day
    n
}

type Days Int

fn days_number(days: Days): Int {
    Days(n) = days
    n
}

`

// findError returns the error whose message is want, or fails.
func findError(t *testing.T, errs []analysis.TypeError, want string) analysis.TypeError {
	t.Helper()
	for _, e := range errs {
		if e.Message == want {
			return e
		}
	}
	t.Fatalf("no %q error; got %v", want, errs)
	return analysis.TypeError{}
}

// An impl header's interface type argument that names no type is reported at
// the argument. It was accepted: the header failed to resolve, every
// conformance check was skipped, and `Day(10) + Days(4)` reached the IR
// builder, which declined it. The argument is found nested and with the
// receiver's own type parameters in scope.
func TestImplHeader_UnknownInterfaceTypeArgument(t *testing.T) {
	for _, tc := range []struct {
		name, header, rhs, msg, hint string
		col                          int
	}{
		{"first argument", "Add<Dbys, Day>", "Days", `unknown type "Dbys"`, "did you mean 'Days'?", 10},
		{"second argument", "Add<Days, Dya>", "Days", `unknown type "Dya"`, "did you mean 'Day'?", 16},
		{"nested argument", "Add<List<Dbys>, Day>", "Days", `unknown type "Dbys"`, "did you mean 'Days'?", 15},
		{"argument arity", "Add<Map<Int>, Day>", "Days", "Map expects 2 type arguments, got 1", "", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := dayDecls + "impl " + tc.header + " for Day {\n    fn add(lhs: Day, rhs: " + tc.rhs + "): Day {\n        Day(day_number(lhs) + days_number(rhs))\n    }\n}\n\nfn main() {\n    day = Day(10) + Days(4)\n    dbg day_number(day)\n}\n"
			_, errs := checkSourceWithStdlib(src)
			e := findError(t, errs, tc.msg)
			if e.Line != 15 || e.Col != tc.col {
				t.Errorf("%q at %d:%d, want 15:%d", tc.msg, e.Line, e.Col, tc.col)
			}
			if tc.hint != "" && !strings.Contains(strings.Join(e.Hints, "\n"), tc.hint) {
				t.Errorf("hints %q, want %q", e.Hints, tc.hint)
			}
		})
	}
	// Mirror: the header spelled right, and one naming the block's own type
	// parameter, are accepted.
	for _, src := range []string{
		dayDecls + "impl Add<Days, Day> for Day {\n    fn add(lhs: Day, rhs: Days): Day {\n        Day(day_number(lhs) + days_number(rhs))\n    }\n}\n\nfn main() {\n    day = Day(10) + Days(4)\n    dbg day_number(day)\n}\n",
		storeDecls + "impl Store<String, T> for Cache<T> {\n    fn get(cache: Cache<T>, key: String): Maybe<T> {\n        Map.get(cache.items, key)\n    }\n\n    fn put(cache: Cache<T>, key: String, value: T): Cache<T> {\n        Cache{items: Map.put(cache.items, key, value)}\n    }\n}\n",
	} {
		if _, errs := checkSourceWithStdlib(src); len(errs) != 0 {
			t.Errorf("the front end rejects a valid impl header: %v\n%s", errs, src)
		}
	}
}

// Every interface function an impl supplies is held to the interface's
// signature as the header's type arguments instantiate it, with `self` the
// receiver: each parameter and the return type, for generic and non-generic
// interfaces alike. A `self` written only in the return type was left
// unsubstituted and bound to whatever the impl returned, and a missing
// return type was compared against nothing. Each mismatch names the
// function the impl must write.
func TestImplFunction_MatchesTheInstantiatedRequirement(t *testing.T) {
	const make = "interface Make {\n    fn make(n: Int): self\n}\n\n"
	const maybeMake = "interface MaybeMake {\n    fn make(n: Int): Maybe<self>\n}\n\n"
	const conv = "interface Conv<T> {\n    fn conv(x: self): T\n}\n\n"
	const show = "interface Show {\n    fn show(x: self): String\n}\n\n"
	const boxDecl = "struct Box<T> {\n    item: T\n}\n\n"
	for _, tc := range []struct {
		name, src, msg, hint string
		line, col            int
	}{
		{
			"parameter of a generic interface",
			dayDecls + "impl Add<Days, Day> for Day {\n    fn add(lhs: Day, rhs: Day): Day {\n        Day(day_number(lhs) + day_number(rhs))\n    }\n}\n",
			"impl function 'add': parameter 2 has type Day, but interface 'Add' declares Days",
			"interface 'Add<Days, Day>' requires `fn add(lhs: Day, rhs: Days): Day`",
			16, 8,
		},
		{
			"return of a generic interface",
			dayDecls + "impl Add<Days, Day> for Day {\n    fn add(lhs: Day, rhs: Days): Days {\n        Days(day_number(lhs) + days_number(rhs))\n    }\n}\n",
			"impl function 'add': return type Days does not match interface 'Add' return type Day",
			"interface 'Add<Days, Day>' requires `fn add(lhs: Day, rhs: Days): Day`",
			16, 8,
		},
		{
			"self-position parameter",
			dayDecls + "impl Add<Days, Day> for Day {\n    fn add(lhs: Days, rhs: Days): Day {\n        Day(days_number(lhs) + days_number(rhs))\n    }\n}\n",
			"impl function 'add': parameter 1 has type Days, but it stands for `self`, which this block implements for Day",
			"interface 'Add<Days, Day>' requires `fn add(lhs: Day, rhs: Days): Day`",
			16, 8,
		},
		{
			"parameter count",
			dayDecls + "impl Add<Days, Day> for Day {\n    fn add(lhs: Day): Day {\n        lhs\n    }\n}\n",
			"impl function 'add' takes 1 parameters, but interface 'Add' declares 2",
			"interface 'Add<Days, Day>' requires `fn add(lhs: Day, rhs: Days): Day`",
			16, 8,
		},
		{
			"return of an interface argument",
			conv + "type Day Int\n\nimpl Conv<String> for Day {\n    fn conv(x: Day): Int {\n        Day(n) = x\n        n\n    }\n}\n",
			"impl function 'conv': return type Int does not match interface 'Conv' return type String",
			"interface 'Conv<String>' requires `fn conv(x: Day): String`",
			8, 8,
		},
		{
			"return-only self",
			make + "type Day Int\n\nimpl Make for Day {\n    fn make(n: Int): Int {\n        n\n    }\n}\n",
			"impl function 'make': return type Int does not match interface 'Make' return type Day",
			"interface 'Make' requires `fn make(n: Int): Day`",
			8, 8,
		},
		{
			"self nested in the return",
			maybeMake + "type Day Int\n\nimpl MaybeMake for Day {\n    fn make(n: Int): Maybe<Int> {\n        Some(n)\n    }\n}\n",
			"impl function 'make': return type Maybe<Int> does not match interface 'MaybeMake' return type Maybe<Day>",
			"interface 'MaybeMake' requires `fn make(n: Int): Maybe<Day>`",
			8, 8,
		},
		{
			"return-only self of a generic receiver",
			make + boxDecl + "impl Make for Box<T> {\n    fn make(n: Int): Box<Int> {\n        Box{item: n}\n    }\n}\n",
			"impl function 'make': return type Box<Int> does not match interface 'Make' return type Box<T>",
			"interface 'Make' requires `fn make(n: Int): Box<T>`",
			10, 8,
		},
		{
			"missing return type",
			show + "type Day Int\n\nimpl Show for Day {\n    fn show(x: Day) {\n        _ = x\n    }\n}\n",
			"impl function 'show': return type Unit does not match interface 'Show' return type String",
			"interface 'Show' requires `fn show(x: Day): String`",
			8, 8,
		},
		{
			"return where the interface has none",
			"interface Ping {\n    fn ping(x: self)\n}\n\ntype Day Int\n\nimpl Ping for Day {\n    fn ping(x: Day): Int {\n        Day(n) = x\n        n\n    }\n}\n",
			"impl function 'ping': return type Int does not match interface 'Ping' return type Unit",
			"interface 'Ping' requires `fn ping(x: Day)`",
			8, 8,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			e := findError(t, errs, tc.msg)
			if e.Line != tc.line || e.Col != tc.col {
				t.Errorf("%q at %d:%d, want %d:%d", tc.msg, e.Line, e.Col, tc.line, tc.col)
			}
			if !strings.Contains(strings.Join(e.Hints, "\n"), tc.hint) {
				t.Errorf("hints %q, want %q", e.Hints, tc.hint)
			}
		})
	}
	// Mirror: each of those interfaces implemented as it requires, including
	// a generic receiver whose `self` keeps the block's type parameter, and a
	// derive, are accepted.
	for _, src := range []string{
		make + "type Day Int\n\nimpl Make for Day {\n    fn make(n: Int): Day {\n        Day(n)\n    }\n}\n",
		maybeMake + "type Day Int\n\nimpl MaybeMake for Day {\n    fn make(n: Int): Maybe<Day> {\n        Some(Day(n))\n    }\n}\n",
		make + boxDecl + "impl Make for Box<Int> {\n    fn make(n: Int): Box<Int> {\n        Box{item: n}\n    }\n}\n",
		maybeMake + boxDecl + "impl MaybeMake for Box<T> {\n    fn make(n: Int): Maybe<Box<T>> {\n        _ = n\n        None\n    }\n}\n",
		conv + "type Day Int\n\nimpl Conv<Int> for Day {\n    fn conv(x: Day): Int {\n        Day(n) = x\n        n\n    }\n}\n",
		show + "type Day Int\n\nimpl Show for Day {\n    fn show(x: Day): String {\n        _ = x\n        \"day\"\n    }\n}\n",
		"struct Point {\n    x: Int\n}\n\nderive Equatable, Hashable, Comparable for Point\n",
	} {
		if _, errs := checkSourceWithStdlib(src); len(errs) != 0 {
			t.Errorf("the front end rejects a conforming impl: %v\n%s", errs, src)
		}
	}
}
