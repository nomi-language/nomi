package parser

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/token"
)

// intLiteralValue is the value of an INT token. A literal past Int's range
// is an error at the literal that says so, and for a base-10 literal
// suggests the Decimal literal that holds it exactly. `-9223372036854775808`,
// the smallest Int, is a negation of a literal one past the largest, so a
// `-` written right before that literal (negated) gets a hint naming
// `Int.min_value` in place of the Decimal one, or, in a pattern, which
// cannot name it, the guard that matches it. Any other failure is a
// malformed literal.
func intLiteralValue(tok token.Token, negated, inPattern bool) (int64, error) {
	val, err := strconv.ParseInt(tok.Lexeme, 0, 64)
	if err == nil {
		return val, nil
	}
	if !errors.Is(err, strconv.ErrRange) {
		return 0, errorAt(tok.Line, tok.Col, "invalid integer %q", tok.Lexeme)
	}
	e := ParseError{
		Line:    tok.Line,
		Col:     tok.Col,
		Message: fmt.Sprintf("`%s` is too large for an `Int`, a 64-bit integer: an integer literal is at most 9223372036854775807", tok.Lexeme),
	}
	if u, uerr := strconv.ParseUint(tok.Lexeme, 0, 64); negated && uerr == nil && u == 1<<63 {
		if inPattern {
			e.Hints = append(e.Hints, "the smallest `Int`, -9223372036854775808, cannot be written as a literal, and a pattern cannot name `Int.min_value`; match it with a guard: `n when n == Int.min_value -> ...`")
		} else {
			e.Hints = append(e.Hints, "the smallest `Int`, -9223372036854775808, cannot be written as a literal; write `Int.min_value`")
		}
	} else if !hasRadixPrefix(tok.Lexeme) {
		e.Hints = append(e.Hints, fmt.Sprintf("for an exact number this large, write a Decimal literal: `%sd`", tok.Lexeme))
	}
	return 0, e
}

// negatedLiteral reports whether the token before the one at index i is a
// `-` written right against it, as in `-9223372036854775808`.
func (p *Parser) negatedLiteral(i int) bool {
	if i < 1 || i >= len(p.tokens) {
		return false
	}
	prev, tok := p.tokens[i-1], p.tokens[i]
	return prev.Type == token.MINUS && prev.Line == tok.Line && prev.Col+1 == tok.Col
}

// hasRadixPrefix reports whether an integer literal is written in hex,
// binary or octal (`0x`, `0b`, `0o`), which a Decimal literal cannot be.
func hasRadixPrefix(lexeme string) bool {
	if len(lexeme) < 2 || lexeme[0] != '0' {
		return false
	}
	return strings.ContainsRune("xXbBoO", rune(lexeme[1]))
}
