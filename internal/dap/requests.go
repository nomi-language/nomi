package dap

import (
	"errors"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	godap "github.com/google/go-dap"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/std"
)

// varRef is what a variables reference names: a frame's locals, or a value's
// children.
type varRef struct {
	frame  int
	locals bool
	// pipeline is the frame's pipeline view (vm.Debugger.Pipeline).
	pipeline bool
	// captured is 1 + the index of the frame's capture this scope shows
	// (vm.Debugger.Captured), or 0.
	captured int
	// lines, when set, are a text's lines, as children (captured.go).
	lines []string
	value rt.Inspected
}

// unpagedLimit is how many children a variables request that asks for no
// page answers; past it, one more entry says how many are left.
const unpagedLimit = 1000

func (sess *session) dispatch(msg godap.Message) {
	s := sess.s
	switch r := msg.(type) {
	case *godap.SetBreakpointsRequest:
		sess.setBreakpoints(r)
	case *godap.SetFunctionBreakpointsRequest:
		sess.setFunctionBreakpoints(r)
	case *godap.SetExceptionBreakpointsRequest:
		f := vm.ExceptionFilters{}
		for _, id := range r.Arguments.Filters {
			switch id {
			case "trap":
				f.Traps = true
			case "assertion":
				f.Assertions = true
			case "mainError":
				f.MainErrors = true
			case "taskFailure":
				f.TaskFailures = true
			}
		}
		if sess.d != nil {
			sess.d.SetExceptionFilters(f)
		}
		s.send(&godap.SetExceptionBreakpointsResponse{Response: response(&r.Request)})
	case *godap.ConfigurationDoneRequest:
		s.send(&godap.ConfigurationDoneResponse{Response: response(&r.Request)})
		sess.start()
	case *godap.ThreadsRequest:
		var threads []godap.Thread
		if sess.d != nil {
			for _, th := range sess.d.Threads() {
				threads = append(threads, godap.Thread{Id: th.ID, Name: th.Name})
			}
		}
		if len(threads) == 0 {
			// Nothing is running Nomi code (the program has not started, or
			// has ended); a client still needs a thread to show.
			threads = []godap.Thread{{Id: 1, Name: "main"}}
		}
		s.send(&godap.ThreadsResponse{Response: response(&r.Request), Body: godap.ThreadsResponseBody{Threads: threads}})
	case *godap.StackTraceRequest:
		sess.stackTrace(r)
	case *godap.ScopesRequest:
		sess.scopes(r)
	case *godap.VariablesRequest:
		sess.variables(r)
	case *godap.ContinueRequest:
		if !sess.resume(&r.Request) {
			return
		}
		s.send(&godap.ContinueResponse{Response: response(&r.Request), Body: godap.ContinueResponseBody{AllThreadsContinued: true}})
		sess.d.Continue()
	case *godap.NextRequest:
		if !sess.resume(&r.Request) {
			return
		}
		s.send(&godap.NextResponse{Response: response(&r.Request)})
		sess.d.StepOver(r.Arguments.ThreadId)
	case *godap.StepInTargetsRequest:
		sess.stepInTargets(r)
	case *godap.StepInRequest:
		if r.Arguments.TargetId != 0 {
			sess.stepInto(r)
			return
		}
		if !sess.resume(&r.Request) {
			return
		}
		s.send(&godap.StepInResponse{Response: response(&r.Request)})
		sess.d.StepIn(r.Arguments.ThreadId)
	case *godap.StepOutRequest:
		if !sess.resume(&r.Request) {
			return
		}
		s.send(&godap.StepOutResponse{Response: response(&r.Request)})
		sess.d.StepOut(r.Arguments.ThreadId)
	case *godap.RestartFrameRequest:
		if sess.d == nil {
			s.fail(&r.Request, "the program runs without debugging")
			return
		}
		// The output and the response go first: the restarted thread's stop
		// follows them.
		sess.restartMu.Lock()
		defer sess.restartMu.Unlock()
		name, err := sess.d.RestartFrame(r.Arguments.FrameId)
		if err != nil {
			s.fail(&r.Request, "Cannot restart this frame: %v.", err)
			return
		}
		sess.mu.Lock()
		sess.refs, sess.localsRefs, sess.frames = nil, map[int]int{}, map[int]frameKind{}
		sess.mu.Unlock()
		s.output("console", "Restarted "+name+" from its first statement with the same arguments. "+
			"What it already did outside the program, such as output, is not undone.\n")
		s.send(&godap.RestartFrameResponse{Response: response(&r.Request)})
	case *godap.StepBackRequest:
		sess.travel(&r.Request, r.Arguments.ThreadId, sess.d.StepBack, func() {
			s.send(&godap.StepBackResponse{Response: response(&r.Request)})
		})
	case *godap.ReverseContinueRequest:
		sess.travel(&r.Request, r.Arguments.ThreadId, sess.d.ReverseContinue, func() {
			s.send(&godap.ReverseContinueResponse{Response: response(&r.Request)})
		})
	case *godap.PauseRequest:
		if sess.d == nil {
			s.fail(&r.Request, "the program runs without debugging")
			return
		}
		s.send(&godap.PauseResponse{Response: response(&r.Request)})
		sess.d.Pause()
	case *godap.ExceptionInfoRequest:
		sess.mu.Lock()
		st, ok := sess.exceptions[r.Arguments.ThreadId]
		sess.mu.Unlock()
		if !ok {
			s.fail(&r.Request, "thread %d stopped for no exception", r.Arguments.ThreadId)
			return
		}
		s.send(&godap.ExceptionInfoResponse{Response: response(&r.Request), Body: godap.ExceptionInfoResponseBody{
			ExceptionId: exceptionID(st.Reason), Description: st.Text, BreakMode: "always",
			Details: &godap.ExceptionDetails{Message: st.Text, TypeName: exceptionDescriptions[st.Reason]},
		}})
	case *godap.EvaluateRequest:
		sess.evaluate(r)
	case *locationsRequest:
		sess.locations(r)
	default:
		req := requestOf(msg)
		s.fail(req, "%s is not supported", req.Command)
	}
}

