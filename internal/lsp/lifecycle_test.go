package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/sourcegraph/jsonrpc2"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// rpcClient is an editor talking to a Server over the real JSON-RPC path.
type rpcClient struct {
	t     testing.TB
	conn  *jsonrpc2.Conn
	diags chan published
}

type published struct {
	uri string
	at  time.Time
	n   int
}

// startRPC serves s over an in-memory pipe and initializes it with root as
// the workspace.
func startRPC(t testing.TB, s *Server, root string) *rpcClient {
	t.Helper()
	server, client := net.Pipe()
	go s.ServeStream(server)
	c := &rpcClient{t: t, diags: make(chan published, 256)}
	c.conn = jsonrpc2.NewConn(context.Background(), jsonrpc2.NewBufferedStream(client, jsonrpc2.VSCodeObjectCodec{}),
		jsonrpc2.HandlerWithError(func(_ context.Context, _ *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
			if req.Method == string(protocol.ServerTextDocumentPublishDiagnostics) && req.Params != nil {
				var p protocol.PublishDiagnosticsParams
				if json.Unmarshal(*req.Params, &p) == nil {
					c.diags <- published{string(p.URI), time.Now(), len(p.Diagnostics)}
				}
			}
			return nil, nil
		}))
	t.Cleanup(func() { c.conn.Close() })
	var res json.RawMessage
	if err := c.conn.Call(context.Background(), "initialize", map[string]any{
		"rootUri": "file://" + root,
		"capabilities": map[string]any{"textDocument": map[string]any{
			"completion": map[string]any{"completionItem": map[string]any{"snippetSupport": true}},
		}},
	}, &res); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	c.notify("initialized", struct{}{})
	return c
}

func (c *rpcClient) notify(method string, params any) {
	c.t.Helper()
	if err := c.conn.Notify(context.Background(), method, params); err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
}

func (c *rpcClient) open(uri, text string) {
	c.notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: protocol.DocumentUri(uri), LanguageID: "nomi", Version: 1, Text: text,
	}})
}

func (c *rpcClient) change(uri string, version int, text string) {
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": version},
		"contentChanges": []map[string]any{{"text": text}},
	})
}

// completion sends a completion request without waiting for its answer.
func (c *rpcClient) completion(uri string, pos protocol.Position, trigger string) (jsonrpc2.Waiter, time.Time) {
	c.t.Helper()
	ctx := map[string]any{"triggerKind": 1}
	if trigger != "" {
		ctx = map[string]any{"triggerKind": 2, "triggerCharacter": trigger}
	}
	sent := time.Now()
	w, err := c.conn.DispatchCall(context.Background(), "textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": uri}, "position": pos, "context": ctx,
	})
	if err != nil {
		c.t.Fatalf("completion: %v", err)
	}
	return w, sent
}

// labels waits for a completion answer and returns its labels and when it
// arrived.
func (c *rpcClient) labels(w jsonrpc2.Waiter) ([]string, time.Time) {
	c.t.Helper()
	var list protocol.CompletionList
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.Wait(ctx, &list); err != nil {
		c.t.Fatalf("completion: %v", err)
	}
	return itemLabels(list.Items), time.Now()
}

func (c *rpcClient) waitDiagnostics(uri string) published {
	c.t.Helper()
	return c.waitPublished(uri, 0)
}

// waitErrors waits for a publication for uri with at least one
// diagnostic. The background workspace scan may publish the clean file
// again; an edit that adds an error is told apart from it this way.
func (c *rpcClient) waitErrors(uri string) published {
	c.t.Helper()
	return c.waitPublished(uri, 1)
}

func (c *rpcClient) waitPublished(uri string, atLeast int) published {
	c.t.Helper()
	timeout := time.After(30 * time.Second)
	for {
		select {
		case p := <-c.diags:
			if p.uri == uri && p.n >= atLeast {
				return p
			}
		case <-timeout:
			c.t.Fatalf("no diagnostics published for %s", uri)
		}
	}
}

// datesFixture copies tests/05-calendar-and-time/dates_test.nomi, the file
// the latency was measured on, into a fresh workspace.
func datesFixture(t testing.TB) (dir, uri, text string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "tests", "05-calendar-and-time", "dates_test.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	path := filepath.Join(dir, "dates_test.nomi")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	text = string(src)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return dir, "file://" + path, text
}

