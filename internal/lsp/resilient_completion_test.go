package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A half-typed function body (see internal/parser/resilient_test.go),
// driven through the LSP's own request path. `  f = |x| n + ` is line 3 (1-based) — LSP line
// 2 — and the caret sits after the trailing space at character 14, which
// is where you are when you have typed the operator and want the operand
// completed.
const halfTypedDoc = "fn compute(n: Int): Int {\n" +
	"  total = n * 2\n" +
	"  f = |x| n + \n" +
	"}\n"

const completedDoc = "fn compute(n: Int): Int {\n" +
	"  total = n * 2\n" +
	"  f = |x| n + x\n" +
	"  total\n" +
	"}\n"

func completionLabels(t *testing.T, s *Server, uri string, line, char int) map[string]bool {
	t.Helper()
	res, err := s.textDocumentCompletion(nil, &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position: protocol.Position{
				Line:      protocol.UInteger(line),
				Character: protocol.UInteger(char),
			},
		},
	})
	if err != nil {
		t.Fatalf("textDocument/completion: %v", err)
	}
	items := completionItemsOf(t, res)
	labels := make(map[string]bool, len(items))
	for _, item := range items {
		labels[item.Label] = true
	}
	return labels
}

// TestCompletion_InsideHalfTypedFunctionBody is what resilient parsing is
// for. Without it the whole of `compute` is discarded, so completion at
// this caret offers only file-level and
// primitive symbols: not the parameter `n`, not the local `total`, not
// the lambda's `x`, and not even `compute` itself.
func TestCompletion_InsideHalfTypedFunctionBody(t *testing.T) {
	// Both documents use the same file BASENAME in different directories:
	// the builder records a module self-symbol named after the file, and
	// comparing two differently-named files would report that difference
	// as a missing completion.
	uri := "file:///a/resilient_completion.nomi"
	s := NewServer()
	s.docs.Open(uri, halfTypedDoc)
	doc := s.docs.Snapshot(uri)
	if len(doc.Errors) == 0 {
		t.Fatal("the fixture is supposed to have a syntax error; it parsed cleanly")
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("want the enclosing function to survive the parse, got %d top-level nodes", len(doc.Nodes))
	}

	got := completionLabels(t, s, uri, 2, 14)
	for _, want := range []string{"n", "total", "x", "compute"} {
		if !got[want] {
			t.Errorf("completion inside the half-typed body did not offer %q", want)
		}
	}

	// The same caret in the completed file is the reference: recovery
	// should offer the same scope, not a degraded one.
	refURI := "file:///b/resilient_completion.nomi"
	ref := NewServer()
	ref.docs.Open(refURI, completedDoc)
	if snap := ref.docs.Snapshot(refURI); len(snap.Errors) != 0 {
		t.Fatalf("the reference fixture must parse cleanly, got %+v", snap.Errors)
	}
	want := completionLabels(t, ref, refURI, 2, 14)
	for label := range want {
		if !got[label] {
			t.Errorf("completion offered %q on the valid file but not on the recovered one", label)
		}
	}
}

// TestCompletion_InsideHalfTypedTestBody covers the same thing for a
// `test` block, where a whole-declaration requirement ("must contain at
// least one assert") would otherwise discard the declaration for the very
// reason that the assertion is half typed.
func TestCompletion_InsideHalfTypedTestBody(t *testing.T) {
	src := "test \"arithmetic\" {\n" +
		"  subject = 2\n" +
		"  assert subject == \n" +
		"}\n"
	uri := "file:///resilient_test_body.nomi"
	s := NewServer()
	s.docs.Open(uri, src)
	if snap := s.docs.Snapshot(uri); len(snap.Errors) == 0 {
		t.Fatal("the fixture is supposed to have a syntax error; it parsed cleanly")
	}
	got := completionLabels(t, s, uri, 2, 13)
	if !got["subject"] {
		t.Error("completion inside a half-typed test body did not offer the local `subject`")
	}
}

// TestDiagnostics_HalfTypedBodyReportsOnlyTheSyntaxError is the other half
// of the contract. A partially-read declaration must not be judged as a
// complete one: recovery makes the checker reachable where it was not
// before, and every judgement it makes about the damaged declaration
// describes the parser's gap rather than the user's code.
func TestDiagnostics_HalfTypedBodyReportsOnlyTheSyntaxError(t *testing.T) {
	// `fn bad` is syntactically fine and genuinely ill-typed. Its
	// diagnostic must survive — the suppression is scoped to the
	// declaration that was repaired, not applied to the file.
	src := halfTypedDoc + "\nfn bad(): Int {\n  \"a string, not an Int\"\n}\n"
	uri := "file:///resilient_diagnostics.nomi"
	s := NewServer()
	s.docs.Open(uri, src)
	doc := s.docs.Snapshot(uri)

	if len(doc.Damaged) != 1 {
		t.Fatalf("want exactly the first declaration marked damaged, got %+v", doc.Damaged)
	}
	if len(doc.Errors) != 1 || !strings.Contains(doc.Errors[0].Message, "unexpected token RBRACE") {
		t.Fatalf("the syntax error must still be reported, got %+v", doc.Errors)
	}

	var sawBadReturn bool
	for _, e := range doc.Analysis.TypeErrors {
		if doc.Damaged[0].Contains(e.Line, e.Col) {
			t.Errorf("type diagnostic inside the repaired declaration reached the editor: %d:%d %s",
				e.Line, e.Col, e.Message)
		}
		if strings.Contains(e.Message, "expected Int, got String") {
			sawBadReturn = true
		}
	}
	if !sawBadReturn {
		t.Error("the clean declaration's genuine type error was suppressed too; " +
			"suppression must be scoped to the repaired declaration")
	}
}
