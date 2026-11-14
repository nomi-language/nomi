package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"

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
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
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

func TestLiteralHover_DateHoversItsValue(t *testing.T) {
	src := "import std::calendar.Date\n\nfn main()\n    _ = Date\"2026-05-04\"\nend\n"
	s, snap := openLiteralDoc(t, src)
	res, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(snap.URI)},
		Position:     protocol.Position{Line: 3, Character: 15},
	}})
	if err != nil || res == nil {
		t.Fatalf("no hover: %v", err)
	}
	got := res.Contents.(protocol.MarkupContent).Value
	want := "```nomi\nDate\"2026-05-04\": Date\n```\n\n```text\n2026-05-04\n```\n\n*typed literal*: a `Date`, not a `String`, built by `Date`'s `Literal` impl from its text"
	if got != want {
		t.Errorf("hover =\n%s\nwant\n%s", got, want)
	}
	wantRange := protocol.Range{Start: protocol.Position{Line: 3, Character: 8}, End: protocol.Position{Line: 3, Character: 24}}
	if res.Range == nil || *res.Range != wantRange {
		t.Errorf("hover range = %+v, want %+v", res.Range, wantRange)
	}
}

// Literal evaluation loads the whole file on the VM, so a function the IR
// builder lowered into an invalid graph (a tail `else if` chain whose every
// arm returns, which panicked on ir.Lint) took the language server down with
// it. The literal is evaluated and hovers its value.
func TestLiteralHover_FileWithReturningElseIfChainIsEvaluated(t *testing.T) {
	src := "import\n    std::calendar.Date\n    std::io\nend\n\n" +
		"fn sign(x: Int) -> Int\n    if x < 0 then\n        return -1\n    else if x == 0 then\n        return 0\n    else\n        return 1\n    end\nend\n\n" +
		"fn main()\n    _ = Date\"2026-05-04\"\n    io.print(sign(2))\nend\n"
	s, snap := openLiteralDoc(t, src)
	res, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(snap.URI)},
		Position:     protocol.Position{Line: 16, Character: 15},
	}})
	if err != nil || res == nil {
		t.Fatalf("no hover: %v", err)
	}
	if got := res.Contents.(protocol.MarkupContent).Value; !strings.Contains(got, "2026-05-04") {
		t.Errorf("the literal was not evaluated; hover =\n%s", got)
	}
}

// A Regex is a host handle; its hover renders the value through std's
// `impl Debug for Regex`, which failed at run time before the VM's Debug
// dispatched on a host handle. A Regex literal without slots is checked at
// compile time, so its type and value are the handler's Ok payload.
func TestLiteralHover_ValidRegexHoversItsValue(t *testing.T) {
	src := "import std::regex.Regex\n\nfn main()\n    _ = Regex`\\d+`\nend\n"
	s, snap := openLiteralDoc(t, src)
	res, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(snap.URI)},
		Position:     protocol.Position{Line: 3, Character: 15},
	}})
	if err != nil || res == nil {
		t.Fatalf("no hover: %v", err)
	}
	got := res.Contents.(protocol.MarkupContent).Value
	want := "```nomi\nRegex`\\d+`: Regex<Unit>\n```\n\n```text\nRegex`\\d+`\n```\n\n*typed literal*: a `Regex<Unit>`, not a `String`, built by `Regex`'s `Literal` impl from its text"
	if got != want {
		t.Errorf("hover =\n%s\nwant\n%s", got, want)
	}
}

