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
		if iface, ok := irbuild.ImplLinkInterface(link); ok {
			implsOf[iface] = append(implsOf[iface], funcs[link])
		}
	}
	var out frontend.Diagnostics
	impure := map[string]bool{}
	for _, rule := range p.res.RefinementRules {
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
			// A function that reaches what this machine cannot run is
			// reported where the program reaches it, not here.
			checks = slices.DeleteFunc(checks, func(f *ir.Func) bool { return len(m.Unretained([]*ir.Func{f}, nil)) > 0 })
		}
		if len(checks) == 0 {
			continue
		}
		if effects := m.Effects(checks...); len(effects) > 0 {
			impure[rule.TryFrom] = true
			if rule.Key != "" {
				impure[rule.Key] = true
			}
			d := frontend.NewDiagnostic(rule.Path, p.sources[rule.Path], rule.Line, rule.Col,
				fmt.Sprintf("the rule of %s has effects (%s): a rule is a pure function of the value it checks", rule.Type, strings.Join(effects, "; ")))
			d.Hints = append(d.Hints, "a rule runs when the program is compiled, on each literal checked as the type; it may call pure functions and read an effect-free `once`, and nothing else")
			d.Code = InvalidRefinementCode
			out = append(out, d)
		}
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
// must satisfy, each parent's before its own.
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
		args := []any{arg}
		if step.Check != nil {
			// A generic refinement's check builds the literal itself.
			f, args = step.Check, nil
		}
		if f == nil || len(m.Unretained([]*ir.Func{f}, nil)) > 0 {
			// A rule the compiler cannot lower is reported where the
			// program reaches it, as every such gap is.
			return d, false, nil
		}
		if effects := m.Effects(f); len(effects) > 0 {
			return fail(fmt.Sprintf("%s cannot be checked as %s %s when the program is compiled: its rule `%s` has effects (%s)",
				site.Text, analysis.ArticleFor(step.Type), step.Type, step.Rule, strings.Join(effects, "; ")),
				"a rule must be a pure function of its value")
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
					chain := strings.Join(append(via[1:], step.Type), ", which is a subtype of ")
					msg = fmt.Sprintf("%s is not %s: %s is a subtype of %s, and %s's rule `%s` is false",
						site.Text, notA(site, step), member, chain, step.Type, step.Rule)
				}
			} else if site.Compound && step.Type != site.Type {
				msg = fmt.Sprintf("this %s literal breaks the rule of %s, a supertype of it: `%s` is false", site.Type, step.Type, step.Rule)
			} else if site.Compound {
				msg = fmt.Sprintf("this %s literal breaks its rule: `%s` is false", site.Type, step.Rule)
			} else if step.Type != site.Type {
				path := site.Type
				link := " is a subtype of "
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
			return fail(msg, fmt.Sprintf("a literal is checked when the program is compiled; a value known only when it runs goes through `%s.try_from(...)`, a `Maybe<%s>`",
				owner, site.Type))
		}
	}
	return d, false, nil
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
// and step a rule of one of its members, that member and the subtypes from
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
