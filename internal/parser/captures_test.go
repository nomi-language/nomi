package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A capture is `&` against a call, a parenthesized expression or a literal,
// and its body is one postfix-level operand: `&f(&1) + 1` adds to the
// capture.
func TestParseCapture_Bodies(t *testing.T) {
	for _, tc := range []struct {
		src, body string
		partial   bool
	}{
		{"&Int.max(&1, 0)", "*ast.Call", true},
		{"&add(&1, b: &2)", "*ast.Call", true},
		{"&f(&2, &1)", "*ast.Call", false},
		{"&f(&1, &1)", "*ast.Call", false},
		{"&Some(&1 + 1)", "*ast.Call", false},
		{"&.Circle(&1)", "*ast.Call", true},
		{"&(&1 > 2)", "*ast.GroupedExpr", false},
		{"&(add(&1, 2))", "*ast.GroupedExpr", false},
		{"&(&2)", "*ast.GroupedExpr", false},
		{"&#Point{x: &1, y: 0}", "*ast.StructLit", false},
		{"&#.Circle{radius: &1}", "*ast.StructLit", false},
		{"&#(&1, 0)", "*ast.TupleLit", false},
		{"&[&1, 0]", "*ast.ListLit", false},
		{"&#[&1]", "*ast.VectorLit", false},
		{"&%[&1]", "*ast.SetLit", false},
		{"&%{&1 => 0}", "*ast.MapLit", false},
	} {
		t.Run(tc.src, func(t *testing.T) {
			c, ok := parseExpr(t, tc.src).(*ast.Capture)
			if !ok {
				t.Fatalf("%q is no capture", tc.src)
			}
			if got := fmt.Sprintf("%T", c.Body); got != tc.body {
				t.Errorf("body %s, want %s", got, tc.body)
			}
			if partial := c.PartialCall() != nil; partial != tc.partial {
				t.Errorf("partial = %v, want %v", partial, tc.partial)
			}
		})
	}
	b, ok := parseExpr(t, "&f(&1) + 1").(*ast.Binary)
	if !ok || b.Op != "+" {
		t.Fatalf("`&f(&1) + 1` is no sum")
	}
	if _, ok := b.Left.(*ast.Capture); !ok {
		t.Errorf("the sum's left operand is %T, want the capture", b.Left)
	}
}

// Each `&N` belongs to the capture around it: an inner capture's and a
// lambda's are not the outer one's.
func TestParseCapture_HolesAreItsOwn(t *testing.T) {
	c := parseExpr(t, "&f(&1, &g(&2), fn x -> &3)").(*ast.Capture)
	var got []int
	for _, h := range c.Holes() {
		got = append(got, ast.HoleNumber(h))
	}
	if fmt.Sprint(got) != "[1]" {
		t.Errorf("holes %v, want [1]", got)
	}
}

// `then` and `tap` take a capture as well as a lambda; the capture ends
// with its body, so the stages after it stay in the pipeline.
func TestParseCapture_ThenAndTap(t *testing.T) {
	b := parseExpr(t, "10 |> then &divide(100, &1) |> dbg").(*ast.Binary)
	inner, ok := b.Left.(*ast.Binary)
	if !ok {
		t.Fatalf("the pipeline's head is %T, want the `then` pipe", b.Left)
	}
	if then, ok := inner.Right.(*ast.Then); !ok || then.Capture == nil || then.Lambda != nil {
		t.Fatalf("stage %T, want a `then` applying a capture", inner.Right)
	}
	b = parseExpr(t, "x |> tap &io.print(&1)").(*ast.Binary)
	if tap, ok := b.Right.(*ast.Tap); !ok || tap.Capture == nil {
		t.Fatalf("stage %T, want a `tap` applying a capture", b.Right)
	}
}

// `&` against anything that starts no capture's body is an error, and so
// is `&` written apart from its body.
func TestParseCapture_Errors(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"f = &x", "a capture is `&` and a call, a parenthesized expression or a literal, as in `&f(&1)` or `&(&1 > 2)`"},
		{"f = &Int.max", "a capture is `&` and a call, a parenthesized expression or a literal"},
		{"f = &f(&1).x", "a capture is `&` and a call, a parenthesized expression or a literal"},
		{"x = 5 |> then &x", "a capture is `&` and a call, a parenthesized expression or a literal"},
		{"f = & (&1 + 1)", "a capture's `&` is written against its body, with no space"},
	} {
		t.Run(tc.src, func(t *testing.T) {
			_, err := Parse(lexer.Lex(tc.src))
			if err == nil {
				t.Fatalf("%q parses; want the error %q", tc.src, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want it to contain %q", err, tc.want)
			}
		})
	}
}
