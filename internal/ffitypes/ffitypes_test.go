package ffitypes

import "testing"

// TestNomiForGoSpellings covers the reverse direction the go/ast preflight
// walks: several Go spellings share one Nomi type, which is why that
// direction cannot be inverted to a check.
func TestNomiForGoSpellings(t *testing.T) {
	idents := map[string]string{
		"string":  NomiString,
		"bool":    NomiBool,
		"byte":    NomiByte,
		"uint8":   NomiByte,
		"int":     NomiInt,
		"int32":   NomiInt,
		"int64":   NomiInt,
		"uint64":  NomiInt,
		"float32": NomiFloat,
		"float64": NomiFloat,
		"any":     NomiDynamic,
	}
	for ident, want := range idents {
		got, ok := NomiForGoIdent(ident)
		if !ok || got != want {
			t.Errorf("NomiForGoIdent(%q) = %q, %v; want %q, true", ident, got, ok, want)
		}
	}
	for _, ident := range []string{"error", "uintptr", "complex128", "Duration", ""} {
		if got, ok := NomiForGoIdent(ident); ok {
			t.Errorf("NomiForGoIdent(%q) = %q, want no projection", ident, got)
		}
	}
	if got, ok := NomiForGoStdlibType("time", "Duration"); !ok || got != NomiDuration {
		t.Errorf("NomiForGoStdlibType(time.Duration) = %q, %v", got, ok)
	}
	if got, ok := NomiForGoStdlibType("time", "Time"); !ok || got != NomiInstant {
		t.Errorf("NomiForGoStdlibType(time.Time) = %q, %v", got, ok)
	}
	// A local type that merely spells `Time` is not time.Time.
	if got, ok := NomiForGoStdlibType("example.com/app", "Time"); ok {
		t.Errorf("NomiForGoStdlibType(app.Time) = %q, want no projection", got)
	}
}
