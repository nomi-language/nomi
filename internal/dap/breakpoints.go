package dap

// Conditional breakpoints, hit counts and logpoints. Each is a decision the
// stopping thread makes when it reaches the breakpoint (vm.BreakpointSpec's
// Hit), in this order, as VS Code's own debuggers make it:
//
//  1. The condition, a Bool expression evaluated in the frame. False passes
//     the breakpoint by. One that cannot be evaluated (a trap, an effect,
//     the step budget) stops, saying why.
//  2. The hit count, which counts the passes whose condition held: `N` or
//     `== N` stops at the Nth, `>= N` (or `> N`, `<= N`, `< N`) at every
//     pass the comparison holds for, `% N` at every Nth.
//  3. The log message: a logpoint writes it to the debug console and does
//     not stop. `{expr}` in it is an expression's value, rendered as the
//     variables pane renders it.
//
// A condition or a message expression that does not compile against the
// variables where the breakpoint lands is reported on the breakpoint, which
// is then not set.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	godap "github.com/google/go-dap"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// breakpointHit is the decision a breakpoint with a condition, a hit count
// or a log message makes at each pass. Its expressions are checked against
// check, the variables where it lands, when it is known.
func (sess *session) breakpointHit(check *vm.Scope, b godap.SourceBreakpoint) (func(*vm.Scope) vm.Hit, error) {
	cond := strings.TrimSpace(b.Condition)
	count, err := parseHitCondition(b.HitCondition)
	if err != nil {
		return nil, err
	}
	var parts []logPart
	if b.LogMessage != "" {
		if parts, err = parseLogMessage(b.LogMessage); err != nil {
			return nil, err
		}
	}
	if scope := check; scope != nil {
		if cond != "" {
			e, err := sess.compile(scope.Pos.File(), scope.Vars, cond)
			if err != nil {
				return nil, fmt.Errorf("the condition does not compile: %s", evalError(err))
			}
			if e.Type != "Bool" {
				return nil, fmt.Errorf("the condition is %s %s, not a Bool", article(e.Type), e.Type)
			}
		}
		for _, p := range parts {
			if p.expr == "" {
				continue
			}
			if _, err := sess.compile(scope.Pos.File(), scope.Vars, p.expr); err != nil {
				return nil, fmt.Errorf("{%s} in the log message does not compile: %s", p.expr, evalError(err))
			}
		}
	}
	var mu sync.Mutex
	hits := 0
	var pass uint64
	line := b.Line
	return func(scope *vm.Scope) vm.Hit {
		if cond != "" {
			v, _, err := sess.evalIn(scope, cond, false)
			if err != nil {
				text := fmt.Sprintf("The breakpoint's condition `%s` could not be evaluated: %s", cond, evalError(err))
				if !scope.Replaying {
					sess.s.output("console", text+"\n")
				}
				return vm.Hit{Stop: true, Text: text}
			}
			if holds, isBool := v.(bool); !isBool || !holds {
				return vm.Hit{}
			}
		}
		mu.Lock()
		if scope.Pass != pass {
			// A step back runs the program again from its start, where
			// the passes are counted again (vm's debugtravel.go).
			pass, hits = scope.Pass, 0
		}
		hits++
		n := hits
		mu.Unlock()
		if count != nil && !count(n) {
			return vm.Hit{}
		}
		if parts != nil {
			if !scope.Replaying {
				// Recorded history logged this pass when it ran.
				sess.logpoint(scope, parts, line)
			}
			return vm.Hit{}
		}
		return vm.Hit{Stop: true}
	}, nil
}

// parseHitCondition is a hit count's test of the pass number, nil for no
// hit count.
func parseHitCondition(text string) (func(int) bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	op := ""
	for _, o := range []string{">=", "<=", "==", ">", "<", "%"} {
		if rest, found := strings.CutPrefix(text, o); found {
			op, text = o, strings.TrimSpace(rest)
			break
		}
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 || (op == "%" && n == 0) {
		return nil, errors.New("the hit count is not understood: write N, == N, >= N, > N, <= N, < N or % N")
	}
	switch op {
	case ">=":
		return func(i int) bool { return i >= n }, nil
	case ">":
		return func(i int) bool { return i > n }, nil
	case "<=":
		return func(i int) bool { return i <= n }, nil
	case "<":
		return func(i int) bool { return i < n }, nil
	case "%":
		return func(i int) bool { return i%n == 0 }, nil
	}
	return func(i int) bool { return i == n }, nil
}

// logPart is a piece of a log message: text, or an expression in braces.
type logPart struct {
	text, expr string
}

// parseLogMessage splits a log message into text and `{expr}` parts.
// `{{` and `}}` are a literal brace.
func parseLogMessage(msg string) ([]logPart, error) {
	var parts []logPart
	var text strings.Builder
	for i := 0; i < len(msg); i++ {
		c := msg[i]
		switch {
		case c == '{' && i+1 < len(msg) && msg[i+1] == '{', c == '}' && i+1 < len(msg) && msg[i+1] == '}':
			text.WriteByte(c)
			i++
		case c == '{':
			depth, j := 1, i+1
			for ; j < len(msg) && depth > 0; j++ {
				switch msg[j] {
				case '{':
					depth++
				case '}':
					depth--
				}
			}
			if depth != 0 {
				return nil, errors.New("the log message has a `{` with no closing `}`; write `{{` for a brace")
			}
			expr := strings.TrimSpace(msg[i+1 : j-1])
			if expr == "" {
				return nil, errors.New("the log message has an empty `{}`")
			}
			if text.Len() > 0 {
				parts = append(parts, logPart{text: text.String()})
				text.Reset()
			}
			parts = append(parts, logPart{expr: expr})
			i = j - 1
		default:
			text.WriteByte(c)
		}
	}
	if text.Len() > 0 || len(parts) == 0 {
		parts = append(parts, logPart{text: text.String()})
	}
	return parts, nil
}

// logpoint writes a logpoint's message, its expressions evaluated in scope,
// to the debug console.
func (sess *session) logpoint(scope *vm.Scope, parts []logPart, line int) {
	var b strings.Builder
	for _, p := range parts {
		if p.expr == "" {
			b.WriteString(p.text)
			continue
		}
		v, _, err := sess.evalIn(scope, p.expr, false)
		if err != nil {
			b.WriteString("<" + evalError(err) + ">")
			continue
		}
		if s, isString := v.(string); isString {
			// A String is its text, as a log line shows it.
			b.WriteString(s)
			continue
		}
		b.WriteString(rt.Inspect(v).Text())
	}
	b.WriteByte('\n')
	src, _ := source(scope.Pos.File())
	sess.s.send(&godap.OutputEvent{Event: event("output"), Body: godap.OutputEventBody{
		Category: "console", Output: b.String(), Source: src, Line: line}})
}

// article is "an" before a word that starts with a vowel, "a" otherwise.
func article(word string) string {
	if word != "" && strings.ContainsRune("AEIOUaeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}
