package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// openLiteralDoc writes main.nomi into a fresh project, opens it on a new
// server and answers the server and the document's snapshot.
func openLiteralDoc(t testing.TB, src string) (*Server, *analysis.DocSnapshot) {
	t.Helper()
	dir := writeProject(t, map[string]string{
		"main.nomi": src,
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
	})
	s := NewServer()
	uri := "file://" + filepath.Join(dir, "main.nomi")
	s.docs.Open(uri, src)
	snap := s.docs.Snapshot(uri)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("the document has no analysis")
	}
	if len(snap.Errors) > 0 || len(snap.Analysis.TypeErrors) > 0 {
		t.Fatalf("the fixture does not check: %v %v", snap.Errors, snap.Analysis.TypeErrors)
	}
	return s, snap
}

func TestLiteralDiagnostics_InvalidStaticDate(t *testing.T) {
	src := "import std/calendar.Date\n\nfn main() {\n    _ = Date\"2026-13-04\"\n}\n"
	s, snap := openLiteralDoc(t, src)
	diags := s.literalDiagnostics(snap, true)
	if len(diags) != 1 {
		t.Fatalf("want one diagnostic, got %d: %+v", len(diags), diags)
	}
	d := diags[0]
	if want := `Date"2026-13-04" is invalid: InvalidValue("month out of range: 13")`; d.Message != want {
		t.Errorf("message = %q, want %q", d.Message, want)
	}
	want := protocol.Range{Start: protocol.Position{Line: 3, Character: 8}, End: protocol.Position{Line: 3, Character: 24}}
	if d.Range != want {
		t.Errorf("range = %+v, want %+v", d.Range, want)
	}
	if d.Severity == nil || *d.Severity != protocol.DiagnosticSeverityError {
		t.Errorf("severity = %v, want Error", d.Severity)
	}
}

func TestLiteralDiagnostics_ValidDateHasNoneAndHoversItsValue(t *testing.T) {
	src := "import std/calendar.Date\n\nfn main() {\n    _ = Date\"2026-05-04\"\n}\n"
	s, snap := openLiteralDoc(t, src)
	if diags := s.literalDiagnostics(snap, true); len(diags) != 0 {
		t.Fatalf("a valid literal is reported: %+v", diags)
	}
	res, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(snap.URI)},
		Position:     protocol.Position{Line: 3, Character: 15},
	}})
	if err != nil || res == nil {
		t.Fatalf("no hover: %v", err)
	}
	got := res.Contents.(protocol.MarkupContent).Value
	want := "```nomi\nDate\"2026-05-04\": Result<Date, Error>\n```\n\n```text\nOk(2026-05-04)\n```"
	if got != want {
		t.Errorf("hover =\n%s\nwant\n%s", got, want)
	}
	wantRange := protocol.Range{Start: protocol.Position{Line: 3, Character: 8}, End: protocol.Position{Line: 3, Character: 24}}
	if res.Range == nil || *res.Range != wantRange {
		t.Errorf("hover range = %+v, want %+v", res.Range, wantRange)
	}
}

// A Regex is a host handle; its hover renders the Ok payload through std's
// `impl Debug for Regex`, which failed at run time before the VM's Debug
// dispatched on a host handle.
func TestLiteralDiagnostics_ValidRegexHoversItsValue(t *testing.T) {
	src := "import std/regex.Regex\n\nfn main() {\n    _ = Regex`\\d+`\n}\n"
	s, snap := openLiteralDoc(t, src)
	if diags := s.literalDiagnostics(snap, true); len(diags) != 0 {
		t.Fatalf("a valid literal is reported: %+v", diags)
	}
	res, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(snap.URI)},
		Position:     protocol.Position{Line: 3, Character: 15},
	}})
	if err != nil || res == nil {
		t.Fatalf("no hover: %v", err)
	}
	got := res.Contents.(protocol.MarkupContent).Value
	want := "```nomi\nRegex`\\d+`: Result<Regex, String>\n```\n\n```text\nOk(Regex`\\d+`)\n```"
	if got != want {
		t.Errorf("hover =\n%s\nwant\n%s", got, want)
	}
}

func TestLiteralDiagnostics_InterpolatedLiteralIsNotEvaluated(t *testing.T) {
	src := "import std/calendar.Date\n\nfn main() {\n    month = \"13\"\n    _ = Date\"2026-${month}-04\"\n}\n"
	s, snap := openLiteralDoc(t, src)
	if lits := staticLiterals(snap.Content, snap.Analysis); len(lits) != 0 {
		t.Fatalf("an interpolated literal is taken as static: %+v", lits)
	}
	if diags := s.literalDiagnostics(snap, true); len(diags) != 0 {
		t.Fatalf("an interpolated literal is reported: %+v", diags)
	}
}

// userLiteral is a file declaring the tag type Probe whose handler's body is
// body, and a main using Probe"x".
func userLiteral(body string) string {
	return "import {\n    std/io\n    std/literals.{Fragment, Literal}\n}\n\n" +
		"type Probe\n\n" +
		"fn spin(n: Int): Result<Probe, String> {\n    if n < 0 {\n        Err(\"negative\")\n    } else {\n        spin(n + 1)\n    }\n}\n\n" +
		"impl Literal for Probe {\n    fn from_fragments(fragments: List<Fragment<String>>): Result<Probe, String> {\n" +
		body +
		"    }\n}\n\n" +
		"fn main() {\n    _ = Probe\"x\"\n    io.print(\"\")\n}\n"
}

