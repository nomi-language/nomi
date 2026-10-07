package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/rotation"
	"github.com/sourcegraph/jsonrpc2"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The rule these tests hold the language server to: it never panics on
// text someone is typing. The server recovers a panic at every boundary
// (recover.go) and keeps running, but each recovered panic is a bug.
//
// A typing session drives a Server over the real JSON-RPC path, as an
// editor does, through the edits someone makes while writing a seed file:
//
//   - grow: the document starts empty and grows to the whole file, a few
//     characters at a time, the cursor at the end.
//   - type: a stretch of the file is removed and typed back one character
//     at a time, with the editor's auto-closed brackets and quotes.
//   - delete: a stretch in the middle is deleted backwards.
//   - paste: a block of the file is pasted at the start of a line, in the
//     middle of a line, and at the end.
//   - save: the whole file is saved, which runs the lowering check.
//
// After every edit the session asks for completion and signature help at
// the cursor (answered from whatever analysis is installed, as while
// typing). At intervals it sends the battery: hover, definition, type
// definition, implementation, references, highlights, prepare-rename,
// rename, call hierarchy, code actions, completion and its resolve,
// signature help, document symbols, semantic tokens, formatting, inlay
// hints, code lenses and document links. A panic anywhere in the server is
// a finding (panicHook), and so is an internal-error diagnostic, a request
// that does not answer within typingHang, and a panic of the lexer or the
// resilient parser run directly on every text the session sends. A request
// that takes over typingSlow is recorded as slow.
//
// TestLSPSurvivesTyping runs a fixed sample of seeds under plain `go
// test`, and TestLSPSurvivesTypingRotating a few more that change every
// day. NOMI_LSP_TYPING=full sweeps every tracked .nomi file and every
// runnable tour block (NOMI_LSP_TYPING_SHARD=k/n runs every n-th seed from
// the k-th, so the sweep can be split across processes).
// FuzzLSPSurvivesEdits mutates a seed and runs the battery on the result.
// A panic that matches a known gap (knownTypingGaps) is not a failure.

const (
	typingHang = 20 * time.Second
	typingSlow = time.Second
	// smallFileLines bounds the files a slow request is reported for.
	smallFileLines = 400
)

// typingFinding is one panic, hang or internal-error diagnostic.
type typingFinding struct {
	kind  string // "panic", "hang", "diagnostic", "parser"
	where string // the boundary, or the request's method
	value string // the panic's value or the error text
	stack string
	// text is the document's latest text when the finding was seen, and
	// request the last request sent with its position.
	text    string
	request string
	// note is the seed's note, said before the finding when it fails.
	note string
}

func (f typingFinding) String() string {
	return fmt.Sprintf("%s in %s: %s\nlast request: %s\ntext:\n%s\n%s", f.kind, f.where, f.value, f.request, numberedText(f.text), f.stack)
}

