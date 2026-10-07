package vmhost

import (
	"bytes"
	"testing"
)

// A type declared in a `//!` test, on a declaration or on an impl's item,
// is typed as one declared in a `test` body is: its constructor's call has a
// type, a value wrapping it is locally determined, and an annotation names it.
const attachedTestTypes = `import std/io

struct User {
    name: String
}

impl User {
    //! type Tag String
    //! t: Tag = Tag(User.greet(User{name: "a"}))
    //! io.print(String(t))
    //! assert t == Tag("hi a")
    pub fn greet(u: User): String {
        "hi ${u.name}"
    }
}

//! type TraceId String
//! traced = Some(TraceId("trace-123"))
//! io.inspect(traced)
//! assert traced == Some(TraceId("trace-123"))
fn answer(): Int {
    42
}
`

func TestAttachedTestTypes_Run(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "attached.nomi"
	p, err := LoadSource(name, attachedTestTypes)
	if err != nil {
		t.Fatalf("the front end or the builder rejects this: %v", err)
	}
	var buf bytes.Buffer
	rep := NewTestReport(&buf)
	p.Test(&buf, rep, name, TestOptions{}, func(n string) string { return TestName(name, n) })
	failed := rep.Summary()
	const want = `hi a
Some(TraceId("trace-123"))
ok attached.nomi :: impl / greet //! test lines 8-11
ok attached.nomi :: answer //! test lines 17-20
test result: ok. 2 passed, 0 failed
`
	if got := buf.String(); failed || got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
