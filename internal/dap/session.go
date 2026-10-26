package dap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	godap "github.com/google/go-dap"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/internal/vmcmd"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// launchArgs is a launch request's configuration.
type launchArgs struct {
	// Mode is "run" (the default) or "test".
	Mode string `json:"mode"`
	// Program is the file to run or test. In run mode a directory names a
	// project whose nomi.toml lists one entry point.
	Program string `json:"program"`
	// Line selects one test, a `group` or a `//!` attached test by a
	// line it starts on, as `nomi test --line` does.
	Line lineNumber `json:"line"`
	// Format is a test run's report format: "text" (the default) or
	// "json", the records `nomi test --format json` prints, which a test
	// runner reading the session's output parses.
	Format string            `json:"format"`
	Args   []string          `json:"args"`
	Cwd    string            `json:"cwd"`
	Env    map[string]string `json:"env"`
	// Console is where the program's input and output go:
	// "integratedTerminal" (the default in run mode, when the client can
	// open one) or "internalConsole" (the debug console, with no input).
	Console string `json:"console"`
	// JustMyCode, true unless set false, makes a step pass through the
	// standard library's code.
	JustMyCode  *bool `json:"justMyCode"`
	StopOnEntry bool  `json:"stopOnEntry"`
	NoDebug     bool  `json:"noDebug"`
}

// lineNumber is a launch configuration's line: a number, or a string of
// digits, as an editor that substitutes a variable into a configuration
// (Zed's `$ZED_ROW`) writes it.
type lineNumber int

func (l *lineNumber) UnmarshalJSON(data []byte) error {
	text := strings.Trim(string(data), `"`)
	if text == "" || text == "null" {
		*l = 0
		return nil
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return fmt.Errorf("line %s is not a line number", data)
	}
	*l = lineNumber(n)
	return nil
}

// session is one launched program.
type session struct {
	s    *Server
	args launchArgs
	// path is the program's file, absolute.
	path string
	test bool
	p    *program
	// d is nil for a launch without debugging (noDebug).
	d  *debugger
	io *programIO

	mu      sync.Mutex
	started bool
	ended   bool
	killed  bool
	// entry says the next pause stop is the one stopOnEntry asked for.
	entry bool
	// refs are the variable references handed out since the last resume,
	// by reference - 1; localsRefs are the locals scopes' among them, by
	// frame.
	refs       []varRef
	localsRefs map[int]int
	// frames are the stack frames handed out since the last resume, by ID.
	frames map[int]frameKind
	labels int
	// bps are the breakpoints set, by the file they landed in; fnBPs are
	// the function breakpoints' IDs, in the order the debugger has them.
	bps    map[string][]breakpoint
	fnBPs  []int
	nextBP int
	// exceptions are each thread's last exception stop, for exceptionInfo.
	exceptions map[int]vm.Stop
	// cache holds the expressions compiled for evaluation (evaluate.go).
	cache compileCache
	// restartMu is held while a restartFrame, stepBack or reverseContinue
	// request answers, so the stop it leads to is reported after the
	// response.
	restartMu sync.Mutex
	// travelled says the console has explained going back once.
	travelled bool
	// locs are the location references handed out (protocol.go), by
	// reference - 1, and locIndex the reference of each position.
	locs     []ir.Pos
	locIndex map[ir.Pos]int
}

type frameKind uint8

const (
	frameNomi frameKind = iota
	// frameVia is a Go-driven operation between two Nomi frames, with no
	// locals.
	frameVia
	// frameLabel is the "(tail call)" label below a tail call's frame.
	frameLabel
)

// labelBase numbers label frames apart from the debugger's frame IDs.
const labelBase = 1 << 20

type breakpoint struct {
	id   int
	line int
}

func (s *Server) launch(r *godap.LaunchRequest) {
	s.mu.Lock()
	launched := s.sess != nil
	s.mu.Unlock()
	if launched {
		s.fail(&r.Request, "this session already launched a program")
		return
	}
	var args launchArgs
	if err := json.Unmarshal(r.Arguments, &args); err != nil {
		s.fail(&r.Request, "the launch configuration does not parse: %v", err)
		return
	}
	sess, err := s.prepare(args)
	if err != nil {
		s.fail(&r.Request, "%s", strings.TrimRight(err.Error(), "\n"))
		return
	}
	s.mu.Lock()
	s.sess = sess
	s.mu.Unlock()
	if sess.d != nil {
		go sess.pump()
	}
	sess.openConsole()
	s.send(&godap.LaunchResponse{Response: response(&r.Request)})
	s.send(&godap.InitializedEvent{Event: event("initialized")})
}

