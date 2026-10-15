package format

import (
	"strings"
	"testing"
)

// A group written without a name is a parse error, so the formatter leaves
// the file as it is. It used to read `group` as an identifier and the
// braces as a separate block, and wrote `group`, a blank line, then `{`.
func TestFormat_LeavesAGroupWithoutANameAlone(t *testing.T) {
	src := "group {\n    test \"a\" {\n        assert 1 == 1\n    }\n}\n"
	out, err := Format(src)
	if err == nil {
		t.Fatalf("Format accepted a group without a name; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "a `group` needs a name") {
		t.Errorf("Format error = %v, want the missing-name error", err)
	}
	if out != src {
		t.Errorf("Format changed the file:\n%s", out)
	}
}
