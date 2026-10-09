package analysis_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A binding whose pattern can fail, `Pattern = value else { ... }`: the else
// leaves (return, break, continue) or supplies a fallback for the one payload
// the pattern names, and its arms plus the pattern must be exhaustive.

const elseNoPayloadMsg = "this pattern has no single payload a fallback could stand in for; every path through `else` must return, break or continue"

func elseErrors(t *testing.T, src string) []string {
	t.Helper()
	_, errs := checkSourceWithStdlib(src)
	var out []string
	for _, e := range errs {
		out = append(out, fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Message))
	}
	return out
}

const elseDecls = `enum LoadError {
    NotFound
    Broken(String)
}

enum Check {
    Valid {addr: String, score: Int}
    Invalid(String)
}

fn why(e: LoadError): String {
    case e {
        .NotFound -> "missing"
        .Broken(s) -> s
    }
}
`

func TestPatternBindingElse_EachFormChecks(t *testing.T) {
	for name, body := range map[string]string{
		"block that leaves": `fn f(m: Maybe<String>): Result<String, String> {
    Some(e) = m else {
        return Err("none")
    }
    Ok(e)
}`,
		"arms that leave": `fn f(r: Result<String, LoadError>): Result<String, String> {
    Ok(user) = r else {
        Err(.NotFound) -> return Err("no user")
        Err(e) -> return Err("loading: ${why(e)}")
    }
    Ok(user)
}`,
		"fallback block": `fn f(m: Maybe<String>): String {
    Some(e) = m else { "none" }
    e
}`,
		"arms mixing fallback and leaving": `fn f(r: Result<Int, LoadError>): Result<Int, String> {
    Ok(port) = r else {
        Err(.NotFound) -> 8080
        Err(e) -> return Err("bad port: ${why(e)}")
    }
    Ok(port)
}`,
		"list pattern that leaves": `fn f(xs: List<Int>): Int {
    [first, .._rest] = xs else { return 0 }
    first
}`,
		"nested refutable pattern that leaves": `fn f(r: Result<Maybe<Int>, String>): Int {
    Ok(Some(n)) = r else { return 0 }
    n
}`,
		"leaving branches inside the else": `fn f(m: Maybe<Int>, loud: Bool): Int {
    Some(n) = m else {
        if loud { return -1 } else { return 0 }
    }
    n
}`,
		"continue in a callback": `fn f(xs: List<Maybe<Int>>): Int {
    Iter.each(xs, |m| {
        Some(n) = m else { continue }
        _ = n
    })
    0
}`,
		"return in a lambda": `fn f(xs: List<Maybe<Int>>): List<Int> {
    xs |> Iter.map(|m| {
        Some(n) = m else { return 0 }
        n
    }) |> Iter.to_list()
}`,
		"try in the else": `fn f(r: Result<Int, String>, other: Result<Int, String>): Result<Int, String> {
    Ok(n) = r else { try other }
    Ok(n)
}`,
	} {
		if errs := elseErrors(t, elseDecls+body+"\n"); len(errs) != 0 {
			t.Errorf("%s: unexpected errors:\n%s", name, strings.Join(errs, "\n"))
		}
	}
}

// The fallback stands in for the payload, and the inner pattern binds from it
// as it does on a match: the names have the payload's component types.
func TestPatternBindingElse_FallbackTypesTheInnerPattern(t *testing.T) {
	src := elseDecls + `fn size(r: Result<(Int, String), String>): String {
    Ok((width, unit)) = r else { (80, "cols") }
    "${width}${unit}"
}

fn describe(c: Check): String {
    .Valid{addr, score} = c else {
        .Invalid(_) -> {addr: "unknown", score: 0}
    }
    "${addr}:${score}"
}

fn whole(c: Check): Int {
    .Valid(r) = c else { {addr: "none", score: -1} }
    r.score
}
`
	fa, errs := checkSourceWithStdlib(src)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	want := map[string]string{"width": "Int", "unit": "String", "addr": "String", "score": "Int"}
	got := map[string]string{}
	for _, sym := range fa.Definitions {
		if _, wanted := want[sym.Name]; wanted && sym.Kind == analysis.SymbolBinding && sym.Type != nil {
			got[sym.Name] = sym.Type.String()
		}
	}
	for name, ty := range want {
		if got[name] != ty {
			t.Errorf("%s: type %q, want %q", name, got[name], ty)
		}
	}
}