// typedIn is dates_test.nomi with a test whose body is typed appended,
// and the cursor position after typed.
func typedIn(text, typed string) (string, protocol.Position) {
	line := strings.Count(text, "\n") + 1
	return text + "test \"typing\" {\n    " + typed + "\n}\n", protocol.Position{Line: uint32(line), Character: uint32(4 + len(typed))}
}

func median(ds []time.Duration) time.Duration {
	s := slices.Clone(ds)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

// TestRPC_CompletionAfterEditDoesNotWaitForAnalysis is the measured case:
// an edit to dates_test.nomi followed at once by a completion request.
// Before analysis moved off the message loop, the completion was answered
// only after the edit's analysis (35-42 ms on this file); now it is
// answered from the text and the previous analysis.
//
// With a 500 ms debounce the edit's analysis cannot have run when the
// answer comes, so an answer inside that window proves the request did not
// wait for it. The diagnostics for the edit arrive after the answer.
func TestRPC_CompletionAfterEditDoesNotWaitForAnalysis(t *testing.T) {
	dir, uri, text := datesFixture(t)
	s := NewServer()
	s.sched.delay = 500 * time.Millisecond
	c := startRPC(t, s, dir)
	c.open(uri, text)
	c.waitDiagnostics(uri)

	edited, pos := typedIn(text, "Date.")
	c.change(uri, 2, edited)
	w, sent := c.completion(uri, pos, ".")
	labels, answered := c.labels(w)
	p := c.waitErrors(uri)
	t.Logf("completion answered in %v; the edit's diagnostics %v after the edit", answered.Sub(sent), p.at.Sub(sent))
	if !answered.Before(p.at) {
		t.Errorf("completion was answered after the edit's diagnostics: it waited for analysis")
	}
	if answered.Sub(sent) >= s.sched.delay {
		t.Errorf("completion took %v, at least the analysis delay", answered.Sub(sent))
	}
	labelsHave(t, labels, "day")
}

// TestRPC_CompletionLatencyAfterEdit reports the completion latency after
// an edit with the real debounce, the number to compare against the 35-42
// ms measured before. The bound is generous for slow and race-enabled CI.
func TestRPC_CompletionLatencyAfterEdit(t *testing.T) {
	dir, uri, text := datesFixture(t)
	s := NewServer()
	c := startRPC(t, s, dir)
	c.open(uri, text)
	c.waitDiagnostics(uri)

	var lat []time.Duration
	for i := range 10 {
		typed := "Date."
		if i%2 == 1 {
			typed = "Date.d"
		}
		edited, pos := typedIn(text, typed)
		c.change(uri, i+2, edited)
		w, sent := c.completion(uri, pos, "")
		_, answered := c.labels(w)
		lat = append(lat, answered.Sub(sent))
		c.waitErrors(uri)
	}
	t.Logf("completion after edit: median %v, all %v", median(lat), lat)
	if m := median(lat); m > 250*time.Millisecond {
		t.Errorf("median completion latency after an edit %v, want under 250ms", m)
	}
}

// TestRPC_TypingBurstAnswersFromTheLatestText types `Date.` one key at a
// time with no pause, each edit followed by a completion request, as
// blink.cmp does. The request for `Date.` must be answered from the
// `Date.` text: Date's members, not the type names `Dat` matched.
func TestRPC_TypingBurstAnswersFromTheLatestText(t *testing.T) {
	dir, uri, text := datesFixture(t)
	s := NewServer()
	c := startRPC(t, s, dir)
	c.open(uri, text)
	c.waitDiagnostics(uri)

	type sentReq struct {
		typed string
		w     jsonrpc2.Waiter
		at    time.Time
	}
	var reqs []sentReq
	for i, typed := range []string{"D", "Da", "Dat", "Date", "Date."} {
		edited, pos := typedIn(text, typed)
		c.change(uri, i+2, edited)
		trigger := ""
		if strings.HasSuffix(typed, ".") {
			trigger = "."
		}
		w, at := c.completion(uri, pos, trigger)
		reqs = append(reqs, sentReq{typed, w, at})
	}
	var report []string
	for _, r := range reqs {
		labels, answered := c.labels(r.w)
		d := answered.Sub(r.at)
		report = append(report, r.typed+"="+d.String())
		if r.typed == "Date." {
			labelsHave(t, labels, "day", "add")
			if slices.Contains(labels, "DateTime") {
				t.Errorf("`Date.` answered with type names, from an older text: %v", labels)
			}
			if d > 250*time.Millisecond {
				t.Errorf("`Date.` answered in %v, want under 250ms", d)
			}
		}
	}
	t.Logf("burst latencies: %s", strings.Join(report, " "))
}

// TestRPC_CancelRequest cancels a hover that waits for an analysis that
// never comes (the text is stored without one being scheduled). The answer
// is RequestCancelled, well before the wait's bound.
func TestRPC_CancelRequest(t *testing.T) {
	dir, uri, text := datesFixture(t)
	s := NewServer()
	c := startRPC(t, s, dir)
	c.open(uri, text)
	c.waitDiagnostics(uri)
	s.docs.SetText(uri, text+"\n")

	id := jsonrpc2.ID{Str: "hover-to-cancel", IsString: true}
	start := time.Now()
	w, err := c.conn.DispatchCall(context.Background(), "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": 8, "character": 12},
	}, jsonrpc2.PickID(id))
	if err != nil {
		t.Fatal(err)
	}
	c.notify("$/cancelRequest", map[string]any{"id": "hover-to-cancel"})
	err = w.Wait(context.Background(), nil)
	var rpcErr *jsonrpc2.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != codeRequestCancelled {
		t.Fatalf("cancelled hover answered %v, want error code %d", err, codeRequestCancelled)
	}
	if d := time.Since(start); d >= s.sched.maxWait() {
		t.Errorf("cancelled hover answered after %v, the full wait", d)
	}
}

