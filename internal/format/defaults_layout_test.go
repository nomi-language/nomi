package format

import "testing"

// A field whose type comes from its default is `name = default`, and a
// type parameter's default is `P = Type`. Both keep their layout.
func TestFormatTypesFromDefaults(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"struct Game\n    word: String\n    misses   =   0\nend\n",
			"struct Game\n    word: String\n    misses = 0\nend\n",
		},
		{
			"enum Shape\n    Circle {radius   = 1.0}\nend\n",
			"enum Shape\n    Circle {radius = 1.0}\nend\n",
		},
		{
			"pub opaque type Route<P   =   Unit> String\n",
			"pub opaque type Route<P = Unit> String\n",
		},
		{
			"struct Pair<A, B=List<Int>>\n    a: A\n    b: B\nend\n",
			"struct Pair<A, B = List<Int>>\n    a: A\n    b: B\nend\n",
		},
	}
	for _, c := range cases {
		got, err := Format(c.in)
		if err != nil {
			t.Fatalf("Format(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("Format(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