func TestPatternBindingElse_Errors(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"list pattern falling back": {`fn f(xs: List<Int>): Int {
    [first, .._rest] = xs else { 0 }
    first
}`, "19:34: " + elseNoPayloadMsg},
		"nested refutable pattern falling back": {`fn f(r: Result<Maybe<Int>, String>): Int {
    Ok(Some(n)) = r else { 0 }
    n
}`, "19:28: " + elseNoPayloadMsg},
		"wrong fallback type": {`fn f(m: Maybe<String>): String {
    Some(e) = m else { 5 }
    e
}`, "19:24: the fallback stands in for Some's payload of type String, got Int"},
		"a path neither leaving nor falling back": {`fn f(m: Maybe<Int>): Int {
    Some(n) = m else { 0 }
    Ok(k) = Ok(n) else {
        Err(e) -> { _ = e }
    }
    k
}`, "21:19: the fallback stands in for Ok's payload of type Int, got Unit"},
		"non-exhaustive arms": {`fn f(r: Result<Int, String>): Int {
    Ok(n) = r else {
        Err("x") -> 0
    }
    n
}`, "19:15: non-exhaustive `else` on Result: missing Err"},
		"non-exhaustive struct-variant arms": {`fn f(c: Check): Int {
    .Valid{score} = c else {
        .Invalid("x") -> {addr: "", score: 0}
    }
    score
}`, "19:23: non-exhaustive `else` on Check: missing Invalid"},
		"irrefutable pattern with else": {`fn f(p: (Int, Int)): Int {
    (x, y) = p else { return 0 }
    x + y
}`, "19:16: this pattern always matches; remove the else"},
		"refutable pattern without else": {`fn f(r: Result<(Int, Int), String>): Int {
    Ok((w, h)) = r
    w * h
}`, "19:5: this pattern can fail to match a value of type Result<(Int, Int), String>; add `else { ... }` to handle a value it does not match, or match it with `case` or `if Pattern = expr`"},
		"variant destructure without else": {`fn f(m: Maybe<Int>): Int {
    Some(n) = m
    n
}`, "19:5: this pattern can fail to match a value of type Maybe<Int>; add `else { ... }` to handle a value it does not match, or match it with `case` or `if Pattern = expr`"},
	} {
		errs := elseErrors(t, elseDecls+"\n"+tc.body+"\n")
		found := false
		for _, e := range errs {
			if e == tc.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %q, got:\n%s", name, tc.want, strings.Join(errs, "\n"))
		}
	}
}

// The pattern's names are in scope after the binding and not inside its
// else, which runs because the pattern did not match.
func TestPatternBindingElse_Scope(t *testing.T) {
	errs := elseErrors(t, elseDecls+`
fn f(p: (Maybe<Int>, Int)): Int {
    (Some(a), b) = p else { return b }
    a + b
}
`)
	want := "19:36: undefined variable 'b'"
	if len(errs) != 1 || errs[0] != want {
		t.Errorf("want only %q, got:\n%s", want, strings.Join(errs, "\n"))
	}
	if errs := elseErrors(t, elseDecls+`
fn g(m: Maybe<Int>): Int {
    n = 10
    Some(n) = m else { return n }
    n
}
`); len(errs) != 0 {
		t.Errorf("the else reads the enclosing n: unexpected errors:\n%s", strings.Join(errs, "\n"))
	}
}