// prepare checks a launch configuration and loads its program.
func (s *Server) prepare(args launchArgs) (*session, error) {
	switch args.Mode {
	case "", "run":
		args.Mode = "run"
	case "test":
	default:
		return nil, fmt.Errorf("unknown mode %q: use \"run\" or \"test\"", args.Mode)
	}
	if args.Program == "" {
		return nil, errors.New("the launch configuration names no program")
	}
	if s.OwnsProcess {
		if args.Cwd != "" {
			if err := os.Chdir(args.Cwd); err != nil {
				return nil, err
			}
		}
		for k, v := range args.Env {
			_ = os.Setenv(k, v)
		}
	}
	path := args.Program
	if !filepath.IsAbs(path) && args.Cwd != "" {
		path = filepath.Join(args.Cwd, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = clientPath(path)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%s: no such file or directory", args.Program)
	}
	if info.IsDir() {
		if args.Mode == "test" {
			return nil, fmt.Errorf("%s is a directory; name the test file to debug", args.Program)
		}
		if path, err = entryOf(path); err != nil {
			return nil, err
		}
	}
	sess := &session{s: s, args: args, path: path, test: args.Mode == "test", io: &programIO{},
		localsRefs: map[int]int{}, frames: map[int]frameKind{}, bps: map[string][]breakpoint{},
		exceptions: map[int]vm.Stop{}, locIndex: map[ir.Pos]int{}}
	sess.io.console(s)
	stdlib := sess.test && vmcmd.IsStdlibPath(path)
	if !stdlib {
		res, err := ffirun.Prepare(path)
		if err != nil {
			return nil, err
		}
		if !res.FastPath {
			return nil, fmt.Errorf("%s uses Go bindings, which nomi dap cannot debug yet; run it with `nomi %s`",
				vmhost.DisplayPath(path), args.Mode)
		}
	}
	if _, err := vmhost.ParseTestFormat(args.Format); args.Format != "" && err != nil {
		return nil, err
	}
	if sess.test && args.Line > 0 {
		if err := testAtLine(path, int(args.Line)); err != nil {
			return nil, err
		}
	}
	opts := []vmhost.Option{vmhost.WithErrorOutput(sess.io.stderr()), vmhost.WithInput(sess.io.stdin())}
	switch {
	case stdlib:
		sess.p, err = vmhost.LoadStdlib(path)
	default:
		sess.p, err = vmhost.Load(path, opts...)
	}
	if err != nil {
		var b strings.Builder
		vmhost.WriteFailure(&b, err)
		return nil, errors.New(b.String())
	}
	sess.p.KeepSources()
	if !sess.test {
		if err := sess.p.RequireMain("run"); err != nil {
			return nil, err
		}
	}
	if !args.NoDebug {
		sess.d = vm.NewDebugger()
		sess.d.SetExceptionFilters(vm.ExceptionFilters{Traps: true, Assertions: true, MainErrors: true})
		if sess.justMyCode() {
			sess.d.SetSkipFiles(std.IsSourcePath)
		}
		sess.p.SetDebugger(sess.d)
	}
	return sess, nil
}

// justMyCode reports whether steps pass through the standard library.
func (sess *session) justMyCode() bool {
	return sess.args.JustMyCode == nil || *sess.args.JustMyCode
}

// entryOf is the program a project directory names: its nomi.toml's one
// entry point.
func entryOf(dir string) (string, error) {
	m, err := analysis.LoadManifest(dir)
	if err != nil || len(m.EntryPoints) == 0 {
		return "", fmt.Errorf("%s is a directory with no entry points; name the program's .nomi file", vmhost.DisplayPath(dir))
	}
	if len(m.EntryPoints) > 1 {
		return "", fmt.Errorf("%s has %d entry points (%s); name the one to debug",
			vmhost.DisplayPath(dir), len(m.EntryPoints), strings.Join(m.EntryPoints, ", "))
	}
	entry := filepath.Join(dir, filepath.FromSlash(m.EntryPoints[0]))
	if filepath.Ext(entry) != ".nomi" {
		if _, err := os.Stat(entry + ".nomi"); err == nil {
			entry += ".nomi"
		}
	}
	return entry, nil
}

// testAtLine is the error `nomi test --line` gives when line selects no test.
func testAtLine(path string, line int) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	selected, nearest, err := frontend.SourceSelectsTestLine(string(src), line)
	if err != nil || selected {
		return nil
	}
	msg := fmt.Sprintf("no test at %s:%d", vmhost.DisplayPath(path), line)
	if nearest > 0 {
		msg += fmt.Sprintf("; the nearest test starts on line %d", nearest)
	}
	return errors.New(msg)
}

