package vmhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// BACKTICK TYPED LITERALS ARE CHECKED AT COMPILE TIME.
//
// A backtick typed literal whose handler can fail (`Regex`\d+``, whose
// `from_fragments` returns `Result<Regex, String>`) has the handler's
// success type, so it needs no `try`. Its body never interpolates, so the
// handler's input is fixed text, and every load runs each such literal's
// handler before anything else runs: `nomi check`, `nomi run`, `nomi test`,
// the REPL, the tour and the language server's lowering all go through
// lower or loadStdlib. An Err is a compile error at the literal carrying the
// handler's message; a handler that reaches an effect, or does not finish
// within literalSteps, is a compile error saying so, with the double-quoted
// spelling as the way out. The evaluation is vm.Machine.Evaluate, the one the
// language server's hovers use, refused as it refuses an editor's
// evaluation (vm.Machine.Effects).
//
// The run calls the handler again, once per literal site (irbuild's
// irliteralcell.go): the cell's first read runs it, and the run finds the Ok
// this check found, since the handler is pure and its input fixed.

const (
	// literalSteps bounds one literal's handler: calls plus backward
	// branches. A Regex or Date literal takes a few hundred.
	literalSteps = 1_000_000
	// literalTime is one literal's wall-clock backstop.
	literalTime = 10 * time.Second
)

// InvalidLiteralCode is the Code of a backtick typed literal's diagnostic.
const InvalidLiteralCode = "invalid-literal"

// literalHint closes a literal diagnostic the handler could not be run for.
const literalHint = "a backtick typed literal is checked at compile time; to check it when the program runs instead, write it with double quotes and handle its Result with `try` or `case`"

// checkLiterals runs the handler of every backtick typed literal the lowering
// checks at compile time, and answers the diagnostics of the ones that fail,
// in source order, or nil.
func (p *Program) checkLiterals() error {
	if p.res == nil || len(p.res.Literals) == 0 {
		return nil
	}
	m := p.machine(io.Discard)
	var ds frontend.Diagnostics
	for _, site := range p.res.Literals {
		d, failed, err := p.checkLiteral(m, site)
		if err != nil {
			return err
		}
		if failed {
			ds = append(ds, d)
		}
	}
	if len(ds) == 0 {
		return nil
	}
	return ds
}

// checkLiteral runs one literal's handler and answers its diagnostic when the
// literal is a compile error. err is an *InternalError, for a compiler panic
// while the VM compiled the handler.
func (p *Program) checkLiteral(m *vm.Machine, site irbuild.LiteralSite) (d frontend.Diagnostic, failed bool, err error) {
	f := site.Cell.Initializer()
	if f == nil {
		return d, false, nil
	}
	if len(m.Unretained([]*ir.Func{f}, nil)) > 0 {
		// A handler the compiler cannot lower is reported where the
		// program reaches it, as every such gap is (Unsupported).
		return d, false, nil
	}
	fail := func(msg string, hints ...string) (frontend.Diagnostic, bool, error) {
		path := site.Path
		if path == "" {
			path = p.moduleWithNodeAt(site.Line, site.Col)
		}
		d := frontend.NewDiagnostic(path, p.sources[path], site.Line, site.Col, msg)
		if site.EndLine > 0 {
			d.EndLine, d.EndCol = site.EndLine, site.EndCol
		}
		d.Hints = append(d.Hints, hints...)
		d.Code = InvalidLiteralCode
		return d, true, nil
	}
	label := literalLabel(site.Text)
	if effects := m.Effects(f); len(effects) > 0 {
		return fail(fmt.Sprintf("%s cannot be checked at compile time: its handler has effects (%s)", label, strings.Join(effects, "; ")), literalHint)
	}
	lim := vm.Limits{Steps: literalSteps, Deadline: time.Now().Add(literalTime)}
	v, err := m.Evaluate(context.Background(), f, nil, lim)
	if errors.Is(err, vm.ErrLimit) {
		return fail(fmt.Sprintf("%s cannot be checked at compile time: its handler did not finish within %d steps", label, literalSteps), literalHint)
	}
	if err != nil {
		failure, _ := programFailure(err)
		var internal *InternalError
		if errors.As(failure, &internal) {
			return d, false, internal
		}
		return fail(fmt.Sprintf("%s cannot be checked at compile time: its handler failed: %s", label, failure.Error()), literalHint)
	}
	rec, ok := v.(*rt.Record)
	if !ok || rec == nil || rec.Desc.Kind != rt.KindEnum || rec.Variant().Name == "Ok" || rec.NumFields() != 1 {
		return d, false, nil
	}
	return fail(fmt.Sprintf("%s is invalid: %s", label, p.literalMessage(m, site, rec.Field(0))))
}

// literalMessage is the handler's Err payload as the literal's diagnostic
// shows it: its Render function's text, or its structural rendering when the
// error type has none.
func (p *Program) literalMessage(m *vm.Machine, site irbuild.LiteralSite, payload any) string {
	if s, ok := payload.(string); ok {
		return s
	}
	if site.Render != nil {
		lim := vm.Limits{Steps: literalSteps, Deadline: time.Now().Add(literalTime)}
		if v, err := m.Evaluate(context.Background(), site.Render, []any{payload}, lim); err == nil {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return rt.RowText(payload)
}

// literalLabel is how a diagnostic names a literal: its text when it is one
// short line, and otherwise its tag with an elided body.
func literalLabel(text string) string {
	if !strings.Contains(text, "\n") && len(text) <= 60 {
		return "typed literal " + text
	}
	tag, _, _ := strings.Cut(text, "`")
	return "typed literal " + tag + "`…`"
}
