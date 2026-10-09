package format

import "testing"

// A lambda whose body is a struct literal keeps it on the parameters' line;
// a literal too wide for the line breaks itself. It used to move to the next
// line and break there, and the next format, reading a stacked literal,
// pulled it back onto the parameters' line.
func TestFormat_LambdaStructLiteralBodyStaysOnItsLine(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn f(r: Result<Int, String>): Result<Int, Shape> {\n    r |> Result.map_err(|_| (Json.ShapeError{path_rather_long_rather_long: [], expected_rather_long_rather_long_rather_long_rather_long: \"json\", got_rather_long_rather_long_rather_long: \"invalid\"}))\n}\n",
			"fn f(r: Result<Int, String>): Result<Int, Shape> {\n    r\n    |> Result.map_err(|_| (Json.ShapeError{\n        path_rather_long_rather_long: [],\n        expected_rather_long_rather_long_rather_long_rather_long: \"json\",\n        got_rather_long_rather_long_rather_long: \"invalid\",\n    }))\n}\n",
		},
		{
			"fn f() {\n    g = |id_rather_long_rather_long| Command.Mark{id_rather_long_rather_long, done_rather_long_rather_long_rather_long: True}\n}\n",
			"fn f() {\n    g = |id_rather_long_rather_long| Command.Mark{\n        id_rather_long_rather_long,\n        done_rather_long_rather_long_rather_long: True,\n    }\n}\n",
		},
		{
			"fn f() {\n    g = Iter.map(xs, |x| Point{x: x + 1})\n}\n",
			"fn f() {\n    g = Iter.map(xs, |x| Point{x: x + 1})\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
