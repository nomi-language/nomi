package format

import "testing"

// A `concurrent` block whose one statement is atomic stays on one line, and
// the formatter judges that statement without the parentheses it drops.
// `concurrent { Task.await((g)) }` used to break onto three lines and then
// join onto one on a second format, once `((g))` had become `g`.
func TestFormat_AtomicBodyIgnoresDroppedParens(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main()\n    x = concurrent\n        Task.await((g))\n    end\nend\n",
			"fn main()\n    x = concurrent\n        Task.await(g)\n    end\nend\n",
		},
		{
			"fn main()\n    x = concurrent\n        Task.await \t ((Task.spawn(g)))\n    end\nend\n",
			"fn main()\n    x = concurrent\n        Task.await(Task.spawn(g))\n    end\nend\n",
		},
		{
			"fn main()\n    x = concurrent\n        ((Ok))(((y).z))\n    end\nend\n",
			"fn main()\n    x = concurrent\n        Ok(y.z)\n    end\nend\n",
		},
		{
			"fn main()\n    x = concurrent\n        (Some(y))\n    end\nend\n",
			"fn main()\n    x = concurrent\n        Some(y)\n    end\nend\n",
		},
		// Parentheses the formatter keeps still count.
		{
			"fn main()\n    x = concurrent\n        Some((a + b))\n    end\nend\n",
			"fn main()\n    x = concurrent\n        Some((a + b))\n    end\nend\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
