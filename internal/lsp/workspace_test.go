package lsp

import (
	"fmt"
	"github.com/tliron/glsp"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// generatedWorkspace writes a workspace of projects projects of perProject
// module files each (plus a main.nomi), a testdata directory of 60 files the
// scan must leave out, and returns its root and the number of files the
// scan covers. Modules come in chains of ten: module i imports module i-1
// and calls it unless i is a multiple of ten.
func generatedWorkspace(t testing.TB, projects, perProject int) (string, int) {
	files := map[string]string{"nomi.toml": "[module]\nname = \"ws\"\n"}
	module := func(i int) string {
		var b strings.Builder
		chained := i%10 > 0
		if chained {
			fmt.Fprintf(&b, "import mod%d\n\n", i-1)
		}
		fmt.Fprintf(&b, "/// Item %d.\npub struct Item%d {\n    id: Int\n    name: String\n}\n\n", i, i)
		fmt.Fprintf(&b, "/// Makes item %d.\npub fn make_%d(n: Int): Item%d {\n", i, i, i)
		if chained {
			fmt.Fprintf(&b, "    prev = mod%d.make_%d(n + 1)\n    Item%d{id: prev.id + n, name: \"item %d\"}\n}\n\n", i-1, i-1, i, i)
		} else {
			fmt.Fprintf(&b, "    Item%d{id: n, name: \"item %d\"}\n}\n\n", i, i)
		}
		fmt.Fprintf(&b, "pub fn total_%d(items: List<Item%d>): Int {\n    items |> Iter.map(|it| it.id) |> Iter.count()\n}\n\n", i, i)
		fmt.Fprintf(&b, "fn label_%d(it: Item%d): String {\n    \"${it.name} #${it.id}\"\n}\n", i, i)
		return b.String()
	}
	covered := 0
	for p := range projects {
		dir := fmt.Sprintf("project%d", p)
		files[dir+"/nomi.toml"] = fmt.Sprintf("[module]\nname = \"p%d\"\n", p)
		for i := range perProject {
			files[fmt.Sprintf("%s/mod%d.nomi", dir, i)] = module(i)
			covered++
		}
		last := perProject - 1
		files[dir+"/main.nomi"] = fmt.Sprintf("import mod%d\n\nfn main() {\n    mod%d.make_%d(1)\n}\n", last, last, last)
		covered++
	}
	for i := range 60 {
		files[fmt.Sprintf("testdata/fixture%d/main.nomi", i)] = "fn main() {\n    1\n}\n"
	}
	return writeProject(t, files), covered
}

// recordingServer is a server whose notifications are recorded, as if
// initialized by a client.
func recordingServer() (*Server, func() map[string][]protocol.Diagnostic) {
	s := NewServer()
	var mu sync.Mutex
	pubs := map[string][]protocol.Diagnostic{}
	s.notify = func(method string, params any) {
		if method != protocol.ServerTextDocumentPublishDiagnostics {
			return
		}
		p := params.(*protocol.PublishDiagnosticsParams)
		mu.Lock()
		pubs[string(p.URI)] = p.Diagnostics
		mu.Unlock()
	}
	return s, func() map[string][]protocol.Diagnostic {
		mu.Lock()
		defer mu.Unlock()
		out := map[string][]protocol.Diagnostic{}
		for k, v := range pubs {
			out[k] = v
		}
		return out
	}
}

func waitClosedDiagnostics(t testing.TB, s *Server, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for s.closedDiagnosticsPending() {
		if time.Now().After(deadline) {
			t.Fatal("closed-file diagnostics did not settle")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func heapAlloc() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// TestWorkspaceScan_KeepsNoAnalyses scans a generated workspace of 300
// files and measures the heap the server retains once the scan and every
// closed file's diagnostics pass have finished. The server keeps an index
// entry per file and no analysis; before this design it kept every file's
// full analysis.
//
//	go test ./internal/lsp -run TestWorkspaceScan_KeepsNoAnalyses -v
func TestWorkspaceScan_KeepsNoAnalyses(t *testing.T) {
	root, covered := generatedWorkspace(t, 3, 99)
	s, published := recordingServer()
	s.docs.SetWorkspaceRoot(root)

	before := heapAlloc()
	start := time.Now()
	s.scanWorkspace()
	indexed := time.Since(start)
	waitClosedDiagnostics(t, s, 5*time.Minute)
	settled := time.Since(start)
	after := heapAlloc()
	retained := int64(after) - int64(before)
	t.Logf("%d files: indexed in %v, diagnostics settled in %v, retained heap %.1f MB",
		covered, indexed.Round(time.Millisecond), settled.Round(time.Millisecond), float64(retained)/(1<<20))

	pubs := published()
	if len(pubs) != covered {
		t.Errorf("published diagnostics for %d files, want %d (testdata left out)", len(pubs), covered)
	}
	for uri, diags := range pubs {
		if strings.Contains(uri, "/testdata/") {
			t.Errorf("published diagnostics for %s, which the scan leaves out", uri)
		}
		if len(diags) > 0 && strings.HasSuffix(uri, "/mod5.nomi") {
			t.Errorf("unexpected diagnostics on generated %s: %v", uri, diagMessages(diags))
		}
	}
	if len(s.docs.OpenURIs()) != 0 {
		t.Error("the scan opened documents")
	}
	if retained > 64<<20 {
		t.Errorf("the scan retained %.1f MB; an index of %d files should hold a few", float64(retained)/(1<<20), covered)
	}

	// Requests that analyze closed files fill a bounded cache.
	for i := range 40 {
		if s.docs.Analyzed("file://"+filepath.Join(root, "project0", fmt.Sprintf("mod%d.nomi", i))) == nil {
			t.Fatalf("no analysis of mod%d", i)
		}
	}
	cached := int64(heapAlloc()) - int64(before)
	t.Logf("with the closed-file cache full: retained heap %.1f MB", float64(cached)/(1<<20))
	runtime.KeepAlive(s)
}

// crossFileProject is a project where lib.nomi declares double, a.nomi and
// b.nomi call it, and c.nomi never mentions it.
func crossFileProject(t *testing.T) (string, *Server) {
	dir := writeProject(t, map[string]string{
		"lib.nomi":  "/// Doubles n.\npub fn double(n: Int): Int {\n    n * 2\n}\n\npub struct Pair {\n    a: Int\n}\n",
		"a.nomi":    "import lib\n\npub fn quad(n: Int): Int {\n    lib.double(lib.double(n))\n}\n",
		"b.nomi":    "import lib.{double}\n\npub fn six(n: Int): Int {\n    double(n) + double(n) + double(n)\n}\n",
		"c.nomi":    "pub fn other(): Int {\n    1\n}\n",
		"main.nomi": "import a\n\nfn main() {\n    a.quad(1)\n}\n",
	})
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	s.scanWorkspace()
	return dir, s
}

func TestReferences_IntoClosedFiles(t *testing.T) {
	dir, s := crossFileProject(t)
	libURI := "file://" + filepath.Join(dir, "lib.nomi")
	src := "/// Doubles n.\npub fn double(n: Int): Int {\n    n * 2\n}\n\npub struct Pair {\n    a: Int\n}\n"
	s.docs.Open(libURI, src)

	locs, err := s.textDocumentReferences(nil, &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(libURI)},
			Position:     protocol.Position{Line: 1, Character: 8},
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, l := range locs {
		count[filepath.Base(uriToPath(string(l.URI)))]++
	}
	// a.nomi: two calls; b.nomi: the import and three calls; lib.nomi:
	// the declaration.
	if count["a.nomi"] != 2 || count["b.nomi"] != 4 || count["lib.nomi"] != 1 || len(count) != 3 {
		t.Errorf("references by file = %v", count)
	}
	// c.nomi and main.nomi never spell double, so they were not analyzed.
	if s.docs.IsOpen("file://"+filepath.Join(dir, "c.nomi")) || s.docs.Get("file://"+filepath.Join(dir, "a.nomi")) != nil {
		t.Error("references opened a closed file")
	}
}

func TestRename_IntoClosedFiles(t *testing.T) {
	dir, s := crossFileProject(t)
	libURI := "file://" + filepath.Join(dir, "lib.nomi")
	s.docs.Open(libURI, "/// Doubles n.\npub fn double(n: Int): Int {\n    n * 2\n}\n\npub struct Pair {\n    a: Int\n}\n")

	edit, err := s.textDocumentRename(nil, &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(libURI)},
			Position:     protocol.Position{Line: 1, Character: 8},
		},
		NewName: "twice",
	})
	if err != nil || edit == nil {
		t.Fatalf("rename: %v %v", edit, err)
	}
	got := map[string]int{}
	for uri, edits := range edit.Changes {
		got[filepath.Base(uriToPath(string(uri)))] = len(edits)
	}
	if got["a.nomi"] != 2 || got["b.nomi"] != 4 || got["lib.nomi"] != 1 || len(got) != 3 {
		t.Errorf("rename edits by file = %v", got)
	}
}

func TestDefinition_SelectiveImportIntoClosedFile(t *testing.T) {
	dir, s := crossFileProject(t)
	bURI := "file://" + filepath.Join(dir, "b.nomi")
	s.docs.Open(bURI, "import lib.{double}\n\npub fn six(n: Int): Int {\n    double(n) + double(n) + double(n)\n}\n")

	res, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(bURI)},
			Position:     protocol.Position{Line: 0, Character: 13},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	loc, ok := res.(*protocol.Location)
	if !ok || loc == nil {
		t.Fatalf("definition = %#v", res)
	}
	want := protocol.Location{
		URI:   protocol.DocumentUri("file://" + filepath.Join(dir, "lib.nomi")),
		Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 7}, End: protocol.Position{Line: 1, Character: 13}},
	}
	if *loc != want {
		t.Errorf("definition = %+v, want %+v", *loc, want)
	}
	if s.docs.IsOpen(string(want.URI)) {
		t.Error("definition opened lib.nomi")
	}
}