// openConsole connects the program's input and output to a terminal the
// client opens, when the configuration and the client allow one.
func (sess *session) openConsole() {
	console := sess.args.Console
	if console == "" && !sess.test {
		console = "integratedTerminal"
	}
	if console != "integratedTerminal" && console != "externalTerminal" {
		return
	}
	sess.s.mu.Lock()
	supported := sess.s.client.SupportsRunInTerminalRequest
	sess.s.mu.Unlock()
	if !supported {
		if sess.args.Console != "" {
			sess.s.output("console", "This client cannot open a terminal, so the program's output goes to the debug console and it has no input.\n")
		}
		return
	}
	kind := "integrated"
	if console == "externalTerminal" {
		kind = "external"
	}
	conn, err := sess.s.openTerminal(kind, "nomi: "+filepath.Base(sess.path), filepath.Dir(sess.path))
	if err != nil {
		sess.s.output("console", fmt.Sprintf("Could not open a terminal (%v), so the program's output goes to the debug console and it has no input.\n", err))
		return
	}
	sess.io.terminal(conn)
}

// pump reports each stop the debugger makes as a stopped event.
func (sess *session) pump() {
	for {
		select {
		case st := <-sess.d.Events():
			sess.stopped(st)
		case <-sess.s.quit:
			return
		}
	}
}

// exceptionID is a stop's exception filter, or "" for a stop that is not
// an exception.
func exceptionID(r vm.StopReason) string {
	switch r {
	case vm.StopTrap:
		return "trap"
	case vm.StopAssertion:
		return "assertion"
	case vm.StopMainError:
		return "mainError"
	case vm.StopTaskFailure:
		return "taskFailure"
	case vm.StopDiverged:
		return "replayDiverged"
	}
	return ""
}

var exceptionDescriptions = map[vm.StopReason]string{
	vm.StopTrap:        "Trap",
	vm.StopAssertion:   "Failed assertion",
	vm.StopMainError:   "main returned Err",
	vm.StopTaskFailure: "Task failed",
	vm.StopDiverged:    "Replay diverged",
}

func (sess *session) stopped(st vm.Stop) {
	sess.restartMu.Lock()
	sess.restartMu.Unlock()
	body := godap.StoppedEventBody{ThreadId: st.Thread, AllThreadsStopped: true}
	note := ""
	sess.mu.Lock()
	if sess.killed {
		sess.mu.Unlock()
		return
	}
	switch st.Reason {
	case vm.StopBreakpoint:
		body.Reason = "breakpoint"
		for _, bp := range sess.bps[st.Pos.File()] {
			if bp.line == st.Pos.Line() && !st.NotLines {
				body.HitBreakpointIds = append(body.HitBreakpointIds, bp.id)
			}
		}
		for _, i := range st.Functions {
			if i < len(sess.fnBPs) {
				body.HitBreakpointIds = append(body.HitBreakpointIds, sess.fnBPs[i])
			}
		}
		if st.NotLines && len(st.Functions) > 0 {
			body.Reason = "function breakpoint"
		}
		if st.Text != "" {
			// A condition that could not be evaluated stops, saying why.
			body.Description, body.Text = "Breakpoint condition failed", st.Text
		}
	case vm.StopStep:
		body.Reason = "step"
		// A step into a target that the statement never entered says so.
		note = st.Text
	case vm.StopRestart:
		body.Reason = "restart"
	case vm.StopPause:
		body.Reason = "pause"
		if sess.entry {
			body.Reason, sess.entry = "entry", false
		}
	case vm.StopStart:
		// A reverse continue that found no breakpoint before it.
		body.Reason, body.Description = "entry", "Start of the program"
		body.Text = "No breakpoint stopped the program before this point, so going back stopped at its first statement."
	case vm.StopDiverged:
		body.Reason, body.Description, body.Text = "exception", exceptionDescriptions[st.Reason], st.Text
		sess.exceptions[st.Thread] = st
	default:
		body.Reason = "exception"
		body.Description = exceptionDescriptions[st.Reason]
		body.Text = st.Text
		sess.exceptions[st.Thread] = st
	}
	sess.mu.Unlock()
	if note != "" {
		sess.s.output("console", note+"\n")
	}
	sess.s.send(&godap.StoppedEvent{Event: event("stopped"), Body: body})
}

