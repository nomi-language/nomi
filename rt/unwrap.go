package rt

import (
	"fmt"
	"unicode/utf8"
)

// The traps of a postfix `!`, `expr!`, whose expression answered Err or
// None. Each names the expression as the program wrote it, on one line and
// cut after bangSourceMax characters:
//
//	line 4: `Date.new(2024, 13, 1)` failed: invalid value: month out of range: 13
//	line 9: `Map.get(counts, "z")` found nothing
//
// An Err shows the error, in its Display text when its type implements
// Display and its Debug text otherwise (the caller renders it). A None has
// no error to show.

// bangSourceMax is how many characters of the expression a `!` trap shows.
// A longer one is cut there and followed by `...`.
const bangSourceMax = 60

// BangFailedError is the fault of `expr!` whose expression answered
// `Err(e)`: `line N: `expr` failed: <text>`, where text is e rendered.
func BangFailedError(line int, source, text string) error {
	return positioned(line, fmt.Sprintf("line %d: `%s` failed: %s", line, bangSource(source), text))
}

// BangNoneError is the fault of `expr!` whose expression answered `None`:
// `line N: `expr` found nothing`.
func BangNoneError(line int, source string) error {
	return positioned(line, fmt.Sprintf("line %d: `%s` found nothing", line, bangSource(source)))
}

// bangSource is source cut after bangSourceMax characters.
func bangSource(source string) string {
	if utf8.RuneCountInString(source) <= bangSourceMax {
		return source
	}
	n := 0
	for i := range source {
		if n == bangSourceMax {
			return source[:i] + "..."
		}
		n++
	}
	return source
}
