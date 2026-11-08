package format

import "testing"

// A `subtests` header keeps its name after the pattern, and one too long for
// its line breaks its tuple pattern one element per line, as a call breaks
// its arguments, with no comma after the last: a tuple pattern has none. A
// header that fits stays on one line. Each output formats to itself.
func TestFormat_SubtestsHeader(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{
			name: "a named header that fits",
			src:  "test \"t\" {\n    subtests cases, (description, year, expected), description {\n        assert year > 0\n    }\n}\n",
			want: "test \"t\" {\n    subtests cases, (description, year, expected), description {\n        assert year > 0\n    }\n}\n",
		},
		{
			name: "a built name",
			src:  "test \"t\" {\n    subtests cases, (a, b, sum),   \"${a} + ${b}\"   {\n        assert a + b == sum\n    }\n}\n",
			want: "test \"t\" {\n    subtests cases, (a, b, sum), \"${a} + ${b}\" {\n        assert a + b == sum\n    }\n}\n",
		},
		{
			name: "a long header breaks its tuple pattern",
			src: "test \"t\" {\n    subtests cases, (description, (white_row, white_column), (black_row, black_column), expected), description {\n" +
				"        assert expected\n    }\n}\n",
			want: "test \"t\" {\n    subtests cases, (\n        description,\n        (white_row, white_column),\n        (black_row, black_column),\n" +
				"        expected\n    ), description {\n        assert expected\n    }\n}\n",
		},
		{
			name: "a long header without a name",
			src: "test \"t\" {\n    subtests cases, (_description, (x, y), facing, instructions, (to_x, to_y), to_facing, extra_column) {\n" +
				"        assert x > 0\n    }\n}\n",
			want: "test \"t\" {\n    subtests cases, (\n        _description,\n        (x, y),\n        facing,\n        instructions,\n        (to_x, to_y),\n" +
				"        to_facing,\n        extra_column\n    ) {\n        assert x > 0\n    }\n}\n",
		},
		{
			name: "a piped table puts the stage on its own line",
			src:  "test \"t\" {\n    [(\"a\", 1), (\"b\", 2)] |> subtests (description, n), description { assert n > 0 }\n}\n",
			want: "test \"t\" {\n    [(\"a\", 1), (\"b\", 2)]\n    |> subtests (description, n), description {\n        assert n > 0\n    }\n}\n",
		},
		{
			name: "a piped table broken across lines",
			src:  "test \"t\" {\n    [\n        (\"a\", 1),\n    ]\n    |> subtests (description, n), description {\n        assert n > 0\n    }\n}\n",
			want: "test \"t\" {\n    [\n        (\"a\", 1),\n    ]\n    |> subtests (description, n), description {\n        assert n > 0\n    }\n}\n",
		},
		{
			name: "a pipeline building the rows is written a stage per line",
			src:  "test \"t\" {\n    xs |> Iter.map(f) |> Iter.to_list() // built\n    |> subtests n {\n        assert n > 0\n    }\n}\n",
			want: "test \"t\" {\n    xs\n    |> Iter.map(f)\n    |> Iter.to_list() // built\n    |> subtests n {\n        assert n > 0\n    }\n}\n",
		},
		{
			name: "a comment above the stage stays there",
			src:  "test \"t\" {\n    xs |> Iter.to_list()\n    // each row\n    |> subtests n {\n        assert n > 0\n    }\n}\n",
			want: "test \"t\" {\n    xs\n    |> Iter.to_list()\n    // each row\n    |> subtests n {\n        assert n > 0\n    }\n}\n",
		},
		{
			name: "a bound table stays bound",
			src:  "test \"t\" {\n    cases = [1]\n    subtests cases, n {\n        assert n > 0\n    }\n}\n",
			want: "test \"t\" {\n    cases = [1]\n\n    subtests cases, n {\n        assert n > 0\n    }\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Format(tc.src)
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			if got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
			again, err := Format(got)
			if err != nil {
				t.Fatalf("Format of the output: %v", err)
			}
			if again != got {
				t.Errorf("a second format changes it:\n%s", again)
			}
		})
	}
}
