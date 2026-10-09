package format

import "testing"

// In a comma-separated field list (an anonymous struct type, a struct
// literal) no blank line is written before a field: the lexer emits no blank
// line after a comma, so the next format would drop it. Found by
// FuzzFormatKeepsMeaning on a field written without a comma before a blank
// line and a comment.
func TestFormat_CommaSeparatedFieldsTakeNoBlankLine(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn greet(p: {\n    name: String\n\n    // why\n    age: Int,\n}): String {\n    p.name\n}\n",
			"fn greet(p: {\n    name: String,\n    // why\n    age: Int,\n}): String {\n    p.name\n}\n",
		},
		{
			"fn main() {\n    p = Point{\n        x: 3\n\n        // why\n        y: 4,\n    }\n}\n",
			"fn main() {\n    p = Point{\n        x: 3,\n        // why\n        y: 4,\n    }\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
