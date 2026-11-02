// Package mcp is `nomi mcp`: a Model Context Protocol server on stdio that
// lets a coding agent ask the compiler what an editor asks it. It speaks
// MCP's JSON-RPC 2.0, one message per line, and answers initialize,
// tools/list and tools/call (and ping). Its tools reuse what already answers
// each question: `nomi check --format json` and `nomi test --format json`
// run as child processes of the same binary, the language server answers
// types, definitions and references in process, internal/format formats, and
// the stdlib's analyzed declarations answer searches.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// protocolVersions are the MCP revisions this server speaks, newest first.
// A client asking for one gets it; any other gets the newest.
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// Options configure a Server.
type Options struct {
	// Nomi is the path of the nomi binary that check and run_tests run.
	// Empty means the running executable.
	Nomi string
	// Root is the workspace the language server is initialized with.
	// Empty means the working directory.
	Root string
	// Version is reported as the server's version in initialize.
	Version string
	// RunCLI runs nomi with args in dir and answers its standard output
	// and exit code. Nil runs Nomi as a child process. Tests replace it.
	RunCLI func(ctx context.Context, dir string, args ...string) (stdout []byte, exit int, err error)
}

// Server answers one client.
type Server struct {
	opts Options

	lspOnce sync.Once
	lsp     *lspClient
	lspErr  error

	outMu sync.Mutex
	out   io.Writer
}

// New returns a Server with opts.
func New(opts Options) *Server {
	if opts.Root == "" {
		opts.Root, _ = os.Getwd()
	}
	if opts.Nomi == "" {
		opts.Nomi, _ = os.Executable()
	}
	if opts.RunCLI == nil {
		nomi := opts.Nomi
		opts.RunCLI = func(ctx context.Context, dir string, args ...string) ([]byte, int, error) {
			cmd := exec.CommandContext(ctx, nomi, args...)
			cmd.Dir = dir
			out, err := cmd.Output()
			if ee, ok := err.(*exec.ExitError); ok {
				return out, ee.ExitCode(), nil
			}
			return out, 0, err
		}
	}
	return &Server{opts: opts}
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC's error codes.
const (
	codeParse          = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Serve reads messages from r and writes replies to w until r ends. Each
// request runs on its own goroutine, so a slow tool does not hold up a ping.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	s.out = w
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	var wg sync.WaitGroup
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			s.reply(nil, nil, &rpcError{codeParse, err.Error()})
			continue
		}
		if m.Method == "" || len(m.ID) == 0 {
			// A response or a notification (initialized, cancelled):
			// nothing to answer.
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.handle(ctx, m.Method, m.Params)
			s.reply(m.ID, result, err)
		}()
	}
	wg.Wait()
	if s.lsp != nil {
		s.lsp.close()
	}
	return sc.Err()
}

func (s *Server) reply(id json.RawMessage, result any, err *rpcError) {
	if id == nil {
		id = json.RawMessage("null")
	}
	m := message{JSONRPC: "2.0", ID: id}
	if err != nil {
		m.Error = err
	} else {
		if result == nil {
			result = struct{}{}
		}
		m.Result = result
	}
	b, merr := json.Marshal(m)
	if merr != nil {
		b, _ = json.Marshal(message{JSONRPC: "2.0", ID: id, Error: &rpcError{-32603, merr.Error()}})
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	s.out.Write(append(b, '\n'))
}

func (s *Server) handle(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		version := protocolVersions[0]
		for _, v := range protocolVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "nomi", "version": s.opts.Version},
			"instructions": "Nomi's compiler. Positions are 1-based lines and 1-based byte columns, " +
				"as `nomi check` prints them. Paths may be absolute or relative to the server's working directory.",
		}, nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		list := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			list = append(list, map[string]any{"name": t.name, "description": t.description, "inputSchema": t.schema})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{codeInvalidParams, err.Error()}
		}
		for _, t := range tools {
			if t.name == p.Name {
				return s.call(ctx, t, p.Arguments), nil
			}
		}
		return nil, &rpcError{codeInvalidParams, fmt.Sprintf("unknown tool %q", p.Name)}
	}
	return nil, &rpcError{codeMethodNotFound, fmt.Sprintf("method %q not found", method)}
}

// call runs a tool. A tool's own failure is a result with isError set, as
// MCP asks, so the model reads it; only a malformed call is a JSON-RPC error.
func (s *Server) call(ctx context.Context, t tool, args json.RawMessage) map[string]any {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	result, err := t.run(ctx, s, args)
	if err != nil {
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		}
	}
	text, _ := json.MarshalIndent(result, "", "  ")
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(text)}},
		"structuredContent": result,
	}
}

// language starts the in-process language server on first use.
func (s *Server) language() (*lspClient, error) {
	s.lspOnce.Do(func() { s.lsp, s.lspErr = startLSP(s.opts.Root) })
	return s.lsp, s.lspErr
}
