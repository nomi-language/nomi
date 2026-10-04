package format

import "testing"

// A destructuring binding whose value is a pipe breaks the way a plain
// `name = ...` pipe binding does: after `=`, with the source and each `|>`
// one level in, and a blank line after the statement.
func TestFormat_DestructurePipeBindingBreaksLikeAPlainBinding(t *testing.T) {
	pipe := "words |> Iter.filter(|word| word != \"\") |> Iter.to_list() |> split_first_word_of_every_line_in_the_input()"
	broken := "        words\n        |> Iter.filter(|word| word != \"\")\n        |> Iter.to_list()\n        |> split_first_word_of_every_line_in_the_input()\n"
	cases := []struct {
		name   string
		target string
	}{
		{"plain", "pair"},
		{"tuple", "(first, rest)"},
		{"tuple with a wildcard", "(first, _)"},
		{"struct", "{first, rest}"},
		{"map", `{"first" => first}`},
		{"distinct", "Pair(pair)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := "fn f(words: List<String>): Int {\n    " + c.target + " = " + pipe + "\n    1\n}\n"
			want := "fn f(words: List<String>): Int {\n    " + c.target + " =\n" + broken + "\n    1\n}\n"
			got, err := Format(in)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("Format()\n got: %q\nwant: %q", got, want)
			}
			again, err := Format(got)
			if err != nil {
				t.Fatal(err)
			}
			if again != got {
				t.Fatalf("not idempotent\nfirst: %q\nagain: %q", got, again)
			}
		})
	}
}

// A destructuring pipe binding that fits stays on one line, as a plain one
// does, and one that fits only after `=` breaks there and keeps the pipe flat.
func TestFormat_DestructurePipeBindingThatFits(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "fits on one line",
			in:   "fn f(words: List<String>): Int {\n    (first, rest) = words |> Iter.to_list() |> split_first()\n    1\n}\n",
			want: "fn f(words: List<String>): Int {\n    (first, rest) = words |> Iter.to_list() |> split_first()\n\n    1\n}\n",
		},
		{
			name: "fits after the =",
			in:   "fn f(words: List<String>): Int {\n    (first_word_of_the_line, every_remaining_word) = words |> Iter.to_list() |> split_first_word_of_each_line()\n    1\n}\n",
			want: "fn f(words: List<String>): Int {\n    (first_word_of_the_line, every_remaining_word) =\n        words |> Iter.to_list() |> split_first_word_of_each_line()\n\n    1\n}\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Format(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("Format()\n got: %q\nwant: %q", got, c.want)
			}
			again, err := Format(got)
			if err != nil {
				t.Fatal(err)
			}
			if again != got {
				t.Fatalf("not idempotent\nfirst: %q\nagain: %q", got, again)
			}
		})
	}
}