// travel answers a stepBack or reverseContinue request: go, which replays
// the program from its start to the stop it finds (vm.Debugger.StepBack,
// ReverseContinue), then respond. The stop is reported after the response.
func (sess *session) travel(req *godap.Request, thread int, goBack func(int) error, respond func()) {
	if sess.d == nil {
		sess.s.fail(req, "the program runs without debugging")
		return
	}
	sess.restartMu.Lock()
	defer sess.restartMu.Unlock()
	if err := goBack(thread); err != nil {
		text := err.Error()
		if errors.Is(err, vm.ErrNoHistory) {
			text = "No more history: this is the first statement the program ran."
		} else if !strings.HasPrefix(text, "Step back is off") {
			text = "Cannot go back: " + text + "."
		}
		sess.s.fail(req, "%s", text)
		return
	}
	sess.mu.Lock()
	sess.refs, sess.localsRefs, sess.frames = nil, map[int]int{}, map[int]frameKind{}
	first := !sess.travelled
	sess.travelled = true
	sess.mu.Unlock()
	if first {
		sess.s.output("console", "Going back runs the program again from its start, answering input, the clock, "+
			"randomness and files from what this session recorded; nothing is printed or read again until it "+
			"passes the point it had reached.\n")
	}
	respond()
}

// resume forgets the frames and variables handed out since the last stop,
// which a resume invalidates, and reports whether the program can resume.
func (sess *session) resume(req *godap.Request) bool {
	if sess.d == nil {
		sess.s.fail(req, "the program runs without debugging")
		return false
	}
	sess.mu.Lock()
	sess.refs, sess.localsRefs, sess.frames = nil, map[int]int{}, map[int]frameKind{}
	sess.mu.Unlock()
	return true
}

