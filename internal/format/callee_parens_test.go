package format

import "testing"

// A parenthesized name before `<` keeps its parentheses: without them the
// parser reads `f < Dog > (x)` as the call `f<Dog>(x)`. The name may end a
// longer operand.
func TestFormat_ParenthesizedNameBeforeLessKeepsParens(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main() {\n    g((f)<Dog>((Dog{weight: 31})))\n}\n",
			"fn main() {\n    g((f) < Dog > (Dog{weight: 31}))\n}\n",
		},
		{
			"fn main() {\n    g(((a.b)) < Dog > (x))\n}\n",
			"fn main() {\n    g((a.b) < Dog > x)\n}\n",
		},
		{
			"fn main() {\n    g(-(f) < Dog > (x))\n}\n",
			"fn main() {\n    g(-(f) < Dog > x)\n}\n",
		},
		{
			"fn main() {\n    g(a + (f) < Dog > (x))\n}\n",
			"fn main() {\n    g(a + (f) < Dog > x)\n}\n",
		},
		// Elsewhere a parenthesized name loses its parentheses.
		{
			"fn main() {\n    g((f) > Dog)\n}\n",
			"fn main() {\n    g(f > Dog)\n}\n",
		},
		// A call with type arguments stays one.
		{
			"fn main() {\n    g(f < Dog > (x))\n}\n",
			"fn main() {\n    g(f<Dog>(x))\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
