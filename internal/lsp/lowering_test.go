package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmhost"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// loweringBlocked calls `skip_odd`, which uses `continue`, from inside a
// lambda: the front end accepts it and the IR builder declines `evens`, so
// `nomi check` reports the call. loweringFixed passes `skip_odd` itself,
// which lowers. If the builder learns the first shape, this fixture needs
// another declined construct, not a looser assertion.
const loweringBlocked = `import std/io

fn skip_odd(n: Int): Int {
  if n % 2 == 1 {
    continue
  }
  n
}

fn evens(xs: List<Int>): List<Int> {
  Iter.map(xs, |x| skip_odd(x)) |> Iter.to_list()
}

fn main() {
  io.print(evens([1, 2]))
}
`

var loweringFixed = strings.Replace(loweringBlocked, "|x| skip_odd(x)", "skip_odd", 1)

// openLoweringDoc opens main.nomi, holding src on disk and in the editor, on
// a new server whose publications arrive on the returned channel.
func openLoweringDoc(t *testing.T, src string) (*Server, string, string, chan []protocol.Diagnostic) {
	t.Helper()
	dir := writeProject(t, map[string]string{
		"main.nomi": src,
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
	})
	path := filepath.Join(dir, "main.nomi")
	uri := "file://" + path
	s := NewServer()
	published := make(chan []protocol.Diagnostic, 64)
	s.notify = func(method string, params any) {
		if p, ok := params.(*protocol.PublishDiagnosticsParams); ok && string(p.URI) == uri {
			published <- p.Diagnostics
		}
	}
	ctx := &glsp.Context{Notify: s.notify}
	if err := s.textDocumentDidOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: protocol.DocumentUri(uri), LanguageID: "nomi", Version: 1, Text: src},
	}); err != nil {
		t.Fatal(err)
	}
	return s, uri, path, published
}

// awaitPublish waits for a publication that satisfies want.
func awaitPublish(t *testing.T, published chan []protocol.Diagnostic, what string, want func([]protocol.Diagnostic) bool) []protocol.Diagnostic {
	t.Helper()
	deadline := time.After(60 * time.Second)
	for {
		select {
		case diags := <-published:
			if want(diags) {
				return diags
			}
		case <-deadline:
			t.Fatalf("no publication %s", what)
		}
	}
}

func notSupported(diags []protocol.Diagnostic) []protocol.Diagnostic {
	var out []protocol.Diagnostic
	for _, d := range diags {
		if strings.Contains(d.Message, "is not supported yet") {
			out = append(out, d)
		}
	}
	return out
}

// Opening a file reports what `nomi check` reports beyond the front end; an
// edit keeps it until the next save; a save that finds the code lowers clears
// it.
func TestLoweringDiagnostics_ReportedOnOpenAndClearedBySave(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	s, uri, path, published := openLoweringDoc(t, loweringBlocked)
	diags := awaitPublish(t, published, "with the lowering diagnostic", func(d []protocol.Diagnostic) bool {
		return len(notSupported(d)) > 0
	})
	got := notSupported(diags)
	want := "this call to `skip_odd` is not supported yet, so `fn evens` cannot run"
	if len(got) != 1 || !strings.HasPrefix(got[0].Message, want+"\nhelp: `skip_odd` uses break or continue") {
		t.Fatalf("lowering diagnostics %+v; want one %q with its hint", got, want)
	}
	r := got[0].Range
	if r.Start.Line != 10 || r.Start.Character != 19 {
		t.Fatalf("the diagnostic is at %d:%d; want 10:19 (the callee of line 11's call)", r.Start.Line, r.Start.Character)
	}
	if strings.Contains(got[0].Message, "not retained") || strings.Contains(got[0].Message, "direct call") {
		t.Fatalf("the diagnostic names compiler internals: %q", got[0].Message)
	}

	// The fix, typed but not saved: the front end's publish still carries
	// the last save's lowering diagnostic.
	ctx := &glsp.Context{Notify: s.notify}
	if err := s.textDocumentDidChange(ctx, &protocol.DidChangeTextDocumentParams{
		TextDocument:   protocol.VersionedTextDocumentIdentifier{TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Version: 2},
		ContentChanges: []any{protocol.TextDocumentContentChangeEventWhole{Text: loweringFixed}},
	}); err != nil {
		t.Fatal(err)
	}
	awaitPublish(t, published, "after the edit", func(d []protocol.Diagnostic) bool {
		snap := s.docs.Snapshot(uri)
		return snap != nil && snap.Content == loweringFixed && len(notSupported(d)) == 1
	})

	// Saved, the fix clears it.
	if err := os.WriteFile(path, []byte(loweringFixed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.textDocumentDidSave(ctx, &protocol.DidSaveTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
	}); err != nil {
		t.Fatal(err)
	}
	awaitPublish(t, published, "without the lowering diagnostic after the save", func(d []protocol.Diagnostic) bool {
		return len(notSupported(d)) == 0
	})
	if d := s.lowering.diagnostics(uri); len(d) != 0 {
		t.Fatalf("the saved fix kept lowering diagnostics: %+v", d)
	}
}

// BenchmarkLoweringDiagnostics is one save's lowering run on the corpus's
// largest file (523 lines, 40 tests), after the process's first lowering of
// the stdlib, beside the front end's analysis of the same text, which every
// keystroke pays.
func BenchmarkLoweringDiagnostics(b *testing.B) {
	path, err := filepath.Abs(filepath.Join("..", "..", "tests", "07-structs-and-enums", "struct_spread", "struct_spread_test.nomi"))
	if err != nil {
		b.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	src := string(data)
	start := time.Now()
	if _, err := vmhost.CheckLowering(path, src); err != nil {
		b.Fatalf("the file does not load, so nothing is lowered: %v", err)
	}
	b.Logf("first run, with the stdlib's lowering: %v", time.Since(start))
	b.Run("lowering", func(b *testing.B) {
		for range b.N {
			loweringDiagnostics(path, src)
		}
	})
	b.Run("analysis", func(b *testing.B) {
		s := NewServer()
		uri := "file://" + path
		for i := range b.N {
			s.docs.Open(uri, src+strings.Repeat("\n", i%2))
		}
	})
}

// A compiler panic during the lowering is one diagnostic, and the server
// keeps running.
func TestLoweringDiagnostics_InternalErrorIsOneDiagnostic(t *testing.T) {
	saved := checkLoweringFn
	checkLoweringFn = func(path, src string, opts ...vmhost.Option) (error, error) {
		return nil, &vmhost.InternalError{Panic: "boom"}
	}
	defer func() { checkLoweringFn = saved }()
	_, _, _, published := openLoweringDoc(t, loweringFixed)
	diags := awaitPublish(t, published, "with the internal error", func(d []protocol.Diagnostic) bool {
		for _, x := range d {
			if strings.HasPrefix(x.Message, "internal compiler error: boom") {
				return true
			}
		}
		return false
	})
	n := 0
	for _, d := range diags {
		if strings.Contains(d.Message, "internal compiler error") {
			n++
			if d.Range.Start.Line != 0 || d.Range.Start.Character != 0 {
				t.Errorf("the internal error is at %+v; want the top of the file", d.Range)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d internal-error diagnostics, want 1: %+v", n, diags)
	}
}
