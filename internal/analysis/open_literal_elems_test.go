package analysis_test

import (
	"strings"
	"testing"
)

// An element of a collection literal still open in a type variable (`None`,
// `[]`, `Ok(1)`) takes the type of the elements beside it, whichever comes
// first, in a list, set and vector item and in a map key and value, with an
// expected type or without. Map literals reported "map key type mismatch:
// expected Maybe<Int>, got Maybe<?1>" for `{Some(1) => 1, None => 2}`.
func TestOpenLiteralElems_TakeTheOtherElementsType(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want string
	}{
		{"map key, open second", `{Some(1) => 1, None => 2}`, "Map<Maybe<Int>, Int>"},
		{"map key, open first", `{None => 1, Some(2) => 3}`, "Map<Maybe<Int>, Int>"},
		{"map value, open first", `{1 => None, 2 => Some(1)}`, "Map<Int, Maybe<Int>>"},
		{"map value, empty list first", `{1 => [], 2 => [1]}`, "Map<Int, List<Int>>"},
		{"map key, empty list first", `{[] => 1, [1] => 2}`, "Map<List<Int>, Int>"},
		{"map key, each side open", `{Ok(1) => 1, Err("e") => 2}`, "Map<Result<Int, String>, Int>"},
		{"nested map", `{1 => {None => [Some(1)]}, 2 => {Some(2) => [None]}}`, "Map<Int, Map<Maybe<Int>, List<Maybe<Int>>>>"},
		{"list", `[None, Some(1)]`, "List<Maybe<Int>>"},
		{"set", `#{None, Some(1)}`, "Set<Maybe<Int>>"},
		{"vector", `#[None, Some(1)]`, "Vector<Maybe<Int>>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn run(): Int {\n    m = " + tc.expr + "\n    _ = m\n    0\n}\n"
			if got := asNameTypes(t, src)["m"]; got != tc.want {
				t.Errorf("m is a %s, want %s", got, tc.want)
			}
		})
	}
}

func TestOpenLiteralElems_FitTheExpectedType(t *testing.T) {
	for _, binding := range []string{
		`m: Map<Maybe<Int>, Int> = {Some(1) => 1, None => 2}`,
		`m: Map<Maybe<Int>, Int> = {None => 1, Some(2) => 3}`,
		`m: Map<Int, Maybe<Int>> = {1 => None, 2 => Some(3)}`,
		`m: Map<Int, List<Int>> = {1 => [], 2 => [1]}`,
		`m: Map<Result<Int, String>, Int> = {Ok(1) => 1, Err("e") => 2}`,
	} {
		t.Run(binding, func(t *testing.T) {
			src := "fn run(): Int {\n    " + binding + "\n    _ = m\n    0\n}\n"
			want := strings.TrimSpace(strings.SplitN(strings.SplitN(binding, ":", 2)[1], "=", 2)[0])
			if got := asNameTypes(t, src)["m"]; got != want {
				t.Errorf("m is a %s, want %s", got, want)
			}
		})
	}
}

// Elements that do not unify are still a mismatch.
func TestOpenLiteralElems_MismatchStillReported(t *testing.T) {
	for _, tc := range []struct{ binding, want string }{
		{`_m = {Some(1) => 1, Some("a") => 2}`, "map key type mismatch: expected Maybe<Int>, got Maybe<String>"},
		{`_m = {1 => [1], 2 => ["a"]}`, "map value type mismatch: expected List<Int>, got List<String>"},
		{`_m: Map<Maybe<Int>, Int> = {None => 1, Some("a") => 2}`, "map key type mismatch: expected Maybe<Int>, got Maybe<String>"},
		{`_m: Map<Int, Maybe<Int>> = {1 => None, 2 => Some("a")}`, "map value type mismatch: expected Maybe<Int>, got Maybe<String>"},
	} {
		t.Run(tc.binding, func(t *testing.T) {
			_, errs := checkSourceWithStdlib("fn run(): Int {\n    " + tc.binding + "\n    0\n}\n")
			found := false
			var all []string
			for _, e := range errs {
				all = append(all, e.Message)
				found = found || strings.Contains(e.Message, tc.want)
			}
			if !found {
				t.Errorf("want an error containing %q, got:\n%s", tc.want, strings.Join(all, "\n"))
			}
		})
	}
}
