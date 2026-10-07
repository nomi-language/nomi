package analysis_test

import (
	"strings"
	"testing"
)

// A pipe stage is a call (pipe_stage_call.go). Each bare stage below passed
// the checker: `3 |> double` ran and `x |> Ok` was BLOCKED at run time with
// "the pipe operator". The error names the call to write and sits on the
// stage.

const pipeStageDecls = `enum Dir {
  North
}

fn double(n: Int): Int {
  n * 2
}

struct Box {
  n: Int
}

impl Box {
  fn make(n: Int): Box {
    Box{n: n}
  }
}

`

func TestPipeStage_BareNameIsRejected(t *testing.T) {
	for _, tc := range []struct {
		stmt string
		want string
		col  int
	}{
		{"x = 3 |> double", "a pipe stage is a call: write `double()`", 12},
		{"x = 3 |> double |> Int.to_string()", "a pipe stage is a call: write `double()`", 12},
		{"x = 3 |> Int.to_string", "a pipe stage is a call: write `Int.to_string()`", 12},
		{"x: Result<Int, String> = 3 |> Ok", "a pipe stage is a call: write `Ok()`", 33},
		{"x: Maybe<Int> = 3 |> Some", "a pipe stage is a call: write `Some()`", 24},
		{"x = 3 |> Box.make", "a pipe stage is a call: write `Box.make()`", 12},
		{"x = 3 |> (double)", "a pipe stage is a call: write `double()`", 12},
		{"x: Dir = 3 |> .North", "a pipe stage is a call: write the variant through its enum, as in `Enum.North()`", 17},
		{"x = 3 |> 4", "a pipe stage is a call, such as `f()`, or a keyword stage", 12},
	} {
		src := pipeStageDecls + "fn main() {\n  " + tc.stmt + "\n  _ = x\n}\n"
		_, errs := checkSourceWithStdlib(src)
		found := false
		for _, e := range errs {
			if strings.HasPrefix(e.Message, tc.want) {
				found = true
				if e.Line != 20 || e.Col != tc.col {
					t.Errorf("%s: the error is at %d:%d, want 20:%d (the stage)", tc.stmt, e.Line, e.Col, tc.col)
				}
				if !strings.Contains(diagText(e), "a name without parentheses is a function reference") {
					t.Errorf("%s: the error has no hint: %s", tc.stmt, diagText(e))
				}
			}
		}
		if !found {
			expectStdlibError(t, errs, tc.want)
		}
	}
}

// The mirror: calls, `then` and keyword stages stay accepted, and a name
// passed as an argument is still a function reference.
func TestPipeStage_CallsLambdasAndKeywordsAreAccepted(t *testing.T) {
	for _, stmt := range []string{
		"x = 3 |> double()",
		"x = 3 |> double() |> Int.to_string()",
		"x: Result<Int, String> = 3 |> Ok()",
		"x = 3 |> Box.make()",
		"x = 3 |> then |n| n + 1",
		"x = 3 |> then |n| { n |> double() }",
		"x = (3, 4) |> then |(a, b)| a + b",
		"x = 3 |> dbg",
		"x = True |> if { 1 } else { 0 }",
		"x = 3 |> case {\n    3 -> True\n    _ -> False\n  }",
		"x = Some(3) |> try",
		"x = [1, 2] |> Iter.map(double) |> Iter.to_list()",
	} {
		src := pipeStageDecls + "fn demo(): Maybe<Unit> {\n  " + stmt + "\n  _ = x\n  Some(Unit)\n}\n"
		_, errs := checkSourceWithStdlib(src)
		expectNoStdlibErrors(t, errs)
	}
}

// A lambda is not a stage: its body runs to the end of its expression, so
// `then` is what applies one to the piped value. The error names the `then`
// to write, sits on the lambda, and is the only error: the lambda is still
// typed against the piped value.
func TestPipeStage_LambdaIsRejected(t *testing.T) {
	for _, tc := range []struct {
		stmt string
		want string
	}{
		{"x = 3 |> |n| n + 1", "a lambda is not a pipe stage: write `then |n| ...`"},
		{"x = 3 |> (|n| n + 1)", "a lambda is not a pipe stage: write `then |n| ...`"},
		{"x = (3, 4) |> |(a, b)| a + b", "a lambda is not a pipe stage: write `then |v| ...`"},
		{"x = 3 |> |n| n + 1 |> double()", "a lambda is not a pipe stage: write `then |n| ...`"},
	} {
		src := pipeStageDecls + "fn main() {\n  " + tc.stmt + "\n  _ = x\n}\n"
		_, errs := checkSourceWithStdlib(src)
		if len(errs) != 1 || errs[0].Message != tc.want {
			t.Fatalf("%s: errors = %v, want exactly %q", tc.stmt, errs, tc.want)
		}
		if !strings.Contains(diagText(errs[0]), "`then` applies a lambda to the piped value") {
			t.Errorf("%s: the error has no hint: %s", tc.stmt, diagText(errs[0]))
		}
	}
}
