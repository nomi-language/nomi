package analysis_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// TestUnknownType_IsTheOneError holds that a type name that names nothing
// is reported once, where it is written, and that nothing reached from it
// reports again: a field of that type, a derive over the field, a payload,
// a distinct type's inner type, a refinement's base, a type argument, an
// annotation, and a declaration that holds a type with such a part.
//
// Each row was a cascade. `email: Maybe<String>` under `derive FromJson`
// printed the unknown type a second time at the struct (the derived body's
// copy of the field's type, resolved again), `Changes has no field 'email'`
// (the field was dropped from the struct), `the type of '__value_email' is
// not determined` at line 1432043520 (the derived body's binding, reported
// after the walk that moves a synthesized position to its declaration),
// and a missing impl for `Maybe` (the derive's demand of the part).
func TestUnknownType_IsTheOneError(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		line, col int
	}{
		{
			name: "a field of a misspelled generic type under derive FromJson",
			src: `import std::json.FromJson

struct Changes
    email: Maybe<String>
end

derive FromJson for Changes

fn main()
end
`,
			line: 4, col: 12,
		},
		{name: "a field under derive Equatable", src: fieldUnder("Equatable"), line: 2, col: 12},
		{name: "a field under derive Hashable", src: fieldUnder("Hashable"), line: 2, col: 12},
		{name: "a field under derive Comparable", src: fieldUnder("Comparable"), line: 2, col: 12},
		{name: "a field under derive Debug", src: fieldUnder("Debug"), line: 2, col: 12},
		{name: "a field under derive Display", src: fieldUnder("Display"), line: 2, col: 12},
		{name: "a field under derive ToJson", src: "import std::json.ToJson\n\n" + fieldUnder("ToJson"), line: 4, col: 12},
		{name: "a field under derive FromJson", src: "import std::json.FromJson\n\n" + fieldUnder("FromJson"), line: 4, col: 12},
		{
			name: "a List of an unknown type under derive FromJson",
			src: `import std::json.FromJson

struct Changes
    emails: List<Nope>
end

derive FromJson for Changes

fn main()
end
`,
			line: 4, col: 18,
		},
		{
			name: "an Option of an unknown type under derive FromJson",
			src: `import std::json.FromJson

struct Changes
    email: Option<Nope>
end

derive FromJson for Changes

fn main()
end
`,
			line: 4, col: 19,
		},
		{
			name: "a payload under derive FromJson",
			src: `import std::json.FromJson

enum Change
    Email(Nope)
    Nothing
end

derive FromJson for Change

fn main()
end
`,
			line: 4, col: 11,
		},
		{
			name: "a record variant's field under derive Equatable",
			src: `enum Change
    Email{address: Nope}
    Nothing
end

derive Equatable for Change

fn main()
end
`,
			line: 2, col: 20,
		},
		{
			name: "a distinct type's inner type under derive FromJson",
			src: `import std::json.FromJson

type Id Nope

derive FromJson for Id

fn main()
end
`,
			line: 3, col: 9,
		},
		{
			name: "a refinement's base",
			src: `refine Port Nope when fn p -> p > 0

fn open(port: Port) -> Port
    port
end

fn main()
    _ = open
end
`,
			line: 1, col: 13,
		},
		{
			name: "a field set by a literal, read and matched",
			src: `struct Changes
    email: Nope
    name: String
end

fn name_of(Changes{email:, name:}) -> String
    _ = email
    name
end

fn main()
    c = Changes{email: 1, name: "a"}
    _ = c.email
    _ = name_of(c)
end
`,
			line: 2, col: 12,
		},
		{
			name: "a type argument whose call's result a pattern binds",
			src: `fn none<T>() -> Option<T>
    None
end

fn main()
    Some(v) = none<Nope>() else return
    _ = v
end
`,
			line: 6, col: 20,
		},
		{
			name: "a distinct type of an unknown inner type, built and taken apart",
			src: `type Id Nope

fn f(i: Id) -> Id
    Id(n) = i
    _ = n.bar
    i
end

fn main()
    x = Id(5)
    _ = f(x)
    _ = 5 |> Id()
end
`,
			line: 1, col: 9,
		},
		{
			name: "a refinement of an unknown base as a field under derive FromJson",
			src: `import std::json.FromJson

struct S
    port: Port
end

refine Port Nope when fn p -> p > 0

derive FromJson for S

fn main()
    s = S{port: 5}
    _ = s.port
end
`,
			line: 7, col: 13,
		},
		{
			name: "a struct with a field of an unknown type held by a derived struct",
			src: `struct Inner
    v: Nope
end

struct Outer
    inner: List<Inner>
end

derive Equatable for Outer

fn main()
    o = Outer{inner: [Inner{v: 1}]}
    _ = o == o
end
`,
			line: 2, col: 8,
		},
		{
			name: "a binding's annotation",
			src: `fn main()
    z: Nope = 3
    _ = z.bar
end
`,
			line: 2, col: 8,
		},
		{
			name: "a once's annotation",
			src: `once limit: Nope = 3

fn main()
    _ = limit.bar
end
`,
			line: 1, col: 13,
		},
		{
			name: "a parameter's annotation",
			src: `fn f(x: Nope) -> Int
    _ = x.bar
    1
end

fn main()
    _ = f(3)
end
`,
			line: 1, col: 9,
		},
		{
			name: "a lambda parameter's annotation",
			src: `fn main()
    g = fn x: Nope -> x.bar
    _ = g
end
`,
			line: 2, col: 15,
		},
		{
			name: "a field with a default",
			src: `struct Cfg
    port: Nope = 80
end

fn main()
    c = Cfg{}
    _ = c.port.bar
end
`,
			line: 2, col: 11,
		},
		{
			name: "an ascription",
			src: `fn main()
    z = 3 as Nope
    _ = z.bar
end
`,
			line: 2, col: 14,
		},
		{name: "Equatable.equal of a struct with a field of no type", src: useOf("_ = Equatable.equal(c, c)"), line: 2, col: 12},
		{name: "== of a struct with a field of no type", src: useOf("_ = c == c"), line: 2, col: 12},
		{name: "< of a struct with a field of no type", src: useOf("_ = c < c"), line: 2, col: 12},
		{name: "Comparable.compare of a struct with a field of no type", src: useOf("_ = Comparable.compare(c, c)"), line: 2, col: 12},
		{name: "Hashable.hash of a struct with a field of no type", src: useOf("_ = Hashable.hash(c)"), line: 2, col: 12},
		{name: "Debug.inspect through a bound", src: useOf("_ = inspect_it(c)"), line: 2, col: 12},
		{name: "a Comparable bound", src: useOf("_ = larger(c, c)"), line: 2, col: 12},
		{name: "a Display bound", src: useOf("_ = shown(c)"), line: 2, col: 12},
		{name: "where an interface is expected", src: useOf("_ = label(c)"), line: 2, col: 12},
		{name: "a set of a struct with a field of no type", src: useOf("_ = Set[c]"), line: 2, col: 12},
		{name: "a sort of a struct with a field of no type", src: useOf("_ = Iter.sort([c])"), line: 2, col: 12},
		{name: "an interpolation of a struct with a field of no type", src: useOf(`_ = "${c}"`), line: 2, col: 12},
		{
			name: "a destructured parameter's annotation",
			src: `fn f((a, b): (Int, Nope)) -> Int
    _ = b.bar
    a
end

fn main()
    _ = f((1, 2))
end
`,
			line: 1, col: 20,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, perrs := parser.ParseWithRecovery(lexer.Lex(tc.src)); len(perrs) > 0 {
				t.Fatalf("the row does not parse: %v", perrs)
			}
			fa, errs := buildAndCheckProject(t, tc.src)
			errs = append(errs, analysis.FinalizeCoherence(fa)...)
			checkNoGeneratedName(t, errs)
			lines := strings.Count(tc.src, "\n")
			var rest []analysis.TypeError
			found := 0
			for _, e := range errs {
				if e.Line < 1 || e.Line > lines {
					t.Errorf("a diagnostic reports line %d, outside the source's 1..%d: %v", e.Line, lines, e)
				}
				if e.Code == analysis.UnknownTypeCode && e.Line == tc.line && e.Col == tc.col {
					found++
					continue
				}
				rest = append(rest, e)
			}
			if found != 1 {
				t.Errorf("want the unknown type reported once at %d:%d, got it %d times: %v", tc.line, tc.col, found, errs)
			}
			for _, e := range rest {
				t.Errorf("a diagnostic beside the unknown type: %v", e)
			}
		})
	}
}

