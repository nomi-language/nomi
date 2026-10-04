package format

import "testing"

// A pipeline with a lambda stage puts each stage on its own line. A bare
// lambda body ends at the next `|>`, which one line hides:
// `5 |> |n| n + 1 |> |n| n * 2` reads as one lambda.
func TestFormat_LambdaStageStacksThePipeline(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"binding": {
			"fn main(): Int {\n    x = 5 |> |n| n + 1 |> |n| n * 2\n    x\n}\n",
			"fn main(): Int {\n    x =\n        5\n        |> |n| n + 1\n        |> |n| n * 2\n\n    x\n}\n",
		},
		"statement": {
			"fn main(): Bool {\n    1 |> |n| n == 1\n}\n",
			"fn main(): Bool {\n    1\n    |> |n| n == 1\n}\n",
		},
		"block-bodied stage": {
			"fn main(): Int {\n    3 |> |n| { n |> Int.abs() }\n}\n",
			"fn main(): Int {\n    3\n    |> |n| { n |> Int.abs() }\n}\n",
		},
		"assert head": {
			"fn main() {\n    assert 2 |> |n| n > 1\n}\n",
			"fn main() {\n    assert 2\n        |> |n| n > 1\n}\n",
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

// A pipeline without a lambda stage keeps its one-line layout, and so does
// one with a lambda stage inside `${...}`, where a line break would split
// the string. A lambda inside a call argument is not a stage.
func TestFormat_LambdaStageRuleLeavesOtherPipelinesInline(t *testing.T) {
	for name, src := range map[string]string{
		"no lambda stage": "fn main(): Int {\n    Iter.map([1, 2], |v| v + 1) |> Iter.count()\n}\n",
		"interpolation":   "fn main() {\n    io.print(\"lambda = ${21 |> |n: Int| n * 2}\")\n}\n",
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
}
