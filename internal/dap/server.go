// Package dap is `nomi dap`: a Debug Adapter Protocol server over stdio that
// runs a Nomi program in process on a debug machine (internal/vm's debug.go)
// and lets an editor set breakpoints, step, and read the stack and locals.
//
// A session is one launch: `mode: "run"` runs `main` of a file (or of a
// project's single entry point), `mode: "test"` runs a file's tests, or the
// one test `line` selects as `nomi test --line` does. The program's output
// becomes `output` events, or, with `console: "integratedTerminal"` and a
// client that supports `runInTerminal`, goes to a terminal the editor opens:
// it runs `nomi dap-terminal <socket>` (RunTerminal), which connects back
// and carries the program's standard input and output.
//
// The message types are github.com/google/go-dap's.
package dap

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	godap "github.com/google/go-dap"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// Server is one debug session over a client's connection.
type Server struct {
	r *bufio.Reader

	wmu sync.Mutex
	w   io.Writer
	seq int

	// Executable is the nomi binary a terminal runs as the stub
	// (`nomi dap-terminal`). Empty means os.Executable.
	Executable string
	// OwnsProcess says the server may change the process: a launch's `cwd`
	// and `env` apply to it, as they do to the `nomi dap` command. A server
	// embedded in a test leaves the process alone.
	OwnsProcess bool

	// pending are the reverse requests waiting for the client's response,
	// by seq.
	pmu     sync.Mutex
	pending map[int]chan godap.Message

	requests chan godap.Message
	quit     chan struct{}
	quitOnce sync.Once

	mu sync.Mutex
	// client is what the client said it supports.
	client godap.InitializeRequestArguments
	sess   *session
}

// NewServer is a server reading requests from r and writing responses and
// events to w.
func NewServer(r io.Reader, w io.Writer) *Server {
	return &Server{
		r: bufio.NewReader(r), w: w,
		pending:  map[int]chan godap.Message{},
		requests: make(chan godap.Message, 16),
		quit:     make(chan struct{}),
	}
}

// Serve answers requests until the client disconnects or its input ends. A
// program still running is killed (vm.Debugger.Kill) on the way out.
func (s *Server) Serve() error {
	readErr := make(chan error, 1)
	go func() { readErr <- s.readLoop() }()
	for {
		select {
		case msg := <-s.requests:
			s.dispatch(msg)
		case err := <-readErr:
			s.shutdown()
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case <-s.quit:
			s.shutdown()
			return nil
		}
	}
}

// readLoop reads the client's messages: a response to a reverse request goes
// to its waiter, anything else to the request queue.
func (s *Server) readLoop() error {
	for {
		data, err := godap.ReadBaseMessage(s.r)
		var msg godap.Message
		if err == nil {
			msg, err = codec.DecodeMessage(data)
		}
		if err != nil {
			var fieldErr *godap.DecodeProtocolMessageFieldError
			if errors.As(err, &fieldErr) {
				// A message this library cannot decode (a request with an
				// unknown command): answer a request as unsupported rather
				// than ending the session.
				if fieldErr.SubType == "request" {
					s.send(&godap.ErrorResponse{Response: godap.Response{
						RequestSeq: fieldErr.Seq, Command: fieldErr.FieldValue,
						Message: "unsupported request " + fieldErr.FieldValue,
					}})
				}
				continue
			}
			return err
		}
		if resp, isResponse := msg.(godap.ResponseMessage); isResponse {
			s.pmu.Lock()
			ch := s.pending[resp.GetResponse().RequestSeq]
			delete(s.pending, resp.GetResponse().RequestSeq)
			s.pmu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		select {
		case s.requests <- msg:
		case <-s.quit:
			return nil
		}
	}
}

// send writes one message, numbering it.
func (s *Server) send(msg godap.Message) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.seq++
	switch m := msg.(type) {
	case godap.ResponseMessage:
		m.GetResponse().Seq = s.seq
		m.GetResponse().Type = "response"
	case godap.EventMessage:
		m.GetEvent().Seq = s.seq
		m.GetEvent().Type = "event"
	case godap.RequestMessage:
		m.GetRequest().Seq = s.seq
		m.GetRequest().Type = "request"
	}
	_ = godap.WriteProtocolMessage(s.w, msg)
}

// reverse sends a request to the client and waits for its response.
func (s *Server) reverse(req godap.RequestMessage) (godap.Message, error) {
	ch := make(chan godap.Message, 1)
	s.wmu.Lock()
	s.seq++
	seq := s.seq
	req.GetRequest().Seq, req.GetRequest().Type = seq, "request"
	s.pmu.Lock()
	s.pending[seq] = ch
	s.pmu.Unlock()
	err := godap.WriteProtocolMessage(s.w, req)
	s.wmu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-s.quit:
		return nil, errors.New("the client disconnected")
	}
}

