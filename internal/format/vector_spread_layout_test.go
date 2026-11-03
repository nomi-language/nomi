package format

import "testing"

// A vector's named spread, `#[..v, x]` or `#[x, ..v]`, is written flat when
// it fits and one item per line otherwise. A spread written last takes no
// comma after it, as a list's `..tail` and a vector's last bare `..` do, and
// comments keep their place.
func TestFormat_VectorNamedSpreadLayout(t *testing.T) {
	const head = "fn f(names: Vector<String>) -> Vector<String> {\n"
	const tail = "}\n"
	for _, c := range []struct{ name, src, want string }{
		{
			"first, flat",
			head + "    #[ ..names , \"Iris\" ]\n" + tail,
			head + "    #[..names, \"Iris\"]\n" + tail,
		},
		{
			"last, flat",
			head + "    #[\"Iris\",..names]\n" + tail,
			head + "    #[\"Iris\", ..names]\n" + tail,
		},
		{
			"a computed spread, flat",
			head + "    #[\"Iris\", ..Vector.push(names, \"Eve\")]\n" + tail,
			head + "    #[\"Iris\", ..Vector.push(names, \"Eve\")]\n" + tail,
		},
		{
			"first, broken for width",
			head + "    #[..names, \"1111111111111111\", \"2222222222222222\", \"3333333333333333\", \"4444444444444444444444\", \"5555555\"]\n" + tail,
			head + "    #[\n        ..names,\n        \"1111111111111111\",\n        \"2222222222222222\",\n        \"3333333333333333\",\n        \"4444444444444444444444\",\n        \"5555555\",\n    ]\n" + tail,
		},
		{
			"last, broken for width",
			head + "    #[\"1111111111111111\", \"2222222222222222\", \"3333333333333333\", \"4444444444444444444444\", \"5555555\", ..names]\n" + tail,
			head + "    #[\n        \"1111111111111111\",\n        \"2222222222222222\",\n        \"3333333333333333\",\n        \"4444444444444444444444\",\n        \"5555555\",\n        ..names\n    ]\n" + tail,
		},
		{
			"first, with comments",
			head + "    #[ // a\n        ..names, // b\n        // c\n        \"Iris\"]\n" + tail,
			head + "    #[\n        // a\n        ..names, // b\n        // c\n        \"Iris\",\n    ]\n" + tail,
		},
		{
			"last, with comments",
			head + "    #[\"Iris\", // a\n        // b\n        ..names // c\n    ]\n" + tail,
			head + "    #[\n        \"Iris\", // a\n        // b\n        ..names // c\n    ]\n" + tail,
		},
		{
			"last, a comment below",
			head + "    #[\"Iris\", ..names\n        // a\n    ]\n" + tail,
			head + "    #[\n        \"Iris\",\n        ..names\n        // a\n    ]\n" + tail,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}
