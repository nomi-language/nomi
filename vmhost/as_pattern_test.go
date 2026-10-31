package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// `P as name` runs in every position a pattern takes: the name holds the
// whole value P matched, and P's own names bind as they do without it.
func TestAsPattern_RunsInEveryPosition(t *testing.T) {
	got := runSourceOutput(t, `import std/io

enum Kind {
    Deposit
    Withdrawal
}

struct Tx {
    kind: Kind
    start: Int
    end: Int
}

struct Line {
    from: Tx
    to: Tx
}

fn failure(r: Result<Tx, String>) -> String {
    case r {
        Err(m) -> m
        Ok(_) -> "?"
    }
}

fn describe(result: Result<Tx, String>) -> String {
    case result {
        Ok(#Tx{kind: .Deposit} as t) -> "deposited ${t.end - t.start}"
        Ok(t) -> "other ${t.start}"
        Err(_) as e -> "failed: ${failure(e)}"
    }
}

fn head(xs: List<Maybe<Int>>) -> String {
    case xs {
        [Some(n) as first, ..rest] as all -> "${n} ${Maybe.with_default(first, 0)} ${Iter.count(rest)} of ${Iter.count(all)}"
        [None, ..] -> "none first"
        [] -> "empty"
    }
}

fn entry(m: Map<String, Int>) -> String {
    case m {
        %{"k" => 1 as one} as all -> "one ${one} of ${Map.size(all)}"
        _ -> "other"
    }
}

fn start_of(l: Line) -> Int {
    case l {
        #Line{from: #Tx{kind: .Withdrawal} as src} -> src.start
        _ -> 0
    }
}

fn tuple_arm(p: #(Maybe<Int>, String)) -> String {
    case p {
        #(Some(_) as m, label) -> "${label} ${Maybe.with_default(m, 0)}"
        #(None, label) as whole -> "${label} none ${whole.1}"
    }
}

fn param(#{start, end} as tx: Tx) -> Int {
    start + end + tx.start
}

fn binding(m: Maybe<Int>, pair: #(Int, Int)) -> String {
    #(x, y) as both = pair
    Some(n) as found = m else { return "none" }
    "${x} ${y} ${both.0} ${n} ${Maybe.with_default(found, 0)}"
}

fn fallback(r: Result<#(Int, Int), String>) -> Int {
    Ok(#(w, h) as size) = r else { #(10, 20) }
    w + h + size.0
}

fn if_pattern(r: Result<Int, String>) -> String {
    if Ok(v) as res = r {
        "${v} ${case res { Ok(_) -> "ok", Err(_) -> "err" }}"
    } else {
        "not ok"
    }
}

fn main() {
    io.print(describe(Ok(#Tx{kind: .Deposit, start: 1, end: 5})))
    io.print(describe(Ok(#Tx{kind: .Withdrawal, start: 1, end: 5})))
    io.print(describe(Err("boom")))
    io.print(head([Some(4), None, Some(6)]))
    io.print(head([None]))
    io.print(entry(%{"k" => 1, "j" => 2}))
    io.print(entry(%{"k" => 2}))
    io.print(start_of(#Line{from: #Tx{kind: .Withdrawal, start: 7, end: 8}, to: #Tx{kind: .Deposit, start: 0, end: 0}}))
    io.print(tuple_arm(#(Some(3), "three")))
    io.print(tuple_arm(#(None, "nothing")))
    io.print(param(#Tx{kind: .Deposit, start: 2, end: 3}))
    io.print(binding(Some(5), #(1, 2)))
    io.print(binding(None, #(1, 2)))
    io.print(fallback(Ok(#(1, 2))))
    io.print(fallback(Err("no")))
    io.print(if_pattern(Ok(9)))
    io.print(if_pattern(Err("e")))
    sums = [#(1, 2), #(3, 4)] |> Iter.map(fn #(a, b) as p -> a + b + p.0) |> Iter.to_list()
    io.print("${sums}")
}
`)
	want := `deposited 4
other 1
failed: boom
4 4 2 of 3
none first
one 1 of 2
other
7
three 3
nothing none nothing
7
1 2 1 5 5
none
4
40
9 ok
not ok
[4, 10]
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// In a test body: `assert P as name = v`, and a test's setup pattern.
func TestAsPattern_RunsInTests(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "as_test.nomi"
	p, err := vmhost.LoadSource(name, `fn find(n: Int) -> Maybe<Int> {
    if n > 0 { Some(n) } else { None }
}

test "assert binds the name" {
    assert Some(n) as m = find(3)
    assert n == 3
    assert m == Some(3)
}

test "assert fails on a mismatch" {
    assert Some(_) as m = find(0)
    assert m == None
}

group "setup" {
    setup {
        #(1, "one")
    }

    test "binds the whole setup value", #(n, word) as pair {
        assert n == 1
        assert word == "one"
        assert pair.1 == "one"
    }
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	out := buf.String()
	if strings.Contains(out, "BLOCKED") || !strings.Contains(out, "2 passed, 1 failed") ||
		!strings.Contains(out, "line 12: pattern did not match\n    assert Some(_) as m = find(0)\n    actual: None") {
		t.Fatalf("want two passing cases and one pattern mismatch:\n%s", out)
	}
}

