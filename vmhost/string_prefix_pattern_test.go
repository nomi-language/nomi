package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A string prefix pattern (`"/users/" + id`) runs wherever a pattern is
// tested: a `case` arm, a pattern `if` as a value and as a statement, an
// `else` binding, a lambda, and nested in a variant payload, a tuple, a map
// value, a struct field, a list head and under `as`. Each matches once and
// misses once. The rest binds the bytes after the literal, so it may be
// empty, a non-ASCII literal matches its own bytes, and a rest may start
// with a combining mark that follows the literal's last codepoint.
func TestRun_StringPrefixPatternRunsWherePatternsAreTested(t *testing.T) {
	out, err := runVM(t, `import std/io

struct Req {
  path: String
  n: Int
}

fn route(path: String): String {
  case path {
    "/users/" + id -> "user [${id}]"
    "/health" -> "health"
    "" + rest -> "other [${rest}]"
    _ -> "unreachable"
  }
}

fn pattern_if(path: String): String {
  if "/users/" + id = path {
    "if user ${id}"
  } else {
    "if miss"
  }
}

fn pattern_if_statement(path: String) {
  if "/a/" + rest = path {
    io.print("statement ${rest}")
  }
  io.print("after")
}

fn else_binding(path: String): String {
  "é" + rest = path else { return "else miss" }
  "else [${rest}]"
}

fn payload(m: Maybe<String>): String {
  case m {
    Some("héllo " + who) -> "greet ${who}"
    Some(s) -> "some ${s}"
    None -> "none"
  }
}

fn tuple(t: (String, Int)): String {
  case t {
    ("k:" + key, n) -> "${key}=${n}"
    _ -> "tuple miss"
  }
}

fn map_value(m: Map<String, String>): String {
  case m {
    {"auth" => "Bearer " + token} -> "token ${token}"
    _ -> "map miss"
  }
}

fn field(r: Req): String {
  case r {
    Req{path: "/v1/" + rest, n} -> "v1 ${rest} ${n}"
    _ -> "field miss"
  }
}

fn list_head(xs: List<String>): String {
  case xs {
    ["-" + flag, ..rest] -> "flag ${flag} +${Iter.count(rest)}"
    _ -> "list miss"
  }
}

fn as_whole(path: String): String {
  case path {
    "x" + rest as whole -> "${rest}|${whole}"
    _ -> "as miss"
  }
}

fn escapes(s: String): String {
  case s {
    "a\tb" + r -> "tab ${r}"
    `+"`c\\d`"+` + r -> "raw ${r}"
    _ -> "escape miss"
  }
}

fn main() {
  io.print(route("/users/42"))
  io.print(route("/users/"))
  io.print(route("/health"))
  io.print(route("/usersx"))
  io.print(pattern_if("/users/7"))
  io.print(pattern_if("/user/7"))
  pattern_if_statement("/a/b")
  pattern_if_statement("/b/a")
  io.print(else_binding("éa"))
  io.print(else_binding("é"))
  io.print(else_binding("e\u{301}a"))
  io.print(payload(Some("héllo wörld")))
  io.print(payload(Some("hello")))
  io.print(payload(None))
  io.print(tuple(("k:id", 3)))
  io.print(tuple(("id", 3)))
  io.print(map_value({"auth" => "Bearer abc"}))
  io.print(map_value({"auth" => "Basic abc"}))
  io.print(field(Req{path: "/v1/items", n: 2}))
  io.print(field(Req{path: "/v2/items", n: 2}))
  io.print(list_head(["-v", "a", "b"]))
  io.print(list_head(["v"]))
  io.print(as_whole("xyz"))
  io.print(as_whole("yz"))
  io.print(escapes("a\tbZ"))
  io.print(escapes("c\\dQ"))
  io.print(escapes("ab"))
  strip = |s: String| if "e" + r = s { "[${r}]" } else { "none" }
  io.print(strip("e\u{301}"))
  io.print(strip("f"))
}
`)
	want := strings.Join([]string{
		"user [42]",
		"user []",
		"health",
		"other [/usersx]",
		"if user 7",
		"if miss",
		"statement b",
		"after",
		"after",
		"else [a]",
		"else []",
		"else miss",
		"greet wörld",
		"some hello",
		"none",
		"id=3",
		"tuple miss",
		"token abc",
		"map miss",
		"v1 items 2",
		"field miss",
		"flag v +2",
		"list miss",
		"yz|xyz",
		"as miss",
		"tab Z",
		"raw Q",
		"escape miss",
		"[́]",
		"none",
	}, "\n") + "\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}

// In a test body, a string prefix pattern runs in a `case` arm and a pattern
// `if`, matched and not.
func TestRun_StringPrefixPatternInATestBody(t *testing.T) {
	const name = "main_test.nomi"
	p, err := vmhost.LoadSource(name, `import std/io

test "string prefix" {
  r = case "/users/9" {
    "/users/" + id -> id
    _ -> "none"
  }
  if "/x" + rest = "/y" {
    io.print("unexpected ${rest}")
  }
  io.print("id ${r}")
  assert r == "9"
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
	if !strings.Contains(out, ":: string prefix\n  id 9\n") || !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("want the line and one passing case:\n%s", out)
	}
}
