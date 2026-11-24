package std

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// std/code_points.nomi's module doc names the Unicode version its `is_`
// functions answer by. They read Go's unicode tables (rt/codepoint.go), so
// the version is the toolchain's: a toolchain with another one fails here
// until the doc names it.
func TestCodePointsNameTheToolchainsUnicodeVersion(t *testing.T) {
	src, err := os.ReadFile("code_points.nomi")
	if err != nil {
		t.Fatal(err)
	}
	var doc strings.Builder
	for _, line := range strings.Split(string(src), "\n") {
		if text, ok := strings.CutPrefix(line, "//#"); ok {
			doc.WriteString(text)
			doc.WriteString(" ")
		}
	}
	named := regexp.MustCompile(`Unicode ([0-9]+\.[0-9]+\.[0-9]+)`).FindAllStringSubmatch(doc.String(), -1)
	if len(named) == 0 {
		t.Fatalf("std/code_points.nomi's module doc names no Unicode version; the toolchain's is %s", unicode.Version)
	}
	for _, m := range named {
		if m[1] != unicode.Version {
			t.Errorf("std/code_points.nomi's module doc names Unicode %s; the toolchain's tables are Unicode %s", m[1], unicode.Version)
		}
	}
}
