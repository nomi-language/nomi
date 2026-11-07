package format

import "testing"

// A `tap` stage that names its function keeps the name as written, on one
// line or across lines.
func TestFormat_TapByNameKeepsTheName(t *testing.T) {
	cases := []struct{ src, want string }{
		{"x |> tap  io.print\n", "x |> tap io.print\n"},
		{"x |> tap log_it |> f()\n", "x |> tap log_it |> f()\n"},
		{"x |> tap Log.info\n", "x |> tap Log.info\n"},
		{
			"x\n    |> f()\n    |> tap   io.print\n    |> g()\n",
			"x\n|> f()\n|> tap io.print\n|> g()\n",
		},
	}
	for _, c := range cases {
		formatsKeepingMeaning(t, c.src, c.want)
	}
	if err := SameMeaning("x |> tap f\n", "x |> tap g\n"); err == nil {
		t.Error("tapping another function kept the meaning, want a change")
	}
}
