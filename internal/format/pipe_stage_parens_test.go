package format

import "testing"

// A parenthesized pipe stage keeps its parentheses. `xs |> (f())` pipes xs
// into the value f() returns, which the checker rejects, and `xs |> f()`
// calls f with xs, so dropping them, as the formatter does around a call
// elsewhere, turned a rejected program into an accepted one.
func TestFormat_PipeStageKeepsItsParentheses(t *testing.T) {
	for name, src := range map[string]string{
		"one line": "fn main() {\n" +
			"    xs = [1, 2] |> (Iter.to_list())\n" +
			"}\n",
		"across lines": "fn main() {\n" +
			"    xs =\n" +
			"        [1, 2]\n" +
			"        |> (Iter.to_list())\n" +
			"        |> Iter.count()\n" +
			"}\n",
		"under try": "fn main() {\n" +
			"    xs = \" 4 \" |> try (String.to_int())\n" +
			"}\n",
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
	for name, src := range map[string]string{
		"before if":   "fn main() {\n    x = 1 |> (f()) |> if { 1 } else { 2 }\n}\n",
		"before case": "fn main() {\n    x = 1 |> (f()) |> case {\n        1 -> 2\n        _ -> 3\n    }\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Format(src)
			if err != nil {
				t.Fatal(err)
			}
			if err := SameMeaning(src, got); err != nil {
				t.Fatalf("%v\n%s", err, got)
			}
		})
	}
	// SameMeaning sees the difference.
	if err := SameMeaning("x = y |> (f())\n", "x = y |> f()\n"); err == nil {
		t.Fatal("SameMeaning takes `y |> (f())` and `y |> f()` for the same program")
	}
}