func (sess *session) setBreakpoints(r *godap.SetBreakpointsRequest) {
	a := r.Arguments
	reqs := a.Breakpoints
	if len(reqs) == 0 {
		for _, l := range a.Lines {
			reqs = append(reqs, godap.SourceBreakpoint{Line: l})
		}
	}
	file := std.PositionPath(clientPath(a.Source.Path))
	out := make([]godap.Breakpoint, len(reqs))
	var specs []vm.BreakpointSpec
	var plain []int
	for i, b := range reqs {
		out[i] = godap.Breakpoint{Line: b.Line, Source: &a.Source}
		spec := vm.BreakpointSpec{Line: b.Line}
		if b.Condition != "" || b.HitCondition != "" || b.LogMessage != "" {
			var check *vm.Scope
			if sess.d != nil {
				if scope, landed := sess.d.LineScope(file, b.Line); landed {
					check = &scope
				}
			}
			hit, err := sess.breakpointHit(check, b)
			if err != nil {
				out[i].Message = err.Error()
				continue
			}
			spec.Hit = hit
		}
		plain = append(plain, i)
		specs = append(specs, spec)
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	for landed := range sess.bps {
		if landed == file || sameFile(landed, file) {
			delete(sess.bps, landed)
		}
	}
	if sess.d != nil {
		got := sess.d.SetBreakpointSpecs(file, specs)
		for j, i := range plain {
			sess.nextBP++
			out[i].Id = sess.nextBP
			if !got[j].Verified {
				out[i].Message = "no statement on or after this line"
				continue
			}
			out[i].Verified, out[i].Line = true, got[j].Resolved
			landed := sess.landedFile(file)
			sess.bps[landed] = append(sess.bps[landed], breakpoint{id: out[i].Id, line: got[j].Resolved})
		}
	}
	sess.s.send(&godap.SetBreakpointsResponse{Response: response(&r.Request),
		Body: godap.SetBreakpointsResponseBody{Breakpoints: out}})
}

// landedFile is the file positions name a client's file by: the program's
// own files are absolute, as the client names them.
func (sess *session) landedFile(file string) string { return filepath.Clean(file) }

// sameFile reports whether two absolute paths name the same file.
func sameFile(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

// clientPath is a path a client sent, spelled as the program's files are:
// on Windows a client may write the drive in lower case (VS Code writes
// `c:\`), so the drive letter is upper-cased there.
func clientPath(p string) string {
	if runtime.GOOS == "windows" && len(p) >= 2 && p[1] == ':' {
		return strings.ToUpper(p[:1]) + p[1:]
	}
	return p
}

// source is the DAP source of a position's file, and whether it is the
// standard library's.
func source(file string) (*godap.Source, bool) {
	isStd := std.IsSourcePath(file)
	path := file
	if isStd {
		path = std.EditorPath(file)
	} else if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	src := &godap.Source{Name: filepath.Base(path), Path: path}
	if isStd {
		src.PresentationHint = "deemphasize"
	}
	return src, isStd
}

func (sess *session) stackTrace(r *godap.StackTraceRequest) {
	if sess.d == nil {
		sess.s.fail(&r.Request, "the program runs without debugging")
		return
	}
	frames, err := sess.d.Stack(r.Arguments.ThreadId)
	if err != nil {
		sess.s.fail(&r.Request, "thread %d is running", r.Arguments.ThreadId)
		return
	}
	sess.mu.Lock()
	var out []godap.StackFrame
	for _, f := range frames {
		sf := godap.StackFrame{Id: f.ID, Name: f.Name, Line: f.Pos.Line(), Column: max(f.Pos.Col(), 1)}
		src, isStd := source(f.Pos.File())
		if isStd && !sess.justMyCode() {
			// A client focuses the first frame whose source is not
			// deemphasized; stepping into std, the user is looking there.
			src.PresentationHint = ""
		}
		sf.Source = src
		kind := frameNomi
		if f.Via {
			kind = frameVia
		}
		if f.Via || isStd {
			sf.PresentationHint = "subtle"
		}
		sess.frames[f.ID] = kind
		out = append(out, sf)
		if f.Tail {
			// The tail call replaced its caller, which the stack no longer
			// has (decision 2 of the debugger plan).
			sess.labels++
			id := labelBase + sess.labels
			sess.frames[id] = frameLabel
			out = append(out, godap.StackFrame{Id: id, Name: "(tail call)", PresentationHint: "label"})
		}
	}
	sess.mu.Unlock()
	total := len(out)
	start := min(r.Arguments.StartFrame, total)
	end := total
	if r.Arguments.Levels > 0 {
		end = min(total, start+r.Arguments.Levels)
	}
	sess.s.send(&godap.StackTraceResponse{Response: response(&r.Request),
		Body: godap.StackTraceResponseBody{StackFrames: append([]godap.StackFrame{}, out[start:end]...), TotalFrames: total}})
}

func (sess *session) scopes(r *godap.ScopesRequest) {
	frameID := r.Arguments.FrameId
	sess.mu.Lock()
	kind, known := sess.frames[frameID]
	sess.mu.Unlock()
	if !known {
		sess.s.fail(&r.Request, "no frame %d; the program resumed", frameID)
		return
	}
	var pipeline []vm.Variable
	var captured []vm.Captured
	if kind == frameNomi && sess.d != nil {
		pipeline, _ = sess.d.Pipeline(frameID)
		captured, _ = sess.d.Captured(frameID)
	}
	sess.mu.Lock()
	var scopes []godap.Scope
	if kind == frameNomi {
		ref, has := sess.localsRefs[frameID]
		if !has {
			sess.refs = append(sess.refs, varRef{frame: frameID, locals: true})
			ref = len(sess.refs)
			sess.localsRefs[frameID] = ref
		}
		scopes = append(scopes, godap.Scope{Name: "Locals", PresentationHint: "locals", VariablesReference: ref})
		if len(pipeline) > 0 {
			// The pipeline the frame is in, or finished last: each stage's
			// value, as `dbg` after each stage would print it.
			sess.refs = append(sess.refs, varRef{frame: frameID, pipeline: true})
			scopes = append(scopes, godap.Scope{Name: "Pipeline", VariablesReference: len(sess.refs),
				NamedVariables: len(pipeline)})
		}
		scopes = append(scopes, sess.capturedScopes(frameID, captured)...)
	}
	sess.mu.Unlock()
	sess.s.send(&godap.ScopesResponse{Response: response(&r.Request), Body: godap.ScopesResponseBody{Scopes: append([]godap.Scope{}, scopes...)}})
}

// indexed reports whether a value's children are elements named by index,
// which a client pages: a List's, a Vector's or a Set's.
func indexed(v rt.Inspected) bool {
	switch v.Type() {
	case "List", "Vector", "Set":
		return true
	}
	return false
}

// variable is one entry of the variables tree, with a reference to its
// children when it has any.
func (sess *session) variable(name, typ string, v rt.Inspected) godap.Variable {
	out := godap.Variable{Name: name, Value: v.Text(), Type: typ}
	if n := v.NumChildren(); n > 0 {
		sess.refs = append(sess.refs, varRef{value: v})
		out.VariablesReference = len(sess.refs)
		if indexed(v) {
			out.IndexedVariables = n
		} else {
			out.NamedVariables = n
		}
	}
	return out
}

func (sess *session) variables(r *godap.VariablesRequest) {
	a := r.Arguments
	sess.mu.Lock()
	if a.VariablesReference < 1 || a.VariablesReference > len(sess.refs) {
		sess.mu.Unlock()
		sess.s.fail(&r.Request, "no variables %d; the program resumed", a.VariablesReference)
		return
	}
	ref := sess.refs[a.VariablesReference-1]
	sess.mu.Unlock()
	if ref.captured > 0 || ref.lines != nil {
		sess.capturedVariables(r, ref)
		return
	}
	var vars []vm.Variable
	if ref.locals || ref.pipeline {
		var err error
		if ref.locals {
			vars, err = sess.d.Locals(ref.frame)
		} else {
			vars, err = sess.d.Pipeline(ref.frame)
		}
		if err != nil {
			sess.s.fail(&r.Request, "%v", err)
			return
		}
	}
	sess.mu.Lock()
	out := []variable{}
	if ref.locals || ref.pipeline {
		for _, v := range vars {
			gv := sess.variable(v.Name, v.Type, v.Value)
			if v.Text != "" {
				gv.Value = v.Text
			}
			out = append(out, variable{Variable: gv, DeclarationLocationReference: sess.locationRef(v.Decl)})
		}
	} else {
		isIndexed := indexed(ref.value)
		if (a.Filter == "indexed" && !isIndexed) || (a.Filter == "named" && isIndexed) {
			a.Count, a.Start = 0, ref.value.NumChildren()
		}
		total := ref.value.NumChildren()
		count := a.Count
		capped := false
		if count <= 0 {
			count = total - a.Start
			if count > unpagedLimit {
				count, capped = unpagedLimit, true
			}
		}
		for _, c := range ref.value.Children(a.Start, count) {
			out = append(out, variable{Variable: sess.variable(c.Name, c.Value.Type(), c.Value)})
		}
		if capped {
			out = append(out, variable{Variable: godap.Variable{Name: "…",
				Value: strconv.Itoa(total-a.Start-count) + " more; a client that pages variables shows them"}})
		}
	}
	sess.mu.Unlock()
	sess.sendVariables(r, out)
}
