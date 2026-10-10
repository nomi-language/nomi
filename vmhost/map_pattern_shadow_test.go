package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A map destructure whose pattern binds a name already in scope runs, and a
// map pattern's key sees the names in scope before the pattern, never one the
// pattern binds, as the checker resolves it. In `{"mode" => a, a => b}` the
// second key is the `a` bound before the pattern ("host"), so b is the host's
// value, in a destructure, a `case` arm, a pattern `if` and a binding with
// `else`. A key that names the shadowed parameter is evaluated once.
func TestRun_MapPatternKeySeesTheScopeBeforeThePattern(t *testing.T) {
	out, err := runVM(t, `import std/io

fn note(s: String): String {
  io.print("key ${s}")
  s
}

fn constant_key(cfg: Map<String, String>) {
  a = "outer"
  io.print(a)
  {"host" => a} = cfg
  io.print("constant: ${a}")
}

fn computed_key(cfg: Map<String, String>) {
  a = "outer"
  io.print(a)
  {note("host") => a} = cfg
  io.print("computed: ${a}")
}

fn self_key(m: Map<String, String>) {
  a = "host"
  {a => a} = m
  io.print("self key: ${a}")
}

fn later_key(m: Map<String, String>) {
  a = "host"
  {"mode" => a, a => b} = m
  io.print("later key: ${a} ${b}")
}

fn param(a: String, m: Map<String, String>) {
  {note(a) => a, a => b} = m
  io.print("param: ${a} ${b}")
}

fn in_case(m: Map<String, String>): String {
  a = "host"
  case m {
    {"mode" => a, a => b} -> "case: ${a} ${b}"
    _ -> "case: no match"
  }
}

fn in_if(m: Map<String, String>): String {
  a = "host"
  if Some({"mode" => a, a => b}) = Some(m) {
    "if: ${a} ${b}"
  } else {
    "if: no match"
  }
}

fn in_else(m: Map<String, String>): String {
  a = "host"
  {"mode" => a, a => b} = m else {
    return "else: no match"
  }
  "else: ${a} ${b}"
}

fn main() {
  cfg = {"host" => "h1", "mode" => "debug", "debug" => "D"}
  constant_key(cfg)
  computed_key(cfg)
  self_key(cfg)
  later_key(cfg)
  param("host", cfg)
  io.print(in_case(cfg))
  io.print(in_if(cfg))
  io.print(in_else(cfg))
}
`)
	want := "outer\nconstant: h1\nouter\nkey host\ncomputed: h1\nself key: h1\nlater key: debug h1\n" +
		"key host\nparam: h1 h1\ncase: debug h1\nif: debug h1\nelse: debug h1\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}

// A destructure with a missing key still faults after rebinding names, and
// the earlier entries' keys ran first.
func TestRun_MapDestructureRebindMissingKeyFaults(t *testing.T) {
	out, err := runVM(t, `import std/io

fn note(s: String): String {
  io.print("key ${s}")
  s
}

fn main() {
  cfg = {"host" => "h1"}
  a = "outer"
  io.print(a)
  {note("host") => a, note("port") => b} = cfg
  io.print("${a} ${b}")
}
`)
	if err == nil || !strings.Contains(err.Error(), "port") || out != "outer\nkey host\nkey port\n" {
		t.Fatalf("output %q, error %v; want the two keys and a missing-key fault naming port", out, err)
	}
}

// In a test body, a map destructure may rebind a name the body bound, and its
// later key sees the earlier value.
func TestRun_MapDestructureRebindInATestBody(t *testing.T) {
	const name = "main_test.nomi"
	p, err := vmhost.LoadSource(name, `import std/io

test "map rebind" {
  m = {"host" => "h1", "mode" => "debug", "debug" => "D"}
  a = "host"
  io.print(a)
  {"mode" => a, a => b} = m
  io.print("${a} ${b}")
  assert b == "h1"
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the test: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	out := buf.String()
	if !strings.Contains(out, ":: map rebind\n  host\n  debug h1\n") || !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("want the two lines and one passing case:\n%s", out)
	}
}
