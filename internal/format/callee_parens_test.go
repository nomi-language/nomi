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

// `try` binds its operand tighter than `<`, so `try (f) < User > (x)` is a
// comparison whose left side is `try (f)`. Without the parentheses it tries
// the call `f<User>(x)`. A pipe ending in a bare `try` is written with the
// `try` before its last stage, so before `<` it is parenthesized.
func TestFormat_TryOfParenthesizedNameBeforeLessKeepsParens(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main() {\n    user = try (FromJson.from_json)<User>( // a1\n    encoded)\n}\n",
			"fn main() {\n    user = try (FromJson.from_json) < User > (\n        // a1\n        encoded\n    )\n}\n",
		},
		{
			"fn main() {\n    a = try (F.from_json)<User>(x)\n}\n",
			"fn main() {\n    a = try (F.from_json) < User > x\n}\n",
		},
		{
			"fn main() {\n    c = -try (f) < Dog > (y)\n}\n",
			"fn main() {\n    c = -try (f) < Dog > y\n}\n",
		},
		{
			"fn main() {\n    a = x |> f |> try < T > ( // c\n        y)\n}\n",
			"fn main() {\n    a = (x |> try f) < T > (\n        // c\n        y\n    )\n}\n",
		},
		// Elsewhere the parentheses go.
		{
			"fn main() {\n    a = try (f) + 1\n}\n",
			"fn main() {\n    a = try f + 1\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
