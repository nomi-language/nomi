package vmhost

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// A partial application's `_` is an argument like any other, so the call
// is held to the callee's argument count. The front end skipped the count
// for any call with a `_`, accepted `pick(False, _, x, 0)` against a
// three-parameter pick and `add(_)` against a two-parameter add, and the
// IR builder then declined each as a partial it could not see.
func TestPartialApplication_ArgumentCountIsChecked(t *testing.T) {
	cases := []struct {
		name, call, want string
	}{
		{"generic callee, too many", "pick(False, _, case Some(1) {\n        .Some(o) -> o\n        .None -> 0\n    }, 0)", "expected 3 arguments, got 4"},
		{"plain callee, too many", "add(_, 1, 2)", "expected 2 arguments, got 3"},
		{"plain callee, too few", "add(_)", "expected 2 arguments, got 1"},
		{"generic callee, too few", "pick(False, _)", "expected 3 arguments, got 2"},
		{"defaulted callee, too many", "connect(_, 1, 2, 3)", "expected 1 to 3 arguments, got 4"},
		{"required slot skipped by a default", "middle(_, 2)", "missing argument for parameter 'c'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `import std/io

fn pick<T>(c: Bool, a: T, b: T): T {
    if c { a } else { b }
}

fn add(a: Int, b: Int): Int {
    a + b
}

fn connect(host: Int, port: Int = 80, timeout: Int = 30): Int {
    host + port + timeout
}

fn middle(a: Int, b: Int = 10, c: Int): Int {
    a + b + c
}

fn main() {
    io.inspect(pick(True, 1, 2))
    io.inspect(add(1, 2))
    io.inspect(connect(1))
    io.inspect(middle(1, c: 2))
    p = ` + tc.call + `
    io.inspect(p(5))
}
`
			o := checkLowers(t.TempDir(), src)
			if o.accepted {
				t.Fatalf("the front end accepts %s", tc.call)
			}
			if !strings.Contains(o.rejection, tc.want) {
				t.Fatalf("rejection:\n%s\nwant it to contain %q", o.rejection, tc.want)
			}
		})
	}
}

// The written arguments of a partial application are evaluated once, when
// the partial is made, in source order, however often it is called; and a
// partial that gives every slot, with defaults left to the callee, runs.
func TestPartialApplication_WrittenArgumentsRunOnce(t *testing.T) {
	src := `import std/io

fn pick<T>(c: Bool, a: T, b: T): T {
    if c { a } else { b }
}

fn noisy(label: String, n: Int): Int {
    io.print(label)
    n
}

fn connect(host: String, port: Int = 80, timeout: Int = 30): String {
    "${host}:${port}/${timeout}"
}

fn main() {
    p = pick(
        noisy("c", 0) == 1,
        _,
        case Some(noisy("b", 7)) {
            .Some(o) -> o
            .None -> 0
        },
    )
    io.print("made")
    io.inspect(p(1))
    io.inspect(p(2))
    f = connect(_, port: _)
    io.print(f("h", 8080))
    io.print(f("h"))
}
`
	o := checkLowers(t.TempDir(), src)
	if !o.accepted {
		t.Fatalf("the front end rejects this:\n%s", o.rejection)
	}
	p, err := LoadSource("main.nomi", src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\noutput so far:\n%s", err, out.String())
	}
	want := "c\nb\nmade\n7\n7\nh:8080/30\nh:80/30\n"
	if got := out.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
