package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A binding or `once` with no type written keeps what its value proves
// about holding something, as a numeric one keeps its range (spec §4
// *Value ranges*): a List, Vector, Set or Map literal that writes an
// element, a literal spreading a value that holds something, a value of a
// non-empty type, and an `if` or `case` whose every finishing branch is
// one. A local converts where a non-empty subtype is expected; a `once`
// is that subtype.

const nonEmptyDecls = `once words = ["nomi", "pipeline"]
once vec = Vector[3, 1]
once tags = Set["a"]
once ages = {"ann" => 3}

fn first_of(xs: NonEmptyList<Int>) -> Int
    NonEmptyList.first(xs)
end

fn head<T>(xs: NonEmptyList<T>) -> T
    NonEmptyList.first(xs)
end

fn vec_first(v: NonEmptyVector<Int>) -> Int
    NonEmptyVector.first(v)
end

fn set_size(s: NonEmptySet<String>) -> Int
    NonEmptySet.size(s)
end

fn map_first(m: NonEmptyMap<String, Int>) -> Int
    (_, v) = NonEmptyMap.first_entry(m)
    v
end

fn shown(xs: NonEmptyList<Display>) -> Int
    String.length(Display.to_string(NonEmptyList.first(xs)))
end

fn positive(xs: NonEmptyList<PositiveInt>) -> Int
    NonEmptyList.first(xs)
end

fn pos(p: PositiveInt) -> Int
    p
end

once title = "nomi"

fn label(s: NonEmptyString) -> Int
    String.length(s)
end

`

// nonEmptySource is nonEmptyDecls and one function over xs: List<Int>,
// ys: NonEmptyList<Int> and flag: Bool, returning an Int.
func nonEmptySource(body string) string {
	return nonEmptyDecls + "fn f(xs: List<Int>, ys: NonEmptyList<Int>, flag: Bool) -> Int\n    _ = (xs, ys, flag)\n" + body + "\nend\n"
}

func TestNonEmptyBinding_Accepted(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"a local list", "    local = [1, 2]\n    first_of(local)"},
		{"a local list of computed elements", "    local = [List.length(xs)]\n    first_of(local)"},
		{"a local list at a generic parameter", "    local = [\"a\"]\n    String.length(head(local))"},
		{"a local vector", "    local = Vector[1]\n    vec_first(local)"},
		{"a local set", "    local = Set[\"a\"]\n    set_size(local)"},
		{"a local map", "    local = {\"a\" => 1}\n    map_first(local)"},
		{"a once list", "    String.length(head(words))"},
		{"a once vector", "    vec_first(vec)"},
		{"a once set", "    set_size(tags)"},
		{"a once map", "    map_first(ages)"},
		{"a spread beside an element", "    local = [..xs, 9]\n    first_of(local)"},
		{"a spread of a non-empty list", "    local = [..xs, ..ys]\n    first_of(local)"},
		{"a spread of a binding that holds something", "    a = [1]\n    local = [..xs, ..a]\n    first_of(local)"},
		{"a spread of a binding that holds something where expected", "    a = [1]\n    first_of([..xs, ..a])"},
		{"a binding of a binding", "    a = [1]\n    b = a\n    first_of(b)"},
		{"a binding of a non-empty value", "    local = ys\n    first_of(local)"},
		{"every branch of an if", "    local = if flag then [1] else [2, 3]\n    first_of(local)"},
		{"every branch of a case", "    local = case flag\n        True -> [1]\n        False -> [2]\n    end\n    first_of(local)"},
		{"a branch that leaves gives nothing", "    local = if flag then [1] else return 0\n    first_of(local)"},
		{"a block's last expression", "    local = begin\n        n = 1\n        [n]\n    end\n    first_of(local)"},
		{"read inside a lambda", "    local = [1]\n    g = fn -> first_of(local)\n    g()"},
		{"a local whose elements convert", "    local = [1, 2]\n    shown(local)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(nonEmptySource(tc.body))
			expectNoStdlibErrors(t, errs)
		})
	}
}

