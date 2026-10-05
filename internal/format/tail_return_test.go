package format

import (
	"reflect"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// A `return` that is the last thing a function or lambda body does is its
// value, so the formatter writes the value alone. Every case is checked for
// its text, for idempotence, and for an AST equal to the source's with the
// same returns stripped.
func TestFormat_StripsTailReturn(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{
			"function tail, comments kept",
			"fn a(x: Int): Int {\n    y = x + 1\n    // the answer\n    return y // trailing\n}\n",
			"fn a(x: Int): Int {\n    y = x + 1\n    // the answer\n    y // trailing\n}\n",
		},
		{
			"every branch of a tail if chain",
			"fn b(x: Int): Int {\n    if x > 0 {\n        return 1\n    } else if x < 0 {\n        return -1\n    } else {\n        return 0\n    }\n}\n",
			"fn b(x: Int): Int {\n    if x > 0 {\n        1\n    } else if x < 0 {\n        -1\n    } else {\n        0\n    }\n}\n",
		},
		{
			"case arms and a nested tail if",
			"fn c(x: Maybe<Int>): Int {\n    case x {\n        Some(n) -> {\n            if n > 0 {\n                return n\n            } else {\n                y = n * 2\n                return y\n            }\n        }\n        None -> return 0\n    }\n}\n",
			"fn c(x: Maybe<Int>): Int {\n    case x {\n        Some(n) -> {\n            if n > 0 {\n                n\n            } else {\n                y = n * 2\n                y\n            }\n        }\n\n        None ->\n            0\n    }\n}\n",
		},
		{
			"a case arm's comment stays on the arm",
			"fn d(x: Maybe<Int>): Int {\n    case x {\n        Some(n) -> return n // note\n        None -> 0\n    }\n}\n",
			"fn d(x: Maybe<Int>): Int {\n    case x {\n        Some(n) -> n // note\n        None -> 0\n    }\n}\n",
		},
		{
			"early return before the tail stays",
			"fn e(x: Int): Int {\n    if x > 0 {\n        return 1\n    }\n    return 2\n}\n",
			"fn e(x: Int): Int {\n    if x > 0 { return 1 }\n    2\n}\n",
		},
		{
			"lambda and callback tails; break and early return stay",
			"fn f(xs: List<Int>): List<Int> {\n    xs |> Iter.map(|n| {\n        if n > 10 { break n }\n        if n < 0 { return 0 }\n        return n * 3\n    }) |> Iter.to_list()\n}\n",
			"fn f(xs: List<Int>): List<Int> {\n    xs\n    |> Iter.map(|n| {\n        if n > 10 { break n }\n        if n < 0 { return 0 }\n        n * 3\n    })\n    |> Iter.to_list()\n}\n",
		},
		{
			"one-statement lambda",
			"fn g(xs: List<Int>): List<Int> {\n    xs |> Iter.map(|n| { return n + 1 }) |> Iter.to_list()\n}\n",
			"fn g(xs: List<Int>): List<Int> {\n    xs |> Iter.map(|n| n + 1) |> Iter.to_list()\n}\n",
		},
		{
			"then lambda keeps its braces",
			"fn h(n: Int): Int {\n    n\n    |> then |v| { return v * 2 }\n}\n",
			"fn h(n: Int): Int {\n    n\n    |> then |v| { v * 2 }\n}\n",
		},
		{
			"returned lambda",
			"fn i(): (Int) -> Int {\n    return |x| x + 1\n}\n",
			"fn i(): (Int) -> Int {\n    |x| x + 1\n}\n",
		},
		{
			"impl function",
			"struct P {\n    x: Int\n}\n\nimpl P {\n    fn x(p: P): Int {\n        return p.x\n    }\n}\n",
			"struct P {\n    x: Int\n}\n\nimpl P {\n    fn x(p: P): Int {\n        p.x\n    }\n}\n",
		},
		{
			"bare return after a Unit statement goes, recursively",
			"fn j(x: Int) {\n    if x > 0 {\n        io.print(\"pos\")\n        return\n    } else {\n        io.print(\"neg\")\n    }\n    return\n}\n",
			"fn j(x: Int) {\n    if x > 0 {\n        io.print(\"pos\")\n    } else {\n        io.print(\"neg\")\n    }\n}\n",
		},
		{
			"bare return in an if with no else",
			"fn k(x: Int) {\n    if x > 0 {\n        io.print(\"pos\")\n        return\n    }\n}\n",
			"fn k(x: Int) {\n    if x > 0 { io.print(\"pos\") }\n}\n",
		},
		{
			"value opening with a brace",
			"fn i(): {n: Int} {\n    n = 1\n    m = n\n    return {n: m}\n}\n",
			"fn i(): {n: Int} {\n    n = 1\n    m = n\n    {n: m}\n}\n",
		},
		{
			"struct update after a binding",
			"fn i2(p: P): P {\n    q = p\n    return {..q, x: q.x + 1}\n}\n",
			"fn i2(p: P): P {\n    q = p\n    {..q, x: q.x + 1}\n}\n",
		},
		{
			"brace values in case arms and if branches",
			"fn j(x: Int): {n: Int} {\n    case x {\n        0 -> return {n: 0}\n        _ -> {\n            if x > 0 {\n                return {n: x}\n            } else {\n                return {n: 1}\n            }\n        }\n    }\n}\n",
			"fn j(x: Int): {n: Int} {\n    case x {\n        0 ->\n            {n: 0}\n\n        _ -> {\n            if x > 0 { {n: x} } else { {n: 1} }\n        }\n    }\n}\n",
		},
		{
			"test body",
			"test \"t\" {\n    assert 1 == 1\n    return\n}\n",
			"test \"t\" {\n    assert 1 == 1\n}\n",
		},
		{
			"value return at a test body's end",
			"test \"t\" {\n    assert 2 > 1\n    if 1 > 2 { return }\n    return testing.check(1 == 1)\n}\n",
			"test \"t\" {\n    assert 2 > 1\n    if 1 > 2 { return }\n    testing.check(1 == 1)\n}\n",
		},
		{
			"bare return in a test body's tail if",
			"test \"t\" {\n    assert 1 == 1\n    if 1 > 0 {\n        io.print(\"a\")\n        return\n    }\n}\n",
			"test \"t\" {\n    assert 1 == 1\n    if 1 > 0 { io.print(\"a\") }\n}\n",
		},
		{
			"test in a group",
			"tests \"g\" {\n    test \"t\" {\n        assert 1 == 1\n        return\n    }\n}\n",
			"tests \"g\" {\n    test \"t\" {\n        assert 1 == 1\n    }\n}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkTailReturnCase(t, tc.src, tc.want)
		})
	}
}

