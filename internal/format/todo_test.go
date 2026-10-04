package format

import "testing"

// `todo` keeps its reason's source form, and formatting is a fixed point.
func TestFormat_TodoRoundTrips(t *testing.T) {
	for name, src := range map[string]string{
		"bare":          "fn main(): Int {\n    todo\n}\n",
		"reason":        "fn main(): Int {\n    todo \"parse the header\"\n}\n",
		"escaped":       "fn main(): Int {\n    todo \"a \\\"quoted\\\" step\"\n}\n",
		"raw reason":    "fn main(): Int {\n    todo `C:\\path`\n}\n",
		"triple reason": "fn main(): Int {\n    todo \"\"\"\n        a longer reason\n        \"\"\"\n}\n",
		"field value":   "fn main(): Point {\n    Point{x: 1, y: todo}\n}\n",
		"pipe stage":    "fn main(): Int {\n    3 |> todo\n}\n",
		"case arm":      "fn main(n: Int): Int {\n    case n {\n        0 -> todo \"zero\"\n        _ -> n\n    }\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Format(src)
			if err != nil {
				t.Fatal(err)
			}
			if got != src {
				t.Fatalf("got:\n%s\nwant:\n%s", got, src)
			}
		})
	}
}
