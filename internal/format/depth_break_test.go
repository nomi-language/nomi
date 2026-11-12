package format

import "testing"

// A literal three compound levels deep breaks whatever its width, and only
// that literal: each element is laid out on its own merits, by width and by
// its own depth.
func TestFormat_DepthBreakLeavesElementsToThemselves(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{
			// The robot-simulator shape: a depth-2 element that fits stays
			// on one line inside the broken list.
			"depth 3 fits",
			"fn main()\n    rs = [R{p: P{x: 0, y: 0}, n: 1}]\nend\n",
			"fn main()\n    rs = [\n        R{p: P{x: 0, y: 0}, n: 1},\n    ]\nend\n",
		},
		{
			// The list-ops shape.
			"list of nested lists",
			"fn main()\n    xs = [[[1], [2]], [[3]]]\nend\n",
			"fn main()\n    xs = [\n        [[1], [2]],\n        [[3]],\n    ]\nend\n",
		},
		{
			// Depth 4: the depth-3 element breaks on its own, and the depth-2
			// element inside it stays on one line.
			"depth 4",
			"fn main()\n    xs = [[[[1, 2]]], [[3]]]\nend\n",
			"fn main()\n    xs = [\n        [\n            [[1, 2]],\n        ],\n        [[3]],\n    ]\nend\n",
		},
		{
			// A table of tuples holding grids: the tuple adds no level, so
			// the list is three deep and breaks, and each row fits.
			"table of grids",
			"fn main()\n    cases = [(\"blinker\", Vector[Vector[0, 1, 0], Vector[0, 1, 0]])]\nend\n",
			"fn main()\n    cases = [\n        (\"blinker\", Vector[Vector[0, 1, 0], Vector[0, 1, 0]]),\n    ]\nend\n",
		},
		{
			"struct three deep",
			"fn main()\n    deep = Struct.update(w, {label: \"two\", person: {address: {zip: \"99999\"}}})\nend\n",
			"fn main()\n    deep = Struct.update(w, {\n        label: \"two\",\n        person: {address: {zip: \"99999\"}},\n    })\nend\n",
		},
		{
			"struct holding a list of structs",
			"fn main()\n    b = Bag{items: [Point{x: 7}, Point{x: 8}], label: \"solo\"}\nend\n",
			"fn main()\n    b = Bag{\n        items: [Point{x: 7}, Point{x: 8}],\n        label: \"solo\",\n    }\nend\n",
		},
		{
			"element too wide breaks by width",
			"fn main()\n    rs = [R{p: P{x_rather_long_rather_long: 0, y_rather_long_rather_long: 0}, n_rather_long_rather_long_rather_long: 1}]\nend\n",
			"fn main()\n    rs = [\n        R{\n            p: P{x_rather_long_rather_long: 0, y_rather_long_rather_long: 0},\n            n_rather_long_rather_long_rather_long: 1,\n        },\n    ]\nend\n",
		},
		{
			"pattern three deep",
			"fn f(x: Maybe<List<List<List<Int>>>>) -> Int\n    case x\n        Some([[[a]]]) -> a\n        _ -> 0\n    end\nend\n",
			"fn f(x: Maybe<List<List<List<Int>>>>) -> Int\n    case x\n        Some([\n            [[a]],\n        ]) -> a\n        _ -> 0\n    end\nend\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}

// A literal broken for its depth keeps its comments: the comments before a
// closing bracket.
func TestFormat_DepthBreakKeepsComments(t *testing.T) {
	src := "fn main()\n    x = Some([[{a: [1, 2] // end\n    }]])\nend\n"
	want := "fn main()\n    x = Some([\n        [\n            {\n                a: [1, 2], // end\n            },\n        ],\n    ])\nend\n"
	formatsKeepingMeaning(t, src, want)
}

// A call that holds no literal lays out by width inside a literal broken
// for its depth, as anywhere else.
func TestFormat_DepthBreakLeafCallBreaksByWidth(t *testing.T) {
	src := "fn f(e_rather_long_rather_long_rather_long_rather_long: Equals) -> Maybe<AssertionDetails>\n" +
		"    Some(AssertionDetails{\n" +
		"        reason: \"values were not equal\",\n" +
		"        actual_rather_long_rather_long_rather_long: Some(Debug.inspect(e_rather_long_rather_long_rather_long_rather_long.actual)),\n" +
		"        details: [AssertionDetail{label: \"actual\", value: Debug.inspect(e_rather_long_rather_long_rather_long_rather_long.actual)}],\n" +
		"    })\n" +
		"end\n"
	got := formatTwice(t, src)
	if err := SameMeaning(src, got); err != nil {
		t.Fatal(err)
	}
}
