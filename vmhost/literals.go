package vmhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// BACKTICK TYPED LITERALS ARE CHECKED AT COMPILE TIME.
//
// A backtick typed literal whose handler can fail (`Regex`\d+``, whose
// `from_fragments` returns `Result<Regex, String>`) has the handler's
// success type, so it needs no `?`. Its body never interpolates, so the
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
const literalHint = "a backtick typed literal is checked at compile time; to check it when the program runs instead, write it with double quotes and handle its Result with `?` or `case`"

// checkLiterals runs the handler of every backtick typed literal the lowering
// checks at compile time, and answers the diagnostics of the ones that fail,
// in source order, or nil.
func (p *Program) checkLiterals() error {
	ds := p.checkCurrencyCalls()
	if p.res == nil || len(p.res.Literals) == 0 || len(p.res.IR) == 0 {
		if len(ds) == 0 {
			return nil
		}
		return ds
	}
	// An entry file every body of which was declined has no module, and a
	// machine needs one to resolve callees in first. A literal elsewhere in
	// the program is still checked, over the first module; the decline is
	// reported where the program reaches it (Unsupported, Run), as every
	// decline is.
	entry := p.entry
	if entry == nil {
		entry = p.res.IR[0]
	}
	m := p.machineOver(entry, io.Discard)
	var lits frontend.Diagnostics
	for _, site := range p.res.Literals {
		d, failed, err := p.checkLiteral(m, site)
		if err != nil {
			return err
		}
		if failed {
			lits = append(lits, d)
		}
	}
	ds = append(lits, ds...)
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
	if !ok || rec == nil || rec.Desc.Kind != rt.KindEnum || rec.NumFields() != 1 {
		return d, false, nil
	}
	if rec.Variant().Name == "Ok" {
		if loc, ok := localeTag(rec.Field(0)); ok && site.Locale {
			if msg, ok := p.notCarried(loc); ok {
				return fail(fmt.Sprintf("%s: %s", label, msg), notCarriedHint)
			}
		}
		if msg, hints, bad := p.unlistedLiteral(site, rec.Field(0)); bad {
			return fail(fmt.Sprintf("%s: %s", label, msg), hints...)
		}
		return d, false, nil
	}
	return fail(fmt.Sprintf("%s is invalid: %s", label, p.literalMessage(m, site, rec.Field(0))))
}

// localeTag is the tag a Locale value holds: a distinct over its String.
func localeTag(v any) (string, bool) {
	if r, ok := v.(*rt.Record); ok && r.Desc.Kind == rt.KindDistinct {
		v = r.Field(0)
	}
	s, ok := v.(string)
	return s, ok
}

// notCarriedHint closes the diagnostic of a Locale literal the program
// does not carry.
const notCarriedHint = "a locale the program does not carry formats as its source locale; to name one it only compares or negotiates, parse it while the program runs with `Locale.parse`"

// notCarried says why tag, a “Locale`…` “ literal's locale, is one the
// program does not carry, when it is: the program lists its locales
// (`[i18n].locales` and `partial_locales`) and tag's fallback chain reaches
// none of them. Such a locale formats as the source locale (cldrdata's
// SourceFallback), so a literal naming it is a mistake: the program will
// never format in it.
func (p *Program) notCarried(tag string) (string, bool) {
	if p.locales == nil || tag == "und" {
		return "", false
	}
	for _, t := range stdlocale.Fallbacks(cldrfull.Full(), tag) {
		if slices.Contains(p.locales, t) {
			return "", false
		}
	}
	listed := make([]string, len(p.locales))
	for i, t := range p.locales {
		listed[i] = "`" + t + "`"
	}
	return fmt.Sprintf("`%s` is not one of this program's locales (%s in nomi.toml's [i18n]): add it there, or format in one of them",
		tag, strings.Join(listed, ", ")), true
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
