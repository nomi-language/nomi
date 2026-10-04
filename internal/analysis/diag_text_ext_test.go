package analysis_test

import "github.com/nomi-language/nomi/internal/analysis"

// diagText is a diagnostic's message followed by its hints, one
// `help:` line each, so a test can pin both in one string.
func diagText(e analysis.TypeError) string {
	s := e.Message
	for _, h := range e.Hints {
		s += "\nhelp: " + h
	}
	return s
}