// TestRPC_EditingRequestFailsWhenAnalysisIsNotReady: a rename that would
// compute edits against an older text fails with ContentModified instead.
func TestRPC_EditingRequestFailsWhenAnalysisIsNotReady(t *testing.T) {
	dir, uri, text := datesFixture(t)
	s := NewServer()
	s.sched.wait = 100 * time.Millisecond
	c := startRPC(t, s, dir)
	c.open(uri, text)
	c.waitDiagnostics(uri)
	s.docs.SetText(uri, text+"\n")

	err := c.conn.Call(context.Background(), "textDocument/rename", map[string]any{
		"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": 8, "character": 14}, "newName": "e",
	}, nil)
	var rpcErr *jsonrpc2.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != codeContentModified {
		t.Fatalf("rename on an unanalyzed edit answered %v, want error code %d", err, codeContentModified)
	}
}

// TestRPC_HoverWaitsForTheEditsAnalysis: a hover after an edit is answered
// from the edit's analysis, without waiting out the debounce.
func TestRPC_HoverWaitsForTheEditsAnalysis(t *testing.T) {
	dir, uri, text := datesFixture(t)
	s := NewServer()
	s.sched.delay = 5 * time.Second
	c := startRPC(t, s, dir)
	c.open(uri, text)
	c.waitDiagnostics(uri)

	edited := strings.Replace(text, "assert Ok(d) = Date\"2026-05-04\"", "assert Ok(renamed) = Date\"2026-05-04\"", 1)
	c.change(uri, 2, edited)
	start := time.Now()
	var hover protocol.Hover
	if err := c.conn.Call(context.Background(), "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": 7, "character": 15},
	}, &hover); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d >= s.sched.delay {
		t.Fatalf("hover waited out the debounce (%v)", d)
	}
	b, _ := json.Marshal(hover.Contents)
	if !strings.Contains(string(b), "renamed") {
		t.Errorf("hover = %s, want the edited binding `renamed`", b)
	}
}

// TestRPC_LiteralDiagnosticsFollowTheLatestEdit: an invalid static typed
// literal is reported by the background evaluation that follows a
// published analysis; after an edit fixes it, no evaluation of the older
// text publishes its diagnostic again.
func TestRPC_LiteralDiagnosticsFollowTheLatestEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	invalid := "import std/calendar.Date\n\nfn main() {\n    _ = Date\"2026-02-30\"\n}\n"
	valid := strings.Replace(invalid, "2026-02-30", "2026-02-28", 1)
	if err := os.WriteFile(path, []byte(invalid), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := "file://" + path
	s := NewServer()
	c := startRPC(t, s, dir)
	c.open(uri, invalid)
	c.waitErrors(uri)

	// Edit, and edit back and forth quickly, ending valid.
	c.change(uri, 2, valid)
	c.change(uri, 3, invalid)
	c.change(uri, 4, valid)
	deadline := time.After(10 * time.Second)
	for clean := false; !clean; {
		select {
		case p := <-c.diags:
			clean = p.uri == uri && p.n == 0
		case <-deadline:
			t.Fatal("the fixed literal was never published clean")
		}
	}
	quiet := time.After(time.Second)
	for {
		select {
		case p := <-c.diags:
			if p.uri == uri && p.n > 0 {
				t.Fatalf("a stale evaluation published %d diagnostics after the fix", p.n)
			}
		case <-quiet:
			return
		}
	}
}

