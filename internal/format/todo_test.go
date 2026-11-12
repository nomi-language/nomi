package format

import "testing"

// `todo` keeps its reason's source form, and formatting is a fixed point.
func TestFormat_TodoRoundTrips(t *testing.T) {
	for name, src := range map[string]string{
		"bare":          "fn main() -> Int\n    todo\nend\n",
		"reason":        "fn main() -> Int\n    todo \"parse the header\"\nend\n",
		"escaped":       "fn main() -> Int\n    todo \"a \\\"quoted\\\" step\"\nend\n",
		"raw reason":    "fn main() -> Int\n    todo `C:\\path`\nend\n",
		"triple reason": "fn main() -> Int\n    todo \"\"\"\n        a longer reason\n        \"\"\"\nend\n",
		"field value":   "fn main() -> Point\n    Point{x: 1, y: todo}\nend\n",
		"pipe stage":    "fn main() -> Int\n    3 |> todo\nend\n",
		"case arm":      "fn main(n: Int) -> Int\n    case n\n        0 -> todo \"zero\"\n        _ -> n\n    end\nend\n",
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
