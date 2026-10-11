package dap

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	godap "github.com/google/go-dap"
)

// A scripted session: the test plays the client, sending requests to a
// server running in process and waiting for its responses and events. Every
// message is written to a transcript, which is compared with a golden file
// in testdata (NOMI_REGENERATE_EXPECTATIONS=1 rewrites them).
//
// A response is recorded when its request's step reads it; events are
// recorded, in the order they arrived, by the wait step that consumes them.
// The order of a program's events is fixed by the program, and the order of
// responses by the script, so a transcript does not depend on how the two
// interleave on the wire.
type harness struct {
	t *testing.T
	// srv is the server under test, which a test reaches into only to
	// simulate an internal failure.
	srv *Server

	wmu sync.Mutex
	w   io.Writer
	seq int

	incoming chan map[string]any
	queue    []map[string]any
	log      strings.Builder
	served   chan error

	// The terminal a runInTerminal request opens, run in process: what the
	// test types into it, and what it shows.
	termIn   *io.PipeWriter
	termOut  *syncBuffer
	termDone chan int
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	srv := NewServer(serverR, serverW)
	srv.Executable = "nomi"
	h := &harness{t: t, srv: srv, w: clientW, incoming: make(chan map[string]any, 256), served: make(chan error, 1)}
	go func() {
		err := srv.Serve()
		serverW.Close()
		h.served <- err
	}()
	go h.read(bufio.NewReader(clientR))
	t.Cleanup(func() {
		clientW.Close()
		if h.termIn != nil {
			h.termIn.Close()
		}
	})
	return h
}

// read reads the server's messages. A runInTerminal request is answered at
// once by running the terminal stub in process.
func (h *harness) read(r *bufio.Reader) {
	defer close(h.incoming)
	for {
		data, err := godap.ReadBaseMessage(r)
		if err != nil {
			return
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return
		}
		if m["type"] == "request" && m["command"] == "runInTerminal" {
			h.startTerminal(m)
		}
		h.incoming <- m
	}
}

func (h *harness) startTerminal(req map[string]any) {
	args := req["arguments"].(map[string]any)["args"].([]any)
	sock := args[len(args)-1].(string)
	in, inW := io.Pipe()
	h.termIn, h.termOut, h.termDone = inW, &syncBuffer{}, make(chan int, 1)
	go func() { h.termDone <- RunTerminal(sock, in, h.termOut) }()
	h.write(map[string]any{"type": "response", "request_seq": req["seq"], "command": "runInTerminal",
		"success": true, "body": map[string]any{}})
}

func (h *harness) write(m map[string]any) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	data, err := json.Marshal(m)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := godap.WriteBaseMessage(h.w, data); err != nil {
		h.t.Fatal(err)
	}
}

// next is the next message from the server.
func (h *harness) next(what string) map[string]any {
	h.t.Helper()
	select {
	case m, ok := <-h.incoming:
		if !ok {
			h.t.Fatalf("the server closed its output waiting for %s; transcript:\n%s", what, h.log.String())
		}
		return m
	case <-time.After(20 * time.Second):
		h.t.Fatalf("no %s within 20s; transcript:\n%s", what, h.log.String())
	}
	return nil
}

// request sends a request and records it and its response, answering the
// response's body.
func (h *harness) request(command string, args any) map[string]any {
	h.t.Helper()
	h.wmu.Lock()
	h.seq++
	seq := h.seq
	h.wmu.Unlock()
	m := map[string]any{"seq": seq, "type": "request", "command": command}
	if args != nil {
		m["arguments"] = args
	}
	h.record("->", command, args)
	h.write(m)
	for {
		msg := h.next("the response to " + command)
		if msg["type"] == "response" && int(msg["request_seq"].(float64)) == seq {
			if msg["success"] != true {
				h.record("<-", "response "+command+" failed", msg["message"])
			} else {
				h.record("<-", "response "+command, msg["body"])
			}
			body, _ := msg["body"].(map[string]any)
			return body
		}
		h.queue = append(h.queue, msg)
	}
}

// wait records every message up to the next event named name, and answers
// that event's body.
func (h *harness) wait(name string) map[string]any {
	h.t.Helper()
	for {
		var msg map[string]any
		if len(h.queue) > 0 {
			msg, h.queue = h.queue[0], h.queue[1:]
		} else {
			msg = h.next("a " + name + " event")
		}
		switch msg["type"] {
		case "event":
			h.record("<-", "event "+msg["event"].(string), msg["body"])
			if msg["event"] == name {
				body, _ := msg["body"].(map[string]any)
				return body
			}
		case "request":
			h.record("<-", "request "+msg["command"].(string), msg["arguments"])
		default:
			h.record("<-", "unexpected", msg)
		}
	}
}

// note adds a line of the test's own to the transcript.
func (h *harness) note(format string, args ...any) {
	fmt.Fprintf(&h.log, "## "+format+"\n", args...)
}

