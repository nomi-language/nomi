package format

import "testing"

// `A or B` prints with one space either side of `or` in every pattern
// position, and is a fixed point.
func TestFormat_OrPatternRoundTrips(t *testing.T) {
	for _, src := range []string{
		"fn f(s: Shape): Float {\n    case s {\n        .Circle{r} or .Square{r} when r > 1.0 -> r\n        .Point -> 0.0\n        _ -> 1.0\n    }\n}\n",
		"fn f(m: Maybe<Tri>): Int {\n    case m {\n        Some(.Equilateral or .Isosceles) -> 1\n        Some(.Scalene) or None -> 0\n    }\n}\n",
		"fn f(cs: List<Codepoint>): Int {\n    case cs {\n        [')' or ']' or '}' as closer, ..rest] -> 1\n        _ -> 0\n    }\n}\n",
		"fn f(e: Ev): Int {\n    case e {\n        .A(n) or .B(n) as whole -> n\n        (.C(n) as c) or (.D(n) as c) -> n\n    }\n}\n",
		"fn f(.L(n) or .R(n): Two): Int {\n    .L(m) or .R(m) = Two.L(n)\n    .A(k) or .B(k) = load() else { return 0 }\n    if 1 or 2 = k { m } else { 0 }\n}\n",
		"test \"t\" {\n    assert 1 or 2 = one()\n}\n",
	} {
		if got := formatTwice(t, src); got != src {
			t.Errorf("got:\n%s\nwant:\n%s", got, src)
		}
		if err := SameMeaning(src, src); err != nil {
			t.Fatal(err)
		}
	}
}

// Grouping parentheses go where `or` already binds as written; those
// around an `as` alternative stay, since `as` binds looser than `or`.
func TestFormat_OrPatternNormalizesSpacingAndParens(t *testing.T) {
	formatsKeepingMeaning(t,
		"fn f(c: Color): Int {\n    case c {\n        (.Red   or .Green) or .Blue -> 1\n        Some((1 or 2)) -> 0\n        (.A(n) as a)   or (.B(n) as a) -> 2\n    }\n}\n",
		"fn f(c: Color): Int {\n    case c {\n        .Red or .Green or .Blue -> 1\n        Some(1 or 2) -> 0\n        (.A(n) as a) or (.B(n) as a) -> 2\n    }\n}\n")
}

// Alternatives too wide for the line go one per line, each `or` ending the
// line before the next alternative, which is indented one level. A comment
// after an `or` stays there.
func TestFormat_OrPatternBreaksOnePerLine(t *testing.T) {
	src := "fn f(e: Event): Int {\n    case e {\n        .KeyboardPressed{code} or .KeyboardReleased{code} or .KeyboardRepeated{code} or .KeyboardHeld{code} -> code\n        _ -> 0\n    }\n}\n"
	want := "fn f(e: Event): Int {\n    case e {\n        .KeyboardPressed{code} or\n            .KeyboardReleased{code} or\n            .KeyboardRepeated{code} or\n            .KeyboardHeld{code} ->\n            code\n\n        _ ->\n            0\n    }\n}\n"
	formatsKeepingMeaning(t, src, want)

	commented := "fn f(c: Color): Int {\n    case c {\n        .Red or // warm\n            .Green ->\n            1\n\n        _ ->\n            0\n    }\n}\n"
	if got := formatTwice(t, commented); got != commented {
		t.Errorf("got:\n%s\nwant:\n%s", got, commented)
	}
}
