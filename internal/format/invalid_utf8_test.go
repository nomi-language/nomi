package format

import (
	"strings"
	"testing"
)

// A string literal holding a byte that is not UTF-8 does not parse, so the
// formatter leaves the source as it is. It used to write the byte back as
// U+FFFD, a different string.
func TestFormat_InvalidUTF8StringIsAnError(t *testing.T) {
	for _, src := range []string{"x = \"\x81\"\n", "x = `\x81`\n", "test \"a\xffb\" {}\n"} {
		got, err := Format(src)
		if err == nil {
			t.Errorf("Format(%q) succeeds", src)
		} else if !strings.HasPrefix(src, "test") && !strings.Contains(err.Error(), "UTF-8") {
			t.Errorf("Format(%q) error = %v, want one about UTF-8", src, err)
		}
		if got != src {
			t.Errorf("Format(%q) = %q, want the source unchanged", src, got)
		}
	}
}
