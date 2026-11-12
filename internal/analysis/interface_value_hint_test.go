package analysis_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/token"
)

const ivNamed = `pub interface Pub
    fn shown(value: self) -> String
end

impl Pub for Box
    fn shown(value: Box) -> String
        "${value.n}"
    end
end

interface Named
    fn name(value: self) -> String
    fn same(a: self, b: self) -> Bool
    fn copy(value: self) -> self
    fn make() -> self
end

struct Box
    n: Int
end

impl Named for Box
    fn name(value: Box) -> String
        "box ${value.n}"
    end

    fn same(a: Box, b: Box) -> Bool
        a.n == b.n
    end

    fn copy(value: Box) -> Box
        value
    end

    fn make() -> Box
        Box{n: 0}
    end
end

interface Ranked<K>
    fn rank(value: self) -> K
end

impl Ranked<Int> for Box
    fn rank(value: Box) -> Int
        value.n
    end
end

`

// checkHints checks src as a one-file project and answers its errors and
// its interface-value hints.
func checkHints(t *testing.T, src string) ([]analysis.TypeError, []analysis.TypeError) {
	t.Helper()
	nodes, perrs := parser.ParseWithRecovery(lexer.Lex(src))
	if len(perrs) > 0 {
		t.Fatalf("parse errors: %v", perrs)
	}
	lib := loadStd()
	fa, _, _ := analysis.BuildProjectWithCache(nodes, lib.Primitives, lib.Modules, lib.Files, t.TempDir(), nil)
	errs := withoutUnreadParams(append(fa.TypeErrors, analysis.CheckTypes(fa, nodes)...))
	return errs, analysis.InterfaceValueHints(fa, nodes, func() []token.Token { return lexer.Lex(src) })
}

// applyFix is src with fix's edits made.
func applyFix(t *testing.T, src string, fix *parser.Fix) string {
	t.Helper()
	lines := strings.SplitAfter(src, "\n")
	offset := func(p parser.Pos) int {
		n := 0
		for i := 0; i < p.Line-1; i++ {
			n += len(lines[i])
		}
		return n + p.Col - 1
	}
	edits := append([]parser.Edit(nil), fix.Edits...)
	sort.Slice(edits, func(i, j int) bool { return offset(edits[i].Start) > offset(edits[j].Start) })
	for _, e := range edits {
		src = src[:offset(e.Start)] + e.Text + src[offset(e.End):]
	}
	return src
}

