package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// `nomi lsp` as an editor runs it: a subprocess on stdio that answers
// initialize with its capabilities, answers shutdown, and exits 0 on exit.
// VS Code's client starts it with `--stdio`, so the test does too.
func TestLSP_ASubprocessAnswersInitializeAndExitsOnExit(t *testing.T) {
	cmd := exec.Command(nomiBin, "lsp", "--stdio")
	cmd.Env = append(os.Environ(), "NOMI_RUNNER_CACHE_ROOT="+t.TempDir())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	msgs := make(chan map[string]any, 64)
	go func() {
		defer close(msgs)
		r := bufio.NewReader(stdout)
		for {
			m, err := readLSPMessage(r)
			if err != nil {
				return
			}
			msgs <- m
		}
	}()
	send := func(m map[string]any) {
		m["jsonrpc"] = "2.0"
		data, _ := json.Marshal(m)
		if _, err := fmt.Fprintf(stdin, "Content-Length: %d\r\n\r\n%s", len(data), data); err != nil {
			t.Fatalf("writing %v: %v\nstderr:\n%s", m["method"], err, stderr.String())
		}
	}
	// response is the reply to request id, past any notification the
	// server sends first.
	response := func(id float64) map[string]any {
		deadline := time.After(time.Minute)
		for {
			select {
			case m, ok := <-msgs:
				if !ok {
					t.Fatalf("nomi lsp closed its output before answering request %v\nstderr:\n%s", id, stderr.String())
				}
				if got, _ := m["id"].(float64); got == id && m["method"] == nil {
					return m
				}
			case <-deadline:
				t.Fatalf("no answer to request %v in a minute\nstderr:\n%s", id, stderr.String())
			}
		}
	}

	send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{
		"processId": nil, "rootUri": nil, "capabilities": map[string]any{},
	}})
	answer := response(1)
	result, _ := answer["result"].(map[string]any)
	if result == nil {
		t.Fatalf("initialize answered with no result: %v", answer)
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != "nomi" {
		t.Errorf("serverInfo.name = %v, want nomi", info["name"])
	}
	if v, _ := info["version"].(string); v == "" {
		t.Errorf("serverInfo carries no version: %v", info)
	}
	caps, _ := result["capabilities"].(map[string]any)
	for _, c := range []string{"textDocumentSync", "hoverProvider", "completionProvider", "definitionProvider"} {
		if caps[c] == nil {
			t.Errorf("initialize's capabilities lack %s: %v", c, caps)
		}
	}

	send(map[string]any{"id": 2, "method": "shutdown"})
	if r := response(2); r["error"] != nil {
		t.Fatalf("shutdown answered with an error: %v", r)
	}
	send(map[string]any{"method": "exit"})
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("nomi lsp exited with %v after exit\nstderr:\n%s", err, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("nomi lsp still runs 30 seconds after exit\nstderr:\n%s", stderr.String())
	}
}

// `nomi lsp` takes no argument but `--stdio`, and names the one it refuses.
func TestLSP_AnArgumentOtherThanStdioIsRefused(t *testing.T) {
	env := append(os.Environ(), "NOMI_RUNNER_CACHE_ROOT="+t.TempDir())
	o := runProcess(t, env, t.TempDir(), "", nomiBin, "lsp", "--port", "9000")
	want := "nomi lsp: unexpected argument \"--port\"\n" + lspUsage + "\n"
	if o.exit != 1 || o.stdout != "" || o.stderr != want {
		t.Errorf("nomi lsp --port 9000: exit %d\n%s\nwant exit 1 and stderr\n%s", o.exit, o.transcript(), want)
	}
}

// readLSPMessage reads one Content-Length framed JSON-RPC message.
func readLSPMessage(r *bufio.Reader) (map[string]any, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			if length, err = strconv.Atoi(v); err != nil {
				return nil, err
			}
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("a message with no Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var m map[string]any
	err := json.Unmarshal(body, &m)
	return m, err
}
