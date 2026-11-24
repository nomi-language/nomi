package format

import "testing"

// A bare list rest prints as the `..` alone and is a fixed point, flat and
// with a comment that breaks the pattern.
func TestFormat_BareListRestRoundTrips(t *testing.T) {
	for _, src := range []string{
		"fn f(xs: List<Int>) -> Int\n    case xs\n        [first, ..] -> first\n        []          -> 0\n    end\nend\n",
		"fn f(xs: List<Int>) -> Int\n    [first, ..] = xs else return 0\n    first\nend\n",
		"fn f(xs: List<Int>) -> Int\n    case xs\n        [\n            first, // the head\n            ..\n        ] -> first\n        [] -> 0\n    end\nend\n",
		"fn f(xs: List<Int>) -> Int\n    case xs\n        [\n            first,\n            // the rest\n            ..,\n            last,\n        ] -> first + last\n        _ -> 0\n    end\nend\n",
		"fn f(xs: List<Int>) -> Int\n    case xs\n        [\n            first,\n            // the rest\n            ..\n        ] -> first\n        [] -> 0\n    end\nend\n",
	} {
		if got := formatTwice(t, src); got != src {
			t.Errorf("got:\n%s\nwant:\n%s", got, src)
		}
		if err := SameMeaning(src, src); err != nil {
			t.Fatal(err)
		}
	}
}

// A comment written at a bare `..` stays at it, with elements after the
// `..` and without: the wildcard the parser puts there is the list's item
// that holds it. When the `..` was not an item, a comment after it went
// above it, and formatting again moved it below.
func TestFormat_ACommentAtABareListRestStays(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"fn f(v: List<Int>) -> Int\n    case v\n        [first, .. // c1\n, last] -> first + last\n        _ -> 0\n    end\nend\n",
			"fn f(v: List<Int>) -> Int\n    case v\n        [\n            first,\n            // c1\n            ..,\n            last,\n        ] -> first + last\n        _ -> 0\n    end\nend\n"},
		{"fn f(v: List<Int>) -> Int\n    case v\n        [first,\n  // above\n  .., last] -> first + last\n        _ -> 0\n    end\nend\n",
			"fn f(v: List<Int>) -> Int\n    case v\n        [\n            first,\n            // above\n            ..,\n            last,\n        ] -> first + last\n        _ -> 0\n    end\nend\n"},
	} {
		if got := formatTwice(t, c.src); got != c.want {
			t.Errorf("got:\n%s\nwant:\n%s", got, c.want)
		}
	}
}