// What a binding's value does not prove stays an error that names
// try_from: an empty literal, spreads that may each be empty, a branch
// that may be empty, a type written on the binding (as an annotated Int is
// where a PositiveInt is expected), a parameter, a value computed by a
// function, and a name rebound to such a value.
func TestNonEmptyBinding_Rejected(t *testing.T) {
	const tryFrom = "help: NonEmptyList is a subtype of List<Int>; check the value with `NonEmptyList.try_from(...)`"
	for _, tc := range []struct{ name, body, want string }{
		{"an empty literal", "    local = Iter.to_list(Iter.take([1], 0))\n    first_of(local)",
			"argument 1: expected NonEmptyList<Int>, got List<Int>\n" + tryFrom},
		{"spreads that may be empty", "    local = [..xs, ..xs]\n    first_of(local)",
			"argument 1: expected NonEmptyList<Int>, got List<Int>\n" + tryFrom},
		{"spreads that may be empty, where expected", "    first_of([..xs, ..xs])",
			"`[..xs, ..xs]` may be empty, and a NonEmptyList holds something: none of its spreads holds something, and it writes no element"},
		{"a spread of a binding that may be empty, where expected", "    a = if flag then [1] else []\n    first_of([..a])",
			"`[..a]` may be empty, and a NonEmptyList holds something: none of its spreads holds something, and it writes no element"},
		{"one branch empty", "    local = if flag then [1] else []\n    first_of(local)",
			"argument 1: expected NonEmptyList<Int>, got List<Int>\n" + tryFrom},
		{"one arm of a case empty", "    local = case flag\n        True -> [1]\n        False -> []\n    end\n    first_of(local)",
			"argument 1: expected NonEmptyList<Int>, got List<Int>\n" + tryFrom},
		{"a type written on the binding", "    local: List<Int> = [1]\n    first_of(local)",
			"argument 1: expected NonEmptyList<Int>, got List<Int>\n" + tryFrom},
		{"a parameter", "    first_of(xs)",
			"argument 1: expected NonEmptyList<Int>, got List<Int>\n" + tryFrom},
		{"a value a function computes", "    local = List.concat([1], xs)\n    first_of(local)",
			"argument 1: expected NonEmptyList<Int>, got List<Int>\n" + tryFrom},
		{"a name rebound", "    local = [1]\n    local = Iter.to_list(Iter.drop(local, 1))\n    first_of(local)",
			"argument 1: expected NonEmptyList<Int>, got List<Int>\n" + tryFrom},
		{"a name shadowed in a branch", "    local = [1]\n    if flag then\n        local = Iter.to_list(Iter.drop(local, 1))\n        first_of(local)\n    else\n        first_of(local)\n    end",
			"argument 1: expected NonEmptyList<Int>, got List<Int>\n" + tryFrom},
		{"a generic parameter", "    local = Iter.to_list(Iter.take([1], 0))\n    head(local)",
			"argument 1: expected NonEmptyList<T>, got List<Int>\nhelp: NonEmptyList is a subtype of List<T>; check the value with `NonEmptyList.try_from(...)`"},
		{"an empty once", "    first_of(none)",
			"argument 1: expected NonEmptyList<Int>, got List<Int>\n" + tryFrom},
		{"a local whose elements do not convert", "    local = [1, 2]\n    positive(local)",
			"argument 1: expected NonEmptyList<PositiveInt>, got NonEmptyList<Int>"},
		{"an annotated Int where a PositiveInt is expected", "    n: Int = 5\n    pos(n)",
			"argument 1: expected PositiveInt, got Int"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectNonEmptyError(t, nonEmptySource(tc.body)+"\nonce none: List<Int> = []\n", tc.want)
		})
	}
}

