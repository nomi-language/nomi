package format

import "testing"

// A pipeline with a `then` stage puts each stage on its own line. A bare
// `then` body ends at the next `|>`, which one line hides:
// `5 |> then fn n -> n + 1 |> then fn n -> n * 2` reads as one lambda.
func TestFormat_ThenStageStacksThePipeline(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"binding": {
			"fn main() -> Int\n    x = 5 |> then fn n -> n + 1 |> then fn n -> n * 2\n    x\nend\n",
			"fn main() -> Int\n    x =\n        5\n        |> then fn n -> n + 1\n        |> then fn n -> n * 2\n\n    x\nend\n",
		},
		"statement": {
			"fn main() -> Bool\n    1 |> then fn n -> n == 1\nend\n",
			"fn main() -> Bool\n    1\n    |> then fn n -> n == 1\nend\n",
		},
		"block-bodied stage": {
			"fn main() -> Int\n    3 |> then fn n ->\n        n |> Int.abs()\n    end\nend\n",
			"fn main() -> Int\n    3\n    |> then fn n ->\n        n |> Int.abs()\n    end\nend\n",
		},
		"braces kept around a non-pipe body": {
			"fn main() -> Int\n    3 |> then fn n -> n + 1\nend\n",
			"fn main() -> Int\n    3\n    |> then fn n -> n + 1\nend\n",
		},
		"destructuring parameter": {
			"fn main() -> Int\n    (1, 2) |> then fn (a, b) -> a + b\nend\n",
			"fn main() -> Int\n    (1, 2)\n    |> then fn (a, b) -> a + b\nend\n",
		},
		"assert head": {
			"fn main()\n    assert 2 |> then fn n -> n > 1\nend\n",
			"fn main()\n    assert 2\n        |> then fn n -> n > 1\nend\n",
		},
		"a `then?` stage is written against its keyword": {
			"fn main() -> Maybe<Int>\n    x = 2 |> then? fn n -> Some(n)\n    Some(x)\nend\n",
			"fn main() -> Maybe<Int>\n    x =\n        2\n        |> then? fn n -> Some(n)\n\n    Some(x)\nend\n",
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

// A pipeline without a `then` stage keeps its one-line layout, and so does
// one with a `then` stage inside `${...}`, where a line break would split
// the string. A lambda inside a call argument is not a stage, and its body
// may hold a pipe without braces.
func TestFormat_ThenStageRuleLeavesOtherPipelinesInline(t *testing.T) {
	for name, src := range map[string]string{
		"no then stage":         "fn main() -> Int\n    Iter.map([1, 2], fn v -> v + 1) |> Iter.count()\nend\n",
		"interpolation":         "fn main()\n    io.print(\"lambda = ${21 |> then fn n: Int -> n * 2}\")\nend\n",
		"pipe in a lambda body": "fn main() -> List<Int>\n    Iter.map([\"1\"], fn s -> String.to_int(s) |> Maybe.with_default(0)) |> Iter.to_list()\nend\n",
		"braced pipe body kept": "fn main() -> List<Int>\n    Iter.map([\"1\"], fn s -> String.to_int(s) |> Maybe.with_default(0)) |> Iter.to_list()\nend\n",
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
