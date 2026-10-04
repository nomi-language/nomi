package analysis_test

import (
	"strings"
	"testing"
)

// A Set finds its elements and a Map its keys by equality, and a function value
// or an Iter has none (spec §30). Building such a Set or Map used to pass the
// checker and stop the program with a BLOCKED line at the literal; the checker
// now rejects the expression that builds it.

func TestKeyWithoutEquality_IsRejectedWhereTheCollectionIsBuilt(t *testing.T) {
	const iterSet = "`Iter<Int>` cannot be a Set element: an Iter has no equality or hashing; " +
		"materialize it with `Iter.to_list` first"
	const iterKey = "`Iter<Int>` cannot be a Map key: an Iter has no equality or hashing; " +
		"materialize it with `Iter.to_list` first"
	cases := []struct {
		name, body, want string
		line, col        int
	}{
		{"set literal", "s = #{it}\n  _ = s", iterSet, 4, 7},
		{"map literal", "m = {it => 1}\n  _ = m", iterKey, 4, 7},
		{"call", "s = Iter.to_set([it])\n  _ = s", iterSet, 4, 18},
		{"pipe", "s = [it] |> Iter.to_set\n  _ = s", iterSet, 4, 12},
		{"annotated empty set", "s: Set<Iter<Int>> = #{}\n  _ = s", iterSet, 4, 23},
		{"nested in a payload", "xs = [Some(#{it})]\n  _ = xs", iterSet, 4, 14},
		{"generic instance", "s = single(it)\n  _ = s", iterSet, 4, 13},
		{"function key", "f = |x: Int| x + 1\n  m = {f => 1}\n  _ = m",
			"`(Int) -> Int` cannot be a Map key: a function value has no equality or hashing", 5, 7},
		{"key holding an Iter", "m = {Feed{items: it} => 1}\n  _ = m",
			"`Feed` cannot be a Map key: it holds `Iter<Int>`, and an Iter has no equality or hashing; " +
				"materialize it with `Iter.to_list` first", 4, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := keyWithoutEqualityProgram(tc.body)
			_, errs := checkSourceWithStdlib(src)
			var got []string
			found := false
			for _, e := range errs {
				got = append(got, e.Message)
				if e.Message == tc.want {
					found = true
					if e.Line != tc.line || e.Col != tc.col {
						t.Errorf("reported at %d:%d, want %d:%d", e.Line, e.Col, tc.line, tc.col)
					}
				}
			}
			if !found {
				t.Errorf("the checker admits this source or reports something else; want %q, got %v\n%s",
					tc.want, got, src)
			}
		})
	}
}

// One collection type is reported once per body, at the expression that
// first builds it, and not again where the value is passed along.
func TestKeyWithoutEquality_IsReportedOncePerBody(t *testing.T) {
	src := keyWithoutEqualityProgram("s = #{it}\n  t = Set.insert(s, it)\n  _ = t")
	_, errs := checkSourceWithStdlib(src)
	n := 0
	for _, e := range errs {
		if strings.Contains(e.Message, "cannot be a Set element") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("want one Set element error, got %d: %v", n, errs)
	}
}

func TestKeyWithoutEquality_AdmitsKeysWithEquality(t *testing.T) {
	for _, body := range []string{
		// An Iter as a Map VALUE is not looked up by equality.
		"m = {\"k\" => it}\n  _ = m",
		"s = #{[1, 2]}\n  _ = s",
		"s = single(1)\n  _ = s",
	} {
		src := keyWithoutEqualityProgram(body)
		_, errs := checkSourceWithStdlib(src)
		for _, e := range errs {
			t.Errorf("rejected: %s\n%s", e.Message, src)
		}
	}
}

// keyWithoutEqualityProgram puts body at line 4 of a main that has `it`, an
// `Iter<Int>`, in scope. A generic body (`single`) builds a Set of its type
// parameter, which is checked where it is instantiated, not where it is
// declared.
func keyWithoutEqualityProgram(body string) string {
	return "fn main() {\n" +
		"  it: Iter<Int> = [1, 2]\n" +
		"  _ = it\n" +
		"  " + body + "\n" +
		"}\n\n" +
		"struct Feed {\n  items: Iter<Int>\n}\n\n" +
		"fn single<T>(x: T): Set<T> {\n  #{x}\n}\n"
}
