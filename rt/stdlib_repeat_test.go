package rt

import (
	"strings"
	"testing"
)

// String.repeat refuses a result past maxStringBytes with a trap that names
// the count, where a count whose bytes still fit an int went on to allocate
// them and ended the process with Go's out-of-memory report.
func TestStringRepeat_RefusesAResultMemoryCannotHold(t *testing.T) {
	for _, count := range []Int{SmallInt(100_000_000_000_000), SmallInt(maxStringBytes/2 + 1)} {
		func() {
			defer func() {
				e, ok := recover().(*Error)
				if !ok || !strings.Contains(e.Error(), "are more than memory holds") {
					t.Fatalf("String.repeat(\"ab\", %s) did not trap with the message: %v", count, e)
				}
			}()
			StringRepeat("ab", count)
		}()
	}
	if got := StringRepeat("ab", SmallInt(3)); got != "ababab" {
		t.Fatalf("String.repeat(\"ab\", 3) = %q", got)
	}
	if got := StringRepeat("", SmallInt(100_000_000_000_000)); got != "" {
		t.Fatalf("the empty string repeated is %q", got)
	}
}