// Each function the hint fires on, with its message and the function the
// fix writes. The fixed program checks, and the hint does not fire on it.
func TestInterfaceValueHint_Fires(t *testing.T) {
	cases := []struct {
		name, fn, msg, fixed string
	}{
		{
			name:  "where clause",
			fn:    "fn label<T>(value: T) -> String where T: Named\n    \"a ${T.name(value)}\"\nend\n",
			msg:   "`T` is used only as the type of `value`, under one bound: write `value: Named`",
			fixed: "fn label(value: Named) -> String\n    \"a ${Named.name(value)}\"\nend\n",
		},
		{
			name:  "a pub fn bounded by a pub interface",
			fn:    "pub fn label<T>(value: T) -> String where T: Pub\n    T.shown(value)\nend\n",
			msg:   "`T` is used only as the type of `value`, under one bound: write `value: Pub`",
			fixed: "pub fn label(value: Pub) -> String\n    Pub.shown(value)\nend\n",
		},
		{
			name:  "one of two type parameters",
			fn:    "fn label<T, U>(value: T, other: U) -> U where T: Named, U: Display\n    _ = value |> T.name()\n    other\nend\n",
			msg:   "`T` is used only as the type of `value`, under one bound: write `value: Named`",
			fixed: "fn label<U>(value: Named, other: U) -> U where U: Display\n    _ = value |> Named.name()\n    other\nend\n",
		},
		{
			name:  "the last of two type parameters",
			fn:    "fn label<U, T>(other: U, value: T) -> U where U: Display, T: Named\n    _ = T.name(value)\n    other\nend\n",
			msg:   "`T` is used only as the type of `value`, under one bound: write `value: Named`",
			fixed: "fn label<U>(other: U, value: Named) -> U where U: Display\n    _ = Named.name(value)\n    other\nend\n",
		},
		{
			name:  "a generic interface with a concrete argument",
			fn:    "fn top<T>(value: T) -> Int where T: Ranked<Int>\n    T.rank(value) + 1\nend\n",
			msg:   "`T` is used only as the type of `value`, under one bound: write `value: Ranked<Int>`",
			fixed: "fn top(value: Ranked<Int>) -> Int\n    Ranked.rank(value) + 1\nend\n",
		},
		{
			name:  "interpolated and passed on",
			fn:    "fn shown<T>(value: T) -> String where T: Display\n    \"${value} ${Debug.inspect(value)}\"\nend\n",
			msg:   "`T` is used only as the type of `value`, under one bound: write `value: Display`",
			fixed: "fn shown(value: Display) -> String\n    \"${value} ${Debug.inspect(value)}\"\nend\n",
		},
		{
			name:  "a function of an inherent impl",
			fn:    "impl Box\n    fn tag<T>(b: Box, value: T) -> String where T: Named\n        \"${b.n} ${T.name(value)}\"\n    end\nend\n",
			msg:   "`T` is used only as the type of `value`, under one bound: write `value: Named`",
			fixed: "impl Box\n    fn tag(b: Box, value: Named) -> String\n        \"${b.n} ${Named.name(value)}\"\n    end\nend\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := ivNamed + c.fn
			errs, hints := checkHints(t, src)
			if len(errs) > 0 {
				t.Fatalf("the source does not check: %v", errs)
			}
			if len(hints) != 1 {
				t.Fatalf("want one hint, got %v", hints)
			}
			h := hints[0]
			if h.Message != c.msg {
				t.Errorf("message:\n got %q\nwant %q", h.Message, c.msg)
			}
			if h.Code != analysis.InterfaceValueParamCode {
				t.Errorf("code %q, want %q", h.Code, analysis.InterfaceValueParamCode)
			}
			if h.Fix == nil {
				t.Fatal("the hint has no fix")
			}
			fixed := applyFix(t, src, h.Fix)
			if got := strings.TrimPrefix(fixed, ivNamed); got != c.fixed {
				t.Errorf("fix:\n got %q\nwant %q", got, c.fixed)
			}
			errs, hints = checkHints(t, fixed)
			if len(errs) > 0 {
				t.Errorf("the fixed source does not check: %v", errs)
			}
			if len(hints) > 0 {
				t.Errorf("the fixed source still has hints: %v", hints)
			}
		})
	}
}

// The hint is at the type parameter's name, with a help line.
func TestInterfaceValueHint_Position(t *testing.T) {
	src := ivNamed + "fn label<T>(value: T) -> String where T: Named\n    T.name(value)\nend\n"
	_, hints := checkHints(t, src)
	if len(hints) != 1 {
		t.Fatalf("want one hint, got %v", hints)
	}
	line := strings.Count(ivNamed, "\n") + 1
	h := hints[0]
	if h.Line != line || h.Col != 10 || h.EndLine != line || h.EndCol != 11 {
		t.Errorf("span %d:%d-%d:%d, want %d:10-%d:11", h.Line, h.Col, h.EndLine, h.EndCol, line, line)
	}
	want := "a parameter of type Named takes a value of any implementor, as `T` does; keep `T` when the caller's type matters past the call"
	if len(h.Hints) != 1 || h.Hints[0] != want {
		t.Errorf("help %q, want %q", h.Hints, want)
	}
	if h.Fix.Title != "Write `value: Named`" {
		t.Errorf("fix title %q", h.Fix.Title)
	}
}

