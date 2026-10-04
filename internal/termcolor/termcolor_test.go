package termcolor

import (
	"os"
	"testing"
)

func TestNomiLineColorsAssertAsKeyword(t *testing.T) {
	t.Setenv("NOMI_COLOR", "always")
	got := NomiLineFor(os.Stdout, "assert has_four")
	want := ansiMagenta + "assert" + ansiReset + " " + ansiBlue + "has_four" + ansiReset
	if got != want {
		t.Fatalf("assert keyword color mismatch\nwant: %q\n got: %q", want, got)
	}
}
