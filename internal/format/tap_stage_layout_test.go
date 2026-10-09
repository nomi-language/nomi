package format

import "testing"

// A pipeline with a `tap` stage is laid out as one with a `then` stage: each
// stage on its own line, the lambda keeping the braces it was written with,
// and a keyword after it staying a stage of its own. A second format changes
// nothing.
func TestFormat_TapStageStacksThePipeline(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"binding": {
			"fn main(): Int {\n    x = 5 |> tap |n| io.print(\"${n}\") |> then |n| n * 2\n    x\n}\n",
			"fn main(): Int {\n    x =\n        5\n        |> tap |n| io.print(\"${n}\")\n        |> then |n| n * 2\n\n    x\n}\n",
		},
		"statement": {
			"fn main() {\n    1 |> tap |n| io.print(\"${n}\")\n}\n",
			"fn main() {\n    1\n    |> tap |n| io.print(\"${n}\")\n}\n",
		},
		"braces kept around a pipe body": {
			"fn main(): Int {\n    3 |> tap |n| { n |> Int.to_string() |> io.print() }\n}\n",
			"fn main(): Int {\n    3\n    |> tap |n| { n |> Int.to_string() |> io.print() }\n}\n",
		},
		"braces kept around a non-pipe body": {
			"fn main(): Int {\n    3 |> tap |n| { io.print(\"${n}\") }\n}\n",
			"fn main(): Int {\n    3\n    |> tap |n| { io.print(\"${n}\") }\n}\n",
		},
		"a trailing try stays its own stage": {
			"fn main(): Maybe<Int> {\n    x = Some(2) |> tap |n| io.print(\"${n}\") |> try\n    Some(x)\n}\n",
			"fn main(): Maybe<Int> {\n    x =\n        Some(2)\n        |> tap |n| io.print(\"${n}\")\n        |> try\n\n    Some(x)\n}\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Format(tc.src)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
			again, err := Format(got)
			if err != nil {
				t.Fatal(err)
			}
			if again != got {
				t.Fatalf("not idempotent:\n%s", again)
			}
		})
	}
}