func TestWorkspaceSymbol_ReadsTheIndex(t *testing.T) {
	dir, s := crossFileProject(t)
	syms, err := s.workspaceSymbol(nil, &protocol.WorkspaceSymbolParams{Query: "dbl"})
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 || syms[0].Name != "double" || syms[0].Kind != protocol.SymbolKindFunction ||
		syms[0].Location.URI != protocol.DocumentUri("file://"+filepath.Join(dir, "lib.nomi")) ||
		syms[0].Location.Range.Start != (protocol.Position{Line: 1, Character: 7}) {
		t.Errorf("workspace symbols for dbl = %+v", syms)
	}
	syms, _ = s.workspaceSymbol(nil, &protocol.WorkspaceSymbolParams{Query: "pair"})
	if len(syms) != 1 || syms[0].Kind != protocol.SymbolKindStruct {
		t.Errorf("workspace symbols for pair = %+v", syms)
	}
}

// A closed file gets diagnostics from the background pass, and a change
// to a file it imports queues it again.
func TestClosedDiagnostics_PublishedAndRepublished(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"lib.nomi":  "pub fn double(n: Int): Int {\n    n * 2\n}\n",
		"user.nomi": "import lib\n\npub fn f(): Int {\n    lib.double(1)\n}\n",
		"main.nomi": "fn main() {\n}\n",
	})
	s, published := recordingServer()
	s.docs.SetWorkspaceRoot(dir)
	s.scanWorkspace()
	waitClosedDiagnostics(t, s, time.Minute)
	userURI := "file://" + filepath.Join(dir, "user.nomi")
	if diags, ok := published()[userURI]; !ok || len(diags) != 0 {
		t.Fatalf("user.nomi: published %v %v, want an empty list", ok, diagMessages(diags))
	}

	libURI := "file://" + filepath.Join(dir, "lib.nomi")
	s.docs.Open(libURI, "pub fn halve(n: Int): Int {\n    n / 2\n}\n")
	s.docs.UpdateImportEdges(libURI)
	s.propagateAsync(libURI)
	deadline := time.Now().Add(time.Minute)
	for len(published()[userURI]) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if diags := published()[userURI]; len(diags) == 0 || !strings.Contains(strings.Join(diagMessages(diags), "\n"), "double") {
		t.Errorf("user.nomi after lib.nomi lost double: %v", diagMessages(diags))
	}
	if s.docs.Get(userURI) != nil {
		t.Error("the diagnostics pass kept user.nomi")
	}
}

