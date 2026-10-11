package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	godap "github.com/google/go-dap"
)

// `nomi dap` as an editor runs it: a subprocess on stdio, whose
// runInTerminal request the client answers by running `nomi dap-terminal`,
// which carries an interactive program's input and output.
func TestDAP_ASubprocessDebugsAnInteractiveProgramThroughItsTerminal(t *testing.T) {
	program := filepath.Join(repoRoot(t), "internal", "dap", "testdata", "interactive.nomi")
	cmd := exec.Command(nomiBin, "dap")
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
		r := bufio.NewReader(stdout)
		for {
			data, err := godap.ReadBaseMessage(r)
			if err != nil {
				close(msgs)
				return
			}
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			msgs <- m
		}
	}()
	var wmu sync.Mutex
	seq := 0
	send := func(m map[string]any) int {
		wmu.Lock()
		defer wmu.Unlock()
		seq++
		m["seq"] = seq
		data, _ := json.Marshal(m)
		if err := godap.WriteBaseMessage(stdin, data); err != nil {
			t.Fatal(err)
		}
		return seq
	}
	var termIn io.WriteCloser
	var termOut bytes.Buffer
	var term *exec.Cmd
	// next answers the next message that is not a runInTerminal request,
	// running the terminal's command for one that is.
	next := func(what string) map[string]any {
		t.Helper()
		for {
			select {
			case m, ok := <-msgs:
				if !ok {
					t.Fatalf("nomi dap ended waiting for %s; stderr:\n%s", what, stderr.String())
				}
				if m["type"] == "request" && m["command"] == "runInTerminal" {
					args := m["arguments"].(map[string]any)["args"].([]any)
					term = exec.Command(args[0].(string))
					for _, a := range args[1:] {
						term.Args = append(term.Args, a.(string))
					}
					termIn, _ = term.StdinPipe()
					term.Stdout = &termOut
					if err := term.Start(); err != nil {
						t.Fatal(err)
					}
					send(map[string]any{"type": "response", "request_seq": m["seq"], "command": "runInTerminal",
						"success": true, "body": map[string]any{"processId": term.Process.Pid}})
					continue
				}
				return m
			case <-time.After(30 * time.Second):
				t.Fatalf("no %s within 30s; stderr:\n%s", what, stderr.String())
			}
		}
	}
	request := func(command string, args map[string]any) map[string]any {
		t.Helper()
		id := send(map[string]any{"type": "request", "command": command, "arguments": args})
		for {
			m := next("the response to " + command)
			if m["type"] == "response" && int(m["request_seq"].(float64)) == id {
				if m["success"] != true {
					t.Fatalf("%s failed: %v", command, m["message"])
				}
				body, _ := m["body"].(map[string]any)
				return body
			}
		}
	}
	waitEvent := func(name string) map[string]any {
		t.Helper()
		for {
			m := next("a " + name + " event")
			if m["type"] == "event" && m["event"] == name {
				body, _ := m["body"].(map[string]any)
				return body
			}
		}
	}

	request("initialize", map[string]any{"adapterID": "nomi", "linesStartAt1": true, "columnsStartAt1": true,
		"supportsRunInTerminalRequest": true})
	request("launch", map[string]any{"mode": "run", "program": program})
	waitEvent("initialized")
	if term == nil {
		t.Fatal("nomi dap asked for no terminal")
	}
	request("setBreakpoints", map[string]any{"source": map[string]any{"path": program},
		"breakpoints": []map[string]any{{"line": 9}}})
	request("configurationDone", map[string]any{})
	if _, err := termIn.Write([]byte("alpha\n")); err != nil {
		t.Fatal(err)
	}
	stop := waitEvent("stopped")
	thread := int(stop["threadId"].(float64))
	frames := request("stackTrace", map[string]any{"threadId": thread})["stackFrames"].([]any)
	frame := frames[0].(map[string]any)
	if frame["line"].(float64) != 9 || frame["source"].(map[string]any)["path"] != program {
		t.Fatalf("stopped at %v", frame)
	}
	scopes := request("scopes", map[string]any{"frameId": frame["id"]})["scopes"].([]any)
	ref := scopes[0].(map[string]any)["variablesReference"]
	vars := request("variables", map[string]any{"variablesReference": ref})["variables"].([]any)
	var locals []string
	for _, v := range vars {
		v := v.(map[string]any)
		locals = append(locals, v["name"].(string)+"="+v["value"].(string))
	}
	if strings.Join(locals, " ") != `seen=0 line="alpha"` {
		t.Fatalf("locals at the breakpoint: %v", locals)
	}
	request("setBreakpoints", map[string]any{"source": map[string]any{"path": program}, "breakpoints": []any{}})
	request("continue", map[string]any{"threadId": thread})
	if _, err := termIn.Write([]byte("beta\n")); err != nil {
		t.Fatal(err)
	}
	termIn.Close()
	exited := waitEvent("exited")
	if exited["exitCode"].(float64) != 0 {
		t.Fatalf("exit code %v", exited["exitCode"])
	}
	waitEvent("terminated")
	if err := term.Wait(); err != nil {
		t.Fatalf("the terminal stub: %v", err)
	}
	if got := termOut.String(); got != "type lines; end with ctrl-d\ngot alpha\ngot beta\n2 lines\n" {
		t.Fatalf("the terminal showed %q", got)
	}
	request("disconnect", map[string]any{"terminateDebuggee": true})
	if err := cmd.Wait(); err != nil {
		t.Fatalf("nomi dap: %v\n%s", err, stderr.String())
	}
}
