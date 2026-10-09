package format

import "testing"

// Blank lines before a literal's closing bracket do not stack it: the
// formatter drops them, so the next format would find nothing to stack it
// for and join it. Only a comment there stacks it. Found by
// FuzzFormatKeepsMeaning on `0{a:0\n\n}`.
func TestFormat_BlankLinesBeforeClosingBracketDoNotStack(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"fn main() {\n    p = {a: 0\n\n    }\n}\n", "fn main() {\n    p = {a: 0}\n}\n"},
		{"fn main() {\n    xs = [1, 2,\n\n    ]\n}\n", "fn main() {\n    xs = [1, 2]\n}\n"},
		{"fn main() {\n    m = {\"a\" => 1\n\n    }\n}\n", "fn main() {\n    m = {\"a\" => 1}\n}\n"},
		{"fn main() {\n    xs = [1, 2, // two\n    ]\n}\n", "fn main() {\n    xs = [\n        1,\n        2,\n        // two\n    ]\n}\n"},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
