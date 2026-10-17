package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A tuple or anonymous struct is Display when every part is, and a function
// value has no Display (spec: Display and Debug). Each rejection names the
// part that has no Display.
func TestDisplayOfStructuralTypesRejectsAPartWithoutDisplay(t *testing.T) {
	const decls = `struct Q {
  a: Int
}

fn inc(x: Int) -> Int {
  x + 1
}

fn show<T>(x: T) -> String where T: Display {
  "${x}"
}
`
	cases := []struct {
		name, body, want string
	}{
		{"a function value", `"${inc}"`, "no impl of `Display` for `(Int) -> Int`; a function value has no `Display`"},
		{"a function in a tuple", `"${#(1, inc)}"`, "no impl of `Display` for `(Int) -> Int`; a function value has no `Display`"},
		{"a struct without Display in a tuple", `"${#(1, Q{a: 1})}"`, "no impl of `Display` for `Q`"},
		{"a struct without Display in a record", `"${{q: Q{a: 1}}}"`, "no impl of `Display` for `Q`"},
		{"a struct without Display in a list", `"${[Q{a: 1}]}"`, "no impl of `Display` for `Q`"},
		{"a tuple with a struct without Display at a bound", `show(#(1, Q{a: 1}))`,
			"#(Int, Q) does not implement Display (required by `where T: Display`)\n" +
				"help: `#(Int, Q)` is Display when each part is, and `Q` is not: add `derive Display` to `Q`"},
		{"a tuple with a function at a bound", `show(#(1, inc))`,
			"help: `#(Int, (Int) -> Int)` is Display when each part is, and `(Int) -> Int` is not: a function value has no `Display`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "\nfn render() -> String {\n  " + tc.body + "\n}\n"
			fa := buildWithStdlibForDerive(src)
			if !hasErrSubstring(fa.TypeErrors, tc.want) {
				t.Fatalf("the checker admits %s, or rejects it with another message; want %q, got:\n%s",
					tc.body, tc.want, errorsText(fa.TypeErrors))
			}
		})
	}
}

// The parts of a tuple or anonymous struct that all have Display make it
// Display, at an interpolation and at a bound, including nested in a generic
// std type whose impl requires its argument to be Display.
func TestDisplayOfStructuralTypesAdmitsDisplayParts(t *testing.T) {
	const decls = `fn show<T>(x: T) -> String where T: Display {
  "${x}"
}
`
	for _, body := range []string{
		`"${#(1, Some(2))}"`,
		`"${{a: 1, b: [Some(2)]}}"`,
		`show(#(1, Some(2)))`,
		`show(Some(#(1, 2)))`,
		`show([#(1, "x")])`,
		`show({a: Some(#(1, 2))})`,
	} {
		t.Run(body, func(t *testing.T) {
			src := decls + "\nfn render() -> String {\n  " + body + "\n}\n"
			fa := buildWithStdlibForDerive(src)
			if len(fa.TypeErrors) != 0 {
				t.Fatalf("the checker rejects %s:\n%s", body, errorsText(fa.TypeErrors))
			}
		})
	}
}

func errorsText(errs []analysis.TypeError) string {
	lines := make([]string, len(errs))
	for i, e := range errs {
		lines[i] = diagText(e)
	}
	return strings.Join(lines, "\n")
}
