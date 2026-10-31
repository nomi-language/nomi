// Package regexgroups reads the capture groups of a regular expression in
// Go's RE2 syntax, the syntax std/regex compiles: each group's name and
// whether every match fills it. The checker builds the capture type of a
// `Regex` literal from them (`Regex<#(String, Maybe<String>)>`), and
// anything else that shows a literal's groups reads the same answer here.
package regexgroups

import "regexp/syntax"

// Group is one capture group, in the order the pattern opens them.
type Group struct {
	// Name is the group's name, `(?P<name>...)` or `(?<name>...)`, or ""
	// for an unnamed group.
	Name string
	// Optional is true when a match can leave the group out: it sits in
	// the operand of `?`, `*` or a repetition whose minimum is 0, in one
	// alternative of a `|`, or inside a group that is optional. A group
	// repeated by `+` or `{n,m}` with n > 0 always takes part (it holds
	// its last iteration), and so does one that can match the empty
	// string.
	Optional bool
}

// Groups reads pattern's capture groups, group 1 onward (the whole match is
// not a group here). The error is the parser's, for a pattern
// regexp.Compile refuses.
func Groups(pattern string) ([]Group, error) {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, err
	}
	groups := make([]Group, re.MaxCap())
	walk(re, false, groups)
	return groups, nil
}

// walk records each capture under re, optional when a match can skip re.
func walk(re *syntax.Regexp, optional bool, groups []Group) {
	switch re.Op {
	case syntax.OpCapture:
		if re.Cap >= 1 && re.Cap <= len(groups) {
			groups[re.Cap-1] = Group{Name: re.Name, Optional: optional}
		}
	case syntax.OpStar, syntax.OpQuest:
		optional = true
	case syntax.OpRepeat:
		if re.Min == 0 {
			optional = true
		}
	case syntax.OpAlternate:
		// Each group is numbered once, so no group stands in two
		// alternatives, and a match takes one alternative: every group
		// under one may be left out.
		optional = true
	}
	for _, sub := range re.Sub {
		walk(sub, optional, groups)
	}
}
