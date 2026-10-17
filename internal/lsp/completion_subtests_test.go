package lsp

import "testing"

// `subtests` is offered where a statement starts, beside `assert`, and not
// as a pipe stage, which it cannot be.
func TestCompletion_SubtestsAtAStatement(t *testing.T) {
	items := complete(t, "test \"t\" {\n    "+cursorMark+"\n}\n")
	labelsInclude(t, items, "subtests", "assert")
	items = complete(t, "fn f(xs: List<Int>) -> Int {\n    xs |> "+cursorMark+"\n}\n")
	labelsExclude(t, items, "subtests")
}
