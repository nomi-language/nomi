package format

import "testing"

// A parenthesized `_` under a postfix `!` or `?` keeps its parentheses:
// `(_)!` parses, and `_!` does not.
func TestFormat_UnderscoreKeepsItsParenthesesUnderAMark(t *testing.T) {
	for name, src := range map[string]string{
		"unwrap":         "fn main() {\n    f((_)!)\n}\n",
		"unwrap, called": "fn main() {\n    f((_)!(0))\n}\n",
		"try":            "fn main() {\n    f((_)?)\n}\n",
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
	got, err := Format("fn main() {\n    f(((_))!)\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := "fn main() {\n    f((_)!)\n}\n"; got != want {
		t.Errorf("doubled parentheses: got:\n%s\nwant:\n%s", got, want)
	}
}
