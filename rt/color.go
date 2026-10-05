package rt

import (
	"io"
	"os"
	"strings"
)

// Terminal colouring, in rt because the test reporter is in rt.
//
// The reporter's output is a byte-for-byte contract, and colour is part of
// those bytes when stdout is a terminal. Two copies of "is colour on" — one
// reading NO_COLOR, one reading CLICOLOR_FORCE, one forgetting `TERM=dumb` —
// would make the contract depend on which code path you ran. So the enablement rule and the escape codes live
// here, and nomi/internal/termcolor calls down into them.
//
// What does not live here is Nomi syntax highlighting, which runs the lexer.
// See Highlight.

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiCyan  = "\x1b[36m"
)

// ColorEnabledFor reports whether colour should be written to w.
//
// The precedence — explicit NOMI_COLOR, then NO_COLOR/TERM=dumb, then
// CLICOLOR_FORCE, then "is it a character device" — is stated only here, and
// nomi/internal/termcolor calls it.
func ColorEnabledFor(w io.Writer) bool {
	switch strings.ToLower(os.Getenv("NOMI_COLOR")) {
	case "always", "1", "true", "yes", "force":
		return true
	case "never", "0", "false", "no":
		return false
	}
	if os.Getenv("NO_COLOR") != "" || strings.ToLower(os.Getenv("TERM")) == "dumb" {
		return false
	}
	if os.Getenv("CLICOLOR_FORCE") != "" {
		return true
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Color wraps s in an ANSI code when colour is on for stdout.
func Color(code, s string) string { return ColorFor(os.Stdout, code, s) }

// ColorFor wraps s in an ANSI code when colour is on for w.
func ColorFor(w io.Writer, code, s string) string {
	if !ColorEnabledFor(w) || code == "" || s == "" {
		return s
	}
	return code + s + ansiReset
}

// The spellings the test report and the diagnostics use. Exported as
// functions rather than as the raw codes so a caller cannot assemble another
// by hand.
func Red(s string) string   { return Color(ansiBold+ansiRed, s) }
func Green(s string) string { return Color(ansiGreen, s) }
func Dim(s string) string   { return Color(ansiDim, s) }

func DimFor(w io.Writer, s string) string  { return ColorFor(w, ansiDim, s) }
func CyanFor(w io.Writer, s string) string { return ColorFor(w, ansiCyan, s) }
func RedFor(w io.Writer, s string) string  { return ColorFor(w, ansiBold+ansiRed, s) }
func BoldFor(w io.Writer, s string) string { return ColorFor(w, ansiBold, s) }

// Highlight renders a fragment of Nomi source with syntax colouring, or returns
// it unchanged. It is a hook rather than an implementation, and the asymmetry
// is deliberate.
//
// Highlighting requires tokenizing Nomi, which requires the lexer, which lives
// in the compiler's module, and rt links no front end. So `nomi test` installs
// termcolor's real highlighter here at init and gets coloured source lines
// inside an assertion failure; a host that does not install one leaves the
// hook nil and prints the same source lines uncoloured. Every other byte of
// the report — the ok/fail markers, the summary, the dim labels, the line
// numbers, the operand values — is the same either way, and when stdout is not
// a terminal the two are identical outright, because highlighting is off.
//
// # The option not taken
//
// Highlighting could work without a lexer if the caller carried the
// tokenization of each asserted expression (spans plus token kinds, computed
// ahead of time) for rt to colour with no parser at all. It is not done,
// because the cost is a per-assertion span table plus a second rendering
// path in rt, and the benefit is a cosmetic that only appears when a host
// without the lexer runs a failing test on a terminal. Piped output, which is
// what CI and the golden files see, is already byte-identical.
var Highlight func(w io.Writer, src string) string

func highlight(w io.Writer, src string) string {
	if Highlight == nil {
		return src
	}
	return Highlight(w, src)
}
