package lsp

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/format"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const refactorURI = "file:///refactor/main.nomi"

// refactorSource strips the range markers from src: « and » around a
// selection, or ‸ at a cursor.
func refactorSource(t *testing.T, src string) (string, protocol.Range) {
	t.Helper()
	pos := func(clean string, off int) protocol.Position {
		line := strings.Count(clean[:off], "\n")
		col := off - (strings.LastIndex(clean[:off], "\n") + 1)
		return protocol.Position{Line: uint32(line), Character: uint32(col)}
	}
	if i := strings.Index(src, "‸"); i >= 0 {
		clean := src[:i] + src[i+len("‸"):]
		p := pos(clean, i)
		return clean, protocol.Range{Start: p, End: p}
	}
	i := strings.Index(src, "«")
	j := strings.Index(src, "»")
	if i < 0 || j < i {
		t.Fatal("source has no range marker")
	}
	clean := src[:i] + src[i+len("«"):j] + src[j+len("»"):]
	return clean, protocol.Range{Start: pos(clean, i), End: pos(clean, j-len("«"))}
}

// refactorActions opens src (with its range marker) and returns every code
// action offered for the range, and the source without the marker.
func refactorActions(t *testing.T, s *Server, src string) ([]protocol.CodeAction, string) {
	t.Helper()
	clean, rng := refactorSource(t, src)
	s.docs.Open(refactorURI, clean)
	snap := s.docs.Snapshot(refactorURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	diags := buildPublishedDiagnostics(refactorURI, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors)
	for i := range diags {
		diags[i].Code = nil
	}
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(refactorURI)},
		Range:        rng,
		Context:      protocol.CodeActionContext{Diagnostics: diags},
	})
	if err != nil {
		t.Fatalf("code action: %v", err)
	}
	actions, _ := res.([]protocol.CodeAction)
	return actions, clean
}

func actionTitles(actions []protocol.CodeAction) []string {
	out := make([]string, len(actions))
	for i, a := range actions {
		out[i] = a.Title
	}
	return out
}

// checkRefactor applies the action titled title, offered for src's range,
// and returns the result after asserting it is `nomi fmt` output that
// parses and whose only errors are the allowed ones. A written hole is
// `todo`, which checks.
func checkRefactor(t *testing.T, src, title string, kind protocol.CodeActionKind, allowed ...string) string {
	t.Helper()
	s := NewServer()
	actions, clean := refactorActions(t, s, src)
	var found *protocol.CodeAction
	for i := range actions {
		if actions[i].Title == title {
			found = &actions[i]
		}
	}
	if found == nil {
		t.Fatalf("no action %q; offered %q", title, actionTitles(actions))
	}
	if found.Kind == nil || *found.Kind != kind {
		t.Errorf("kind = %v, want %s", found.Kind, kind)
	}
	edited := applyWorkspaceEdit(t, clean, *found, refactorURI)
	if out, err := format.Format(edited); err != nil || out != edited {
		t.Errorf("result is not nomi fmt output (err %v):\n%s\nformatted:\n%s", err, edited, out)
	}
	s.docs.Open(refactorURI, edited)
	snap := s.docs.Snapshot(refactorURI)
	if len(snap.Errors) > 0 {
		t.Fatalf("result does not parse: %v\n%s", snap.Errors, edited)
	}
	for _, e := range snap.Analysis.TypeErrors {
		ok := false
		for _, m := range allowed {
			ok = ok || e.Message == m
		}
		if !ok {
			t.Errorf("result has error %d:%d %s\n%s", e.Line, e.Col, e.Message, edited)
		}
	}
	return edited
}

// refuseRefactor asserts src's range is offered no action with a title
// starting with prefix. src must parse and check, but for errors whose
// message is one of allowed, so the refusal is not for a broken source.
func refuseRefactor(t *testing.T, src, prefix string, allowed ...string) {
	t.Helper()
	s := NewServer()
	actions, _ := refactorActions(t, s, src)
	snap := s.docs.Snapshot(refactorURI)
	if len(snap.Errors) > 0 {
		t.Fatalf("source does not parse: %v", snap.Errors)
	}
	for _, e := range snap.Analysis.TypeErrors {
		ok := false
		for _, m := range allowed {
			ok = ok || e.Message == m
		}
		if !ok {
			t.Errorf("source has error %d:%d %s", e.Line, e.Col, e.Message)
		}
	}
	for _, a := range actions {
		if strings.HasPrefix(a.Title, prefix) {
			t.Fatalf("offered %q", a.Title)
		}
	}
}

// fnMain wraps body lines in `fn main() { ... }`, each line indented once,
// after an import of std/io.
func fnMain(body string) string {
	return fnMainAfter("", body)
}

// fnMainAfter is fnMain with decls between the import and main.
func fnMainAfter(decls, body string) string {
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = "    " + l
		}
	}
	return "import std/io\n\n" + decls + "fn main() {\n" + strings.Join(lines, "\n") + "\n}\n"
}

// TestRefactor_DamagedDeclarationOffersNoRewrite: in a declaration the
// parser had to repair while it is being typed, no rewrite is offered.
// The rewrites render parts of the declaration with the formatter, which
// cannot render the parser's stand-in for the unreadable text, and a
// rewrite of half-read code would invent the other half. Found by
// TestLSPSurvivesTyping: each request panicked.
func TestRefactor_DamagedDeclarationOffersNoRewrite(t *testing.T) {
	for _, src := range []string{
		// Convert to case, over an `if` whose else branch is broken.
		"fn count(n: Int): Int {\n    ‸if n == 0 { 0 } else { count(n -)} n))\n    }\n}\n",
		// Convert from pipe, over a pipe whose lambda is broken.
		"fn main() {\n    mapped =\n        [1, 2, 3]\n        |> Iter.map(‸|x| {\n            if x })0 }\n            x * 10\n        })\n        |> Iter.to_list()\n    dbg mapped\n}\n",
		"import std/io\n\nfn main() {\n    result =\n        Range.naturals()\n        |> Iter.map(|n| {\n            io.print(\"squaring ${}\")})   |> Iter.take‸(2)\n        |> Iter.to_list()\n    dbg result\n}\n",
	} {
		actions, _ := refactorActions(t, NewServer(), src)
		for _, a := range actions {
			if a.Kind != nil && strings.HasPrefix(string(*a.Kind), "refactor") {
				t.Errorf("offered %q in a damaged declaration:\n%s", a.Title, src)
			}
		}
	}
}
