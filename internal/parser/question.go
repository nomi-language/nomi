package parser

import "github.com/nomi-language/nomi/internal/token"

// A postfix `?`, `parse(text)?`, is Nomi's error propagation: on an Ok or a
// Some it is the payload, and on an Err or a None the enclosing function,
// lambda or test returns that value. It is the companion of the postfix
// `!`, which traps instead, and is parsed in the same position.

// postfixQuestionAllowed reports whether a `?` right after prev follows an
// operand.
func postfixQuestionAllowed(prev token.Token) bool {
	return postfixBangAllowed(prev)
}
