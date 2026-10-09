package analysis_test

import (
	"testing"
)

// A `tap` stage answers the piped value, so the pipeline keeps its type
// whatever the lambda does with it.
func TestPipeTapStage_AnswersThePipedValue(t *testing.T) {
	for name, src := range map[string]string{
		"unit call":       "import std/io\n\nfn f(): Int {\n    1\n    |> tap |n| io.print(Int.to_string(n))\n}\n",
		"mid pipeline":    "import std/io\n\nfn f(): List<String> {\n    [1, 2]\n    |> tap |xs| io.print(Int.to_string(Iter.count(xs)))\n    |> Iter.map(Int.to_string)\n    |> Iter.to_list()\n}\n",
		"block body":      "import std/io\n\nfn f(): Int {\n    3\n    |> tap |n| {\n        io.print(\"a\")\n        io.print(Int.to_string(n))\n    }\n}\n",
		"binding tail":    "fn f(): Int {\n    3\n    |> tap |n| { _ = n + 1 }\n}\n",
		"dbg tail":        "fn f(): Int {\n    3\n    |> tap |n| dbg n * 2\n}\n",
		"piped dbg tail":  "fn f(): Int {\n    3\n    |> tap |n| { n |> Int.to_string() |> dbg }\n}\n",
		"todo body":       "fn f(): Int {\n    3\n    |> tap |_| todo\n}\n",
		"after try stage": "import std/io\n\nfn f(r: Result<Int, String>): Result<Int, String> {\n    x =\n        r\n        |> try\n        |> tap |n| io.print(Int.to_string(n))\n\n    Ok(x)\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(src)
			expectNoStdlibErrors(t, errs)
		})
	}
}

// A `tap` lambda whose body answers a value other than Unit is an error that
// points to `then`, at the body's last expression.
func TestPipeTapStage_LambdaMustReturnUnit(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		line, col int
		want      string
	}{
		{
			"Int body",
			"fn f(): Int {\n    1\n    |> tap |n| n + 1\n}\n",
			3, 16,
			"`tap` passes its input on unchanged, so its lambda must return Unit; got Int\nhelp: use `then` to replace the value with the lambda's result",
		},
		{
			"block ending in a value",
			"import std/io\n\nfn f(): Int {\n    1\n    |> tap |n| {\n        io.print(\"x\")\n        Int.to_string(n)\n    }\n}\n",
			7, 9,
			"`tap` passes its input on unchanged, so its lambda must return Unit; got String\nhelp: use `then` to replace the value with the lambda's result",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			got := errorsAt(errs, tc.line, tc.col)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("errors at %d:%d = %q, want exactly [%q]; all: %v", tc.line, tc.col, got, tc.want, errs)
			}
		})
	}
}

// A `tap` lambda takes one parameter, and a name used after its bare body
// ended gets the boundary error naming the `tap` stage.
func TestPipeTapStage_ArityAndBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		line, col int
		want      string
	}{
		{
			"two parameters",
			"fn f(): Int {\n    1\n    |> tap |_a, _b| {}\n}\n",
			3, 12,
			"a `tap` lambda takes one parameter, the piped value; this one takes 2",
		},
		{
			"parameter after the body",
			"import std/io\n\nfn f(): Int {\n    1\n    |> tap |n| io.print(Int.to_string(n))\n    |> then |m| m + n\n}\n",
			6, 21,
			"'n' is the parameter of the `tap` stage on line 5, whose body ends at the next `|>`\nhelp: write `tap |n| { ... }` to keep the pipe inside it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			got := errorsAt(errs, tc.line, tc.col)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("errors at %d:%d = %q, want exactly [%q]; all: %v", tc.line, tc.col, got, tc.want, errs)
			}
		})
	}
}
