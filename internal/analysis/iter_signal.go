package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// An iter-sensitive function is a top-level function whose own scope holds a
// `break` or a `continue` (spec "Named Functions as Callbacks"). Nomi has no
// non-local control flow: `return`, `break` and `continue` each leave only
// the function they are written in, and `break` and `continue` hand a signal
// to the iteration function that called it. So such a function may only be
// passed as an iteration callback itself (`Iter.map(xs, keep)`,
// `Iter.reduce(xs, keep, from: 0)`); any call of it is an error
// (iter_sensitive.go).

// IterSignals is which of `break` and `continue` a function's own scope
// uses.
type IterSignals struct {
	Break, Continue bool
}

// Any reports whether the scope uses either.
func (s IterSignals) Any() bool { return s.Break || s.Continue }

// Words names the keywords used, as an error message writes them:
// "`break`", "`continue`" or "`break` and `continue`".
func (s IterSignals) Words() string {
	var w []string
	if s.Break {
		w = append(w, "`break`")
	}
	if s.Continue {
		w = append(w, "`continue`")
	}
	return strings.Join(w, " and ")
}

// FuncIterSignals is the `break` and `continue` fd's own scope uses. The
// bodies of the functions it builds (a lambda, a capture, a nested `fn`)
// and a `concurrent` block are not its scope: a signal there is theirs, or
// an error.
func FuncIterSignals(fd *ast.FuncDef) IterSignals {
	if fd == nil || fd.Body == nil {
		return IterSignals{}
	}
	return OwnScopeSignals(fd.Body)
}

// IterSensitive reports whether fd uses `break` or `continue` in its own
// scope. The checker admits such a top-level function only as an iteration
// callback itself, and the IR builder lowers its body in the signalling form.
func IterSensitive(fd *ast.FuncDef) bool { return FuncIterSignals(fd).Any() }

// OwnScopeSignals is the `break` and `continue` n's own scope holds, as
// FuncIterSignals reads a function body.
func OwnScopeSignals(n ast.Node) IterSignals {
	var s IterSignals
	if n == nil {
		return s
	}
	ast.Inspect(n, func(m ast.Node) bool {
		switch m.(type) {
		case *ast.Break:
			s.Break = true
		case *ast.Continue:
			s.Continue = true
		case *ast.Lambda, *ast.FuncDef, *ast.Capture, *ast.ConcurrentBlock:
			return false
		}
		return true
	})
	return s
}

// IterSensitiveCallee is the declaration of the iter-sensitive function a
// callee or a reference names (`keep`, `other.keep`, a generic one's
// per-call-site reference), in this file or another, or nil.
func (fa *FileAnalysis) IterSensitiveCallee(fn ast.Node) *ast.FuncDef {
	fd := fa.topLevelFunc(fn)
	if fd == nil || !IterSensitive(fd) {
		return nil
	}
	return fd
}

// topLevelFunc is the declaration of the top-level function an Ident or a
// file-qualified FieldAccess names, or nil.
func (fa *FileAnalysis) topLevelFunc(fn ast.Node) *ast.FuncDef {
	if fa == nil {
		return nil
	}
	var pos Pos
	switch n := fn.(type) {
	case *ast.Ident:
		if n == nil {
			return nil
		}
		pos = Pos{Line: n.Line, Col: n.Col}
	case *ast.FieldAccess:
		if n == nil || n.Field == nil {
			return nil
		}
		pos = Pos{Line: n.Field.Line, Col: n.Field.Col}
	default:
		return nil
	}
	sym := resolvedSymbol(fa.References[pos])
	if sym == nil || sym.Kind != SymbolFunction || sym.OwningType != "" {
		return nil
	}
	fd, _ := sym.Node.(*ast.FuncDef)
	return fd
}

// breaksIteration reports whether a callback can end its iteration: a
// lambda or a capture with a `break` of its own, or an iter-sensitive
// function whose own scope breaks, passed as the callback itself. Any other
// function value cannot signal. LoopNeverBreaks asks it.
func (fa *FileAnalysis) breaksIteration(cb ast.Node) bool {
	switch t := cb.(type) {
	case *ast.Lambda:
		return t.Body != nil && OwnScopeSignals(t.Body).Break
	case *ast.Capture:
		return OwnScopeSignals(t.Body).Break
	}
	return FuncIterSignals(fa.IterSensitiveCallee(cb)).Break
}