// A struct-variant pattern names one variant: on its own it covers neither
// the enum (so it is refutable) nor, in a case, the other variants.
func TestPatternBindingElse_StructVariantPatternIsNotACatchAll(t *testing.T) {
	errs := elseErrors(t, elseDecls+`
fn f(c: Check): Int {
    case c {
        .Valid{score} -> score
    }
}
`)
	want := "19:5: non-exhaustive case on Check: missing Invalid"
	if len(errs) != 1 || errs[0] != want {
		t.Errorf("want only %q, got:\n%s", want, strings.Join(errs, "\n"))
	}
}

// A variant binding with no else, `Ok(x) = r`, is a pattern that can fail.
// The parser reads `Name(x) = value` as a distinct-type destructure, and the
// checker reports it as the refutable binding it is. On `Ok` over a Result
// and `Some` over a Maybe the hint points at `try`, which is usually meant.
// Each case fails if the source starts being accepted.
func TestPatternBinding_RefutableVariantWithoutElse(t *testing.T) {
	refutable := func(ty string) string {
		return "this pattern can fail to match a value of type " + ty +
			"; add `else { ... }` to handle a value it does not match, or match it with `case` or `if Pattern = expr`"
	}
	for name, tc := range map[string]struct {
		body, at, msg, hint string
	}{
		"Ok over a Result": {`fn f(r: Result<Int, String>): Result<Int, String> {
    Ok(x) = r
    Ok(x)
}`, "19:5", refutable("Result<Int, String>"),
			"`Ok(x)` does not match an `Err`. To unwrap the `Result` and return early on `Err`, write `x = try ...`"},
		"Some over a Maybe": {`fn f(m: Maybe<Int>): Maybe<Int> {
    Some(x) = m
    Some(x)
}`, "19:5", refutable("Maybe<Int>"),
			"`Some(x)` does not match `None`. To unwrap the `Maybe` and return early on `None`, write `x = try ...`"},
		"dotted Some over a Maybe": {`fn f(m: Maybe<Int>): Maybe<Int> {
    .Some(x) = m
    Some(x)
}`, "19:5", refutable("Maybe<Int>"),
			"`Some(x)` does not match `None`. To unwrap the `Maybe` and return early on `None`, write `x = try ...`"},
		"Err over a Result": {`fn f(r: Result<Int, String>): String {
    Err(e) = r
    e
}`, "19:5", refutable("Result<Int, String>"), ""},
		"a user enum variant": {`fn f(e: LoadError): String {
    Broken(s) = e
    s
}`, "19:5", refutable("LoadError"), ""},
		// The program from a user's report: a pipe, whose hint is the
		// `|> try` stage, ending in `Maybe.to_result`, whose Result was
		// copied before std/results.nomi filled in its variants.
		"a pipe ending in Maybe.to_result": {`import std/io

fn main(): Result<Unit, String> {
    Ok(balance) =
        try io.read_line()
        |> String.to_int()
        |> Maybe.to_result("is not a number!")
    io.print(Int.to_string(balance))
    Ok(Unit)
}`, "4:5", refutable("Result<Int, String>"),
			"`Ok(balance)` does not match an `Err`. To unwrap the `Result` and return early on `Err`, write `balance = ...` and end the pipe with `|> try`"},
	} {
		src := elseDecls + "\n" + tc.body + "\n"
		if strings.HasPrefix(tc.body, "import") {
			src = tc.body + "\n"
		}
		_, errs := checkSourceWithStdlib(src)
		var got []string
		found := false
		for _, e := range errs {
			at := fmt.Sprintf("%d:%d", e.Line, e.Col)
			got = append(got, fmt.Sprintf("%s: %s %q", at, e.Message, e.Hints))
			if at != tc.at || e.Message != tc.msg {
				continue
			}
			found = true
			var want []string
			if tc.hint != "" {
				want = []string{tc.hint}
			}
			if strings.Join(e.Hints, "\n") != strings.Join(want, "\n") {
				t.Errorf("%s: hints %q, want %q", name, e.Hints, want)
			}
		}
		if !found {
			t.Errorf("%s: want %s: %q, got:\n%s", name, tc.at, tc.msg, strings.Join(got, "\n"))
		}
	}
}