func TestLiteralDiagnostics_UserHandlerIsEvaluated(t *testing.T) {
	s, snap := openLiteralDoc(t, userLiteral("        _ = fragments\n        Err(\"no\")\n"))
	diags := s.literalDiagnostics(snap, true)
	if len(diags) != 1 || diags[0].Message != `Probe"x" is invalid: "no"` {
		t.Fatalf("want the user handler's Err reported, got %+v", diags)
	}
}

func TestLiteralDiagnostics_LoopingHandlerIsSkippedWithinItsBudget(t *testing.T) {
	s, snap := openLiteralDoc(t, userLiteral("        _ = fragments\n        spin(0)\n"))
	start := time.Now()
	diags := s.literalDiagnostics(snap, true)
	took := time.Since(start)
	if len(diags) != 0 {
		t.Fatalf("a handler that never returns is reported: %+v", diags)
	}
	lits := staticLiterals(snap.Content, snap.Analysis)
	if r, ok := s.literals.lookup(snap.URI, snap.Content, lits[0]); !ok || !r.skipped || r.why != "limit" {
		t.Fatalf("the literal is not recorded as stopped by its limit: %+v %v", r, ok)
	}
	// The load is most of it; the evaluation stops at literalSteps, well
	// inside literalTime.
	if took > time.Second {
		t.Errorf("evaluating a looping handler took %v", took)
	}
}

func TestLiteralDiagnostics_EffectfulHandlerIsSkipped(t *testing.T) {
	s, snap := openLiteralDoc(t, userLiteral("        _ = fragments\n        io.print(\"evaluated\")\n        Err(\"no\")\n"))
	if diags := s.literalDiagnostics(snap, true); len(diags) != 0 {
		t.Fatalf("a handler that prints was evaluated: %+v", diags)
	}
	lits := staticLiterals(snap.Content, snap.Analysis)
	r, _ := s.literals.lookup(snap.URI, snap.Content, lits[0])
	if want := "effects: Probe.from_fragments calls io.print"; r.why != want {
		t.Fatalf("skipped for %q, want %q", r.why, want)
	}
}

// fiftyLiterals is a file with 50 static Date literals, every fifth invalid.
func fiftyLiterals() string {
	var b strings.Builder
	b.WriteString("import std/calendar.Date\n\nfn main() {\n")
	for i := range 50 {
		month := 1 + i%12
		if i%5 == 0 {
			month = 13 + i
		}
		fmt.Fprintf(&b, "    _ = Date\"2026-%02d-%02d\"\n", month, 1+i%28)
	}
	b.WriteString("}\n")
	return b.String()
}

func TestLiteralDiagnostics_FiftyLiterals(t *testing.T) {
	s, snap := openLiteralDoc(t, fiftyLiterals())
	if diags := s.literalDiagnostics(snap, true); len(diags) != 10 {
		t.Fatalf("want 10 invalid literals reported, got %d", len(diags))
	}
}

// BenchmarkLiteralDiagnostics_Cold is the background work for a buffer of 50
// static literals: loading the buffer with its probes and evaluating them.
func BenchmarkLiteralDiagnostics_Cold(b *testing.B) {
	_, snap := openLiteralDoc(b, fiftyLiterals())
	lits := staticLiterals(snap.Content, snap.Analysis)
	path := uriToPath(snap.URI)
	evaluateLiterals(context.Background(), path, snap.Content, lits) // the process's stdlib lowering
	b.ResetTimer()
	for range b.N {
		evaluateLiterals(context.Background(), path, snap.Content, lits)
	}
}

// BenchmarkLiteralDiagnostics_Publish is what a publish adds for a buffer of
// 50 static literals once their results are cached: finding the literals and
// reading the cache.
func BenchmarkLiteralDiagnostics_Publish(b *testing.B) {
	s, snap := openLiteralDoc(b, fiftyLiterals())
	s.literalDiagnostics(snap, true)
	b.ResetTimer()
	for range b.N {
		s.literalDiagnostics(snap, false)
	}
}

// A publish does not wait for the evaluation: it reports what is cached, and
// the background evaluation publishes again with the literal's diagnostic.
func TestLiteralDiagnostics_PublishEvaluatesInTheBackground(t *testing.T) {
	src := "import std/calendar.Date\n\nfn main() {\n    _ = Date\"2026-02-30\"\n}\n"
	s, snap := openLiteralDoc(t, src)
	published := make(chan []protocol.Diagnostic, 4)
	s.notify = func(method string, params any) {
		if p, ok := params.(*protocol.PublishDiagnosticsParams); ok {
			published <- p.Diagnostics
		}
	}
	s.publish(s.notify, snap.URI)
	if first := <-published; len(first) != 0 {
		t.Fatalf("the first publish waited for the evaluation: %+v", first)
	}
	select {
	case second := <-published:
		if len(second) != 1 || !strings.HasPrefix(second[0].Message, `Date"2026-02-30" is invalid: `) {
			t.Fatalf("the background publish = %+v", second)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the background evaluation never published")
	}
}