// TestUnknownType_AHealthyTypeBesideItStillReports holds that a type with a
// part of no type silences only what follows from its own part: the same
// demands, bounds and interface positions of a healthy type in the program
// still report, each once.
func TestUnknownType_AHealthyTypeBesideItStillReports(t *testing.T) {
	src := strings.Replace(useOf(`_ = c < c
    p = Point{x: 1}
    _ = p < p
    _ = Comparable.compare(p, p)
    _ = larger(p, p)
    _ = shown(p)
    _ = label(p)
    _ = "${p}"`), "fn main()", "struct Point\n    x: Int\nend\n\nfn main()", 1)
	fa, errs := buildAndCheckProject(t, src)
	errs = append(errs, analysis.FinalizeCoherence(fa)...)
	want := []string{
		`line 2, col 12: unknown type "Nope"`,
		// One missing impl of Comparable for the `<` and the call of
		// Comparable.compare: a bucket reports at its first demand.
		"line 29, col 11: no impl of `Comparable` for `Point` (required via ordering operator `<`)",
		"line 31, col 9: Point does not implement Comparable (required by `where T: Comparable`)",
		"line 32, col 9: Point does not implement Display (required by `where T: Display`)",
		"line 33, col 15: argument 1: expected Display, got Point",
		"line 34, col 12: no impl of `Display` for `Point` (required via call site)",
	}
	var got []string
	for _, e := range errs {
		got = append(got, e.Error())
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the program reports:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// fieldUnder is a struct with a field of a type that names nothing, and a
// derive of iface for it.
func fieldUnder(iface string) string {
	return `struct Changes
    email: Nope
end

derive ` + iface + ` for Changes

fn main()
end
`
}

// useOf is a struct with a field of a type that names nothing, functions
// that demand an interface of their argument, and a main that builds a
// value of the struct and runs use on it. Each demand of the struct would
// draw a missing impl, and the field's error is the one error.
func useOf(use string) string {
	return `struct Changes
    email: Nope
end

fn inspect_it<T>(x: T) -> String where T: Debug
    Debug.inspect(x)
end

fn larger<T>(a: T, b: T) -> T where T: Comparable
    if a > b then a else b
end

fn shown<T>(x: T) -> String where T: Display
    "${x}"
end

fn label(x: Display) -> String
    "${x}"
end

fn main()
    c = Changes{email: 1}
    ` + use + `
end
`
}
