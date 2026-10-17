package vmhost

import (
	"strings"
	"testing"
)

// A decline taken outside `lower` (a tail `if` or `case`, a test-body
// statement) is reported at the innermost node the builder could not
// handle, not at the `fn` or the test's line. Each source here declines at
// its call to `planted_decline` (plantDecline).
//
// A pattern test (irDeclineAtPattern) reports the same way, but that path
// has no reproducer here. Add a pattern case when a real pattern decline
// turns up.
func TestDeclinePosition_IsTheInnermostNode(t *testing.T) {
	plantDecline(t)
	const planted = `fn planted_decline(a: Int) -> Int {
    a
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

` + planted + `
fn first(x: Int) -> Int {
    if x == 1 {
        w = planted_decline(1)
        w
    } else {
        0
    }
}

fn main() {
    io.inspect(first(1))
}
`,
			want: "main.nomi:9:13: this call to `planted_decline` is not supported yet, so `fn first` cannot run",
		},
		{
			name: "a call in a test body",
			src: planted + `
test "loc" {
    w = planted_decline(1)
    assert w == 1
}
`,
			want: "main.nomi:6:9: this call to `planted_decline` is not supported yet, so test \"loc\" cannot run",
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
				t.Fatalf("want one decline, got %q", o.declines)
			}
			if !strings.HasPrefix(o.declines[0], c.want+"\n") {
				t.Fatalf("decline:\n%s\nwant it to start with %q", o.declines[0], c.want)
			}
		})
	}
}
