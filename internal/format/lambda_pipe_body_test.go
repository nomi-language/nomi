package format

import "testing"

// A lambda body never extends over a pipe: `|y| xs |> f()` is
// `(|y| xs) |> f()`. A lambda whose body is a pipe therefore keeps its
// braces, on one line and across lines, or formatting changes what the
// program means.
func TestFormat_LambdaPipeBodyKeepsItsBraces(t *testing.T) {
	cases := map[string]string{
		"one line": "fn main() {\n" +
			"    r = [1, 2] |> Iter.map(|y| { [y] |> Iter.to_list() }) |> Iter.to_list()\n" +
			"}\n",
		"across lines, as a call's last argument": "fn main() {\n" +
			"    rows =\n" +
			"        1..=2\n" +
			"        |> Iter.map(|y| {\n" +
			"            1..=3\n" +
			"            |> Iter.map(|x| x * y)\n" +
			"            |> Iter.to_list()\n" +
			"        })\n" +
			"        |> Iter.to_list()\n" +
			"}\n",
		"bound to a name": "fn main() {\n" +
			"    f = |xs: List<Int>| { xs |> Iter.to_list() }\n" +
			"}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Format(src)
			if err != nil {
				t.Fatal(err)
			}
			if got != src {
				t.Fatalf("Format changed a lambda with a pipe body:\ngot:\n%s\nwant:\n%s", got, src)
			}
		})
	}
}
