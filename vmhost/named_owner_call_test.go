package vmhost_test

import (
	"bytes"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A named argument to a std owner call or a host function is placed as a
// named argument to a Nomi-bodied qualified call is: operands evaluated in
// written order, then the call made in parameter order, with a slot left
// out before a supplied one taking its default.
func TestNamedArgs_StdOwnerAndHostCalls(t *testing.T) {
	out, err := runVM(t, `import { std/io std/calendar.Time }

fn mark(label: String, s: String): String {
    io.print(label)
    s
}

fn main() {
    s = "abc"
    dbg String.length(s: s)
    dbg String.trim(s: "  a ")
    dbg List.head(list: [1, 2])
    config = {"host" => "localhost"}
    dbg Map.get(m: config, key: "host")
    dbg Map.get(key: mark("key first", "host"), m: {mark("map second", "host") => "x"})
    xs = ["a", "b"]
    dbg String.join(parts: xs)
    dbg String.join(separator: "-", parts: xs)
    dbg String.join(xs, separator: "+")
    dbg Time.new(hour: 1, minute: 2, nanosecond: 5)
    dbg Time.new(minute: 2, hour: 1)
    io.print(value: "printed")
    io.inspect(value: [1, 2])
}
`)
	if err != nil {
		if b, blocked := vmhost.IsBlocked(err); blocked {
			t.Fatalf("blocked: %v (%q)", err, b.Reasons)
		}
		t.Fatal(err)
	}
	want := `dbg line 10: String.length(s: s) = 3
dbg line 11: String.trim(s: "  a ") = "a"
dbg line 12: List.head(list: [1, 2]) = Some(1)
dbg line 14: Map.get(m: config, key: "host") = Some("localhost")
key first
map second
dbg line 15: Map.get(key: mark("key first", "host"), m: {mark("map second", "host") => "x"}) = Some("x")
dbg line 17: String.join(parts: xs) = "ab"
dbg line 18: String.join(separator: "-", parts: xs) = "a-b"
dbg line 19: String.join(xs, separator: "+") = "a+b"
dbg line 20: Time.new(hour: 1, minute: 2, nanosecond: 5) = Ok(01:02:00.000000005)
dbg line 21: Time.new(minute: 2, hour: 1) = Ok(01:02:00)
printed
[1, 2]
`
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

// In an assertion subject a named std owner call records its rows as any
// call does: each written operand that is not a literal, then the call.
func TestNamedArgs_StdOwnerCallInAnAssertion(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "named_test.nomi"
	p, err := vmhost.LoadSource(name, `test "named owner calls pass" {
    s = "abc"
    config = {"host" => "localhost"}
    assert String.length(s: s) == 3
    assert Map.get(key: "host", m: config) == Some("localhost")
    assert String.join(separator: "-", parts: ["a", "b"]) == "a-b"
}

test "a named owner call's rows" {
    config = {"host" => "localhost"}
    assert Map.get(key: "port", m: config) == Some("80")
}

test "a named host call with a default's rows" {
    parts = ["a", "b"]
    assert String.join(parts: parts) == "a-b"
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	want := `ok named_test.nomi :: named owner calls pass
FAIL named_test.nomi :: a named owner call's rows
  line 11: assertion failed
    assert Map.get(key: "port", m: config) == Some("80")
    values:
      config
        = {"host" => "localhost"}
      Map.get(key: "port", m: config)
        = None
FAIL named_test.nomi :: a named host call with a default's rows
  line 16: assertion failed
    assert String.join(parts: parts) == "a-b"
    values:
      parts
        = ["a", "b"]
      String.join(parts: parts)
        = "ab"
test result: FAILED. 1 passed, 2 failed
`
	if got := buf.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
