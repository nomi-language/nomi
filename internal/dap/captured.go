package dap

import (
	"strconv"
	"strings"

	godap "github.com/google/go-dap"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// The "Captured output" scopes: one for each `io.capture` or `io.replay`
// call in force at a stopped frame, innermost first (vm.Debugger.Captured).
// Output written inside such a call goes into it, not to the console, so
// this is the only place a stopped program's captured output shows.
//
// A scope holds `output`, everything written so far, and `transcript`, the
// output with each line read written in. For `io.replay` the transcript is
// in the form the replay compares (rt.ReplayText), and the scope also holds
// the script's input lines read so far and still to come, and `diverged`
// when the transcript already differs from the script's. A text's value is
// the whole text as a String shows, which a client copies, and its children
// are its lines, which a client pages.

// capturedScopes are the frame's captured-output scopes. sess.mu is held.
func (sess *session) capturedScopes(frameID int, captured []vm.Captured) []godap.Scope {
	var out []godap.Scope
	for i, c := range captured {
		sess.refs = append(sess.refs, varRef{frame: frameID, captured: i + 1})
		n := 2
		if c.Replay {
			n = 4
			if c.Diverged != "" {
				n++
			}
		}
		out = append(out, godap.Scope{Name: "Captured output (" + c.Label + ")",
			VariablesReference: len(sess.refs), NamedVariables: n})
	}
	return out
}

// capturedVariables answers a variables request for a captured-output scope
// or a text's lines.
func (sess *session) capturedVariables(r *godap.VariablesRequest, ref varRef) {
	out := []variable{}
	if ref.lines != nil {
		a := r.Arguments
		total := len(ref.lines)
		start, count := min(max(a.Start, 0), total), a.Count
		if a.Filter == "named" {
			count, start = 0, total
		}
		capped := false
		if count <= 0 {
			count = total - start
			if count > unpagedLimit {
				count, capped = unpagedLimit, true
			}
		}
		end := min(total, start+count)
		for i := start; i < end; i++ {
			out = append(out, variable{Variable: godap.Variable{Name: "[" + strconv.Itoa(i) + "]", Type: "String",
				Value: rt.InspectString(ref.lines[i])}})
		}
		if capped {
			out = append(out, variable{Variable: godap.Variable{Name: "…",
				Value: strconv.Itoa(total-end) + " more; a client that pages variables shows them"}})
		}
		sess.sendVariables(r, out)
		return
	}
	captured, err := sess.d.Captured(ref.frame)
	if err != nil {
		sess.s.fail(&r.Request, "%v", err)
		return
	}
	if ref.captured > len(captured) {
		sess.s.fail(&r.Request, "no capture %d at frame %d", ref.captured, ref.frame)
		return
	}
	c := captured[ref.captured-1]
	sess.mu.Lock()
	// Each value's declaration is the `io.capture` or `io.replay` call
	// that holds it.
	site := sess.locationRef(c.Site)
	add := func(v godap.Variable) {
		out = append(out, variable{Variable: v, DeclarationLocationReference: site})
	}
	add(sess.textVariable("output", c.Output))
	add(sess.textVariable("transcript", c.Transcript))
	if c.Replay {
		add(sess.listVariable("input read", c.Read))
		add(sess.listVariable("input to come", c.Unread))
		if c.Diverged != "" {
			add(godap.Variable{Name: "diverged", Type: "String", Value: c.Diverged})
		}
	}
	sess.mu.Unlock()
	sess.sendVariables(r, out)
}

// textVariable is a text as one String whose children are its lines.
// sess.mu is held.
func (sess *session) textVariable(name, text string) godap.Variable {
	var lines []string
	if text != "" {
		lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	v := sess.linesVariable(name, lines)
	v.Type, v.Value = "String", rt.InspectString(text)
	return v
}

// listVariable is a List of lines. sess.mu is held.
func (sess *session) listVariable(name string, lines []string) godap.Variable {
	v := sess.linesVariable(name, lines)
	quoted := make([]string, len(lines))
	for i, l := range lines {
		quoted[i] = rt.InspectString(l)
	}
	v.Type, v.Value = "List<String>", "["+strings.Join(quoted, ", ")+"]"
	return v
}

func (sess *session) linesVariable(name string, lines []string) godap.Variable {
	v := godap.Variable{Name: name}
	if len(lines) > 0 {
		sess.refs = append(sess.refs, varRef{lines: lines})
		v.VariablesReference, v.IndexedVariables = len(sess.refs), len(lines)
	}
	return v
}