func (s *Server) stop() { s.quitOnce.Do(func() { close(s.quit) }) }

func (s *Server) shutdown() {
	s.mu.Lock()
	sess := s.sess
	s.mu.Unlock()
	if sess != nil {
		sess.kill()
	}
	s.stop()
}

func response(req *godap.Request) godap.Response {
	return godap.Response{RequestSeq: req.Seq, Success: true, Command: req.Command}
}

// fail answers req with an error the client shows its user.
func (s *Server) fail(req *godap.Request, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	r := response(req)
	r.Success = false
	r.Message = text
	s.send(&godap.ErrorResponse{Response: r, Body: godap.ErrorResponseBody{
		Error: &godap.ErrorMessage{Id: 1, Format: text, ShowUser: true}}})
}

func event(name string) godap.Event { return godap.Event{Event: name} }

// output sends text as an output event of category.
func (s *Server) output(category, text string) {
	if text == "" {
		return
	}
	s.send(&godap.OutputEvent{Event: event("output"), Body: godap.OutputEventBody{Category: category, Output: text}})
}

// exceptionFilters are the exception breakpoints a client can turn on.
var exceptionFilters = []godap.ExceptionBreakpointsFilter{
	{Filter: "trap", Label: "Traps", Default: true,
		Description: "Stop where a trap happens: overflow, division by zero, an index out of range, `todo`, a `case` with no arm."},
	{Filter: "assertion", Label: "Failed assertions", Default: true,
		Description: "Stop at a failed `assert`, `refute` or `testing.check`."},
	{Filter: "mainError", Label: "Err from main", Default: true,
		Description: "Stop when `main` returns `Err`, before it ends."},
	{Filter: "taskFailure", Label: "Uncaught task failures", Default: false,
		Description: "Stop where a failed task's fault reaches the code awaiting it (`Task.await`, `Task.await_all`)."},
}

func (s *Server) dispatch(msg godap.Message) {
	switch r := msg.(type) {
	case *godap.InitializeRequest:
		s.mu.Lock()
		s.client = r.Arguments
		s.mu.Unlock()
		s.send(&godap.InitializeResponse{Response: response(&r.Request), Body: godap.Capabilities{
			SupportsConfigurationDoneRequest:  true,
			ExceptionBreakpointFilters:        exceptionFilters,
			SupportsExceptionInfoRequest:      true,
			SupportsTerminateRequest:          true,
			SupportTerminateDebuggee:          true,
			SupportsDelayedStackTraceLoading:  true,
			SupportsEvaluateForHovers:         true,
			SupportsClipboardContext:          true,
			SupportsConditionalBreakpoints:    true,
			SupportsHitConditionalBreakpoints: true,
			SupportsLogPoints:                 true,
			SupportsRestartFrame:              true,
			SupportsStepBack:                  true,
			SupportsFunctionBreakpoints:       true,
			SupportsStepInTargetsRequest:      true,
		}})
	case *godap.LaunchRequest:
		s.launch(r)
	case *godap.AttachRequest:
		s.fail(&r.Request, "nomi dap launches programs; attaching to a running one is not supported")
	case *godap.DisconnectRequest:
		s.shutdown0()
		s.send(&godap.DisconnectResponse{Response: response(&r.Request)})
		s.stop()
	case *godap.TerminateRequest:
		s.withSession(&r.Request, func(sess *session) {
			sess.terminate()
			s.send(&godap.TerminateResponse{Response: response(&r.Request)})
		})
	default:
		s.withSession(requestOf(msg), func(sess *session) { sess.dispatch(msg) })
	}
}

// shutdown0 kills the program a disconnect leaves behind.
func (s *Server) shutdown0() {
	s.mu.Lock()
	sess := s.sess
	s.mu.Unlock()
	if sess != nil {
		sess.kill()
	}
}

func requestOf(msg godap.Message) *godap.Request {
	if r, ok := msg.(godap.RequestMessage); ok {
		return r.GetRequest()
	}
	return &godap.Request{}
}

// withSession runs f with the launched session, or answers req with an error
// when nothing was launched.
func (s *Server) withSession(req *godap.Request, f func(*session)) {
	s.mu.Lock()
	sess := s.sess
	s.mu.Unlock()
	if sess == nil {
		s.fail(req, "%s before launch", req.Command)
		return
	}
	f(sess)
}

// executable is the binary a terminal runs as the stub.
func (s *Server) executable() string {
	if s.Executable != "" {
		return s.Executable
	}
	exe, err := os.Executable()
	if err != nil {
		return "nomi"
	}
	return exe
}

// The session's program types, named here so session.go reads plainly.
type (
	program  = vmhost.Program
	debugger = vm.Debugger
)
