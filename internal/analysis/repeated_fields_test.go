package analysis_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

const repeatedFieldsDecls = `struct Point {
    x: Int
    y: Int
}

enum Shape {
    Rect{w: Int, h: Int}
    Dot
}

fn f(a: Int, b: Int): Int {
    a + b
}

`

// The body starts on this line of every source below.
const repeatedFieldsBodyLine = 16

// TestRepeatedFields_RejectedAtTheSecondName pins that each place a field
// list appears rejects a field given twice, at the second name, with a
// message naming what the author wrote. Each of these was accepted before
// (the anonymous literal and Struct.update's patch with another message),
// and a named literal reached the IR builder, which declined it.
func TestRepeatedFields_RejectedAtTheSecondName(t *testing.T) {
	cases := []struct {
		name string
		body string
		line int // relative to the body's first line
		col  int
		msg  string
	}{
		{"named literal", "    _p = Point{x: 1, y: 1, y: 3}", 0, 28, "field 'y' is given twice in this Point literal"},
		{"call-form literal", "    _p = Point({x: 1, y: 1, y: 3})", 0, 29, "field 'y' is given twice in this struct literal"},
		{"target-typed literal", "    _p: Point = {x: 1, y: 1, y: 3}", 0, 30, "field 'y' is given twice in this struct literal"},
		{"anonymous literal", "    _p = {x: 1, x: 2}", 0, 17, "field 'x' is given twice in this struct literal"},
		{"dot variant literal", "    _s: Shape = .Rect{w: 1, w: 2, h: 3}", 0, 29, "field 'w' is given twice in this Rect literal"},
		{"qualified variant literal", "    _s = Shape.Rect{w: 1, w: 2, h: 3}", 0, 27, "field 'w' is given twice in this Shape.Rect literal"},
		{"spread update", "    p = Point{x: 1, y: 1}\n    _q = {..p, y: 1, y: 2}", 1, 22, "field 'y' is given twice in this struct literal"},
		{"nested patch under a spread", "    p = {a: {x: 1, y: 1}}\n    _q = {..p, a: {y: 1, y: 2}}", 1, 26, "field 'y' is given twice in this struct literal"},
		{"Struct.update patch", "    p = Point{x: 1, y: 1}\n    _q = Struct.update(p, {y: 1, y: 2})", 1, 34, "field 'y' is given twice in this struct literal"},
		{"named pattern", "    p = Point{x: 1, y: 1}\n    case p {\n        Point{x: 1, x: 2} -> 1\n        _ -> 2\n    }", 2, 21, "field 'x' is given twice in this Point pattern"},
		{"named pattern with bindings", "    p = Point{x: 1, y: 1}\n    case p {\n        Point{x: a, x: _b} -> a\n    }", 2, 21, "field 'x' is given twice in this Point pattern"},
		{"variant pattern", "    s: Shape = .Rect{w: 1, h: 3}\n    case s {\n        .Rect{w: 1, w: 2, h} -> h\n        _ -> 2\n    }", 2, 21, "field 'w' is given twice in this Rect pattern"},
		{"anonymous pattern", "    p = Point{x: 1, y: 1}\n    case p {\n        {x: 1, x: 2} -> 1\n        _ -> 2\n    }", 2, 16, "field 'x' is given twice in this struct pattern"},
		{"destructuring binding", "    {x: a, x: _b} = Point{x: 1, y: 2}\n    a", 0, 12, "field 'x' is given twice in this struct pattern"},
		{"named argument", "    f(a: 1, a: 2, b: 3)", 0, 1, "parameter 'a' already has a value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := repeatedFieldsDecls + "fn run(): Int {\n" + tc.body + "\n}\n"
			_, errs := checkSourceWithStdlib(src)
			line := repeatedFieldsBodyLine + tc.line
			var all []string
			for _, e := range errs {
				all = append(all, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
				if e.Message == tc.msg && e.Line == line && e.Col == tc.col {
					return
				}
			}
			t.Fatalf("want %d:%d %q; the front end reported:\n  %s", line, tc.col, tc.msg, strings.Join(all, "\n  "))
		})
	}
}

// TestRepeatedFields_SpreadMayRepeatItsBase pins the one repetition that is
// the point of the syntax: a field after a spread replaces the base's.
func TestRepeatedFields_SpreadMayRepeatItsBase(t *testing.T) {
	src := repeatedFieldsDecls + `fn run(): Int {
    p = Point{x: 1, y: 1}
    q = {..p, y: 5}
    r = Struct.update(q, {y: 6})
    r.y
}
`
	_, errs := checkSourceWithStdlib(src)
	for _, e := range errs {
		t.Errorf("%d:%d %s", e.Line, e.Col, e.Message)
	}
}

// TestRepeatedFields_OnceForDeriveOptions pins that a repeated derive option
// keeps its own diagnostic and is not reported a second time as a field.
func TestRepeatedFields_OnceForDeriveOptions(t *testing.T) {
	src := `import std/json.{Json, ToJson}

struct User {
    first_name: String
}

derive ToJson for User with ToJson.Options{rename_all: Json.Case.Camel, rename_all: Json.Case.Camel}
`
	// checkSourceWithStdlib drops LowerDerives' errors, where the derive
	// option's own diagnostic is, so they are gathered here.
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	_, errs := analysis.LowerDerives(nodes)
	_, checkErrs := checkSourceWithStdlib(src)
	errs = append(errs, checkErrs...)
	var got []analysis.TypeError
	for _, e := range errs {
		if strings.Contains(e.Message, "rename_all") {
			got = append(got, e)
		}
	}
	if len(got) != 1 || got[0].Message != "duplicate derive option `rename_all`" {
		t.Fatalf("want one `duplicate derive option` error, got %v", got)
	}
}
