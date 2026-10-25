package stdstrings

import (
	"strings"

	"github.com/nomi-language/nomi/internal/strlit"
)

// Literal is `String.to_literal`: s as a Nomi string literal that reads
// back as s. It escapes with strlit's table, the one the lexer decodes
// and the formatter encodes with (`\n`, `\t`, `\\`, `\"`, and `\u{...}`
// for any other character that is not printable, a carriage return
// among them), and writes `${` as `\${`, so nothing in s interpolates.
func Literal(s string) string {
	return `"` + strings.ReplaceAll(strlit.EncodeStringBody(s), "${", `\${`) + `"`
}
