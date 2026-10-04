package lsp

import (
	"fmt"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const spanURI = "file:///span/main.nomi"

// publishedFor opens src and answers its published diagnostics, for a client
// that reads relatedInformation or one that does not.
func publishedFor(t *testing.T, s *Server, src string, related bool) []protocol.Diagnostic {
	t.Helper()
	s.docs.Open(spanURI, src)
	snap := s.docs.Snapshot(spanURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	return buildPublishedDiagnosticsFor(spanURI, snap.Content, nil, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors, related)
}

func findDiag(t *testing.T, diags []protocol.Diagnostic, headline string) protocol.Diagnostic {
	t.Helper()
	for _, d := range diags {
		if diagnosticHeadline(d.Message) == headline {
			return d
		}
	}
	var all []string
	for _, d := range diags {
		all = append(all, d.Message)
	}
	t.Fatalf("no diagnostic %q in:\n%s", headline, strings.Join(all, "\n"))
	return protocol.Diagnostic{}
}

func rangeText(r protocol.Range) string {
	return fmt.Sprintf("%d:%d-%d:%d", r.Start.Line, r.Start.Character, r.End.Line, r.End.Character)
}

// A diagnostic spans what it is about, its hints are `help:` lines of its
// message, and its related locations are relatedInformation.
func TestPublishedDiagnostics_RangeHintsAndRelated(t *testing.T) {
	src := "fn dup(): Int {\n    1\n}\n\nfn dup(): Int {\n    2\n}\n\nfn f(): Int {\n    count = 1\n    x: Int = \"é\"\n    count + cuont + x\n}\n"
	diags := publishedFor(t, NewServer(), src, true)

	dup := findDiag(t, diags, "'dup' is already defined in this scope as a function")
	if got := rangeText(dup.Range); got != "4:3-4:6" {
		t.Errorf("redeclaration range %s, want 4:3-4:6", got)
	}
	if !strings.HasSuffix(dup.Message, "\nhelp: pick a different name") {
		t.Errorf("message %q lacks its hint line", dup.Message)
	}
	if len(dup.RelatedInformation) != 1 {
		t.Fatalf("related = %+v", dup.RelatedInformation)
	}
	rel := dup.RelatedInformation[0]
	if rel.Location.URI != spanURI || rangeText(rel.Location.Range) != "0:3-0:6" || rel.Message != "'dup' is first defined here" {
		t.Errorf("related = %+v", rel)
	}

	undefined := findDiag(t, diags, "undefined variable 'cuont'")
	if got := rangeText(undefined.Range); got != "11:12-11:17" {
		t.Errorf("undefined-name range %s, want 11:12-11:17", got)
	}
	if !strings.Contains(undefined.Message, "\nhelp: did you mean 'count'?") {
		t.Errorf("message %q lacks its suggestion", undefined.Message)
	}

	// The value `"é"` is three UTF-16 units wide (two quotes and é).
	mismatch := findDiag(t, diags, "type mismatch: expected Int, got String")
	if got := rangeText(mismatch.Range); got != "10:13-10:16" {
		t.Errorf("mismatch range %s, want 10:13-10:16", got)
	}

	// A client that does not read relatedInformation gets it as a note line.
	plain := findDiag(t, publishedFor(t, NewServer(), src, false), "'dup' is already defined in this scope as a function")
	if plain.RelatedInformation != nil || !strings.HasSuffix(plain.Message, "\nnote: 'dup' is first defined here (main.nomi:1:4)") {
		t.Errorf("without relatedInformation support: %q %+v", plain.Message, plain.RelatedInformation)
	}
}

func TestClientRelatedInformation(t *testing.T) {
	yes := true
	params := &protocol.InitializeParams{}
	if clientRelatedInformation(params) {
		t.Error("no capabilities read as support")
	}
	params.Capabilities.TextDocument = &protocol.TextDocumentClientCapabilities{
		PublishDiagnostics: &protocol.PublishDiagnosticsClientCapabilities{RelatedInformation: &yes},
	}
	if !clientRelatedInformation(params) {
		t.Error("declared support not read")
	}
}

// A parse error spans the token it is reported at.
func TestPublishedDiagnostics_ParseErrorSpansItsToken(t *testing.T) {
	diags := publishedFor(t, NewServer(), "fn f(): Int {\n    1 +\n}\n", true)
	if len(diags) == 0 {
		t.Fatal("no diagnostics")
	}
	if r := diags[0].Range; r.End == r.Start {
		t.Errorf("parse error is a point: %s %q", rangeText(r), diags[0].Message)
	}
}

// fillAt asks for quick fixes as a client does that sends only the
// diagnostics whose range holds the cursor, the cursor being where ‸ is.
func fillAt(t *testing.T, marked string) []protocol.CodeAction {
	t.Helper()
	at := strings.Index(marked, "‸")
	src := strings.Replace(marked, "‸", "", 1)
	line := strings.Count(src[:at], "\n")
	col := at - (strings.LastIndex(src[:at], "\n") + 1)
	cursor := protocol.Position{Line: uint32(line), Character: uint32(col)}
	s := NewServer()
	all := publishedFor(t, s, src, true)
	var sent []protocol.Diagnostic
	for _, d := range all {
		if !posBefore(cursor, d.Range.Start) && posBefore(cursor, d.Range.End) {
			d.Code = nil
			sent = append(sent, d)
		}
	}
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: spanURI},
		Range:        protocol.Range{Start: cursor, End: cursor},
		Context:      protocol.CodeActionContext{Diagnostics: sent, Only: []protocol.CodeActionKind{protocol.CodeActionKindQuickFix}},
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, _ := res.([]protocol.CodeAction)
	return actions
}

// A quick fix is offered with the cursor anywhere in its diagnostic's span,
// not only on the span's first character.
func TestQuickFixes_OfferedAnywhereInTheSpan(t *testing.T) {
	for _, tc := range []struct{ name, src, title string }{
		{"missing function", fillShapeDecls + "impl Shape for Squ‸are {\n}\n", "Implement missing functions"},
		{"missing field", "struct Point {\n    x: Int\n    y: Int\n}\n\nfn f(): Point {\n    Poi‸nt{x: 1}\n}\n", "Add missing field 'y'"},
		{"missing arm", "enum Color {\n    Red\n    Green\n}\n\nfn f(c: Color): Int {\n    case ‸c {\n        Color.Red -> 1\n    }\n}\n", "Add missing arm .Green"},
		{"undefined call", "fn f(): Int {\n    sho‸ut(1)\n}\n", "Generate function 'shout'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actions := fillAt(t, tc.src)
			for _, a := range actions {
				if a.Title == tc.title {
					if len(a.Diagnostics) == 0 {
						t.Errorf("%q names no diagnostic", a.Title)
					}
					return
				}
			}
			t.Errorf("no %q with the cursor inside the span; got %q", tc.title, actionTitles(actions))
		})
	}
}

// A fix finds its node by the diagnostic's range, so a range that starts
// before the node's own position still reaches it.
func TestFill_FindsTheNodeInsideTheRange(t *testing.T) {
	src := "struct Point {\n    x: Int\n    y: Int\n}\n\nfn f(): Point {\n    Point{x: 1}\n}\n"
	s := NewServer()
	all := publishedFor(t, s, src, true)
	d := findDiag(t, all, "missing field 'y' of Point")
	d.Code = nil
	d.Range.Start.Character = 0 // the whole line up to the span's end
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: spanURI},
		Range:        d.Range,
		Context:      protocol.CodeActionContext{Diagnostics: []protocol.Diagnostic{d}, Only: []protocol.CodeActionKind{protocol.CodeActionKindQuickFix}},
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, _ := res.([]protocol.CodeAction)
	for _, a := range actions {
		if a.Title == "Add missing field 'y'" {
			return
		}
	}
	t.Fatalf("no fill fix: %q", actionTitles(actions))
}
