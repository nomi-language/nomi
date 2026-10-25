package analysis

import (
	"strings"
	"testing"
)

// An out-of-scope tag names the literal in the form it was written: a raw
// literal with backticks, not quotes.
func TestTaggedStringOutOfScopeNamesTheLiteralAsWritten(t *testing.T) {
	cases := []struct {
		name, literal, want string
	}{
		{"quoted", `Shout"hi"`, "a typed literal `Shout\"...\"` requires"},
		{"triple", "Shout\"\"\"\nhi\n\"\"\"", "a typed literal `Shout\"\"\"...\"\"\"` requires"},
		{"raw", "Shout`hi`", "a typed literal ``Shout`...` `` requires"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSource("fn main() {\n    x = " + tc.literal + "\n}\n")
			var got []string
			for _, e := range errs {
				if strings.Contains(e.Message, "is not in scope") {
					got = append(got, e.Message)
				}
			}
			if len(got) == 0 {
				t.Fatalf("an out-of-scope tag is accepted; errors: %v", errs)
			}
			if !strings.Contains(got[0], tc.want) {
				t.Errorf("message %q does not contain %q", got[0], tc.want)
			}
		})
	}
}