func (h *harness) record(dir, what string, v any) {
	text := ""
	if v != nil {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			h.t.Fatal(err)
		}
		text = " " + strings.TrimSuffix(b.String(), "\n")
	}
	fmt.Fprintf(&h.log, "%s %s%s\n", dir, what, normalize(text))
}

var socketPath = regexp.MustCompile(`[^"]*/nomi-dap-[^"/]*/terminal\.sock`)

// normalize replaces the paths a transcript would otherwise depend on.
func normalize(s string) string {
	abs, _ := filepath.Abs("testdata")
	s = strings.ReplaceAll(s, abs, "$TESTDATA")
	std, _ := filepath.Abs(filepath.Join("..", "..", "std"))
	s = strings.ReplaceAll(s, std, "$STD")
	return socketPath.ReplaceAllString(s, "$$SOCKET")
}

// check compares the transcript with testdata/name.golden.
func (h *harness) check(name string) {
	h.t.Helper()
	golden := filepath.Join("testdata", name+".golden")
	got := h.log.String()
	if os.Getenv("NOMI_REGENERATE_EXPECTATIONS") != "" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			h.t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		h.t.Fatalf("%v (NOMI_REGENERATE_EXPECTATIONS=1 writes it); transcript:\n%s", err, got)
	}
	if got != string(want) {
		h.t.Fatalf("the transcript differs from %s (NOMI_REGENERATE_EXPECTATIONS=1 rewrites it):\n%s",
			golden, lineDiff(string(want), got))
	}
}

// lineDiff shows the first line where two transcripts differ, with the
// lines around it.
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("line %d:\n  want %s\n  got  %s", i+1, wl, gl)
		}
	}
	return "(no line differs)"
}

// served waits for the server to end after a disconnect.
func (h *harness) ended() {
	h.t.Helper()
	select {
	case err := <-h.served:
		if err != nil {
			h.t.Fatalf("the server ended with %v", err)
		}
	case <-time.After(20 * time.Second):
		h.t.Fatalf("the server did not end after disconnect")
	}
}

// --- script helpers ---------------------------------------------------------

func testdata(name string) string {
	abs, _ := filepath.Abs(filepath.Join("testdata", name))
	return abs
}

// start initializes a session and launches config, up to the initialized
// event.
func (h *harness) start(terminal bool, config map[string]any) {
	h.t.Helper()
	h.request("initialize", map[string]any{"adapterID": "nomi", "clientID": "test", "linesStartAt1": true,
		"columnsStartAt1": true, "pathFormat": "path", "supportsVariableType": true, "supportsVariablePaging": true,
		"supportsRunInTerminalRequest": terminal})
	h.request("launch", config)
	h.wait("initialized")
}

func (h *harness) breakAt(file string, lines ...int) {
	h.t.Helper()
	bps := []map[string]any{}
	for _, l := range lines {
		bps = append(bps, map[string]any{"line": l})
	}
	h.request("setBreakpoints", map[string]any{"source": map[string]any{"path": testdata(file)}, "breakpoints": bps})
}

// configure sets the default exception filters and finishes configuration,
// which starts the program.
func (h *harness) configure() {
	h.t.Helper()
	h.request("setExceptionBreakpoints", map[string]any{"filters": []string{"trap", "assertion", "mainError"}})
	h.request("configurationDone", nil)
}

// stopped waits for the next stop and answers its thread.
func (h *harness) stopped() int {
	h.t.Helper()
	body := h.wait("stopped")
	return int(body["threadId"].(float64))
}

// stack requests a thread's stack and answers its frames' IDs.
func (h *harness) stack(thread int) []int {
	h.t.Helper()
	body := h.request("stackTrace", map[string]any{"threadId": thread})
	var ids []int
	for _, f := range body["stackFrames"].([]any) {
		ids = append(ids, int(f.(map[string]any)["id"].(float64)))
	}
	return ids
}

// locals requests a frame's scopes and its locals, answering the locals'
// references by name.
func (h *harness) locals(frame int) map[string]int {
	h.t.Helper()
	body := h.request("scopes", map[string]any{"frameId": frame})
	scopes := body["scopes"].([]any)
	if len(scopes) == 0 {
		return nil
	}
	ref := int(scopes[0].(map[string]any)["variablesReference"].(float64))
	return h.variables(map[string]any{"variablesReference": ref})
}

func (h *harness) variables(args map[string]any) map[string]int {
	h.t.Helper()
	body := h.request("variables", args)
	refs := map[string]int{}
	for _, v := range body["variables"].([]any) {
		v := v.(map[string]any)
		refs[v["name"].(string)] = int(v["variablesReference"].(float64))
	}
	return refs
}

func (h *harness) do(command string, thread int) {
	h.t.Helper()
	h.request(command, map[string]any{"threadId": thread})
}

func (h *harness) disconnect() {
	h.t.Helper()
	h.request("disconnect", map[string]any{"terminateDebuggee": true})
	h.ended()
}

func runConfig(file string) map[string]any {
	return map[string]any{"type": "nomi", "request": "launch", "mode": "run", "program": testdata(file),
		"console": "internalConsole"}
}
