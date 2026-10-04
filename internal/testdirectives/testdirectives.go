// Package testdirectives parses the `// expect ...:` annotations used by
// editor diagnostic fixtures. Two failure-class directives:
//
//	// expect runtime-error: <text>   — program type-checks clean,
//	                                    traps at runtime; <text> is
//	                                    substring-matched against the
//	                                    runtime error message.
//	// expect type-error: <text>      — program is rejected at
//	                                    analysis time; <text> is
//	                                    substring-matched against
//	                                    at least one diagnostic's
//	                                    full Error() output. A program may
//	                                    carry multiple type-error
//	                                    directives; each one must match a
//	                                    distinct diagnostic.
//
// (Output assertions — `// expect: <line>` — belong to doctest-style examples
// and are not parsed here.)
//
// Directives are line-attached: they sit anywhere in the source file,
// most naturally at the end-of-line on the offending statement or as
// a standalone comment near the top of the file. Substring matching
// trades over-specification for diagnostic-text stability; tighten
// only if a wrong-reason regression escapes.
package testdirectives

import (
	"bufio"
	"strings"
)

// ExpectedTypeError carries one `// expect type-error:` assertion.
type ExpectedTypeError struct {
	Text string
	Line int
}

// Directives carries the failure-class assertions found in a fixture.
// RuntimeError / TypeError are mutually exclusive in practice (an analyzer
// rejection short-circuits the runtime), but the parser doesn't enforce that.
type Directives struct {
	RuntimeError string // non-empty iff `// expect runtime-error:` present
	TypeError    string // last `// expect type-error:` text, for legacy callers
	TypeErrors   []ExpectedTypeError
}

const (
	runtimePrefix = "// expect runtime-error:"
	typePrefix    = "// expect type-error:"
)

// Parse scans `source` for failure directives. A line carries a
// directive iff its FIRST `//` (the comment introducer) is followed
// directly by one of the recognized prefixes — so a directive must be
// THE comment, not text inside a wider comment block. The remainder
// of the line after the prefix (trimmed) is the expected text. Runtime
// directives use last-write-wins; type-error directives accumulate so
// one negative fixture can assert every expected diagnostic.
//
// Examples of what does (✓) and doesn't (✗) match:
//
//	io.print(1d / 0d) // expect runtime-error: decimal …   ✓
//	  // expect type-error: refutable pattern…             ✓
//	// The `// expect runtime-error:` directive below…     ✗  (inside prose)
//	/// doc `// expect runtime-error:` …                   ✗  (doc comment)
func Parse(source string) Directives {
	var d Directives
	sc := bufio.NewScanner(strings.NewReader(source))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		idx := strings.Index(line, "//")
		if idx < 0 {
			continue
		}
		rest := line[idx:]
		switch {
		case strings.HasPrefix(rest, runtimePrefix):
			d.RuntimeError = strings.TrimSpace(rest[len(runtimePrefix):])
		case strings.HasPrefix(rest, typePrefix):
			text := strings.TrimSpace(rest[len(typePrefix):])
			d.TypeError = text
			d.TypeErrors = append(d.TypeErrors, ExpectedTypeError{
				Text: text,
				Line: lineNo,
			})
		}
	}
	return d
}
