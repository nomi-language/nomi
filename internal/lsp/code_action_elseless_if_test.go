package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// "Add `else None`" adds an `else None` to an `if` without `else` whose
// branch is a Maybe, after a one-line `if` and as an `else` branch before a
// block's `end`, and the fixed program checks.
func TestElselessIfFix_AddsElseNone(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"inline",
			"fn label(n: Int) -> Maybe<String>\n    if n > 0 then Some(\"positive\")\nend\n",
			"fn label(n: Int) -> Maybe<String>\n    if n > 0 then Some(\"positive\") else None\nend\n",
		},
		{
			"block form",
			"fn label(n: Int) -> Maybe<Int>\n    if n > 0 then\n        m = n * 2\n        Some(m + 1)\n    end\nend\n",
			"fn label(n: Int) -> Maybe<Int>\n    if n > 0 then\n        m = n * 2\n        Some(m + 1)\n    else\n        None\n    end\nend\n",
		},
		{
			"a template slot",
			"import std::html.Html\n\nfn badge(on: Bool) -> Html\n    Html\"<b class=${if on then Some(\"on\")}></b>\"\nend\n",
			"import std::html.Html\n\nfn badge(on: Bool) -> Html\n    Html\"<b class=${if on then Some(\"on\") else None}></b>\"\nend\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer()
			actions := releaseActions(t, s, tc.src, "Add `else None`")
			if len(actions) != 1 {
				t.Fatalf("got %d fixes titled Add `else None`, want 1", len(actions))
			}
			got := applyTextEditsLastFirst(tc.src, actions[0].Edit.Changes[protocol.DocumentUri(releaseURI)])
			if got != tc.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tc.want)
			}
			s.docs.Open(releaseURI, got)
			if errs := s.docs.Snapshot(releaseURI).Analysis.TypeErrors; len(errs) > 0 {
				t.Fatalf("the fixed program has errors: %v", errs)
			}
		})
	}
}

// "Drop the value with `_ = ...`" writes `_ = ` before the value of an `if`
// without `else` that a statement drops, in block form when the `if` is
// one line, and the fixed program checks.
func TestElselessIfFix_DropsAStatementsValue(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"inline",
			"fn label(n: Int) -> Int\n    if n > 0 then n + 1\n    n\nend\n",
			"fn label(n: Int) -> Int\n    if n > 0 then\n        _ = n + 1\n    end\n    n\nend\n",
		},
		{
			"block form",
			"fn label(n: Int)\n    if n > 0 then\n        m = n * 2\n        m + 1\n    end\nend\n",
			"fn label(n: Int)\n    if n > 0 then\n        m = n * 2\n        _ = m + 1\n    end\nend\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer()
			actions := releaseActions(t, s, tc.src, "Drop the value with `_ = ...`")
			if len(actions) != 1 {
				t.Fatalf("got %d fixes titled Drop the value with `_ = ...`, want 1", len(actions))
			}
			got := applyTextEditsLastFirst(tc.src, actions[0].Edit.Changes[protocol.DocumentUri(releaseURI)])
			if got != tc.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tc.want)
			}
			s.docs.Open(releaseURI, got)
			if errs := s.docs.Snapshot(releaseURI).Analysis.TypeErrors; len(errs) > 0 {
				t.Fatalf("the fixed program has errors: %v", errs)
			}
		})
	}
}
