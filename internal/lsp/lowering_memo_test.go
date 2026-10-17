package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/vmhost"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// save writes text to path and tells the server the document was saved.
func (c *rpcClient) save(uri, path, text string) {
	c.t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		c.t.Fatal(err)
	}
	c.notify("textDocument/didSave", protocol.DidSaveTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}})
}

// waitLoweringRuns waits until the server's runs have lowered lowered
// times and taken reused answers from the memo.
func waitLoweringRuns(t *testing.T, s *Server, lowered, reused int64) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		l, r := s.lowering.lowered.Load(), s.lowering.reused.Load()
		if l == lowered && r == reused {
			return
		}
		if l > lowered || r > reused || time.Now().After(deadline) {
			t.Fatalf("%d lowerings and %d memo answers, want %d and %d", l, r, lowered, reused)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Open, edit, wait for the edit's run, save: the open and the edit lower,
// and the save's run of the same text answers from the memo and publishes
// what the edit's run published. An edit after the save lowers again.
func TestLoweringMemo_SaveWithNoChangeDoesNotLowerAgain(t *testing.T) {
	plantDecline(t)
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	s, c, uri := openLoweringRPC(t, loweringFixed, 50*time.Millisecond)
	path := uriToPath(uri)
	c.waitDiagnostics(uri)
	waitLoweringRuns(t, s, 1, 0)

	c.change(uri, 2, loweringBlocked)
	const blocked = "this call to `planted_decline` is not supported yet, so `fn evens` cannot run"
	c.waitMessage(uri, blocked)
	waitLoweringRuns(t, s, 2, 0)
	afterEdit := s.lowering.diagnostics(uri)

	c.save(uri, path, loweringBlocked)
	waitLoweringRuns(t, s, 2, 1)
	if got := s.lowering.diagnostics(uri); !reflect.DeepEqual(got, afterEdit) {
		t.Fatalf("the save's run stored\n%s\nthe edit's run\n%s", renderDiagnostics(got), renderDiagnostics(afterEdit))
	}
	c.waitMessage(uri, blocked)

	c.change(uri, 3, loweringFixed)
	c.waitNoLowering(uri)
	waitLoweringRuns(t, s, 3, 1)
}

// The memo answers only while every file the lowering read holds the text
// it read: an imported file with new text on disk lowers the entry again
// (from its text, since the analysis read the old one), and so does a
// change to nomi.toml; the file back as it was answers from the memo.
func TestLoweringMemo_AnImportedFileChangedOnDiskLowersAgain(t *testing.T) {
	plantDecline(t)
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	dir := writeProject(t, map[string]string{"nomi.toml": multiManifest, "main.nomi": multiMain, "shapes.nomi": multiShapes})
	s := NewServer()
	path := filepath.Join(dir, "main.nomi")
	uri := pathToURI(path)
	s.docs.Open(uri, multiMain)
	run := func() []protocol.Diagnostic {
		t.Helper()
		a, latest := s.loweringAnalysis(uri, multiMain)
		if !latest {
			t.Fatal("the opened text is not the document's latest")
		}
		release := s.docs.View()
		defer release()
		return s.safeLoweringDiagnostics(context.Background(), uri, multiMain, a)
	}
	want := run()
	if n := len(notSupported(want)); n != 2 {
		t.Fatalf("%d lowering diagnostics, want 2:\n%s", n, renderDiagnostics(want))
	}
	steps := []struct {
		name            string
		change          func()
		lowered, reused int64
	}{
		{"nothing changed", func() {}, 1, 1},
		{"shapes.nomi changed on disk", func() {
			write(t, filepath.Join(dir, "shapes.nomi"), strings.Replace(multiShapes, "r * 3", "r * 4", 1))
		}, 2, 1},
		// The texts the kept lowering read, again.
		{"shapes.nomi back as it was", func() { write(t, filepath.Join(dir, "shapes.nomi"), multiShapes) }, 2, 2},
		{"nomi.toml changed", func() { write(t, filepath.Join(dir, "nomi.toml"), multiManifest+"\n") }, 3, 2},
	}
	for _, step := range steps {
		step.change()
		got := run()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: published\n%s\nwant\n%s", step.name, renderDiagnostics(got), renderDiagnostics(want))
		}
		if l, r := s.lowering.lowered.Load(), s.lowering.reused.Load(); l != step.lowered || r != step.reused {
			t.Errorf("%s: %d lowerings and %d memo answers, want %d and %d", step.name, l, r, step.lowered, step.reused)
		}
	}
}

// A lowering whose context is done answers the context's error and finds
// nothing, from the analysis and from the text alike, and the memo keeps
// nothing of it.
func TestLowering_AStoppedContextStopsTheLowering(t *testing.T) {
	plantDecline(t)
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	dir := writeProject(t, map[string]string{"nomi.toml": multiManifest, "main.nomi": multiMain, "shapes.nomi": multiShapes})
	s := NewServer()
	path := filepath.Join(dir, "main.nomi")
	uri := pathToURI(path)
	s.docs.Open(uri, multiMain)
	a, _ := s.loweringAnalysis(uri, multiMain)
	if a == nil {
		t.Fatal("main.nomi's program is not offered")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release := s.docs.View()
	problems, ok, err := analyzedlowering.Check(ctx, path, multiMain, a)
	release()
	if !ok || problems != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("from the analysis: %v, %v, %v; want the context's error", problems, ok, err)
	}
	if problems, err := vmhost.CheckLowering(ctx, path, multiMain); problems != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("from the text: %v, %v; want the context's error", problems, err)
	}
	release = s.docs.View()
	problems, err = lowerFromAnalysis(ctx, path, multiMain, a, &s.lowering.memo)
	release()
	if problems != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("the server's lowering: %v, %v; want the context's error", problems, err)
	}
	if _, _, ok := s.lowering.memo.Lookup(path, multiMain, a); ok {
		t.Fatal("the memo kept a stopped lowering")
	}
}
