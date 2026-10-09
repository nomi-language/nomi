package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// `P as name` matches P and binds name to the whole value P matched.

const asDecls = `enum Kind {
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

fn ok_count<T>(r: Result<T, String>): Int {
    case r {
        Ok(_) -> 1
        Err(_) -> 0
    }
}
`

// asNameTypes checks src with no errors and returns each binding's type by
// name.
func asNameTypes(t *testing.T, src string) map[string]string {
	t.Helper()
	fa, errs := checkSourceWithStdlib(src)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors:\n%s", strings.Join(elseErrors(t, src), "\n"))
	}
	got := map[string]string{}
	for _, sym := range fa.Definitions {
		if (sym.Kind == analysis.SymbolBinding || sym.Kind == analysis.SymbolParam) && sym.Type != nil {
			got[sym.Name] = sym.Type.String()
		}
	}
	return got
}

// The name has the type of the position its pattern sits in, unnarrowed.
func TestAsPattern_NameHasTheTypeOfItsPosition(t *testing.T) {
	src := asDecls + `fn arm(r: Result<Tx, String>): Int {
    case r {
        Ok(Tx{kind: .Deposit} as deposit) -> deposit.end
        Ok(_) as whole -> ok_count(whole)
        Err(_) -> 0
    }
}

fn nested(xs: List<(Maybe<Int>, String)>, m: Map<String, Kind>, l: Line): Int {
    a = case xs {
        [(Some(_) as first, label), .._] as all -> Iter.count(all) + Maybe.with_default(first, 0) + String.length(label)
        _ -> 0
    }
    b = case m {
        {"k" => .Deposit as kind} -> case kind { .Deposit -> 1, .Withdrawal -> 2 }
        _ -> 0
    }
    c = case l {
        Line{from: Tx{kind: .Withdrawal} as src} -> src.start
        _ -> 0
    }
    a + b + c
}

fn param({start, end} as tx: Tx): Int {
    start + end + tx.start
}

fn statements(m: Maybe<Int>, pair: (Int, Int)): Int {
    (x, y) as both = pair
    Some(n) as found = m else { return 0 }
    r: Result<Int, String> = Ok(n)
    total = if Ok(v) as res = r { v + ok_count(res) } else { 0 }
    sums = [(1, 2)] |> Iter.map(|(p, q) as whole_pair| p + q + whole_pair.0) |> Iter.to_list()
    x + y + both.0 + n + total + Iter.count(sums) + Maybe.with_default(found, 0)
}
`
	got := asNameTypes(t, src)
	for name, want := range map[string]string{
		"deposit": "Tx", "whole": "Result<Tx, String>", "first": "Maybe<Int>",
		"all": "List<(Maybe<Int>, String)>", "kind": "Kind", "src": "Tx", "tx": "Tx",
		"both": "(Int, Int)", "found": "Maybe<Int>", "res": "Result<Int, String>",
		"whole_pair": "(Int, Int)",
	} {
		if got[name] != want {
			t.Errorf("%s: type %q, want %q", name, got[name], want)
		}
	}
}

// An `as` never changes what its pattern matches: arms cover the scrutinee
// as their patterns would without it.
func TestAsPattern_ExhaustivenessSeesThroughTheName(t *testing.T) {
	if errs := elseErrors(t, asDecls+`fn f(m: Maybe<Int>): Int {
    case m {
        Some(n) as s -> n + Maybe.with_default(s, 0)
        None as nothing -> Maybe.with_default(nothing, 0)
    }
}
`); len(errs) != 0 {
		t.Errorf("unexpected errors:\n%s", strings.Join(errs, "\n"))
	}
	errs := elseErrors(t, asDecls+`fn f(m: Maybe<Int>): Int {
    case m {
        Some(n) as s -> n + Maybe.with_default(s, 0)
    }
}
`)
	if len(errs) != 1 || !strings.HasSuffix(errs[0], "non-exhaustive case on Maybe: missing None") {
		t.Errorf("got:\n%s", strings.Join(errs, "\n"))
	}
}

