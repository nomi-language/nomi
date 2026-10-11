package dap

import (
	godap "github.com/google/go-dap"
)

// stepInTargets answers a stepInTargets request: the calls the frame's
// statement makes that a step in could enter (internal/vm's
// debugtarget.go), each ranged over its call or lambda in the source.
func (sess *session) stepInTargets(r *godap.StepInTargetsRequest) {
	if sess.d == nil {
		sess.s.fail(&r.Request, "the program runs without debugging")
		return
	}
	sess.mu.Lock()
	kind, known := sess.frames[r.Arguments.FrameId]
	sess.mu.Unlock()
	if !known {
		sess.s.fail(&r.Request, "no frame %d; the program resumed", r.Arguments.FrameId)
		return
	}
	targets := []godap.StepInTarget{}
	if kind == frameNomi {
		found, err := sess.d.StepInTargets(r.Arguments.FrameId)
		if err != nil {
			sess.s.fail(&r.Request, "%v", err)
			return
		}
		for _, t := range found {
			st := godap.StepInTarget{Id: t.ID, Label: t.Label, Line: t.Pos.Line(), Column: t.Pos.Col()}
			if l, c, el, ec, ok := sess.p.SourceSpan(t.Pos.File(), t.Pos.Line(), t.Pos.Col()); ok {
				st.Line, st.Column, st.EndLine, st.EndColumn = l, c, el, ec
			}
			targets = append(targets, st)
		}
	}
	sess.s.send(&godap.StepInTargetsResponse{Response: response(&r.Request),
		Body: godap.StepInTargetsResponseBody{Targets: targets}})
}

// stepInto answers a stepIn request that names a target: the thread runs
// until it enters it (vm.Debugger.StepInto). The stop is reported after the
// response.
func (sess *session) stepInto(r *godap.StepInRequest) {
	if sess.d == nil {
		sess.s.fail(&r.Request, "the program runs without debugging")
		return
	}
	sess.restartMu.Lock()
	defer sess.restartMu.Unlock()
	if err := sess.d.StepInto(r.Arguments.ThreadId, r.Arguments.TargetId); err != nil {
		sess.s.fail(&r.Request, "Cannot step into that target: %v.", err)
		return
	}
	sess.mu.Lock()
	sess.refs, sess.localsRefs, sess.frames = nil, map[int]int{}, map[int]frameKind{}
	sess.mu.Unlock()
	sess.s.send(&godap.StepInResponse{Response: response(&r.Request)})
}
