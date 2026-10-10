package analysis_test

import (
	"strings"
	"testing"
)

// A file's scope binds its own name (the implicit self binding) and the
// files it imports whole, but neither is a member another file reaches
// through it. `io.io.print("x")` checked clean, recorded no type for the
// call, and the IR builder declined it ("qualified call with a 3-segment
// qualifier"). The error is at the segment that names nothing.
func TestModuleNameIsNotAFileMember(t *testing.T) {
	cases := []struct {
		name string
		body string
		line int
		col  int
	}{
		{"call", `  io.io.print("x")`, 4, 6},
		{"longer call", `  io.io.io.print("x")`, 4, 6},
		{"function value", `  f = io.io.print`, 4, 10},
		{"module value", `  g = io.io`, 4, 10},
		{"argument", `  io.print(io.io.print)`, 4, 15},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "import std/io\n\nfn main() {\n" + tc.body + "\n}\n"
			_, errs := checkSourceWithStdlib(src)
			if len(errs) == 0 {
				t.Fatalf("the front end admits this:\n%s", src)
			}
			var got []string
			for _, e := range errs {
				got = append(got, e.Message)
				if e.Message == "file 'io' has no member 'io'" && e.Line == tc.line && e.Col == tc.col {
					return
				}
			}
			t.Fatalf("want \"file 'io' has no member 'io'\" at %d:%d, got:\n  %s", tc.line, tc.col, strings.Join(got, "\n  "))
		})
	}
}
