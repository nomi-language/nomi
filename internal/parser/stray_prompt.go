package parser

import (
	"errors"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/token"
)

// strayPromptErrors reports each `//!` that does not start its line. The
// lexer reads a `//!` with only whitespace before it as an attached-test
// prompt and any other `//!` as a plain comment, so `x = 1 //! note` would
// stop being a comment as soon as anything moved it to a line of its own,
// as `nomi fmt` does with a comment before a literal's `}`. A `//!` is a
// prompt or an error, never a comment, as a `///` is a doc comment or an
// error.
func strayPromptErrors(tokens []token.Token) []ParseError {
	var errs []ParseError
	for _, tok := range tokens {
		if tok.Type == token.COMMENT && strings.HasPrefix(tok.Lexeme, "//!") {
			errs = append(errs, ParseError{
				Line:    tok.Line,
				Col:     tok.Col,
				Message: "a `//!` attached test must start its own line, above the declaration it tests; write `//` for a comment after code",
			})
		}
	}
	return errs
}

// withStrayPrompt returns the earlier of err and the first stray `//!`, the
// error a parse that stops at the first one reports.
func withStrayPrompt(tokens []token.Token, err error) error {
	stray := strayPromptErrors(tokens)
	if len(stray) == 0 {
		return err
	}
	var pe ParseError
	if err != nil && errors.As(err, &pe) &&
		(pe.Line < stray[0].Line || pe.Line == stray[0].Line && pe.Col < stray[0].Col) {
		return err
	}
	return stray[0]
}

// withStrayPrompts adds an error for each stray `//!` to errs, in source
// order, for a parse that reports every error.
func withStrayPrompts(tokens []token.Token, errs []ParseError) []ParseError {
	stray := strayPromptErrors(tokens)
	if len(stray) == 0 {
		return errs
	}
	errs = append(errs, stray...)
	sort.SliceStable(errs, func(i, j int) bool {
		a, b := errs[i], errs[j]
		return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col
	})
	return errs
}