// Each function the hint must not fire on: the source checks, and there is
// no hint.
func TestInterfaceValueHint_DoesNotFire(t *testing.T) {
	cases := []struct{ name, src string }{
		{"in the result", "fn pick<T>(value: T) -> T where T: Named\n    value\nend\n"},
		{"in two parameters", "fn both<T>(a: T, b: T) -> String where T: Named\n    T.name(a) + T.name(b)\nend\n"},
		{"inside another type", "fn all<T>(values: List<T>) -> Int where T: Named\n    Iter.count(values)\nend\n"},
		{"two bounds", "fn two<T>(value: T) -> String where T: Named, T: Display\n    \"${value}${T.name(value)}\"\nend\n"},
		{"a pub fn bounded by a private interface", "pub fn label<T>(value: T) -> String where T: Named\n    T.name(value)\nend\n"},
		{"no bound", "fn any<T>(value: T) -> String\n    Debug.inspect(value)\nend\n"},
		{"a bound naming another type parameter", "fn holds<T, K>(value: T, k: K) -> K where T: Ranked<K>\n    _ = T.rank(value)\n    k\nend\n"},
		{"a call returning self", "fn dup<T>(value: T) -> String where T: Named\n    T.name(T.copy(value))\nend\n"},
		{"a call taking self twice", "fn self_same<T>(value: T) -> Bool where T: Named\n    T.same(value, value)\nend\n"},
		{"a call without self", "fn fresh<T>(value: T) -> String where T: Named\n    _ = value\n    T.name(T.make())\nend\n"},
		{"T named as a type", "fn typed<T>(value: T) -> String where T: Named\n    v: T = value\n    T.name(v)\nend\n"},
		{"T as a type argument", "fn listed<T>(value: T) -> Int where T: Named\n    xs: List<T> = [value]\n    Iter.count(xs)\nend\n"},
		{"T in an ascription", "fn ascribed<T>(value: T) -> String where T: Named\n    T.name(value as T)\nend\n"},
		{"the value in a list", "fn listed<T>(value: T) -> Int where T: Named\n    Iter.count([value])\nend\n"},
		{"an operator on the value", "fn eq<T>(value: T) -> Bool where T: Equatable\n    value == value\nend\n"},
		{"explicit type arguments at a call", "fn label<T>(value: T) -> String where T: Named\n    T.name(value)\nend\n\nfn main()\n    _ = label<Box>(Box{n: 1})\nend\n"},
		{"an interface impl's function", "interface Tagger\n    fn tag<T>(t: self, value: T) -> String where T: Named\nend\n\nimpl Tagger for Box\n    fn tag<T>(t: Box, value: T) -> String where T: Named\n        T.name(value)\n    end\nend\n"},
		{"an interface's own function", "interface Tagger\n    fn tag<T>(t: self, value: T) -> String where T: Named\n        T.name(value)\n    end\nend\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs, hints := checkHints(t, ivNamed+c.src)
			if len(errs) > 0 {
				t.Fatalf("the source does not check, so the case shows nothing: %v", errs)
			}
			if len(hints) > 0 {
				t.Errorf("want no hint, got %v", hints)
			}
		})
	}
}

// A value whose type does not implement an interface-typed parameter's
// interface gets the help a bound gives the same type, so the fix's form
// explains a wrong argument as the type parameter's did.
func TestInterfaceValueParam_MismatchHelp(t *testing.T) {
	cases := []struct{ call, msg, help string }{
		{"has(\"abc\", 3)", "argument 2: expected Matcher, got Int",
			"only std can implement `Matcher` for `Int`; `Codepoint`, `Regex` and `String` implement it"},
		{"label(Point{x: 1})", "argument 1: expected Named, got Point",
			"write an `impl Named for Point` block with `fn name(value: Point) -> String`"},
	}
	for _, c := range cases {
		src := "import std::matcher.Matcher\n\ninterface Named\n    fn name(value: self) -> String\nend\n\nstruct Point\n    x: Int\nend\n\n" +
			"fn has(text: String, m: Matcher) -> Bool\n    Matcher.is_contained_in(m, text)\nend\n\n" +
			"fn label(value: Named) -> String\n    Named.name(value)\nend\n\n" +
			"fn main()\n    _ = " + c.call + "\nend\n"
		errs, _ := checkHints(t, src)
		if len(errs) != 1 || errs[0].Message != c.msg {
			t.Errorf("%s: want %q, got %v", c.call, c.msg, errs)
			continue
		}
		if len(errs[0].Hints) != 1 || errs[0].Hints[0] != c.help {
			t.Errorf("%s: help %q, want %q", c.call, errs[0].Hints, c.help)
		}
	}
}

// An interface of a std module the IR builder lowers with each program
// (std/sql's ToSql) gets the hint as any interface does: a value of it
// dispatches in any module (irbuild's TestProgramStdInterfaceValue_*).
func TestInterfaceValueHint_FiresForAPerProgramStdInterface(t *testing.T) {
	src := "import std::sql.{ToSql, Value}\n\nfn bind<T>(value: T) -> Value where T: ToSql\n    T.to_sql(value)\nend\n"
	errs, hints := checkHints(t, src)
	if len(errs) > 0 {
		t.Fatalf("the source does not check: %v", errs)
	}
	if len(hints) != 1 {
		t.Fatalf("want one hint, got %v", hints)
	}
	const want = "import std::sql.{ToSql, Value}\n\nfn bind(value: ToSql) -> Value\n    ToSql.to_sql(value)\nend\n"
	if got := applyFix(t, src, hints[0].Fix); got != want {
		t.Errorf("fix:\n got %q\nwant %q", got, want)
	}
}
