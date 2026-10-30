package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

const repeatedKeysDecls = `enum Direction {
    North
    South
    Move(Int)
}

enum Level {
    Low
    High
}

impl Equatable for Level {
    fn equal(_a: Level, _b: Level) -> Bool {
        False
    }
}

type Kvs Map<String, Int>

`

// The body starts on this line of every source below.
var repeatedKeysBodyLine = strings.Count(repeatedKeysDecls, "\n") + 2

func repeatedKeysErrors(body string) []string {
	src := repeatedKeysDecls + "fn run() -> Int {\n" + body + "\n    0\n}\n"
	_, errs := checkSourceWithStdlib(src)
	var all []string
	for _, e := range errs {
		all = append(all, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	return all
}

// TestRepeatedKeys_RejectedAtTheSecondKey pins that a map literal, a set
// literal, a map pattern and a map destructuring reject two keys that are
// the same constant, at the second one, for each kind of constant. Each was
// accepted before: the literal kept the later entry and the pattern matched
// one value twice.
func TestRepeatedKeys_RejectedAtTheSecondKey(t *testing.T) {
	lit := func(k string) string {
		return "key `" + k + "` is given twice in this map literal; the later entry would replace the earlier"
	}
	pat := func(k string) string {
		return "key `" + k + "` is given twice in this map pattern; match its value once"
	}
	set := func(k string) string {
		return "element `" + k + "` is given twice in this set literal; a set holds it once"
	}
	cases := []struct {
		name string
		body string
		line int // relative to the body's first line
		col  int
		msg  string
	}{
		{"Int", `    _m = %{1 => "a", 1 => "b"}`, 0, 22, lit("1")},
		{"Int in another base", `    _m = %{255 => "a", 0xFF => "b"}`, 0, 24, lit("0xFF")},
		{"negative Int", `    _m = %{-1 => "a", -1 => "b"}`, 0, 23, lit("-1")},
		{"String", `    _m = %{"k" => 1, "k" => 2}`, 0, 22, lit(`"k"`)},
		{"raw String", "    _m = %{\"k\" => 1, `k` => 2}", 0, 22, lit(`"k"`)},
		{"Float", `    _m = %{1.0 => 1, 1.00 => 2}`, 0, 22, lit("1.00")},
		{"Float zero and negative zero", `    _m = %{0.0 => 1, -0.0 => 2}`, 0, 22, lit("-0.0")},
		{"Decimal by value", `    _m = %{1.0d => 1, 1.00d => 2}`, 0, 23, lit("1.00d")},
		{"Codepoint", `    _m = %{'a' => 1, 'a' => 2}`, 0, 22, lit("'a'")},
		{"Bool", `    _m = %{True => 1, True => 2}`, 0, 23, lit("True")},
		{"None", `    _m: Map<Maybe<Int>, Int> = %{None => 1, None => 2}`, 0, 45, lit("None")},
		{"Some of a constant", `    _m = %{Some(1) => 1, Some(1) => 2}`, 0, 26, lit("Some(1)")},
		{"qualified variant", `    _m = %{Direction.North => 1, Direction.North => 2}`, 0, 34, lit("Direction.North")},
		{"dot variant", `    _m: Map<Direction, Int> = %{.North => 1, .North => 2}`, 0, 46, lit(".North")},
		{"dot and qualified variant", `    _m: Map<Direction, Int> = %{Direction.North => 1, .North => 2}`, 0, 55, lit(".North")},
		{"variant constructor", `    _m = %{Direction.Move(1) => 1, Direction.Move(1) => 2}`, 0, 36, lit("Direction.Move(1)")},
		{"tuple", `    _m = %{#(1, "a") => 1, #(1, "a") => 2}`, 0, 28, lit(`#(1, "a")`)},
		{"parenthesized", `    _m = %{1 => 1, (1) => 2}`, 0, 20, lit("1")},
		{"map-distinct literal", "    _k = %Kvs{\"a\" => 1, \"a\" => 2}", 0, 25, lit(`"a"`)},
		{"set", `    _s = %[1, 2, 1]`, 0, 18, set("1")},
		{"set of variants", `    _s = %[Direction.North, Direction.North]`, 0, 29, set("Direction.North")},
		{"case pattern", "    m = %{\"k\" => 1}\n    _ = case m {\n        %{\"k\" => 1, \"k\" => 2} -> 1\n        _ -> 2\n    }", 2, 21, pat(`"k"`)},
		{"destructuring", "    %{\"k\" => _a, \"k\" => _b} = %{\"k\" => 1}", 0, 18, pat(`"k"`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			all := repeatedKeysErrors(tc.body)
			line := repeatedKeysBodyLine + tc.line
			want := fmt.Sprintf("%d:%d %s", line, tc.col, tc.msg)
			for _, got := range all {
				if got == want {
					return
				}
			}
			t.Fatalf("want %s; the front end reported:\n  %s", want, strings.Join(all, "\n  "))
		})
	}
}

// TestRepeatedKeys_AcceptedWhenEqualityIsNotKnown pins what the check leaves
// alone: keys whose values are only known at run time, constants that
// differ, and a variant of an enum whose own `impl Equatable` decides
// equality.
func TestRepeatedKeys_AcceptedWhenEqualityIsNotKnown(t *testing.T) {
	for _, body := range []string{
		"    x = 1\n    y = 1\n    _m = %{x => \"a\", y => \"b\", x => \"c\"}",
		"    _m = %{1 => \"a\", 2 => \"b\", -1 => \"c\"}",
		"    _m = %{1.5 => \"a\", 1.25 => \"b\"}",
		"    _m = %{1.5d => \"a\", 1.50001d => \"b\"}",
		"    _m = %{\"a\" => 1, \"A\" => 2}",
		"    _m: Map<Maybe<Int>, Int> = %{Some(1) => 1, Some(2) => 2}",
		"    _m = %{#(1, 2) => 1, #(2, 1) => 2}",
		"    _m = %{Direction.North => 1, Direction.South => 2}",
		"    _m = %{Level.Low => 1, Level.Low => 2}",
		"    _s = %[Level.High, Level.High]",
		"    n = 1\n    _m = %{Direction.Move(n) => 1, Direction.Move(n) => 2}",
		"    _s = %[1, 2, 3]",
		"    _m = %{True => 1, False => 2}",
		"    m = %{\"k\" => 1}\n    _ = case m {\n        %{\"k\" => 1, \"j\" => 2} -> 1\n        _ -> 2\n    }",
	} {
		if all := repeatedKeysErrors(body); len(all) > 0 {
			t.Errorf("%s\nthe front end reported:\n  %s", body, strings.Join(all, "\n  "))
		}
	}
}
