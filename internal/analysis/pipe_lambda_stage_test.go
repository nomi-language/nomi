package analysis_test

import (
	"testing"
)

// TestPipeLambdaStage_BoundaryError pins the error for a name used after a
// lambda stage's bare body has ended: the body stops at the next `|>`, so
// the parameter is out of scope in the following stages, and the error says
// so instead of reporting an undefined variable.
func TestPipeLambdaStage_BoundaryError(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		line, col int
		want      string
	}{
		{
			"inside a later stage's lambda",
			"fn f(): Int {\n    limit = 2\n    [1, 2, 3]\n    |> |xs| xs |> Iter.filter(|x| x > Iter.count(xs) - limit)\n    |> Iter.count()\n}\n",
			4, 50,
			"'xs' is the parameter of the lambda stage on line 4, whose body ends at the next `|>`\nhelp: write `|xs| { ... }` to keep the pipe inside it",
		},
		{
			"two stages later, on its own line",
			"fn f(): Int {\n    [1, 2, 3]\n    |> |xs| xs\n    |> Iter.to_list()\n    |> Iter.filter(|x| x > Iter.count(xs))\n    |> Iter.count()\n}\n",
			5, 39,
			"'xs' is the parameter of the lambda stage on line 3, whose body ends at the next `|>`\nhelp: write `|xs| { ... }` to keep the pipe inside it",
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

// TestPipeLambdaStage_OtherNamesKeepTheUndefinedError pins that only a
// parameter of an earlier lambda stage gets the boundary error: any other
// unbound name in a later stage, and a name used after a block-bodied lambda
// stage, report the ordinary undefined-variable error.
func TestPipeLambdaStage_OtherNamesKeepTheUndefinedError(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		line, col int
		want      string
	}{
		{
			"another name",
			"fn f(): Int {\n    [1, 2, 3]\n    |> |xs| xs\n    |> Iter.filter(|x| x > nope)\n    |> Iter.count()\n}\n",
			4, 28, "undefined variable 'nope'",
		},
		{
			"after a block-bodied stage",
			"fn f(): Int {\n    [1, 2, 3]\n    |> |xs| { xs }\n    |> Iter.filter(|x| x > Iter.count(xs))\n    |> Iter.count()\n}\n",
			4, 39, "undefined variable 'xs'\nhelp: did you mean 'x'?",
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

// TestPipeLambdaStage_BlockBodyKeepsThePipeInside is the acceptance mirror:
// with a block body the inner pipe stays inside the lambda and `xs` is bound.
func TestPipeLambdaStage_BlockBodyKeepsThePipeInside(t *testing.T) {
	_, errs := checkSourceWithStdlib("fn f(): Int {\n    limit = 2\n    [1, 2, 3]\n    |> |xs| { xs |> Iter.filter(|x| x > Iter.count(xs) - limit) }\n    |> Iter.count()\n}\n")
	expectNoStdlibErrors(t, errs)
}