func labelsHave(t *testing.T, labels []string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !slices.Contains(labels, w) {
			t.Errorf("completion does not offer %q; got %v", w, labels)
		}
	}
}

// TestCompletion_StaleAnalysisMapsPositions answers completion from an
// analysis of an older text after lines were inserted above the cursor:
// scopes come from the old analysis at the mapped position, and an import
// edit is computed against the latest text.
func TestCompletion_StaleAnalysisMapsPositions(t *testing.T) {
	s := NewServer()
	s.snippetSupport = true
	uri := "file:///stale.nomi"
	old := "fn f(count: Int): Int {\n    total = count + 1\n    total\n}\n"
	s.docs.Open(uri, old)

	src := "// a comment\n// and another\n\nfn f(count: Int): Int {\n    total = count + 1\n    tot" + cursorMark + "\n    total\n}\n"
	text, pos := splitCursor(t, src)
	s.docs.SetText(uri, text)
	if snap := s.docs.Snapshot(uri); snap.Current() {
		t.Fatal("the edit must still be unanalyzed for this test")
	}
	items := completeAt(t, s, uri, pos)
	labelsInclude(t, items, "total")

	src = "\n\nfn f(count: Int): Int {\n    total = count + 1\n    jso" + cursorMark + "\n    total\n}\n"
	text, pos = splitCursor(t, src)
	s.docs.SetText(uri, text)
	items = completeAt(t, s, uri, pos)
	it := mustItem(t, items, "json")
	if len(it.AdditionalTextEdits) != 1 {
		t.Fatalf("json offered with edits %+v, want one import edit", it.AdditionalTextEdits)
	}
	if e := it.AdditionalTextEdits[0]; e.Range.Start.Line != 0 || e.NewText != "import std/json\n\n" {
		t.Errorf("import edit = %+v, want `import std/json` at the top of the latest text", e)
	}
}

func completeAt(t *testing.T, s *Server, uri string, pos protocol.Position) []protocol.CompletionItem {
	t.Helper()
	res, err := s.textDocumentCompletion(nil, &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     pos,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return completionItemsOf(t, res)
}

func TestCompletionPositions(t *testing.T) {
	old := "a\nbb Dat\ncc\n"
	cur := "x\ny\na\nbb Date.\ncc\n"
	m := newCompletionPositions(old, cur)
	// The change starts at offset 0, so nothing before it maps by identity;
	// `cc` after it shifts up two lines.
	if p, ok := m.toAnalyzed(analysis.Pos{Line: 5, Col: 1}); !ok || p != (analysis.Pos{Line: 3, Col: 1}) {
		t.Errorf("cc maps to %v %v, want 3:1", p, ok)
	}
	m = newCompletionPositions(old, "a\nbb Date.\ncc\n")
	if p, ok := m.toAnalyzed(analysis.Pos{Line: 1, Col: 1}); !ok || p != (analysis.Pos{Line: 1, Col: 1}) {
		t.Errorf("a maps to %v %v, want 1:1", p, ok)
	}
	// `Date` starts where `Dat` did, on the change's line: no counterpart.
	if _, ok := m.toAnalyzed(analysis.Pos{Line: 2, Col: 4}); ok {
		t.Error("a node on the changed line before the change maps into the older text")
	}
	if p := m.scopePos(analysis.Pos{Line: 2, Col: 9}); p != (analysis.Pos{Line: 2, Col: 7}) {
		t.Errorf("scope position %v, want where the change starts, 2:7", p)
	}
	if p, ok := m.toAnalyzed(analysis.Pos{Line: 3, Col: 2}); !ok || p != (analysis.Pos{Line: 3, Col: 2}) {
		t.Errorf("cc maps to %v %v, want 3:2", p, ok)
	}
}
