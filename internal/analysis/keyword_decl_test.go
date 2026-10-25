package analysis_test

import (
	"strings"
	"testing"
)

// A keyword declaration whose keyword Nomi does not know is refused at the
// keyword.
func TestKeywordDecl_UnknownKeywordsAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
		line, col       int
	}{
		{"unknown", "database main\n", "unknown declaration keyword `database`", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			got := errorsAt(errs, tc.line, tc.col)
			if len(got) != 1 || !strings.HasPrefix(got[0], tc.want+"\n") {
				t.Fatalf("errors at %d:%d = %q, want exactly [%q]; all: %v", tc.line, tc.col, got, tc.want, errs)
			}
		})
	}
}