// The length or size of a value that holds something is positive (spec §4
// *Value ranges*), a binding's included: `List.length(local)` passes where
// a PositiveInt is expected with local bound to `[1]`, and with local a
// parameter, an annotated binding or one that may be empty it does not.
func TestNonEmptyBinding_Lengths(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"a local list", "    local = [1, 2]\n    pos(List.length(local))"},
		{"a local list piped", "    local = [1, 2]\n    local |> List.length() |> pos()"},
		{"a local vector", "    local = Vector[1]\n    pos(Vector.length(local))"},
		{"a local set", "    local = Set[\"a\"]\n    pos(Set.size(local))"},
		{"a local map", "    local = {\"a\" => 1}\n    pos(Map.size(local))"},
		{"a literal", "    pos(List.length([..xs, 1]))"},
		{"a binding of every branch", "    local = if flag then [1] else [2, 3]\n    pos(List.length(local))"},
		{"a once list", "    pos(List.length(words))"},
		{"a length bound and read", "    local = [1]\n    n = List.length(local)\n    pos(n)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(nonEmptySource(tc.body))
			expectNoStdlibErrors(t, errs)
		})
	}
	for _, tc := range []struct{ name, body string }{
		{"a parameter", "    pos(List.length(xs))"},
		{"a type written on the binding", "    local: List<Int> = [1]\n    pos(List.length(local))"},
		{"a binding that may be empty", "    local = if flag then [1] else []\n    pos(List.length(local))"},
		{"spreads that may be empty", "    pos(List.length([..xs]))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectNonEmptyError(t, nonEmptySource(tc.body), "argument 1: expected PositiveInt, got NonNegativeInt")
		})
	}
}

// A String holds something by the same rule: a string literal that
// writes any text, whatever its `${...}` parts are, a binding of one with
// no type written, a block's tail and every finishing branch. A `once` of
// one is a NonEmptyString. A literal that is only `${...}` parts, `""`, a
// type written on the binding and a value a function computes prove
// nothing, as for a collection.
func TestNonEmptyBinding_Strings(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"a local string", "    local = \"abc\"\n    label(local)"},
		{"a local raw string", "    local = `a\\b`\n    label(local)"},
		{"a local string with a slot", "    n = 1\n    local = \"n=${n}\"\n    label(local)"},
		{"a string with a slot where expected", "    n = 1\n    label(\"${n}!\")"},
		{"a once string", "    label(title)"},
		{"a binding of a binding", "    a = \"x\"\n    b = a\n    label(b)"},
		{"every branch of an if", "    local = if flag then \"a\" else \"b\"\n    label(local)"},
		{"a block's last expression", "    local = begin\n        n = 1\n        \"${n} items\"\n    end\n    label(local)"},
		{"the call form", "    local = \"abc\"\n    label(NonEmptyString(local))"},
		{"the length of a local string", "    local = \"abc\"\n    pos(String.length(local))"},
		{"the length of a once string", "    pos(String.length(title))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(nonEmptySource(tc.body))
			expectNoStdlibErrors(t, errs)
		})
	}
	const mismatch = "argument 1: expected NonEmptyString, got String\nhelp: NonEmptyString is a subtype of String; check the value with `NonEmptyString.try_from(...)`"
	for _, tc := range []struct{ name, body, want string }{
		{"an empty literal", "    local = \"\"\n    label(local)", mismatch},
		{"only a slot", "    local = \"${title}\"\n    label(local)", mismatch},
		{"only slots where expected", "    label(\"${title}${title}\")", mismatch},
		{"a type written on the binding", "    local: String = \"abc\"\n    label(local)", mismatch},
		{"one branch empty", "    local = if flag then \"a\" else \"\"\n    label(local)", mismatch},
		{"a value a function computes", "    local = String.trim(\" a \")\n    label(local)", mismatch},
		{"the length of an empty literal", "    local = \"\"\n    pos(String.length(local))",
			"argument 1: expected PositiveInt, got NonNegativeInt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectNonEmptyError(t, nonEmptySource(tc.body), tc.want)
		})
	}
}

// expectNonEmptyError fails unless src has an error, at a position, that
// starts with want.
func expectNonEmptyError(t *testing.T, src, want string) {
	t.Helper()
	_, errs := checkSourceWithStdlib(src)
	errs = withoutUnusedBindingErrors(errs)
	for _, e := range errs {
		if strings.HasPrefix(diagText(e), want) {
			requirePosition(t, e)
			return
		}
	}
	if len(errs) == 0 {
		t.Fatalf("the checker accepts this; want %q", want)
	}
	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, diagText(e))
	}
	t.Fatalf("errors = %q, want one reading %q", msgs, want)
}