// An anonymous record pattern, with or without `as`, nested in a variant
// payload, a struct field or a tuple: in a pattern assertion, a `case` arm,
// a binding with `else` and a lambda parameter. A record pattern of names
// cannot fail, so `assert #Holder{r: #{a: x, b}} = h` has no mismatch edge.
func TestAsPattern_NestedRecordPatternsRun(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "record_as_test.nomi"
	p, err := vmhost.LoadSource(name, `struct Holder {
    r: #{a: Int, b: Int}
}

test "assertion" {
    rec: Result<#{a: Int, b: Int}, String> = Ok(#{a: 1, b: 2})
    assert Ok(#{a: x, b} as whole) = rec
    assert x + b == whole.a + whole.b
    assert Some(#{a: y, b: _} as w) = Some(#{a: 3, b: 4})
    assert y + w.b == 7
}

test "case arm" {
    rec: Maybe<#{a: Int, b: Int}> = Some(#{a: 1, b: 2})
    n = case rec {
        Some(#{a: 2, b} as whole) -> b + whole.a
        Some(#{a: x, b: _} as whole) -> x + whole.b
        None -> 0
    }
    assert n == 3
}

test "binding with else" {
    rec: Result<#{a: Int, b: Int}, String> = Ok(#{a: 1, b: 2})
    Ok(#{a: x, b} as whole) = rec else {
        Err(_) -> {
            assert False
            return
        }
    }
    assert x + b + whole.b == 5
}

test "lambda parameter" {
    pairs = [#(#{a: 1, b: 2}, 3), #(#{a: 4, b: 5}, 6)]
    sums = pairs |> Iter.map(fn #(#{a: x, b} as whole, c) -> x + b + c + whole.a) |> Iter.to_list()
    assert sums == [7, 19]
}

test "struct field and tuple" {
    h = #Holder{r: #{a: 1, b: 2}}
    assert #Holder{r: #{a: x, b}} = h
    assert #Holder{r: #{a: 1, b: y} as whole} = h
    assert #(#{a: p, b: q} as rec, c) = #(whole, 3)
    assert x + b + y + p + q + c + rec.a == 12
}

test "a record test fails" {
    rec: Result<#{a: Int, b: Int}, String> = Ok(#{a: 1, b: 2})
    assert Ok(#{a: 9, b} as whole) = rec
    assert b == whole.b
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	out := buf.String()
	if strings.Contains(out, "BLOCKED") || !strings.Contains(out, "5 passed, 1 failed") ||
		!strings.Contains(out, "pattern did not match\n    assert Ok(#{a: 9, b} as whole) = rec") {
		t.Fatalf("want five passing cases and one pattern mismatch:\n%s", out)
	}
}
