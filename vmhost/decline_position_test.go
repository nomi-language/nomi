package vmhost

import (
	"strings"
	"testing"
)

// A decline taken outside `lower` (a tail `if` or `case`, a test-body
// statement) is reported at the innermost node the builder could not
// handle, not at the `fn` or the test's line. Each source here leans on
// gapPartialDefaultBeforeLaterPlaceholder (`span(b: _, _)`) to decline.
// When that gap is fixed, its cases here need another construct the
// builder declines at the same place.
//
// A pattern test (irDeclineAtPattern) reports the same way, but that path
// has no reproducer here. Add a pattern case when a real pattern decline
// turns up.
func TestDeclinePosition_IsTheInnermostNode(t *testing.T) {
	const span = `fn span(a: Int, b: Int = a * 2): (Int, Int) {
    (a, b)
}
`
	for _, c := range []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a call in a tail if's arm",
			src: `import std/io

` + span + `
fn first(x: Int): Int {
    if x == 1 {
        w = span(b: _, _)
        w(1, 2).0
    } else {
        0
    }
}

fn main() {
    io.inspect(first(1))
}
`,
			want: "main.nomi:9:13: this call to `span` is not supported yet, so `fn first` cannot run",
		},
		{
			name: "a call in a test body",
			src: span + `
test "loc" {
    w = span(b: _, _)
    assert w(1, 2).0 == 1
}
`,
			want: "main.nomi:6:9: this call to `span` is not supported yet, so test \"loc\" cannot run",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := checkLowers(t.TempDir(), c.src)
			if !o.accepted {
				t.Fatalf("the front end rejects this, so it no longer tests a decline's position: %s", o.rejection)
			}
			if o.internal != "" {
				t.Fatalf("the compiler panics:\n%s", o.internal)
			}
			if len(o.declines) != 1 {
				t.Fatalf("want one decline, got %q; if the gap this source leans on is fixed, "+
					"replace it with another decline at the same place", o.declines)
			}
			if !strings.HasPrefix(o.declines[0], c.want+"\n") {
				t.Fatalf("decline:\n%s\nwant it to start with %q", o.declines[0], c.want)
			}
		})
	}
}
