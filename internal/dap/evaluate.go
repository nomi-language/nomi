package dap

// Evaluation: watches, hovers, the debug console, and the conditions,
// hit counts and log messages of breakpoints, all evaluated in a stopped
// frame through vmhost.Program.CompileExpr and vm.Scope.Run.
//
// The rules, the same for every client:
//
//   - An expression that can reach an effect (output, files, the clock,
//     tasks) is refused before it runs, unless it ends with the marker
//     `// allow effects`, a Nomi comment, so the line stays valid Nomi. A
//     hover never runs one.
//   - Every evaluation runs under a step budget and a deadline, so one that
//     loops forever is stopped and reported.
//   - A variable, or a field path on one (`p.x`, `pair.0`), is read from the
//     frame directly, with nothing compiled.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	godap "github.com/google/go-dap"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

// allowEffectsMarker, ending an expression, lets it reach effects.
const allowEffectsMarker = "// allow effects"

// evalSteps and evalTime bound one evaluation.
const (
	evalSteps = 1_000_000
	evalTime  = 2 * time.Second
)

func evalLimits() vmhost.EvalLimits {
	return vmhost.EvalLimits{Steps: evalSteps, Deadline: time.Now().Add(evalTime)}
}

// compileCache holds compiled expressions by file, scope and text, so a
// condition evaluated at every pass of a loop compiles once.
type compileCache struct {
	mu    sync.Mutex
	exprs map[string]*compiled
}

type compiled struct {
	once sync.Once
	expr *vmhost.DebugExpr
	err  error
}

// compile is expr compiled for a frame of file whose variables are vars.
func (sess *session) compile(file string, vars []vm.ScopeVar, expr string) (*vmhost.DebugExpr, error) {
	params := make([]vmhost.DebugParam, len(vars))
	var key strings.Builder
	key.WriteString(file + "\x00" + expr)
	for i, v := range vars {
		params[i] = vmhost.DebugParam{Name: v.Name, Type: v.Type}
		key.WriteString("\x00" + v.Name + ":" + v.Type)
	}
	c := &sess.cache
	c.mu.Lock()
	if c.exprs == nil {
		c.exprs = map[string]*compiled{}
	}
	entry, ok := c.exprs[key.String()]
	if !ok {
		entry = &compiled{}
		c.exprs[key.String()] = entry
	}
	c.mu.Unlock()
	entry.once.Do(func() { entry.expr, entry.err = sess.p.CompileExpr(file, params, expr) })
	return entry.expr, entry.err
}

// evalIn evaluates expr in scope, answering its value and type.
func (sess *session) evalIn(scope *vm.Scope, expr string, allowEffects bool) (any, string, error) {
	if v, typ, ok := readPath(scope, expr); ok {
		return v, typ, nil
	}
	e, err := sess.compile(scope.Pos.File(), scope.Vars, expr)
	if err != nil {
		return nil, "", err
	}
	v, err := e.Run(scope, evalLimits(), allowEffects)
	if err != nil {
		return nil, "", err
	}
	return v, e.Type, nil
}

// readPath reads a variable, or a field path on one, from scope.
func readPath(scope *vm.Scope, expr string) (any, string, bool) {
	parts := strings.Split(strings.TrimSpace(expr), ".")
	for _, p := range parts {
		if !isName(p) && !isIndex(p) {
			return nil, "", false
		}
	}
	if !isName(parts[0]) {
		return nil, "", false
	}
	v, ok := scope.Var(parts[0])
	if !ok {
		return nil, "", false
	}
	if len(parts) == 1 {
		return v.Value, v.Type, true
	}
	node := rt.Inspect(v.Value)
	for _, p := range parts[1:] {
		found := false
		for _, c := range node.Children(0, node.NumChildren()) {
			if c.Name == p {
				node, found = c.Value, true
				break
			}
		}
		if !found {
			return nil, "", false
		}
	}
	// A field's type is not recorded: the caller names it by its value.
	return node.Value(), "", true
}

func isName(s string) bool {
	if s == "" || !(s[0] == '_' || (s[0] >= 'a' && s[0] <= 'z')) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			(c == '?' && i == len(s)-1)
		if !ok {
			return false
		}
	}
	return true
}

func isIndex(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil && s != "" && s[0] != '-' && s[0] != '+'
}

// evalError words an evaluation's failure for the client.
func evalError(err error) string {
	var effects *vm.EffectsError
	var exprErr *vmhost.ExprError
	var failure *vmhost.EvalFailure
	switch {
	case errors.As(err, &effects):
		var what []string
		for _, e := range effects.Effects {
			what = append(what, strings.ReplaceAll(e, "nomi_debug_eval", "the expression"))
		}
		return "not run, because " + strings.Join(what, "; ") +
			", which is an effect. End the expression with `" + allowEffectsMarker +
			"` to run it anyway; what it does is not undone."
	case errors.Is(err, vmhost.ErrEvalLimit):
		return fmt.Sprintf("stopped after %d steps or %s without finishing; does the expression loop forever?",
			evalSteps, evalTime)
	case errors.As(err, &exprErr):
		return strings.Join(exprErr.Messages, "\n")
	case errors.As(err, &failure):
		return "the expression failed: " + failure.Text
	}
	return strings.TrimRight(err.Error(), "\n")
}

// splitEffectsMarker is expr without a trailing allow-effects marker, and
// whether it had one.
func splitEffectsMarker(expr string) (string, bool) {
	trimmed := strings.TrimRight(expr, " \t\r\n")
	if rest, found := strings.CutSuffix(trimmed, allowEffectsMarker); found {
		return strings.TrimRight(rest, " \t"), true
	}
	return expr, false
}

func (sess *session) evaluate(r *godap.EvaluateRequest) {
	a := r.Arguments
	if sess.d == nil {
		sess.s.fail(&r.Request, "the program runs without debugging")
		return
	}
	if a.FrameId == 0 {
		sess.s.fail(&r.Request, "the program is running; pause it to evaluate an expression")
		return
	}
	sess.mu.Lock()
	kind, known := sess.frames[a.FrameId]
	sess.mu.Unlock()
	if !known || kind == frameLabel {
		sess.s.fail(&r.Request, "no frame %d to evaluate in; the program resumed", a.FrameId)
		return
	}
	expr, allow := splitEffectsMarker(a.Expression)
	if a.Context == "hover" {
		allow = false
	}
	scope, err := sess.d.Scope(a.FrameId)
	if err != nil {
		sess.s.fail(&r.Request, "%v", err)
		return
	}
	v, typ, err := sess.evalIn(scope, expr, allow)
	if err != nil {
		sess.s.fail(&r.Request, "%s", evalError(err))
		return
	}
	in := rt.Inspect(v)
	if typ == "" {
		typ = in.Type()
	}
	sess.mu.Lock()
	out := sess.variable("", typ, in)
	sess.mu.Unlock()
	body := godap.EvaluateResponseBody{Result: out.Value, Type: typ, VariablesReference: out.VariablesReference,
		NamedVariables: out.NamedVariables, IndexedVariables: out.IndexedVariables}
	if a.Context == "clipboard" {
		body.Result = rt.RowText(v)
	}
	sess.s.send(&godap.EvaluateResponse{Response: response(&r.Request), Body: body})
}
