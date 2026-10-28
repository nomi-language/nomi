package format

import "testing"

// A bare list rest prints as the `..` alone and is a fixed point, flat and
// with a comment that breaks the pattern.
func TestFormat_BareListRestRoundTrips(t *testing.T) {
	for _, src := range []string{
		"fn f(xs: List<Int>) -> Int {\n    case xs {\n        [first, ..] -> first\n        [] -> 0\n    }\n}\n",
		"fn f(xs: List<Int>) -> Int {\n    [first, ..] = xs else { return 0 }\n    first\n}\n",
		"fn f(xs: List<Int>) -> Int {\n    case xs {\n        [\n            first, // the head\n            ..\n        ] -> first\n        [] -> 0\n    }\n}\n",
	} {
		if got := formatTwice(t, src); got != src {
			t.Errorf("got:\n%s\nwant:\n%s", got, src)
		}
		if err := SameMeaning(src, src); err != nil {
			t.Fatal(err)
		}
	}
}
