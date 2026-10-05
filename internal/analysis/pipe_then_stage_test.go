package analysis_test

import (
	"testing"
)

// TestPipeThenStage_BoundaryError pins the error for a name used after a
// `then` stage's bare body has ended: the body stops at the next `|>`, so
// the parameter is out of scope in the following stages, and the error says
// so instead of reporting an undefined variable.
func TestPipeThenStage_BoundaryError(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		line, col int
		want      string
	}{
		{
			"inside a later stage's lambda",
			"fn f(): Int {\n    limit = 2\n    [1, 2, 3]\n    |> then |xs| xs |> Iter.filter(|x| x > Iter.count(xs) - limit)\n    |> Iter.count()\n}\n",
			4, 55,
			"'xs' is the parameter of the `then` stage on line 4, whose body ends at the next `|>`\nhelp: write `then |xs| { ... }` to keep the pipe inside it",
		},
		{
			"two stages later, on its own line",
			"fn f(): Int {\n    [1, 2, 3]\n    |> then |xs| xs\n    |> Iter.to_list()\n    |> Iter.filter(|x| x > Iter.count(xs))\n    |> Iter.count()\n}\n",
			5, 39,
			"'xs' is the parameter of the `then` stage on line 3, whose body ends at the next `|>`\nhelp: write `then |xs| { ... }` to keep the pipe inside it",
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

// TestPipeThenStage_OtherNamesKeepTheUndefinedError pins that only a
// parameter of an earlier `then` stage gets the boundary error: any other
// unbound name in a later stage, and a name used after a block-bodied `then`
// stage, report the ordinary undefined-variable error.
func TestPipeThenStage_OtherNamesKeepTheUndefinedError(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		line, col int
		want      string
	}{
		{
			"another name",
			"fn f(): Int {\n    [1, 2, 3]\n    |> then |xs| xs\n    |> Iter.filter(|x| x > nope)\n    |> Iter.count()\n}\n",
			4, 28, "undefined variable 'nope'",
		},
		{
			"after a block-bodied stage",
			"fn f(): Int {\n    [1, 2, 3]\n    |> then |xs| { xs }\n    |> Iter.filter(|x| x > Iter.count(xs))\n    |> Iter.count()\n}\n",
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

// TestPipeThenStage_BlockBodyKeepsThePipeInside is the acceptance mirror:
// with a block body the inner pipe stays inside the `then` lambda and `xs`
// is bound.
func TestPipeThenStage_BlockBodyKeepsThePipeInside(t *testing.T) {
	_, errs := checkSourceWithStdlib("fn f(): Int {\n    limit = 2\n    [1, 2, 3]\n    |> then |xs| { xs |> Iter.filter(|x| x > Iter.count(xs) - limit) }\n    |> Iter.count()\n}\n")
	expectNoStdlibErrors(t, errs)
}
