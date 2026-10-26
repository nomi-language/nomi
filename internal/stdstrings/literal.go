package stdstrings

import "github.com/nomi-language/nomi/rt"

// Literal is `String.to_literal`: rt.StringLiteral, the literal a String's
// Debug writes too.
func Literal(s string) string {
	return rt.StringLiteral(s)
}
