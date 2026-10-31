package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/regexgroups"
	"github.com/nomi-language/nomi/internal/token"
)

// A REGEX LITERAL'S TYPE NAMES ITS CAPTURE GROUPS.
//
// std/regex declares `Regex<C>`, where C is what `String.captures` answers
// for a match: a value with one part per capture group. A regex compiled
// while the program runs (`Regex.compile`, or a literal that interpolates)
// is `Regex<List<Maybe<String>>>`, since nothing knows its groups before it
// runs. A `Regex` literal whose pattern is fixed text has C computed here
// from the pattern, as Swift types `/(\d+)-(\w+)?/`:
//
//   - no group: `Unit`;
//   - every group named, by a name a field can have: the anonymous struct
//     of the groups, `#{year: String, month: Maybe<String>}`, one field or
//     several;
//   - one unnamed group: that group's type, `String` or `Maybe<String>`,
//     since Nomi has no one-element tuple;
//   - otherwise: the tuple of the groups in order, `#(String, Maybe<String>)`.
//
// A group is `String` when every match fills it and `Maybe<String>` when a
// match can leave it out (regexgroups.Group.Optional).
//
// This is the one std type whose literal's type the compiler computes: a
// typed literal's type is otherwise its handler's result, which cannot
// depend on the literal's text. std's handler returns the run-time shape
// `Result<Regex<List<Maybe<String>>>, String>`, and the checker replaces the
// type argument. The value is the same compiled pattern either way: C is
// phantom (no part of the handle stores it), and `String.captures` builds
// the C value from the match's groups by C alone.

// regexOrigin and regexName identify std/regex's `Regex`.
const (
	regexOrigin = "std/regex"
	regexName   = "Regex"
)

// isRegexType reports whether t is std/regex's `Regex<C>`.
func isRegexType(t *DistinctType) bool {
	return t != nil && t.Origin == regexOrigin && t.Name == regexName
}

// regexLiteralType is the type of the typed literal n, whose handler
// returns ret, when n is a `Regex` literal with no interpolation: ret with
// the `Regex`'s capture type computed from the pattern. Nil for any other
// literal.
func regexLiteralType(n *ast.TaggedString, ret Type) Type {
	var text strings.Builder
	for _, part := range n.Parts {
		lit, isText := part.(ast.StringText)
		if !isText {
			return nil
		}
		text.WriteString(lit.Value)
	}
	return regexPatternType(text.String(), ret)
}

// StaticLiteralType is the type of a typed literal with no interpolation,
// whose body is text and whose handler returns ret: the checker's answer
// for it, which the language server's literal hover shows.
func StaticLiteralType(raw bool, text string, ret Type) Type {
	if typed := regexPatternType(text, ret); typed != nil {
		ret = typed
	}
	if ok, _ := RawLiteralType(raw, ret); ok != nil {
		return ok
	}
	return ret
}

// regexPatternType is ret, a `Regex` handler's result, with the capture
// type of pattern. Nil when ret is not std's `Regex` handler's, and for a
// pattern that does not compile, whose literal is a compile error (a
// backtick one) or an Err when it runs (a double-quoted one) and keeps the
// handler's type.
func regexPatternType(pattern string, ret Type) Type {
	result, isEnum := resolveTypeVar(ret).(*EnumType)
	if !isEnum || result.Name != "Result" || len(result.TypeArgs) != 2 {
		return nil
	}
	re, isRegex := resolveTypeVar(result.TypeArgs[0]).(*DistinctType)
	if !isRegex || !isRegexType(re) || len(re.TypeArgs) != 1 {
		return nil
	}
	list, isList := resolveTypeVar(re.TypeArgs[0]).(*ListType)
	if !isList {
		return nil
	}
	groups, err := regexgroups.Groups(pattern)
	if err != nil {
		return nil
	}
	typed := *re
	typed.TypeArgs = []Type{RegexCaptureType(groups, list.Elem)}
	out := *result
	out.TypeArgs = []Type{&typed, result.TypeArgs[1]}
	return &out
}

// RegexCaptureType is the capture type C of a `Regex<C>` literal with these
// groups (see the comment above). maybeString is the type `Maybe<String>`.
func RegexCaptureType(groups []regexgroups.Group, maybeString Type) Type {
	part := func(g regexgroups.Group) Type {
		if g.Optional {
			return maybeString
		}
		return TypeString
	}
	if len(groups) == 0 {
		return TypeUnit
	}
	if regexGroupsNamed(groups) {
		fields := make([]FieldDef, len(groups))
		for i, g := range groups {
			fields[i] = FieldDef{Name: g.Name, Type: part(g)}
		}
		return &AnonStructType{Fields: fields}
	}
	if len(groups) == 1 {
		return part(groups[0])
	}
	elems := make([]Type, len(groups))
	for i, g := range groups {
		elems[i] = part(g)
	}
	return &TupleType{Elems: elems}
}

// regexGroupsNamed reports whether every group has a name a field can have
// (a lowercase identifier that is not a keyword), each a different one.
func regexGroupsNamed(groups []regexgroups.Group) bool {
	seen := map[string]bool{}
	for _, g := range groups {
		if g.Name == "" || seen[g.Name] {
			return false
		}
		seen[g.Name] = true
		toks := lexer.Lex(g.Name)
		if len(toks) == 0 || toks[0].Type != token.IDENT || toks[0].Lexeme != g.Name {
			return false
		}
		if len(toks) > 1 && toks[1].Type != token.EOF {
			return false
		}
	}
	return true
}
