package rt

import (
	"fmt"
	"io"
	"strings"
)

// DbgText is the whole of `dbg`'s LAYOUT and COLOUR, composed for a writer and
// returned rather than written.
//
// # WHY THE COMPOSITION IS SPLIT FROM THE WRITE, AND WHY IT IS THE TEXT THAT
// IS SHARED RATHER THAN THE WRITE
//
// `internal/vm`'s output destination is a WRITER A CALLER SUPPLIED: a
// `Machine` is opened with one, because a record in the committed expectation
// files is a transcript and a transcript has to be captured.
//
// It also has TWO writers for one print, which is what decided this signature.
// A `Machine` wraps the caller's writer in its own mutex at construction, and
// `ColorEnabledFor` ends in `w.(*os.File)` plus a character-device test — so
// the wrapper answers FALSE for every destination including a terminal. The
// colour question therefore has to be asked about the DESTINATION and the
// bytes written to the WRAPPER. A `DbgTo(w, …)` that did both over one `w`
// cannot serve that, and giving it two writers would put a hole for one caller
// in the rule every printer in this package keeps: ask `ColorEnabledFor(w)`
// and write to `w`. Answering the TEXT leaves each caller its own write and
// its own serialization, and there is still exactly one expression of the
// three shapes.
//
// THE ONE-WRITE PROPERTY is the caller's: `internal/vm`'s binding composes
// the whole text first and writes it once through its machine's mutex, so a
// multi-line `dbg` from two concurrent tasks cannot interleave line by line.
//
// SO THE LAYOUT AND THE COLOUR RULE ARE CALLED RATHER THAN TRANSCRIBED, which
// is the whole reason this is exported. A second expression of them inside
// `internal/vm` is the shape this package keeps deleting: `StringLength`'s
// header calls two implementations of one boundary rule "a divergence waiting
// for the input [the fixtures] do not [name]", and `FormatAssertionFailure`
// lives here once for the same reason.
func DbgText(w io.Writer, line int, expr, rendered string) string {
	rendered = highlight(w, rendered)
	header := CyanFor(w, "dbg") + " " + DimFor(w, fmt.Sprintf("line %d:", line))
	expr = strings.TrimSpace(expr)

	var b strings.Builder
	switch {
	case expr == "":
		fmt.Fprintf(&b, "%s = %s\n", header, rendered)
	case !strings.Contains(expr, "\n"):
		fmt.Fprintf(&b, "%s %s = %s\n", header, highlight(w, expr), rendered)
	default:
		fmt.Fprintf(&b, "%s\n", header)
		for _, part := range strings.Split(expr, "\n") {
			writeSourceLine(&b, "  ", part, func(s string) string { return highlight(w, s) })
		}
		fmt.Fprintf(&b, "  = %s\n", rendered)
	}
	return b.String()
}
