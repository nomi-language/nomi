package rt

import (
	"strings"
	"testing"
)

// A `!` trap names the expression, cut after 60 characters.
func TestBangTraps(t *testing.T) {
	if got := BangNoneError(9, `Map.get(counts, "z")`).Error(); got != "line 9: `Map.get(counts, \"z\")` found nothing" {
		t.Errorf("None: %q", got)
	}
	if got := BangFailedError(4, "half(3)", "3 is odd").Error(); got != "line 4: `half(3)` failed: 3 is odd" {
		t.Errorf("Err: %q", got)
	}
	long := "f(" + strings.Repeat("x", 70) + ")"
	want := "line 2: `" + long[:60] + "...` found nothing"
	if got := BangNoneError(2, long).Error(); got != want {
		t.Errorf("long: %q, want %q", got, want)
	}
}
