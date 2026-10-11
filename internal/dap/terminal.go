package dap

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	godap "github.com/google/go-dap"
)

// TerminalCommand is the hidden `nomi` subcommand a terminal runs to carry a
// debugged program's input and output: `nomi dap-terminal <socket>`.
const TerminalCommand = "dap-terminal"

// terminalTimeout is how long a launch waits for the terminal's stub to
// connect back.
const terminalTimeout = 15 * time.Second

// programIO is where a program's standard streams go: the debug console
// (output events, no input) until a terminal connects, and then the
// terminal. The program is loaded with these proxies, so the choice can be
// made after loading.
type programIO struct {
	mu   sync.Mutex
	out  io.Writer
	err  io.Writer
	in   io.Reader
	conn net.Conn
}

// console sends output to s's debug console, and gives no input.
func (p *programIO) console(s *Server) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.out = &eventWriter{s: s, category: "stdout"}
	p.err = &eventWriter{s: s, category: "stderr"}
	p.in = &consoleInput{s: s}
}

// terminal sends the program's input and output through conn.
func (p *programIO) terminal(conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.out, p.err, p.in, p.conn = conn, conn, conn, conn
}

// close ends the terminal's connection, which ends its stub.
func (p *programIO) close() {
	p.mu.Lock()
	conn := p.conn
	p.conn = nil
	p.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

func (p *programIO) stdout() io.Writer { return proxyWriter{p: p} }
func (p *programIO) stderr() io.Writer { return proxyWriter{p: p, isErr: true} }
func (p *programIO) stdin() io.Reader  { return proxyReader{p: p} }

type proxyWriter struct {
	p     *programIO
	isErr bool
}

func (w proxyWriter) Write(b []byte) (int, error) {
	w.p.mu.Lock()
	dst := w.p.out
	if w.isErr {
		dst = w.p.err
	}
	w.p.mu.Unlock()
	if dst == nil {
		return len(b), nil
	}
	if _, err := dst.Write(b); err != nil {
		// The terminal went away; the program's output has nowhere to go,
		// which is not the program's failure.
		return len(b), nil
	}
	return len(b), nil
}

type proxyReader struct{ p *programIO }

func (r proxyReader) Read(b []byte) (int, error) {
	r.p.mu.Lock()
	src := r.p.in
	r.p.mu.Unlock()
	if src == nil {
		return 0, io.EOF
	}
	return src.Read(b)
}

// eventWriter sends each write as an output event.
type eventWriter struct {
	s        *Server
	category string
}

func (w *eventWriter) Write(b []byte) (int, error) {
	w.s.output(w.category, string(b))
	return len(b), nil
}

// consoleInput is the input of a program whose output goes to the debug
// console, which cannot type into it: the first read says so, and every read
// is the end of input.
type consoleInput struct {
	s    *Server
	once sync.Once
}

func (c *consoleInput) Read([]byte) (int, error) {
	c.once.Do(func() {
		c.s.output("console", "The program reads standard input, which the debug console cannot supply, so it reads end of input. "+
			"To type into it, launch with \"console\": \"integratedTerminal\".\n")
	})
	return 0, io.EOF
}

// openTerminal asks the client to run the stub in a terminal of kind
// ("integrated" or "external") and waits for it to connect.
func (s *Server) openTerminal(kind, title, cwd string) (net.Conn, error) {
	dir, err := os.MkdirTemp(socketDir(), "nomi-dap-")
	if err != nil {
		return nil, err
	}
	sock := filepath.Join(dir, "terminal.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	defer func() {
		ln.Close()
		os.RemoveAll(dir)
	}()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- conn
	}()
	args := godap.RunInTerminalRequestArguments{Kind: kind, Title: title, Cwd: cwd,
		Args: []string{s.executable(), TerminalCommand, sock}}
	resp, err := s.reverse(&godap.RunInTerminalRequest{Request: godap.Request{Command: "runInTerminal"}, Arguments: args})
	if err != nil {
		return nil, err
	}
	if r, ok := resp.(godap.ResponseMessage); ok && !r.GetResponse().Success {
		return nil, errors.New(r.GetResponse().Message)
	}
	select {
	case conn, ok := <-accepted:
		if !ok {
			return nil, errors.New("the terminal did not connect")
		}
		return conn, nil
	case <-time.After(terminalTimeout):
		return nil, fmt.Errorf("the terminal did not connect within %s", terminalTimeout)
	}
}

// socketDir is where a terminal's socket goes: the temporary directory,
// unless its path is too long for a Unix socket's.
func socketDir() string {
	dir := os.TempDir()
	if len(dir) > 60 {
		if info, err := os.Stat("/tmp"); err == nil && info.IsDir() {
			return "/tmp"
		}
	}
	return dir
}

// RunTerminal is the stub a terminal runs: it connects to the debug
// session's socket and carries in to the program and the program's output to
// out, until the program ends. It answers the stub's exit status.
func RunTerminal(sock string, in io.Reader, out io.Writer) int {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nomi %s: %v\n", TerminalCommand, err)
		return 1
	}
	defer conn.Close()
	go func() {
		_, _ = io.Copy(conn, in)
		if uc, ok := conn.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
	}()
	_, _ = io.Copy(out, conn)
	return 0
}
