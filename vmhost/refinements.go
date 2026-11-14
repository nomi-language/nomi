package vmhost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// LITERALS OF REFINEMENTS ARE CHECKED AT COMPILE TIME (spec §4 *Refinement
// types*).
//
// A literal written where a refinement is expected (`p: Port = 8080`,
// `Port(8080)`) is checked against each rule of the refinement's chain
// before anything else runs, by calling each refinement's generated
// `try_from` on it (irbuild's RefinementSites), from the one that refines
// the base up. The first that answers None names the rule the literal
// breaks. A rule that traps, has an effect or runs past the step limit is
// an error that says so. The evaluation is the one typed literals use
// (checkLiterals): vm.Machine.Evaluate under literalSteps.

// InvalidRefinementCode is the Code of a refinement literal's diagnostic.
const InvalidRefinementCode = "invalid-refinement-literal"

// checkRefinements answers the diagnostics of the refinement literals that
// fail their rules, in source order.
func (p *Program) checkRefinements(m *vm.Machine) (frontend.Diagnostics, error) {
	if p.res == nil || len(p.res.Refinements) == 0 && len(p.res.RefinementRules) == 0 {
		return nil, nil
	}
	funcs := map[string]*ir.Func{}
	for _, mod := range p.res.IRModules() {
		for _, f := range mod.Funcs() {
			if link := f.Sym().Link(); link != "" {
				funcs[link] = f
			}
		}
	}
	var out frontend.Diagnostics
	impure := map[string]bool{}
	for _, rule := range p.res.RefinementRules {
		f := funcs[rule.TryFrom]
		if f == nil || len(m.Unretained([]*ir.Func{f}, nil)) > 0 {
			continue
		}
		if effects := m.Effects(f); len(effects) > 0 {
			impure[rule.TryFrom] = true
			d := frontend.NewDiagnostic(rule.Path, p.sources[rule.Path], rule.Line, rule.Col,
				fmt.Sprintf("the rule of the refinement %s has effects (%s): a rule is a pure function of the value it checks", rule.Type, strings.Join(effects, "; ")))
			d.Hints = append(d.Hints, "a rule runs when the program is compiled, on each literal checked as the type; it may call pure functions and read an effect-free `once`, and nothing else")
			d.Code = InvalidRefinementCode
			out = append(out, d)
		}
	}
	for _, site := range p.res.Refinements {
		if len(site.Steps) > 0 && impure[site.Steps[len(site.Steps)-1].TryFrom] {
			continue
		}
		d, failed, err := p.checkRefinement(m, funcs, site)
		if err != nil {
			return nil, err
		}
		if failed {
			out = append(out, d)
		}
	}
	return out, nil
}

// checkRefinement checks one literal against each step of its chain.
func (p *Program) checkRefinement(m *vm.Machine, funcs map[string]*ir.Func, site irbuild.RefinementSite) (d frontend.Diagnostic, failed bool, err error) {
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
		d.Code = InvalidRefinementCode
		return d, true, nil
	}
	arg := site.Value
	if site.Build != nil {
		lim := vm.Limits{Steps: literalSteps, Deadline: time.Now().Add(literalTime)}
		rec, err := m.Evaluate(context.Background(), site.Build, nil, lim)
		if err != nil {
			return d, false, nil
		}
		arg = rec
	}
	for _, step := range site.Steps {
		f := funcs[step.TryFrom]
		if f == nil || len(m.Unretained([]*ir.Func{f}, nil)) > 0 {
			// A rule the compiler cannot lower is reported where the
			// program reaches it, as every such gap is.
			return d, false, nil
		}
		if effects := m.Effects(f); len(effects) > 0 {
			return fail(fmt.Sprintf("%s cannot be checked as %s %s when the program is compiled: its rule `%s` has effects (%s)",
				site.Text, article(step.Type), step.Type, step.Rule, strings.Join(effects, "; ")),
				"a refinement's rule must be a pure function of its value")
		}
		lim := vm.Limits{Steps: literalSteps, Deadline: time.Now().Add(literalTime)}
		v, err := m.Evaluate(context.Background(), f, []any{arg}, lim)
		if errors.Is(err, vm.ErrLimit) || errors.Is(err, vm.ErrDeadline) {
			return fail(fmt.Sprintf("%s cannot be checked as %s %s when the program is compiled: its rule `%s` did not finish within %d steps",
				site.Text, article(step.Type), step.Type, step.Rule, literalSteps))
		}
		if err != nil {
			failure, _ := programFailure(err)
			var internal *InternalError
			if errors.As(failure, &internal) {
				return d, false, internal
			}
			return fail(fmt.Sprintf("%s is not %s %s: %s's rule `%s` failed: %s",
				site.Text, article(site.Type), site.Type, step.Type, step.Rule, failure.Error()))
		}
		if rec, ok := v.(*rt.Record); ok && rec != nil && rec.Desc.Kind == rt.KindEnum && rec.Variant().Name == "None" {
			msg := fmt.Sprintf("%s is not %s %s: `%s` is false", site.Text, article(site.Type), site.Type, step.Rule)
			if site.Build != nil {
				msg = fmt.Sprintf("this %s literal breaks its rule: `%s` is false", site.Type, step.Rule)
			} else if step.Type != site.Type {
				msg = fmt.Sprintf("%s is not %s %s: %s refines %s, and %s's rule `%s` is false",
					site.Text, article(site.Type), site.Type, site.Type, step.Type, step.Type, step.Rule)
			}
			return fail(msg, fmt.Sprintf("a literal is checked when the program is compiled; a value known only when it runs goes through `%s.try_from(...)`, a `Maybe<%s>`",
				site.Type, site.Type))
		}
	}
	return d, false, nil
}

// article is "a" or "an" before name.
func article(name string) string {
	if name != "" && strings.ContainsRune("AEIOU", rune(name[0])) {
		return "an"
	}
	return "a"
}