// start runs the program, once the client's configuration is done.
func (sess *session) start() {
	sess.mu.Lock()
	if sess.started {
		sess.mu.Unlock()
		return
	}
	sess.started = true
	if sess.args.StopOnEntry && sess.d != nil {
		sess.entry = true
		sess.d.Pause()
	}
	sess.mu.Unlock()
	go func() {
		for {
			var pass *vm.Pass
			if sess.d != nil {
				pass = sess.d.StartPass()
			}
			code := sess.run(pass)
			if pass != nil && pass.Abandoned() {
				// A step back or reverse continue abandoned this run of the
				// program and planned the replay that runs next.
				if sess.d.Planned() {
					continue
				}
				return
			}
			sess.finish(code)
			return
		}
	}()
}

// run runs the program as `nomi run` or `nomi test` would, and answers its
// exit status. On a debug machine it is one pass over the program
// (vm.Pass): what a replay of recorded history writes is dropped.
func (sess *session) run(pass *vm.Pass) int {
	out, errOut := sess.io.stdout(), sess.io.stderr()
	if pass != nil {
		out, errOut = pass.Gate(out), pass.Gate(errOut)
	}
	// The program's own stderr (`io.eprint`) is gated as its stdout is.
	sess.p.SetErrorOutput(errOut)
	if !sess.test {
		err := sess.p.Run(context.Background(), out, sess.args.Args, false)
		if err == nil {
			return 0
		}
		if blocked, ok := vmhost.IsBlocked(err); ok {
			blocked.Write(errOut, vmhost.DisplayPath(sess.path))
			return 1
		}
		vmhost.WriteFailure(errOut, err)
		return vmhost.ExitCode(err)
	}
	if restore, err := vmhost.DefaultTestEnv(); err == nil {
		defer restore()
	}
	format := vmhost.TestFormatText
	if sess.args.Format != "" {
		format, _ = vmhost.ParseTestFormat(sess.args.Format)
	}
	rep := vmhost.NewTestReportFormat(out, format)
	// A case's own output is in its record; in json mode the output stream
	// carries only records, so output from outside every case goes to the
	// error stream, as `nomi test --format json` sends it.
	caseOut := out
	if format == vmhost.TestFormatJSON {
		caseOut = errOut
	}
	// Each case's output reaches the console as the case writes it, so a
	// stopped case shows what it has printed so far, and the text report
	// does not print it again under the case's line. It goes to the
	// program's output in json mode too, where a test runner finds the
	// records among other lines; the records still carry it.
	sess.p.StreamTestOutput(out)
	opts := vmhost.TestOptions{}
	if sess.args.Line > 0 {
		opts = vmhost.TestOptions{Line: int(sess.args.Line), LineSet: true}
	}
	label := func(name string) string { return vmhost.TestName(sess.path, name) }
	sess.p.Test(caseOut, rep, sess.path, opts, label)
	if rep.Summary() {
		return 1
	}
	return 0
}

// finish reports the program's end.
func (sess *session) finish(code int) {
	sess.io.close()
	sess.mu.Lock()
	if sess.killed || sess.ended {
		sess.mu.Unlock()
		return
	}
	sess.ended = true
	sess.mu.Unlock()
	sess.s.send(&godap.ExitedEvent{Event: event("exited"), Body: godap.ExitedEventBody{ExitCode: code}})
	sess.s.send(&godap.TerminatedEvent{Event: event("terminated")})
}

// kill stops the program for good (vm.Debugger.Kill) and closes its terminal.
func (sess *session) kill() {
	sess.mu.Lock()
	if sess.killed {
		sess.mu.Unlock()
		return
	}
	sess.killed = true
	sess.mu.Unlock()
	if sess.d != nil {
		sess.d.Kill()
	}
	sess.io.close()
}

// terminate is the terminate request: the program is killed and the
// session ends.
func (sess *session) terminate() {
	sess.mu.Lock()
	ended := sess.ended
	sess.ended = true
	sess.mu.Unlock()
	sess.kill()
	if !ended {
		sess.s.send(&godap.TerminatedEvent{Event: event("terminated")})
	}
}
