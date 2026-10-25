package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const limitInfiniteURI = "file:///limitinfinite/main.nomi"

// limitInfiniteActions opens src and returns the "Limit with `Iter.take`"
// fixes offered over every diagnostic the client holds, its codes dropped
// as glsp drops them on the round trip.
func limitInfiniteActions(t *testing.T, s *Server, src string) []protocol.CodeAction {
	t.Helper()
	s.docs.Open(limitInfiniteURI, src)
	snap := s.docs.Snapshot(limitInfiniteURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	diags := buildPublishedDiagnostics(limitInfiniteURI, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors)
	for i := range diags {
		diags[i].Code = nil
	}
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(limitInfiniteURI)},
		Context: protocol.CodeActionContext{
			Diagnostics: diags,
			Only:        []protocol.CodeActionKind{protocol.CodeActionKindQuickFix},
		},
	})
	if err != nil {
		t.Fatalf("code action: %v", err)
	}
	actions, _ := res.([]protocol.CodeAction)
	var out []protocol.CodeAction
	for _, a := range actions {
		if a.Title == "Limit with `Iter.take`" {
			out = append(out, a)
		}
	}
	return out
}

// A pipe stage draining an infinite iterator gets `Iter.take(todo)` before
// it, and the fixed program's only diagnostic is the `todo`.
func TestLimitInfiniteFix_InsertsTake(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"pipe on its own line",
			"fn coins(coin: Int) -> List<Int> {\n    coin\n    |> Iter.repeat()\n    |> Iter.to_list()\n}\n",
			"fn coins(coin: Int) -> List<Int> {\n    coin\n    |> Iter.repeat()\n    |> Iter.take(todo) |> Iter.to_list()\n}\n",
		},
		{
			"one line",
			"fn total() -> Int {\n    Iter.from(0) |> Iter.map(&(&1 * 2)) |> Iter.sum()\n}\n",
			"fn total() -> Int {\n    Iter.from(0) |> Iter.map(&(&1 * 2)) |> Iter.take(todo) |> Iter.sum()\n}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer()
			actions := limitInfiniteActions(t, s, tc.src)
			if len(actions) != 1 {
				t.Fatalf("got %d fixes, want 1", len(actions))
			}
			got := applyWorkspaceEdit(t, tc.src, actions[0], limitInfiniteURI)
			if got != tc.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tc.want)
			}
			s.docs.Open(limitInfiniteURI, got)
			for _, e := range s.docs.Snapshot(limitInfiniteURI).Analysis.TypeErrors {
				if !strings.Contains(e.Message, "todo") {
					t.Fatalf("the fixed program has an error: %v", e)
				}
			}
		})
	}
}

// A nested call has no pipe to put a stage in, so it gets no fix.
func TestLimitInfiniteFix_NotOfferedForANestedCall(t *testing.T) {
	src := "fn ones() -> List<Int> {\n    Iter.to_list(Iter.repeat(1))\n}\n"
	s := NewServer()
	if actions := limitInfiniteActions(t, s, src); len(actions) != 0 {
		t.Fatalf("got %d fixes, want none", len(actions))
	}
	errs := s.docs.Snapshot(limitInfiniteURI).Analysis.TypeErrors
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "of an infinite iterator") {
		t.Fatalf("want the infinite-iterator error alone, got %v", errs)
	}
}
