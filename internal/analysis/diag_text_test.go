package analysis

import "testing"

// diagText is a diagnostic's message followed by its hints, one
// `help:` line each, so a test can pin both in one string.
func diagText(e TypeError) string {
	s := e.Message
	for _, h := range e.Hints {
		s += "\nhelp: " + h
	}
	return s
}

// requirePosition fails t when e, the diagnostic a rejection test matched,
// names no line: the user would read its message with nowhere to look.
func requirePosition(t *testing.T, e TypeError) {
	t.Helper()
	if e.Line <= 0 {
		t.Fatalf("diagnostic carries no position: %s", e.Message)
	}
}