// What syntax alone cannot show is safe stays as written.
func TestFormat_KeepsTailReturnItCannotStrip(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"bare return alone in a body", "fn a() {\n    return\n}\n"},
		{"bare return alone in a branch", "fn b(x: Int) {\n    if x > 0 { return } else { io.print(\"x\") }\n}\n"},
		{"bare return after a binding", "fn c() {\n    _ = 1\n    return\n}\n"},
		{"bare return after dbg", "fn d() {\n    dbg 1\n    return\n}\n"},
		{"bare return after a dbg stage", "fn e() {\n    1 |> dbg\n\n    return\n}\n"},
		{"bare return with a comment", "fn f() {\n    io.print(\"a\")\n    // done\n    return\n}\n"},
		{"bare return as a case arm", "fn h(x: Int) {\n    case x {\n        0 -> return\n        _ -> io.print(\"x\")\n    }\n}\n"},
		{"concurrent block", "fn k(): Int {\n    concurrent {\n        t = Task.spawn(|| 1)\n        return Task.await(t)\n    }\n}\n"},
		{"value return in a test body's tail if", "test \"t\" {\n    assert 1 == 1\n    if 1 > 0 { return 1 } else { return \"a\" }\n}\n"},
		{"value return in a test body's tail case arm", "test \"t\" {\n    assert 1 == 1\n\n    case 1 {\n        0 -> return 1\n        _ -> return \"a\"\n    }\n}\n"},
		{"group setup", "tests \"g\" {\n    setup {\n        x = 1\n        return x\n    }\n\n    test \"t\", x {\n        assert x == 1\n    }\n}\n"},
		{"binding else", "fn l(x: Maybe<Int>): Int {\n    n = x else { return 0 }\n    n\n}\n"},
		{"not the last statement of a tail if's block", "fn m(x: Int): Int {\n    if x > 0 { return 1 }\n    x\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkTailReturnCase(t, tc.src, tc.src)
		})
	}
}

func checkTailReturnCase(t *testing.T, src, want string) {
	t.Helper()
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if again, _ := Format(got); again != got {
		t.Errorf("not idempotent:\n%s\nthen:\n%s", got, again)
	}
	orig, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("source did not parse: %v", err)
	}
	normalizeBodies(orig)
	formatted, err := parser.Parse(lexer.Lex(got))
	if err != nil {
		t.Fatalf("output did not parse: %v", err)
	}
	if !astEquivalent(orig, formatted) {
		t.Errorf("output's AST differs from the stripped source's:\n%s\n%s",
			astDump(reflect.ValueOf(orig), 0), astDump(reflect.ValueOf(formatted), 0))
	}
}
