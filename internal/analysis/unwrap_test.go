package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// unwrapDecls are the declarations the postfix `!` cases read.
const unwrapDecls = `struct User {
    name: String
    nickname: Maybe<String>
}

fn half(n: Int) -> Result<Int, String> {
    if n % 2 == 0 {
        Ok(n / 2)
    } else {
        Err("odd")
    }
}

fn found(n: Int) -> Maybe<String> {
    if n > 0 { Some("yes") } else { None }
}

fn count(n: Int) -> Int {
    n
}

fn small(n: Int) -> Bool {
    n < 10
}
`

// A postfix `!`'s type is the payload of the Result's Ok or the Maybe's
// Some, on a call, a pipe stage, a field, a binding or a group.
func TestUnwrap_IsThePayloadsType(t *testing.T) {
	src := unwrapDecls + `
fn main() {
    u = #User{name: "a", nickname: Some("b")}
    maybe = found(2)
    a = half(4)!
    b = found(1)!
    c = 8 |> half()!
    d = Map.get(%{"k" => 1.5}, "k")!
    e = "3" |> String.to_int()! |> Int.abs()
    f = u.nickname!
    g = maybe!
    h = (8 |> half())! + 1
    _ = #(a, b, c, d, e, f, g, h)
}
`
	fa, errs := checkSourceWithStdlib(src)
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	want := map[string]string{"a": "Int", "b": "String", "c": "Int", "d": "Float", "e": "Int", "f": "String", "g": "String", "h": "Int"}
	got := map[string]string{}
	for n := range fa.ExprTypes {
		if b, ok := n.(*ast.Binding); ok {
			if ty := fa.ExprTypes[b.Value]; ty != nil {
				got[b.Name] = ty.String()
			}
		}
	}
	for name, ty := range want {
		if got[name] != ty {
			t.Errorf("%s: %q, want %s", name, got[name], ty)
		}
	}
}

// A `!` on anything but a Result or a Maybe is an error at the `!`; on a
// Bool, its hint says to write an assertion. `?` over a `!` whose payload
// is not a Result or a Maybe is `?`'s own error.
func TestUnwrap_RejectsOtherTypes(t *testing.T) {
	cases := []struct {
		body, msg, hint string
		col             int
	}{
		{"    _ = count(1)!", "`!` unwraps a Result or a Maybe, and `count(1)` is Int", "remove the `!`", 17},
		{"    _ = small(1)!", "`!` unwraps a Result or a Maybe, and `small(1)` is Bool", "write `assert small(1)`", 17},
		{"    _ = [1] |> Iter.count()!", "`!` unwraps a Result or a Maybe, and `[1] |> Iter.count()` is Int", "remove the `!`", 28},
	}
	for _, tc := range cases {
		_, errs := checkSourceWithStdlib(unwrapDecls + "\nfn main() {\n" + tc.body + "\n}\n")
		var hit bool
		for _, e := range errs {
			if e.Message == tc.msg {
				hit = true
				if e.Col != tc.col || len(e.Hints) == 0 || !strings.Contains(e.Hints[0], tc.hint) {
					t.Errorf("%s: at column %d with hints %q, want column %d and %q", tc.body, e.Col, e.Hints, tc.col, tc.hint)
				}
			}
		}
		if !hit {
			t.Errorf("%s: no %q in %v", tc.body, tc.msg, errs)
		}
	}
	_, errs := checkSourceWithStdlib(unwrapDecls + "\nfn g() -> Result<Int, String> {\n    Ok(half(4)!?)\n}\n")
	var tryErr bool
	for _, e := range errs {
		tryErr = tryErr || strings.Contains(e.Message, "`?` requires a Result or Maybe, got Int")
	}
	if !tryErr {
		t.Errorf("`half(4)!?`: %v", errs)
	}
}
