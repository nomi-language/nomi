package rt

import (
	"fmt"
	"io"
	"strings"
)

// DbgText is the whole of `dbg`'s layout and colour, composed for a writer and
// returned rather than written.
//
// # Why the composition is split from the write, and why it is the text that
// is shared rather than the write
//
// `internal/vm`'s output destination is a writer a caller supplied: a
// `Machine` is opened with one, because a record in the committed expectation
// files is a transcript and a transcript has to be captured.
//
// It also has two writers for one print, which is what decided this signature.
// A `Machine` wraps the caller's writer in its own mutex at construction, and
// `ColorEnabledFor` ends in `w.(*os.File)` plus a character-device test — so
// the wrapper answers false for every destination including a terminal. The
// colour question therefore has to be asked about the destination and the
// bytes written to the wrapper. A `DbgTo(w, …)` that did both over one `w`
// cannot serve that, and giving it two writers would put a hole for one caller
// in the rule every printer in this package keeps: ask `ColorEnabledFor(w)`
// and write to `w`. Answering the text leaves each caller its own write and
// its own serialization, and there is still exactly one expression of the
// three shapes.
//
// The one-write property is the caller's: `internal/vm`'s binding composes
// the whole text first and writes it once through its machine's mutex, so a
// multi-line `dbg` from two concurrent tasks cannot interleave line by line.
//
// So the layout and the colour rule are called rather than transcribed, which
// is why this is exported. A second expression of them inside
// `internal/vm` is what this package avoids: `StringLength`'s
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