// A local keeps its literal's type, List<Int>, wherever it is read and
// converts only where the subtype is expected, where its read is recorded
// as the NonEmptyList the IR builder reads; a `once` is the
// NonEmptyList, as `once k = 60 * 60` is a PositiveInt.
func TestNonEmptyBinding_TheTypeAValueHas(t *testing.T) {
	src := `once names = ["a"]

fn first_of(xs: NonEmptyList<Int>) -> Int
    NonEmptyList.first(xs)
end

fn f() -> Int
    local = [1]
    n = List.length(local)
    m = List.length(names)
    n + m + first_of(local)
end
`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	got := map[string]string{}
	for node, ty := range fa.ExprTypes {
		if id, ok := node.(*ast.Ident); ok && ty != nil {
			got[id.Name+"@"+itoa(id.Line)+":"+itoa(id.Col)] = ty.String()
		}
	}
	for at, want := range map[string]string{
		"local@9:21":  "List<Int>",
		"names@10:21": "NonEmptyList<String>",
		"local@11:22": "NonEmptyList<Int>",
	} {
		if got[at] != want {
			t.Errorf("%s is a %s; want %s (all: %v)", at, got[at], want, got)
		}
	}
}

// A string local keeps its literal's type, String, and converts only where
// a NonEmptyString is expected, where its read is recorded as one; a
// `once` of a string with text is a NonEmptyString, and of `""` a String.
func TestNonEmptyBinding_AStringsType(t *testing.T) {
	src := `once title = "nomi"

once blank = ""

fn label(s: NonEmptyString) -> Int
    String.length(s)
end

fn f() -> Int
    local = "abc"
    n = String.length(local)
    n + String.length(blank) + label(local) + label(title)
end
`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	got := map[string]string{}
	for node, ty := range fa.ExprTypes {
		if id, ok := node.(*ast.Ident); ok && ty != nil {
			got[id.Name+"@"+itoa(id.Line)+":"+itoa(id.Col)] = ty.String()
		}
	}
	for at, want := range map[string]string{
		"local@11:23": "String",
		"blank@12:23": "String",
		"local@12:38": "NonEmptyString",
		"title@12:53": "NonEmptyString",
	} {
		if got[at] != want {
			t.Errorf("%s is a %s; want %s (all: %v)", at, got[at], want, got)
		}
	}
}

// A `pub once` crosses to the files that read it as its type, so another
// file reads `names` as the NonEmptyList it is and `title` as the
// NonEmptyString it is, whichever file is checked first, and an empty one
// as the List or String it is.
func TestNonEmptyBinding_APublicOnceCrossesAsItsType(t *testing.T) {
	lib := `pub once names = ["a", "b"]

pub once none: List<String> = []

pub once title = "nomi"

pub once blank = ""
`
	main := `import lib.{names, none, title, blank}

fn head(xs: NonEmptyList<String>) -> String
    NonEmptyList.first(xs)
end

fn label(s: NonEmptyString) -> String
    s
end

fn main()
    _ = head(names)
    _ = head(none)
    _ = label(title)
    _ = label(blank)
end
`
	files := map[string]string{"main.nomi": main, "lib.nomi": lib}
	for _, order := range [][]string{{"", "lib"}, {"lib", ""}} {
		got := checkOnceProject(t, files, order)
		if errs := got["lib.nomi"]; len(errs) > 0 {
			t.Fatalf("order %v: lib.nomi: %q", order, messages(errs))
		}
		errs := got["main.nomi"]
		if len(errs) != 2 ||
			errs[0].Line != 13 || !strings.HasPrefix(errs[0].Message, "argument 1: expected NonEmptyList<String>, got List<String>") ||
			errs[1].Line != 15 || !strings.HasPrefix(errs[1].Message, "argument 1: expected NonEmptyString, got String") {
			t.Fatalf("order %v: want errors at line 13 for none and line 15 for blank, got %q", order, messages(errs))
		}
	}
}
