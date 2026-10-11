package dap

import (
	"fmt"
	"strings"

	godap "github.com/google/go-dap"

	"github.com/nomi-language/nomi/internal/vm"
)

// setFunctionBreakpoints answers a setFunctionBreakpoints request: each name
// stops at the first statement of the functions it names, as
// vm.Debugger.SetFunctionBreakpoints spells them (internal/vm's
// debugfunc.go). A name that names nothing is reported unverified, with the
// closest names the program has.
func (sess *session) setFunctionBreakpoints(r *godap.SetFunctionBreakpointsRequest) {
	reqs := r.Arguments.Breakpoints
	out := make([]godap.Breakpoint, len(reqs))
	var specs []vm.FunctionBreakpointSpec
	var set []int
	for i, b := range reqs {
		name := strings.TrimSpace(b.Name)
		spec := vm.FunctionBreakpointSpec{Name: name}
		if b.Condition != "" || b.HitCondition != "" {
			var check *vm.Scope
			if sess.d != nil {
				if scope, found := sess.d.FunctionScope(name); found {
					check = &scope
				}
			}
			hit, err := sess.breakpointHit(check, godap.SourceBreakpoint{Condition: b.Condition, HitCondition: b.HitCondition})
			if err != nil {
				out[i].Message = err.Error()
				continue
			}
			spec.Hit = hit
		}
		set = append(set, i)
		specs = append(specs, spec)
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.fnBPs = nil
	if sess.d != nil {
		got := sess.d.SetFunctionBreakpoints(specs)
		for j, i := range set {
			sess.nextBP++
			out[i].Id = sess.nextBP
			sess.fnBPs = append(sess.fnBPs, out[i].Id)
			fb := got[j]
			if !fb.Verified {
				out[i].Message = unknownFunction(fb)
				continue
			}
			src, _ := source(fb.Pos.File())
			out[i].Verified, out[i].Source, out[i].Line = true, src, fb.Pos.Line()
			out[i].Message = stopsIn(fb.Functions)
		}
	}
	sess.s.send(&godap.SetFunctionBreakpointsResponse{Response: response(&r.Request),
		Body: godap.SetFunctionBreakpointsResponseBody{Breakpoints: out}})
}

// stopsIn is the message on a function breakpoint that names several
// functions: each instance of a generic function, or each impl of an
// interface method. Empty for one.
func stopsIn(functions []string) string {
	if len(functions) < 2 {
		return ""
	}
	var distinct []string
	seen := map[string]bool{}
	for _, f := range functions {
		if !seen[f] {
			seen[f] = true
			distinct = append(distinct, f)
		}
	}
	if len(distinct) == 1 {
		return fmt.Sprintf("stops in each of the %d instances of `%s`", len(functions), distinct[0])
	}
	return fmt.Sprintf("stops in %d functions: `%s`", len(functions), strings.Join(distinct, "`, `"))
}

// unknownFunction is the message on a function breakpoint whose name names
// no function.
func unknownFunction(fb vm.FunctionBreakpoint) string {
	text := fmt.Sprintf("no function named `%s` in this program", fb.Name)
	switch len(fb.Similar) {
	case 0:
	case 1:
		text += fmt.Sprintf("; did you mean `%s`?", fb.Similar[0])
	default:
		text += "; did you mean one of `" + strings.Join(fb.Similar, "`, `") + "`?"
	}
	return text
}