// key groups findings by cause: the panic's value and the innermost
// frame of Nomi's code that raised it.
func (f typingFinding) key() string {
	return f.kind + ": " + firstLine(f.value) + " @ " + raisingFrame(f.stack)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

var nomiFrame = regexp.MustCompile(`(?m)^(github\.com/nomi-language/nomi/[^\n]+)\n\t([^\n]+?)( \+0x[0-9a-f]+)?$`)

// raisingFrame is the first frame of the stack in Nomi's own code after
// the runtime's panic machinery and the recovery helper.
func raisingFrame(stack string) string {
	if i := strings.Index(stack, "panic("); i >= 0 {
		stack = stack[i:]
	}
	for _, m := range nomiFrame.FindAllStringSubmatch(stack, -1) {
		if strings.Contains(m[1], "recoverPanic") || strings.Contains(m[1], "typing_test") {
			continue
		}
		file := m[2]
		if i := strings.Index(file, "/internal/"); i >= 0 {
			file = file[i+1:]
		}
		return file
	}
	return "(no frame)"
}

func numberedText(src string) string {
	var b strings.Builder
	for i, line := range strings.Split(src, "\n") {
		fmt.Fprintf(&b, "%4d | %s\n", i+1, line)
	}
	return b.String()
}

// slowRequest is a request that took longer than typingSlow.
type slowRequest struct {
	method string
	took   time.Duration
	lines  int
	seed   string
}

// recordedPanic is one panic panicHook received.
type recordedPanic struct {
	where, value, stack string
}

// panicRecorder collects the panics the server recovers while a test runs.
// Sessions run one at a time, so every recorded panic belongs to the
// session running.
var panicRecorder struct {
	mu     sync.Mutex
	active int
	got    []recordedPanic
}

// recordPanics installs panicHook until the test ends.
func recordPanics(t testing.TB) {
	t.Helper()
	panicRecorder.mu.Lock()
	panicRecorder.active++
	panicRecorder.mu.Unlock()
	hook := func(where string, value any, stack []byte) {
		panicRecorder.mu.Lock()
		panicRecorder.got = append(panicRecorder.got, recordedPanic{where, fmt.Sprint(value), string(stack)})
		panicRecorder.mu.Unlock()
	}
	panicHook.Store(&hook)
	// The log would repeat every recorded panic's stack on stderr.
	panicLogMu.Lock()
	saved := panicLog
	panicLog = discard{}
	panicLogMu.Unlock()
	t.Cleanup(func() {
		panicRecorder.mu.Lock()
		panicRecorder.active--
		last := panicRecorder.active == 0
		panicRecorder.mu.Unlock()
		if last {
			panicHook.Store(nil)
			panicLogMu.Lock()
			panicLog = saved
			panicLogMu.Unlock()
		}
	})
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func takePanics() []recordedPanic {
	panicRecorder.mu.Lock()
	defer panicRecorder.mu.Unlock()
	got := panicRecorder.got
	panicRecorder.got = nil
	return got
}

// typingCounts are what a run did.
type typingCounts struct {
	docs, edits, requests int
}

// typingSession is one document being edited in a fresh server.
type typingSession struct {
	t    testing.TB
	seed string
	s    *Server
	c    *rpcClient
	dir  string
	uri  string
	ver  int
	text string
	// last is the last request sent, with its position.
	last string
	rng  *rand.Rand
	// broken is set once the document's analysis panicked: every request
	// that waits for the analysis would then wait analysisWait, so the
	// session sends no more.
	broken bool

	counts   *typingCounts
	findings []typingFinding
	slow     []slowRequest

	diagMu    sync.Mutex
	diagFound []typingFinding
	stop      chan struct{}
	stopped   chan struct{}
}

// newTypingSession writes files to a fresh workspace and opens a server
// on it. open names the file the session edits; it starts empty.
func newTypingSession(t testing.TB, seed string, files map[string]string, open string, counts *typingCounts) *typingSession {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		if rel == open {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The edited file exists on disk as the editor last saved it: empty.
	path := filepath.Join(dir, filepath.FromSlash(open))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	ss := &typingSession{
		t: t, seed: seed, s: s, dir: dir, uri: pathToURI(path), counts: counts,
		rng:  rand.New(rand.NewSource(int64(hashString(seed)))),
		stop: make(chan struct{}), stopped: make(chan struct{}),
	}
	ss.c = startRPC(t, s, dir)
	go ss.drainDiagnostics()
	ss.c.open(ss.uri, "")
	ss.ver = 1
	counts.docs++
	return ss
}

// reopen closes the session's document and opens a new, empty one named
// open in the same workspace and server, for the test t. Findings start
// over.
func (ss *typingSession) reopen(t testing.TB, seed, open string) {
	ss.t, ss.c.t, ss.seed = t, t, seed
	ss.c.notify("textDocument/didClose", map[string]any{"textDocument": ss.doc()})
	ss.collect()
	ss.uri = pathToURI(filepath.Join(ss.dir, filepath.FromSlash(open)))
	ss.rng = rand.New(rand.NewSource(int64(hashString(seed))))
	ss.findings, ss.slow, ss.broken, ss.text, ss.last = nil, nil, false, "", ""
	ss.c.open(ss.uri, "")
	ss.ver = 1
	ss.counts.docs++
}

func hashString(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// drainDiagnostics reads every publication, so the client never blocks
// the server, and records internal-error diagnostics.
func (ss *typingSession) drainDiagnostics() {
	defer close(ss.stopped)
	for {
		select {
		case p := <-ss.c.diags:
			for _, m := range p.msgs {
				if strings.Contains(m, "internal compiler error") || strings.Contains(m, "internal language server error") {
					ss.diagMu.Lock()
					ss.diagFound = append(ss.diagFound, typingFinding{kind: "diagnostic", where: "publishDiagnostics for " + p.uri, value: m})
					ss.diagMu.Unlock()
				}
			}
		case <-ss.stop:
			return
		}
	}
}

// close shuts the server down and collects what is left.
func (ss *typingSession) close() {
	ctx, cancel := context.WithTimeout(context.Background(), typingHang)
	defer cancel()
	_ = ss.c.conn.Call(ctx, "shutdown", nil, nil)
	ss.collect()
	close(ss.stop)
	<-ss.stopped
	ss.c.conn.Close()
}

// collect turns the panics recorded since the last call, and the
// internal-error diagnostics, into findings against the current text.
func (ss *typingSession) collect() {
	for _, p := range takePanics() {
		if strings.HasPrefix(p.where, "analyzing ") {
			ss.broken = true
		}
		ss.findings = append(ss.findings, typingFinding{
			kind: "panic", where: p.where, value: p.value, stack: p.stack, text: ss.text, request: ss.last,
		})
	}
	ss.diagMu.Lock()
	found := ss.diagFound
	ss.diagFound = nil
	ss.diagMu.Unlock()
	for _, f := range found {
		// The server's own panic already produced a finding with its
		// stack; only the compiler's, recovered by vmhost, is new.
		if strings.Contains(f.value, "internal language server error") || f.where != "publishDiagnostics for "+ss.uri {
			continue
		}
		f.text, f.request = ss.text, ss.last
		ss.findings = append(ss.findings, f)
	}
}

// setText replaces the document's text and checks the lexer and parser on
// it directly.
func (ss *typingSession) setText(text string) {
	ss.text = text
	ss.ver++
	ss.c.change(ss.uri, ss.ver, text)
	ss.counts.edits++
	ss.parseDirectly(text)
}

func (ss *typingSession) parseDirectly(text string) {
	defer func() {
		if r := recover(); r != nil {
			ss.findings = append(ss.findings, typingFinding{
				kind: "parser", where: "lexer.Lex/parser.ParseResilient", value: fmt.Sprint(r), stack: string(debug.Stack()), text: text,
			})
		}
	}()
	parser.ParseResilient(lexer.Lex(text))
}

// call sends one request and waits for its answer. An InternalError
// answer is the panic panicHook recorded; a missing answer is a hang.
func (ss *typingSession) call(method string, params any, result any) bool {
	if ss.broken {
		return false
	}
	ss.counts.requests++
	ctx, cancel := context.WithTimeout(context.Background(), typingHang)
	defer cancel()
	start := time.Now()
	err := ss.c.conn.Call(ctx, method, params, result)
	took := time.Since(start)
	if took > typingSlow {
		ss.slow = append(ss.slow, slowRequest{method: ss.last, took: took, lines: strings.Count(ss.text, "\n") + 1, seed: ss.seed})
	}
	if errors.Is(err, context.DeadlineExceeded) {
		ss.findings = append(ss.findings, typingFinding{kind: "hang", where: method, value: "no answer in " + typingHang.String(), text: ss.text, request: ss.last})
		return false
	}
	var rpcErr *jsonrpc2.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == jsonrpc2.CodeInternalError && !strings.Contains(rpcErr.Message, "internal language server error") {
		ss.findings = append(ss.findings, typingFinding{kind: "panic", where: method, value: rpcErr.Message, text: ss.text, request: ss.last})
	}
	ss.collect()
	return err == nil
}

// position is the LSP position of byte offset off in text: a line and a
// UTF-16 column.
func position(text string, off int) protocol.Position {
	if off > len(text) {
		off = len(text)
	}
	line := strings.Count(text[:off], "\n")
	start := strings.LastIndexByte(text[:off], '\n') + 1
	col := 0
	for _, r := range text[start:off] {
		if r >= 0x10000 {
			col += 2
		} else {
			col++
		}
	}
	return protocol.Position{Line: uint32(line), Character: uint32(col)}
}

func (ss *typingSession) doc() map[string]any { return map[string]any{"uri": ss.uri} }

func (ss *typingSession) at(method string, pos protocol.Position) map[string]any {
	ss.last = fmt.Sprintf("%s at %d:%d", method, pos.Line, pos.Character)
	return map[string]any{"textDocument": ss.doc(), "position": pos}
}

// completeAt asks for completion at pos, triggered by the character
// before it when that is a trigger character, and returns the first item.
func (ss *typingSession) completeAt(pos protocol.Position, before string) *protocol.CompletionItem {
	params := ss.at("textDocument/completion", pos)
	params["context"] = map[string]any{"triggerKind": 1}
	if before != "" && strings.Contains(".>{}\"`:,", before) {
		params["context"] = map[string]any{"triggerKind": 2, "triggerCharacter": before}
	}
	var list json.RawMessage
	if !ss.call("textDocument/completion", params, &list) {
		return nil
	}
	var cl protocol.CompletionList
	if json.Unmarshal(list, &cl) == nil && len(cl.Items) > 0 {
		return &cl.Items[0]
	}
	var items []protocol.CompletionItem
	if json.Unmarshal(list, &items) == nil && len(items) > 0 {
		return &items[0]
	}
	return nil
}

// keystroke is what an editor asks after each edit: completion and
// signature help at the cursor.
func (ss *typingSession) keystroke(cursor int) {
	pos := position(ss.text, cursor)
	before := ""
	if cursor > 0 && cursor <= len(ss.text) {
		_, n := utf8.DecodeLastRuneInString(ss.text[:cursor])
		before = ss.text[cursor-n : cursor]
	}
	ss.completeAt(pos, before)
	var help json.RawMessage
	ss.call("textDocument/signatureHelp", ss.at("textDocument/signatureHelp", pos), &help)
}

// battery sends every position request at the cursor and at one other
// place, and every document request.
func (ss *typingSession) battery(cursor int) {
	ss.positionRequests(position(ss.text, cursor), cursor)
	if len(ss.text) > 0 {
		other := ss.rng.Intn(len(ss.text) + 1)
		for other > 0 && other < len(ss.text) && !utf8.RuneStart(ss.text[other]) {
			other--
		}
		ss.positionRequests(position(ss.text, other), other)
	}
	ss.documentRequests()
}

func (ss *typingSession) positionRequests(pos protocol.Position, off int) {
	var raw json.RawMessage
	for _, m := range []string{
		"textDocument/hover", "textDocument/definition", "textDocument/typeDefinition",
		"textDocument/implementation", "textDocument/documentHighlight", "textDocument/prepareRename",
		"textDocument/signatureHelp",
	} {
		ss.call(m, ss.at(m, pos), &raw)
	}
	refs := ss.at("textDocument/references", pos)
	refs["context"] = map[string]any{"includeDeclaration": true}
	ss.call("textDocument/references", refs, &raw)
	rename := ss.at("textDocument/rename", pos)
	rename["newName"] = "renamed"
	ss.call("textDocument/rename", rename, &raw)

	var items []protocol.CallHierarchyItem
	if ss.call("textDocument/prepareCallHierarchy", ss.at("textDocument/prepareCallHierarchy", pos), &items) && len(items) > 0 {
		ss.last = "callHierarchy/incomingCalls on " + items[0].Name
		ss.call("callHierarchy/incomingCalls", map[string]any{"item": items[0]}, &raw)
		ss.last = "callHierarchy/outgoingCalls on " + items[0].Name
		ss.call("callHierarchy/outgoingCalls", map[string]any{"item": items[0]}, &raw)
	}

	before := ""
	if off > 0 && off <= len(ss.text) {
		_, n := utf8.DecodeLastRuneInString(ss.text[:off])
		before = ss.text[off-n : off]
	}
	if item := ss.completeAt(pos, before); item != nil {
		ss.last = "completionItem/resolve of " + item.Label
		ss.call("completionItem/resolve", item, &raw)
	}

	action := ss.at("textDocument/codeAction", pos)
	action["range"] = map[string]any{"start": pos, "end": pos}
	action["context"] = map[string]any{"diagnostics": []any{}}
	ss.call("textDocument/codeAction", action, &raw)
}

func (ss *typingSession) documentRequests() {
	var raw json.RawMessage
	end := position(ss.text, len(ss.text))
	whole := map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": end}
	for _, r := range []struct {
		method string
		params map[string]any
	}{
		{"textDocument/documentSymbol", nil},
		{"textDocument/semanticTokens/full", nil},
		{"textDocument/formatting", map[string]any{"options": map[string]any{"tabSize": 4, "insertSpaces": true}}},
		{"textDocument/inlayHint", map[string]any{"range": whole}},
		{"textDocument/codeLens", nil},
		{"textDocument/documentLink", nil},
		{"textDocument/codeAction", map[string]any{"range": whole, "context": map[string]any{"diagnostics": []any{}}}},
	} {
		params := map[string]any{"textDocument": ss.doc()}
		for k, v := range r.params {
			params[k] = v
		}
		ss.last = r.method + " (whole document)"
		ss.call(r.method, params, &raw)
	}
	// A request whose position the edits have moved past: a line beyond the
	// end, and a column beyond the last line's end.
	ss.call("textDocument/hover", ss.at("textDocument/hover", protocol.Position{Line: end.Line + 3, Character: 2}), &raw)
	ss.completeAt(protocol.Position{Line: end.Line, Character: end.Character + 5}, "")
}

func (ss *typingSession) save() {
	ss.last = "textDocument/didSave"
	ss.c.notify("textDocument/didSave", map[string]any{"textDocument": ss.doc()})
}

// typingPlan sets how much of each phase a session runs.
type typingPlan struct {
	// growSteps bounds the edits that grow the document to the whole
	// file; battery runs every batteryEvery of them.
	growSteps, batteryEvery int
	// typeChars is the length of the stretch typed one character at a
	// time; typeBattery is how often its battery runs.
	typeChars, typeBattery int
	// deleteSteps bounds the edits that delete a stretch in the middle.
	deleteSteps int
	pastes      int
}

var (
	quickPlan = typingPlan{growSteps: 60, batteryEvery: 12, typeChars: 80, typeBattery: 20, deleteSteps: 12, pastes: 2}
	fullPlan  = typingPlan{growSteps: 160, batteryEvery: 8, typeChars: 160, typeBattery: 12, deleteSteps: 30, pastes: 3}
)

// pieceRe splits text into the pieces typing adds: a word, a number, a run
// of spaces, or one other character.
var pieceRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_?]*|[0-9][0-9_]*|[ \t]+|(?s).`)

// boundaries are the piece boundaries of text, grouped so there are at
// most steps of them, ending at len(text).
func boundaries(text string, steps int) []int {
	locs := pieceRe.FindAllStringIndex(text, -1)
	every := (len(locs) + steps - 1) / max(steps, 1)
	every = max(every, 1)
	var out []int
	for i := every - 1; i < len(locs); i += every {
		out = append(out, locs[i][1])
	}
	if len(out) == 0 || out[len(out)-1] != len(text) {
		out = append(out, len(text))
	}
	return out
}

// lineStart is the start of the line holding off.
func lineStart(text string, off int) int {
	return strings.LastIndexByte(text[:off], '\n') + 1
}

// runTypingSession runs every phase of plan over full, the seed's text.
func runTypingSession(ss *typingSession, full string, plan typingPlan) {
	// grow
	for i, b := range boundaries(full, plan.growSteps) {
		ss.setText(full[:b])
		ss.keystroke(b)
		if (i+1)%plan.batteryEvery == 0 {
			ss.battery(b)
		}
	}
	ss.battery(len(full))
	ss.save()
	if len(full) < 2 {
		return
	}

	// type: a stretch starting at a line start, typed back one character
	// at a time with brackets and quotes auto-closed.
	start := lineStart(full, ss.rng.Intn(len(full)))
	end := min(start+plan.typeChars, len(full))
	for end < len(full) && !utf8.RuneStart(full[end]) {
		end++
	}
	typeStretch(ss, full[:start], full[start:end], full[end:], plan.typeBattery)

	// delete: backspace a stretch in the middle.
	from := ss.rng.Intn(len(full))
	for from > 0 && !utf8.RuneStart(full[from]) {
		from--
	}
	to := min(from+1+ss.rng.Intn(400), len(full))
	for to < len(full) && !utf8.RuneStart(full[to]) {
		to++
	}
	cuts := boundaries(full[from:to], plan.deleteSteps)
	for i := len(cuts) - 2; i >= -1; i-- {
		keep := 0
		if i >= 0 {
			keep = cuts[i]
		}
		ss.setText(full[:from+keep] + full[to:])
		ss.keystroke(from + keep)
		if i%4 == 0 {
			ss.battery(from + keep)
		}
	}

	// paste: a block of whole lines, at a line start, mid-line and at the
	// end.
	lines := strings.SplitAfter(full, "\n")
	first := ss.rng.Intn(len(lines))
	block := strings.Join(lines[first:min(first+1+ss.rng.Intn(12), len(lines))], "")
	for p := range plan.pastes {
		var at int
		switch p % 3 {
		case 0:
			at = lineStart(full, ss.rng.Intn(len(full)))
		case 1:
			at = ss.rng.Intn(len(full))
			for at > 0 && !utf8.RuneStart(full[at]) {
				at--
			}
		default:
			at = len(full)
		}
		ss.setText(full[:at] + block + full[at:])
		ss.keystroke(at + len(block))
		ss.battery(at + len(block))
	}

	ss.setText(full)
	ss.save()
	ss.battery(len(full))
}

// closers are the pairs an editor auto-closes.
var closers = map[rune]string{'(': ")", '[': "]", '{': "}", '"': `"`}

// typeStretch types typed between before and after one character at a
// time. An opening bracket or quote inserts its closer after the cursor;
// typing that closer later steps over it, as editors do.
func typeStretch(ss *typingSession, before, typed, after string, batteryEvery int) {
	var pending []string // auto-inserted closers after the cursor, innermost first
	cur := before
	n := 0
	for _, r := range typed {
		ch := string(r)
		switch {
		case len(pending) > 0 && pending[0] == ch:
			pending = pending[1:]
			cur += ch
		case r == '\n':
			// A newline drops the pending closers onto the line below.
			cur += ch
		default:
			cur += ch
			if c, ok := closers[r]; ok && (r != '"' || !strings.HasSuffix(cur[:len(cur)-1], `\`)) {
				pending = append([]string{c}, pending...)
			}
		}
		ss.setText(cur + strings.Join(pending, "") + after)
		ss.keystroke(len(cur))
		n++
		if n%batteryEvery == 0 {
			ss.battery(len(cur))
		}
	}
	ss.setText(cur + strings.Join(pending, "") + after)
	ss.battery(len(cur))
}

// typingSeed is one document to type, with the files beside it.
type typingSeed struct {
	name  string
	files map[string]string
	open  string
	// note is said before a finding in this seed: where a rotating seed
	// came from and how to reproduce it.
	note string
}

func (s typingSeed) text() string { return s.files[s.open] }

// repoRoot is the repository root, two levels above this package.
const repoRoot = "../.."

// trackedSeeds are every tracked .nomi file, each with the .nomi files and
// nomi.toml of its own directory. A file that names a Go package (`gopkg`)
// is left out: analyzing it builds Go code.
func trackedSeeds(tb testing.TB) []typingSeed {
	tb.Helper()
	out, err := exec.Command("git", "-C", repoRoot, "ls-files", "*.nomi").Output()
	if err != nil {
		tb.Skipf("git ls-files: %v", err)
	}
	var seeds []typingSeed
	for _, rel := range strings.Fields(string(out)) {
		data, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			tb.Fatal(err)
		}
		if strings.Contains(string(data), "gopkg") {
			continue
		}
		name := filepath.Base(rel)
		files := map[string]string{name: string(data)}
		dir := filepath.Join(repoRoot, filepath.Dir(rel))
		entries, _ := os.ReadDir(dir)
		if len(entries) <= 40 {
			for _, e := range entries {
				if e.IsDir() || e.Name() == name || !(strings.HasSuffix(e.Name(), ".nomi") || e.Name() == "nomi.toml") {
					continue
				}
				if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil && !strings.Contains(string(b), "gopkg") {
					files[e.Name()] = string(b)
				}
			}
		}
		seeds = append(seeds, typingSeed{name: rel, files: files, open: name})
	}
	return seeds
}

var fileMarker = regexp.MustCompile(`(?m)^// FILE: ([A-Za-z0-9_./-]+)\n`)

// tourTypingSeeds are the tour's runnable blocks. A block with `// FILE:`
// sections is a project; its main.nomi (else its first file) is typed.
func tourTypingSeeds(tb testing.TB) []typingSeed {
	tb.Helper()
	root := filepath.Join(repoRoot, "tour", "src", "content", "docs")
	chapters, _ := filepath.Glob(filepath.Join(root, "*.md"))
	sort.Strings(chapters)
	var seeds []typingSeed
	for _, chapter := range chapters {
		data, err := os.ReadFile(chapter)
		if err != nil {
			tb.Fatal(err)
		}
		for _, b := range doctest.ExtractBlocks(string(data), "nomi-run") {
			name := fmt.Sprintf("tour/%s:L%d", filepath.Base(chapter), b.Line)
			code := b.Code + "\n"
			marks := fileMarker.FindAllStringSubmatchIndex(code, -1)
			if len(marks) == 0 {
				seeds = append(seeds, typingSeed{name: name, files: map[string]string{"main.nomi": code}, open: "main.nomi"})
				continue
			}
			files := map[string]string{}
			open := ""
			for i, m := range marks {
				end := len(code)
				if i+1 < len(marks) {
					end = marks[i+1][0]
				}
				file := code[m[2]:m[3]]
				files[file] = code[m[1]:end]
				if open == "" && strings.HasSuffix(file, ".nomi") {
					open = file
				}
			}
			if _, ok := files["main.nomi"]; ok {
				open = "main.nomi"
			}
			if open != "" {
				seeds = append(seeds, typingSeed{name: name, files: files, open: open})
			}
		}
	}
	return seeds
}

// quickSeeds is the sample plain `go test` types: a spread of corpus
// files, stdlib modules and tour blocks.
var quickSeeds = []string{
	"tests/05-calendar-and-time/dates_test.nomi",
	"tests/07-structs-and-enums/struct_spread/struct_spread_test.nomi",
	"std/maybe.nomi",
	"tour/pattern-matching.md",
	"tour/pipes.md",
	"tour/modules-and-imports.md",
	"tour/generics.md",
	"tour/interfaces-and-dispatch.md",
}

// runTyping runs one session per seed and fails on every finding no known
// gap explains. It returns the counts, so the caller can log them.
func runTyping(t *testing.T, seeds []typingSeed, plan typingPlan) {
	recordPanics(t)
	var counts typingCounts
	groups := map[string][]typingFinding{}
	var slow []slowRequest
	start := time.Now()
	for _, seed := range seeds {
		ss := newTypingSession(t, seed.name, seed.files, seed.open, &counts)
		runTypingSession(ss, seed.text(), plan)
		ss.close()
		for _, f := range ss.findings {
			if knownTypingGapFor(f) != nil {
				continue
			}
			f.request = seed.name + ": " + f.request
			f.note = seed.note
			groups[f.key()] = append(groups[f.key()], f)
		}
		slow = append(slow, ss.slow...)
	}
	t.Logf("%d documents, %d edits, %d requests in %v", counts.docs, counts.edits, counts.requests, time.Since(start).Round(time.Millisecond))
	for _, r := range slow {
		if r.lines <= smallFileLines {
			t.Logf("slow: %s took %v on %d lines of %s", r.method, r.took.Round(time.Millisecond), r.lines, r.seed)
		}
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fs := groups[k]
		// The smallest text is the best reproducer.
		sort.Slice(fs, func(i, j int) bool { return len(fs[i].text) < len(fs[j].text) })
		var notes []string
		seen := map[string]bool{}
		for _, f := range fs {
			if f.note != "" && !seen[f.note] {
				seen[f.note] = true
				notes = append(notes, f.note)
			}
		}
		t.Errorf("%s%d× %s\nsmallest:\n%s", strings.Join(notes, ""), len(fs), k, fs[0])
	}
}

// rotatingTypingCount is how many documents TestLSPSurvivesTypingRotating
// types.
const rotatingTypingCount = 5

// TestLSPSurvivesTypingRotating types rotatingTypingCount documents beside
// the fixed sample, a different handful every UTC day (internal/rotation):
// seed s types the document a rand source seeded with s picks from the
// tracked files and tour blocks of at most smallFileLines lines, under
// quickPlan. NOMI_GEN_SEED pins the start seed, and a failure names its
// seed and the command that types exactly that document again. The large
// files are left to NOMI_LSP_TYPING=full, which types them all.
func TestLSPSurvivesTypingRotating(t *testing.T) {
	var small []typingSeed
	for _, s := range append(trackedSeeds(t), tourTypingSeeds(t)...) {
		if strings.Count(s.text(), "\n") < smallFileLines {
			small = append(small, s)
		}
	}
	set := rotation.For(t, "./internal/lsp", rotatingTypingCount)
	var seeds []typingSeed
	for _, seed := range set.Seeds() {
		s := small[rand.New(rand.NewSource(seed)).Intn(len(small))]
		s.note = set.Failure(seed) + "document: " + s.name + "\n"
		t.Logf("seed %d types %s", seed, s.name)
		seeds = append(seeds, s)
	}
	runTyping(t, seeds, quickPlan)
}

// TestLSPSurvivesTyping types a fixed sample of seeds (quickSeeds); a
// tour chapter's entry types its first runnable block. NOMI_LSP_TYPING=full
// types every tracked .nomi file and tour block instead:
//
//	NOMI_LSP_TYPING=full go test ./internal/lsp -run '^TestLSPSurvivesTyping$' -count=1 -timeout 0 -v
func TestLSPSurvivesTyping(t *testing.T) {
	all := append(trackedSeeds(t), tourTypingSeeds(t)...)
	if os.Getenv("NOMI_LSP_TYPING") == "full" {
		k, n := 0, 1
		if shard := os.Getenv("NOMI_LSP_TYPING_SHARD"); shard != "" {
			a, b, ok := strings.Cut(shard, "/")
			k, _ = strconv.Atoi(a)
			n, _ = strconv.Atoi(b)
			if !ok || n < 1 || k < 0 || k >= n {
				t.Fatalf("NOMI_LSP_TYPING_SHARD=%q, want k/n with 0 <= k < n", shard)
			}
		}
		var seeds []typingSeed
		for i := k; i < len(all); i += n {
			seeds = append(seeds, all[i])
		}
		runTyping(t, seeds, fullPlan)
		return
	}
	var seeds []typingSeed
	for _, want := range quickSeeds {
		found := false
		for _, s := range all {
			if s.name == want || strings.HasPrefix(s.name, want+":") {
				seeds = append(seeds, s)
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("quick seed %s is not a tracked file or tour chapter", want)
		}
	}
	runTyping(t, seeds, quickPlan)
}

// TestLSPSurvivesTypingPathologicalShapes types two documents the fuzzer
// found. `type E E` overflowed the Go stack in the checker, which no
// recover catches, so it killed the server; a few thousand nested `{` made
// each analysis of the document take minutes.
func TestLSPSurvivesTypingPathologicalShapes(t *testing.T) {
	runTyping(t, []typingSeed{
		{
			name:  "type cycle",
			files: map[string]string{"main.nomi": "type E E\n\ntype A B\n\ntype B List<A>\n\ntypealias X Y\n\ntypealias Y X\n\nfn main() {\n}\n"},
			open:  "main.nomi",
		},
		{
			name:  "deep nesting",
			files: map[string]string{"main.nomi": "fn main() {\n  x = " + strings.Repeat("{", 3000) + "\n  todo\n"},
			open:  "main.nomi",
		},
	}, quickPlan)
}
