package vmhost

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
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
const InvalidRefinementCode = "literal-breaks-rule"

// UncheckedRefinementCode is the Code of the diagnostic at a refinement
// literal, or a declared rule, whose check could not be made or run: a
// literal is never let through unchecked. Its message starts "internal:"
// when the compiler lost the check, a bug in Nomi.
const UncheckedRefinementCode = "unchecked-literal"

// internalCheckHint closes an "internal:" diagnostic of a check the
// compiler lost.
const internalCheckHint = "this is a bug in Nomi, not in your program; please report it at https://github.com/nomi-language/nomi/issues"

// ruleWithoutApp says why a rule that reads an app field is refused.
const ruleWithoutApp = "a rule is checked with the value alone, and an app field exists only while the program runs"

// ruleHint closes the diagnostic of a rule that cannot run when the program
// is compiled.
const ruleHint = "a rule runs when the program is compiled, on each literal checked as the type; it may call pure functions whose cost its value's size bounds and read an effect-free `once`, and nothing else"

// checkRefinements answers the diagnostics of the refinement literals that
// fail their rules, in source order.
func (p *Program) checkRefinements(m *vm.Machine) (frontend.Diagnostics, error) {
	if p.res == nil || len(p.res.Refinements) == 0 && len(p.res.RefinementRules) == 0 {
		return nil, nil
	}
	funcs := map[string]*ir.Func{}
	var links []string
	// implsOf holds, by interface name, the functions of the program's impl
	// blocks of it, each instance of a generic one among them.
	implsOf := map[string][]*ir.Func{}
	for _, mod := range p.res.IRModules() {
		for _, f := range mod.Funcs() {
			if link := f.Sym().Link(); link != "" {
				funcs[link] = f
				links = append(links, link)
			}
		}
	}
	slices.Sort(links)
	for _, link := range links {
		if impl, ok := funcs[link].Sym().Impl(); ok && impl.Interface != "" {
			implsOf[impl.Interface] = append(implsOf[impl.Interface], funcs[link])
		}
	}
	var out frontend.Diagnostics
	impure := map[string]bool{}
	// A rule refused here is not reported again at each literal of it.
	for _, rule := range p.res.RefinementRules {
		unchecked := rule.Unchecked
		if unchecked == "" && rule.Key == "" && funcs[rule.TryFrom] == nil {
			unchecked = fmt.Sprintf("could not be found (%s)", rule.TryFrom)
		}
		if unchecked != "" {
			d := frontend.NewDiagnostic(rule.Path, p.sources[rule.Path], rule.Line, rule.Col,
				fmt.Sprintf("internal: the check of %s's rule %s", rule.Type, unchecked))
			d.Hints = append(d.Hints, internalCheckHint)
			d.Code = UncheckedRefinementCode
			out = append(out, d)
			impure[rule.TryFrom] = true
			if rule.Key != "" {
				impure[rule.Key] = true
			}
			continue
		}
		var checks []*ir.Func
		if f := funcs[rule.TryFrom]; f != nil {
			checks = append(checks, f)
		}
		if rule.Check != nil {
			// A generic refinement's rule at its declaration's type
			// arguments (analysis.RefinementRuleCheck).
			checks = append(checks, rule.Check)
		}
		if rule.Instances != "" {
			// And every instance of its `try_from` the program has, at
			// whatever type arguments.
			at, _ := slices.BinarySearch(links, rule.Instances)
			for ; at < len(links) && strings.HasPrefix(links[at], rule.Instances); at++ {
				checks = append(checks, funcs[links[at]])
			}
		}
		for _, iface := range rule.Bounds {
			// And every impl of a bound's interface the program has: one
			// of them is the argument's, whatever type the argument is.
			checks = append(checks, implsOf[iface]...)
		}
		if len(checks) > 0 && len(m.Unretained(checks, nil)) > 0 {
			// The effects walk reads what a function reaches, so one that
			// reaches what this machine cannot run is left out: a literal
			// checked through it is reported at the literal
			// (checkRefinement).
			checks = slices.DeleteFunc(checks, func(f *ir.Func) bool { return len(m.Unretained([]*ir.Func{f}, nil)) > 0 })
		}
		if len(checks) == 0 {
			// Nothing left to walk: every function of the rule reaches what
			// the machine cannot run, which each literal of it reports, or
			// a generic rule the program never instantiates past its
			// declaration's check, which did not lower.
			continue
		}
		found := m.Effects(checks...)
		var msg string
		switch {
		case len(found.Effects) > 0:
			msg = fmt.Sprintf("the rule of %s has effects (%s): a rule is a pure function of the value it checks", rule.Type, strings.Join(found.Effects, "; "))
		case len(found.AppFields) > 0:
			msg = fmt.Sprintf("the rule of %s reads %s: %s", rule.Type, quotedNames(p.appFieldNames(found.AppFields)), ruleWithoutApp)
		case len(found.Unbounded) > 0:
			msg = fmt.Sprintf("the rule of %s calls a function whose cost grows with its argument (%s): a rule must be bounded", rule.Type, strings.Join(found.Unbounded, "; "))
		default:
			continue
		}
		impure[rule.TryFrom] = true
		if rule.Key != "" {
			impure[rule.Key] = true
		}
		d := frontend.NewDiagnostic(rule.Path, p.sources[rule.Path], rule.Line, rule.Col, msg)
		d.Hints = append(d.Hints, ruleHint)
		d.Code = InvalidRefinementCode
		out = append(out, d)
	}
	for _, site := range p.res.Refinements {
		if len(site.Steps) > 0 {
			last := site.Steps[len(site.Steps)-1]
			if last.TryFrom != "" && impure[last.TryFrom] || last.Key != "" && impure[last.Key] {
				continue
			}
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

// checkRefinement checks one literal against each refinement whose rule it
// must satisfy, each parent's before its own. Every literal either passes
// every rule or is a diagnostic: one whose check the compiler lost
// (site.Unchecked, a `try_from` no module defines) is an internal error at
// the literal, and one whose check reaches what the machine cannot run is
// an error that names it.
func (p *Program) checkRefinement(m *vm.Machine, funcs map[string]*ir.Func, site irbuild.RefinementSite) (d frontend.Diagnostic, failed bool, err error) {
	report := func(code, msg string, hints ...string) (frontend.Diagnostic, bool, error) {
		path := site.Path
		if path == "" {
			path = p.moduleWithNodeAt(site.Line, site.Col)
		}
		d := frontend.NewDiagnostic(path, p.sources[path], site.Line, site.Col, msg)
		if site.EndLine > 0 {
			d.EndLine, d.EndCol = site.EndLine, site.EndCol
		}
		d.Hints = append(d.Hints, hints...)
		d.Code = code
		return d, true, nil
	}
	fail := func(msg string, hints ...string) (frontend.Diagnostic, bool, error) {
		return report(InvalidRefinementCode, msg, hints...)
	}
	lost := func(why string) (frontend.Diagnostic, bool, error) {
		return report(UncheckedRefinementCode, fmt.Sprintf("internal: the check for this %s literal %s", site.Type, why), internalCheckHint)
	}
	unrunnable := func(found []vm.Unretained) (frontend.Diagnostic, bool, error) {
		return report(UncheckedRefinementCode, fmt.Sprintf("this %s literal cannot be checked when the program is compiled: its check reaches what the compiler does not support yet (%s)",
			site.Type, strings.Join(p.reasons(found), "; ")),
			"a literal of a refinement is checked by running its rule when the program is compiled; until its rule can run, build the value with `"+tryFromOwner(site.Type)+".try_from(...)`")
	}
	if site.Unchecked != "" {
		return lost(site.Unchecked)
	}
	arg := site.Value
	if site.Build != nil {
		if found := m.Unretained([]*ir.Func{site.Build}, nil); len(found) > 0 {
			return unrunnable(found)
		}
		lim := vm.Limits{Steps: literalSteps, Deadline: time.Now().Add(literalTime)}
		rec, err := m.Evaluate(context.Background(), site.Build, nil, lim)
		if err != nil {
			failure, _ := programFailure(err)
			var internal *InternalError
			if errors.As(failure, &internal) {
				return d, false, internal
			}
			if p.holdsTypedLiteral(site) {
				// A literal's value is constants and typed literals, and
				// only a typed literal's handler can fail: its own check
				// (checkLiterals) reports it at the typed literal.
				return d, false, nil
			}
			return lost(fmt.Sprintf("could not be built: making its value failed: %s", failure.Error()))
		}
		arg = rec
	}
	for _, step := range site.Steps {
		f := funcs[step.TryFrom]
		args := []any{arg}
		if step.Check != nil {
			// A generic refinement's check builds the literal itself.
			f, args = step.Check, nil
		}
		if f == nil {
			name := step.TryFrom
			if name == "" {
				name = step.Type + ".try_from"
			}
			return lost(fmt.Sprintf("could not be found (%s)", name))
		}
		if found := m.Unretained([]*ir.Func{f}, nil); len(found) > 0 {
			return unrunnable(found)
		}
		found := m.Effects(f)
		if len(found.Effects) > 0 {
			return fail(fmt.Sprintf("%s cannot be checked as %s %s when the program is compiled: its rule `%s` has effects (%s)",
				site.Text, analysis.ArticleFor(step.Type), step.Type, step.Rule, strings.Join(found.Effects, "; ")),
				"a rule must be a pure function of its value")
		}
		if len(found.AppFields) > 0 {
			return fail(fmt.Sprintf("%s cannot be checked as %s %s when the program is compiled: its rule `%s` reads %s: %s",
				site.Text, analysis.ArticleFor(step.Type), step.Type, step.Rule, quotedNames(p.appFieldNames(found.AppFields)), ruleWithoutApp))
		}
		if len(found.Unbounded) > 0 {
			return fail(fmt.Sprintf("%s cannot be checked as %s %s when the program is compiled: its rule `%s` calls a function whose cost grows with its argument (%s)",
				site.Text, analysis.ArticleFor(step.Type), step.Type, step.Rule, strings.Join(found.Unbounded, "; ")),
				"a rule must be bounded")
		}
		lim := vm.Limits{Steps: literalSteps, Deadline: time.Now().Add(literalTime)}
		v, err := m.Evaluate(context.Background(), f, args, lim)
		if errors.Is(err, vm.ErrLimit) || errors.Is(err, vm.ErrDeadline) {
			return fail(fmt.Sprintf("%s cannot be checked as %s %s when the program is compiled: its rule `%s` did not finish within %d steps",
				site.Text, analysis.ArticleFor(step.Type), step.Type, step.Rule, literalSteps))
		}
		if err != nil {
			failure, _ := programFailure(err)
			var internal *InternalError
			if errors.As(failure, &internal) {
				return d, false, internal
			}
			return fail(fmt.Sprintf("%s is not %s: %s's rule `%s` failed: %s",
				site.Text, notA(site, step), step.Type, step.Rule, failure.Error()))
		}
		if rec, ok := v.(*rt.Record); ok && rec != nil && rec.Desc.Kind == rt.KindEnum && rec.Variant().Name == "None" {
			msg := fmt.Sprintf("%s is not %s: `%s` is false", site.Text, notA(site, step), step.Rule)
			if member, via, ok := intersectionMember(site, step); ok {
				// `0` where `NonNegativeInt and NonZeroInt` is expected:
				// the member whose rule failed is what the value is not.
				if member != step.Type {
					chain := strings.Join(append(via[1:], step.Type), ", which is a refinement of ")
					msg = fmt.Sprintf("%s is not %s: %s is a refinement of %s, and %s's rule `%s` is false",
						site.Text, notA(site, step), member, chain, step.Type, step.Rule)
				}
			} else if site.Compound && step.Type != site.Type {
				msg = fmt.Sprintf("this %s literal breaks the rule of %s, a supertype of it: `%s` is false", site.Type, step.Type, step.Rule)
			} else if site.Compound {
				msg = fmt.Sprintf("this %s literal breaks its rule: `%s` is false", site.Type, step.Rule)
			} else if step.Type != site.Type {
				path := site.Type
				link := " is a refinement of "
				for i, v := range step.Via {
					if i == 0 && site.Wraps {
						path += " wraps " + v + ", which"
						continue
					}
					path += link + v + ", which"
				}
				if site.Wraps && len(step.Via) == 0 {
					link = " wraps "
				}
				msg = fmt.Sprintf("%s is not %s: %s%s%s, and %s's rule `%s` is false",
					site.Text, notA(site, step), path, link, step.Type, step.Type, step.Rule)
			}
			if members := intersectionMembers(site.Type); len(members) > 1 && !site.Wraps {
				// An intersection has no `try_from` of its own: a value is
				// one when each member's answers Some.
				var calls []string
				for _, m := range members {
					owner, _, _ := strings.Cut(m, "<")
					calls = append(calls, "`"+owner+".try_from(...)`")
				}
				return fail(msg, fmt.Sprintf("a literal is checked when the program is compiled; a value known only when it runs is %s when each of %s is Some",
					site.Type, strings.Join(calls, " and ")))
			}
			owner, _, _ := strings.Cut(site.Type, "<")
			return fail(msg, fmt.Sprintf("a literal is checked when the program is compiled; a value known only when it runs goes through `%s.try_from(...)`, an `Option<%s>`",
				owner, site.Type))
		}
	}
	return d, false, nil
}

// holdsTypedLiteral reports whether a typed literal the program checks
// (irbuild.Result.Literals) lies within the refinement literal site.
func (p *Program) holdsTypedLiteral(site irbuild.RefinementSite) bool {
	before := func(l1, c1, l2, c2 int) bool { return l1 < l2 || l1 == l2 && c1 <= c2 }
	for _, lit := range p.res.Literals {
		if lit.Path == site.Path && before(site.Line, site.Col, lit.Line, lit.Col) &&
			(site.EndLine == 0 || before(lit.Line, lit.Col, site.EndLine, site.EndCol)) {
			return true
		}
	}
	return false
}

// tryFromOwner is the name a call of the refinement t's `try_from` is written
// on: t without its type arguments.
func tryFromOwner(t string) string {
	owner, _, _ := strings.Cut(t, "<")
	return owner
}

// notA is what a literal that fails step is not, after "is not": the
// site's type with its article, or, for an intersection, the member whose
// rule failed and the intersection that needs it ("a NonZeroInt, as
// NonNegativeInt and NonZeroInt requires").
func notA(site irbuild.RefinementSite, step irbuild.RefinementSiteStep) string {
	if member, _, ok := intersectionMember(site, step); ok {
		return analysis.ArticleFor(member) + " " + member + ", as " + site.Type + " requires"
	}
	return analysis.ArticleFor(site.Type) + " " + site.Type
}

// intersectionMember answers, when the literal's type is an intersection
// and step a rule of one of its members, that member and the refinements from
// it down to step's type (step.Via), the member first.
func intersectionMember(site irbuild.RefinementSite, step irbuild.RefinementSiteStep) (string, []string, bool) {
	if site.Wraps || site.Compound || len(intersectionMembers(site.Type)) < 2 || step.Type == site.Type {
		return "", nil, false
	}
	if len(step.Via) > 0 {
		return step.Via[0], step.Via, true
	}
	return step.Type, nil, true
}

// intersectionMembers splits the type text t at each ` and ` outside its
// brackets: `NonNegativeInt and NonZeroInt` is two members.
func intersectionMembers(t string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '<', '(', '[', '{':
			depth++
		case '>', ')', ']', '}':
			depth--
		}
		if depth == 0 && strings.HasPrefix(t[i:], " and ") {
			out = append(out, t[start:i])
			start = i + len(" and ")
			i += len(" and ") - 1
		}
	}
	return append(out, t[start:])
}