// `P as x` is refutable exactly when P is, in every position that asks.
func TestAsPattern_RefutabilityIsThePatterns(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"binding without else": {`fn f(m: Maybe<Int>): Int {
    Some(n) as whole = m
    n + Maybe.with_default(whole, 0)
}`, "this pattern can fail to match a value of type Maybe<Int>; add `else { ... }` to handle a value it does not match, or match it with `case` or `if Pattern = expr`"},
		"parameter": {`fn f(Some(n) as whole: Maybe<Int>): Int {
    n + Maybe.with_default(whole, 0)
}`, "refutable pattern in parameter; bind the parameter and use a `case` in the body"},
		"if over an irrefutable pattern": {`fn f(pair: (Int, Int)): Int {
    if (a, b) as p = pair { a + b + p.0 } else { 0 }
}`, "this pattern always matches a value of type (Int, Int), so the `if` has nothing to test; bind it on its own line with `pattern = value`"},
		"else after an irrefutable pattern": {`fn f(pair: (Int, Int)): Int {
    (a, b) as p = pair else { return 0 }
    a + b + p.0
}`, "this pattern always matches; remove the else"},
		"a fallback for the whole value": {`fn f(m: Maybe<Int>): Int {
    Some(n) as whole = m else { 0 }
    n + Maybe.with_default(whole, 0)
}`, elseNoPayloadMsg},
	} {
		errs := elseErrors(t, asDecls+tc.body+"\n")
		if len(errs) != 1 || !strings.HasSuffix(errs[0], ": "+tc.want) {
			t.Errorf("%s: got:\n%s\nwant one error:\n%s", name, strings.Join(errs, "\n"), tc.want)
		}
	}
}

// A fallback may stand in for a payload whose pattern carries an `as`: the
// name binds the fallback, as the pattern's other names do.
func TestAsPattern_FallbackBindsTheNameInsideThePayload(t *testing.T) {
	got := asNameTypes(t, asDecls+`fn f(r: Result<(Int, Int), String>): Int {
    Ok((w, h) as size) = r else { (0, 0) }
    w + h + size.0
}
`)
	if got["size"] != "(Int, Int)" {
		t.Errorf("size: type %q, want (Int, Int)", got["size"])
	}
}

func TestAsPattern_Errors(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"a name as a name": {`fn f(m: Maybe<Int>): Int {
    case m {
        n as whole -> Maybe.with_default(whole, 0) + Maybe.with_default(n, 0)
    }
}`, "25:14: `n as whole` binds the same value twice; use one name"},
		"a wildcard as a name": {`fn f(m: Maybe<Int>): Int {
    case m {
        _ as whole -> Maybe.with_default(whole, 0)
    }
}`, "25:14: `_ as whole` matches every value, as `whole` does; write `whole`"},
		"two names": {`fn f(m: Maybe<Int>): Int {
    case m {
        Some(n) as a as b -> n + Maybe.with_default(a, 0) + Maybe.with_default(b, 0)
        None -> 0
    }
}`, "25:25: `as a as b` binds the same value twice; use one name"},
		"the name repeats a pattern name": {`fn f(m: Maybe<Int>): Int {
    case m {
        Some(n) as n -> 0
        None -> 0
    }
}`, "25:20: `n` is bound twice in one pattern; give each binding its own name"},
		"a type name": {`fn f(m: Maybe<Int>): Int {
    case m {
        Some(n) as nOK -> n + Maybe.with_default(nOK, 0)
        None -> 0
    }
}`, `25:20: binding name "nOK" must be snake_case`},
	} {
		errs := elseErrors(t, asDecls+tc.body+"\n")
		found := false
		for _, e := range errs {
			if e == tc.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: got:\n%s\nwant:\n%s", name, strings.Join(errs, "\n"), tc.want)
		}
	}
}