// A Regex literal's hover shows its whole type, the capture type the checker
// computes from its pattern included.
func TestLiteralHover_RegexHoverShowsItsCaptureType(t *testing.T) {
	for _, tc := range []struct{ lit, want string }{
		{"Regex`(\\d+)-(\\w+)?`", "Regex<(String, Maybe<String>)>"},
		{"Regex`(?P<year>\\d{4})`", "Regex<{year: String}>"},
		{"Regex\"(a)\"", "Regex<String>"},
	} {
		src := "import std::regex.Regex\n\nfn main()\n    _ = " + tc.lit + "\nend\n"
		s, snap := openLiteralDoc(t, src)
		res, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(snap.URI)},
			Position:     protocol.Position{Line: 3, Character: 15},
		}})
		if err != nil || res == nil {
			t.Fatalf("%s: no hover: %v", tc.lit, err)
		}
		got := res.Contents.(protocol.MarkupContent).Value
		if !strings.Contains(got, ": "+tc.want+"\n```") {
			t.Errorf("%s: hover =\n%s\nwant the type %s", tc.lit, got, tc.want)
		}
	}
}

func TestLiteralHover_InterpolatedLiteralIsNotEvaluated(t *testing.T) {
	src := "import std::calendar.Date\n\nfn main()\n    month = \"13\"\n    _ = Date\"2026-${month}-04\"\nend\n"
	_, snap := openLiteralDoc(t, src)
	if lits := staticLiterals(snap.Content, snap.Analysis); len(lits) != 0 {
		t.Fatalf("an interpolated literal is taken as static: %+v", lits)
	}
}

// userLiteral is a file declaring the tag type Probe whose handler's body is
// body, and a main using Probe"x".
func userLiteral(body string) string {
	return "import\n    std::io\n    std::literals.{Fragment, Literal}\nend\n\n" +
		"type Probe\n\n" +
		"fn spin(n: Int) -> Result<Probe, String>\n    if n < 0 then\n        Err(\"negative\")\n    else\n        spin(n + 1)\n    end\nend\n\n" +
		"impl Literal for Probe\n    fn from_fragments(fragments: List<Fragment<String>>) -> Result<Probe, String>\n" +
		body +
		"    end\nend\n\n" +
		"fn main()\n    _ = Probe\"x\"\n    io.print(\"\")\nend\n"
}

// A handler that never finishes makes its literal a compile error, so the
// buffer does not load and the hover has no value.
func TestLiteralHover_LoopingHandlerIsSkipped(t *testing.T) {
	s, snap := openLiteralDoc(t, userLiteral("        _ = fragments\n        spin(0)\n"))
	lits := staticLiterals(snap.Content, snap.Analysis)
	r := s.literalResults(snap, lits)[0]
	if !r.skipped || !strings.Contains(r.why, "did not finish within") {
		t.Fatalf("the literal is not skipped for its compile-time limit: %+v", r)
	}
}

// A handler with effects is a compile error too.
func TestLiteralHover_EffectfulHandlerIsSkipped(t *testing.T) {
	s, snap := openLiteralDoc(t, userLiteral("        _ = fragments\n        io.print(\"evaluated\")\n        Err(\"no\")\n"))
	lits := staticLiterals(snap.Content, snap.Analysis)
	r := s.literalResults(snap, lits)[0]
	if want := "its handler has effects (Probe.from_fragments calls io.print)"; !r.skipped || !strings.Contains(r.why, want) {
		t.Fatalf("skipped for %q, want it to name %q", r.why, want)
	}
}

// fiftyLiterals is a file with 50 static Date literals.
func fiftyLiterals() string {
	var b strings.Builder
	b.WriteString("import std::calendar.Date\n\nfn main()\n")
	for i := range 50 {
		month := 1 + i%12
		fmt.Fprintf(&b, "    _ = Date\"2026-%02d-%02d\"\n", month, 1+i%28)
	}
	b.WriteString("end\n")
	return b.String()
}

// BenchmarkLiteralHover_Cold is the background work for a buffer of 50
// static literals: loading the buffer with its probes and evaluating them.
func BenchmarkLiteralHover_Cold(b *testing.B) {
	_, snap := openLiteralDoc(b, fiftyLiterals())
	lits := staticLiterals(snap.Content, snap.Analysis)
	path := uriToPath(snap.URI)
	evaluateLiterals(context.Background(), path, snap.Content, lits) // the process's stdlib lowering
	b.ResetTimer()
	for range b.N {
		evaluateLiterals(context.Background(), path, snap.Content, lits)
	}
}
