package format

import "testing"

// The depth cascade writes every literal of a deep chain stacked, one-field
// literals and lists included. Read back, its output is a literal stacked by
// hand, whose children the formatter would otherwise lay out by width,
// joining the one-field literals and hugging the lists; it recognizes the
// cascade's own shape (cascadeShaped) and cascades again.
func TestFormat_DepthCascadeIsAFixedPoint(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main() {\n    deep = Struct.update(w, {label: \"two\", person: {address: {zip: \"99999\"}}})\n}\n",
			"fn main() {\n    deep = Struct.update(w, {\n        label: \"two\",\n        person: {\n            address: {\n                zip: \"99999\",\n            },\n        },\n    })\n}\n",
		},
		{
			"fn main() {\n    b = Bag{ items: [Point{x: 7}], label: \"solo\",\n    }\n}\n",
			"fn main() {\n    b = Bag{\n        items: [\n            Point{\n                x: 7,\n            },\n        ],\n        label: \"solo\",\n    }\n}\n",
		},
		{
			"fn main() {\n    b = Bag{items: [Point{x: 7}, Point{x: 8}], label: \"solo\"}\n}\n",
			"fn main() {\n    b = Bag{\n        items: [\n            Point{\n                x: 7,\n            },\n            Point{\n                x: 8,\n            },\n        ],\n        label: \"solo\",\n    }\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}

// A literal stacked by hand, whose children are not all stacked, keeps the
// children's own layout.
func TestFormat_HandStackedLiteralKeepsChildLayout(t *testing.T) {
	src := "fn main() {\n    b = Bag{\n        items: [Point{x: 7}],\n        label: \"solo\",\n    }\n}\n"
	formatsKeepingMeaning(t, src, src)
}

// A call that holds no literal lays out by width inside a cascade, as
// anywhere else: the cascade's own call layout, which never breaks, would
// differ from the one a second format gives the call.
func TestFormat_CascadeLeafCallBreaksByWidth(t *testing.T) {
	src := "fn f(e_rather_long_rather_long_rather_long_rather_long: Equals): Maybe<AssertionDetails> {\n" +
		"    Some(AssertionDetails{\n" +
		"        reason: \"values were not equal\",\n" +
		"        actual_rather_long_rather_long_rather_long: Some(Debug.inspect(e_rather_long_rather_long_rather_long_rather_long.actual)),\n" +
		"        details: [AssertionDetail{label: \"actual\", value: Debug.inspect(e_rather_long_rather_long_rather_long_rather_long.actual)}],\n" +
		"    })\n" +
		"}\n"
	got := formatTwice(t, src)
	if err := SameMeaning(src, got); err != nil {
		t.Fatal(err)
	}
}
