package analysis_test

import (
	"strings"
	"testing"
)

// `value is Pattern` checks its pattern against the value's type as a `case`
// arm does, and binds nothing.

const isDecls = `enum Kind
    A
    B
end
`

// isErrors checks body after isDecls and answers its errors as
// "message | hint | hint", with every error's hints.
func isErrors(t *testing.T, body string) []string {
	t.Helper()
	_, errs := checkSourceWithStdlib(isDecls + "\n" + body + "\n")
	var out []string
	for _, e := range errs {
		out = append(out, strings.Join(append([]string{e.Message}, e.Hints...), " | "))
	}
	return out
}

func TestIs_Errors(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"a name": {`fn f(m: Maybe<Int>) -> Bool
    m is Some(n)
end`, "an `is` test binds nothing, so its pattern cannot name `n` | to use `n`, match with `if Some(n) = m then` | to test only, write `_` where `n` is"},
		"a bare name": {`fn f(a: Int, b: Int) -> Bool
    a is b
end`, "an `is` test binds nothing, so its pattern cannot name `b` | to compare the value with `b`, write `a == b`"},
		"an as name": {`fn f(m: Maybe<Int>) -> Bool
    m is Some(1) as whole
end`, "an `is` test binds nothing, so its pattern cannot name `whole` | to use `whole`, match with `if Some(1) as whole = m then` | to test only, remove `as whole`"},
		"a punned field": {`struct P
    x: Int
end

fn f(p: P) -> Bool
    p is P{x:}
end`, "an `is` test binds nothing, so its pattern cannot name `x`"},
		"a pattern that always matches": {`fn f(k: Kind) -> Bool
    k is _
end`, "`_` matches every value of type Kind, so this test is always True"},
		"a pattern that covers every variant": {`fn f(k: Kind) -> Bool
    k is (.A or .B)
end`, "`.A or .B` matches every value of type Kind, so this test is always True"},
		"a variant the type lacks": {`fn f(k: Kind) -> Bool
    k is .C
end`, "variant C not found in enum Kind"},
		"a literal of another type": {`fn f(n: Int) -> Bool
    n is "x"
end`, "pattern \"x\" is a String, but the value is an Int"},
		"a bare or whose right side is not a Bool": {`fn f(m: Maybe<Int>) -> Bool
    m is Some(_) or None
end`, "'or' operands must be Bool, got Maybe<?1> | `or` here joins two Bool values; to test several patterns, put them in parentheses: `m is (Some(_) or None)`"},
	} {
		got := isErrors(t, tc.body)
		found := false
		for _, g := range got {
			if strings.HasPrefix(g, tc.want) {
				found = true
			}
			if strings.HasPrefix(g, "binding '") && strings.Contains(g, "never read") {
				t.Errorf("%s: a name the test binds is reported unused too: %s", name, g)
			}
		}
		if !found {
			t.Errorf("%s: the front end accepts this; want %q, got:\n%s", name, tc.want, strings.Join(got, "\n"))
		}
	}
}

// A test that can fail checks, `_rest` discard names included, and is a
// Bool.
func TestIs_Accepted(t *testing.T) {
	for name, body := range map[string]string{
		"a variant": `fn f(k: Kind) -> Bool
    k is .A
end`,
		"a parenthesized or": `fn f(n: Int) -> Bool
    n is (1 or 2)
end`,
		"a codepoint or": `fn f(c: Codepoint) -> Bool
    c is ('a' or 'b')
end`,
		"a discard name": `fn f(s: String) -> Bool
    s is "/users/" + _rest
end`,
		"under not, in a lambda": `fn f(ks: List<Kind>) -> Int
    ks |> Iter.filter(fn k -> not k is .A) |> Iter.count()
end`,
		"in a capture": `fn f(ms: List<Maybe<Int>>) -> Int
    ms |> Iter.reject(&(&1 is None)) |> Iter.count()
end`,
	} {
		if got := isErrors(t, body); len(got) != 0 {
			t.Errorf("%s: unexpected errors:\n%s", name, strings.Join(got, "\n"))
		}
	}
}
