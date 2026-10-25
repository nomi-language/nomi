package nodeenv

import (
	"slices"
	"testing"
)

func TestMinimalKeepsWhatNodeNeedsAndDropsTheRest(t *testing.T) {
	env := []string{"PATH=/bin", "BIG=xxxx", "HOME=/h", "NOMI_COLOR=auto", "GOROOT=/go", "TMPDIR=/t", "NOMI_COLOR=never", "noequals"}
	got := Minimal(env, "GOROOT")
	want := []string{"PATH=/bin", "HOME=/h", "NOMI_COLOR=never", "GOROOT=/go", "TMPDIR=/t"}
	if !slices.Equal(got, want) {
		t.Fatalf("Minimal = %q, want %q", got, want)
	}
	if got := Minimal(env); slices.Contains(got, "GOROOT=/go") || slices.Contains(got, "BIG=xxxx") {
		t.Fatalf("Minimal kept a variable it was not asked to: %q", got)
	}
}