// Closing a document publishes its disk text's diagnostics from the
// background pass, and reopening it publishes the editor's again: the
// reopened document counts versions from 1.
func TestDidClose_RepublishesDiskThenReopenPublishes(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"lib.nomi": "pub fn f(): Int {\n    1\n}\n",
	})
	s, published := recordingServer()
	s.docs.SetWorkspaceRoot(dir)
	ctx := &glsp.Context{Notify: s.notify}
	uri := "file://" + filepath.Join(dir, "lib.nomi")
	waitFor := func(what string, ok func([]protocol.Diagnostic) bool) {
		t.Helper()
		deadline := time.Now().Add(time.Minute)
		for !ok(published()[uri]) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; have %v", what, diagMessages(published()[uri]))
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	hasError := func(d []protocol.Diagnostic) bool { return len(d) > 0 }
	clean := func(d []protocol.Diagnostic) bool { return d != nil && len(d) == 0 }
	open := func(text string) {
		s.textDocumentDidOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: protocol.DocumentUri(uri), Text: text}})
	}

	open("pub fn f(): Int {\n    undefined_name\n}\n")
	waitFor("the open text's error", hasError)

	s.textDocumentDidClose(ctx, &protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}})
	waitFor("the disk text's clean diagnostics", clean)
	if s.docs.Get(uri) != nil {
		t.Fatal("closed document still held")
	}

	open("pub fn f(): Int {\n    undefined_name\n}\n")
	waitFor("the reopened text's error", hasError)
}
