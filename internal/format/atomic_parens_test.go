package format

import "testing"

// A `concurrent` block whose one statement is atomic stays on one line, and
// the formatter judges that statement without the parentheses it drops.
// `concurrent { Task.await((g)) }` used to break onto three lines and then
// join onto one on a second format, once `((g))` had become `g`.
func TestFormat_AtomicBodyIgnoresDroppedParens(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main() {\n    x = concurrent { Task.await((g)) }\n}\n",
			"fn main() {\n    x = concurrent { Task.await(g) }\n}\n",
		},
		{
			"fn main() {\n    x = concurrent { Task.await \t ((Task.spawn(g))) }\n}\n",
			"fn main() {\n    x = concurrent { Task.await(Task.spawn(g)) }\n}\n",
		},
		{
			"fn main() {\n    x = concurrent { ((Ok))(((y).z)) }\n}\n",
			"fn main() {\n    x = concurrent { Ok(y.z) }\n}\n",
		},
		{
			"fn main() {\n    x = concurrent { (Some(y)) }\n}\n",
			"fn main() {\n    x = concurrent { Some(y) }\n}\n",
		},
		// Parentheses the formatter keeps still count.
		{
			"fn main() {\n    x = concurrent { Some((a + b)) }\n}\n",
			"fn main() {\n    x = concurrent {\n        Some((a + b))\n    }\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
