package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// A `fn` declared inside a body is CHECKED, and its signature is RESOLVED.
//
// Until checkNestedFunc existed, neither happened. CheckTypes's walk reaches
// module-level declarations, namespace items and impl-block items; `checkNode`
// had no `*ast.FuncDef` arm, so a nested `fn`'s body was never visited. And a
// `fn` nested inside a `test` body had no FuncType attached either, because
// BuildTypes' pass 2b only descended into top-level `*ast.FuncDef` bodies.
//
// The diagnostics below are the visible half. The half that motivated the fix
// is invisible from here and lives in internal/irbuild: `checkGenericCall`
// attaches the instantiated signature to a call-site symbol, so `Some(1)`
// inside a function returning `Maybe<Int>` records `(Int) -> Maybe<Int>` — and
// with the body unvisited nothing was recorded, which the IR builder reported
// as `generic enum over a type parameter`. See
// TestNestedFunc_PreludeConstructorSolvesInsideATestBody in internal/irbuild.

// TestNestedFunc_BodyIsCheckedInsideAFunction is the baseline: the same
// declaration at file scope has always been checked, so this pins that nesting
// no longer hides it.
func TestNestedFunc_BodyIsCheckedInsideAFunction(t *testing.T) {
	src := `fn main() {
  fn bad(): Int {
    "nope"
  }

  _ = bad()
}`
	expectErrorContaining(t, checkWithStdlib(src),
		"return type mismatch: expected Int, got String")
}

// TestNestedFunc_BodyIsCheckedInsideATestBody is the second half, and it needed
// a separate fix from the arm above: the checker reached the declaration but
// `sym.Type` was nil, so `checkFunc` returned before looking at the body.
func TestNestedFunc_BodyIsCheckedInsideATestBody(t *testing.T) {
	src := `test "t" {
  fn bad(): Int {
    "nope"
  }

  assert bad() == 1
}`
	expectErrorContaining(t, checkWithStdlib(src),
		"return type mismatch: expected Int, got String")
}

// TestNestedFunc_BodyIsCheckedInsideAGroupedTestBody covers the recursion: a
// `tests` group's children are TestDecls of their own, so the signature walk has
// to re-enter itself rather than stop at the outer group.
func TestNestedFunc_BodyIsCheckedInsideAGroupedTestBody(t *testing.T) {
	src := `tests "group" {
  test "t" {
    fn bad(): Int {
      "nope"
    }

    assert bad() == 1
  }
}`
	expectErrorContaining(t, checkWithStdlib(src),
		"return type mismatch: expected Int, got String")
}

// TestNestedFunc_IsNotCheckedAgainstAModuleSignatureOfTheSameName is the
// collapse fixture: a nested `fn`'s identity is its DECLARATION, never its bare
// name.
//
// Nomi permits a nested `fn helper` beside a module-level `fn helper`, and they
// are DIFFERENT functions — internal/irbuild/nestedfn.go pins a call answering
// the nested one's 101 rather than the module one's 1. So checking the
// nested body against the module declaration would report an error about a
// function the programmer did not write at this position.
//
// Two distinct return types on purpose: with both `Int` the check would agree by
// accident and the fixture would pass either way.
//
// Both mutations were RUN rather than reasoned, and they do not agree:
//
//   - Bare-name identity — make `checkFunc` consult `ModuleScope.Lookup(fn.Name)
//     BEFORE the position — FAILS here, with `line 6, col 6: return type
//     mismatch: expected String, got Int`. The module `helper`'s return type,
//     reported at the nested `helper`. That is the mutation this fixture exists
//     for and it is caught.
//
//   - Deleting checkNestedFunc's position guard, so `checkFunc`'s own
//     name fallback becomes reachable again, is UNAVAILABLE: the guard cannot
//     be discriminated by any program, because the resolver records a
//     Definition at every nested `fn`'s position, so the fallback below it is
//     never reached. The guard is therefore a precondition rather than a
//     covered branch, and it is kept for the same reason a bounds check is:
//     the wrong answer it forecloses is silent.
func TestNestedFunc_IsNotCheckedAgainstAModuleSignatureOfTheSameName(t *testing.T) {
	src := `fn helper(): String {
  "module"
}

fn main() {
  fn helper(): Int {
    101
  }

  _ = helper()
  _ = 1
}`
	expectClean(t, checkWithStdlib(src))
}

// TestNestedFunc_TwoTestBodiesDeclaringTheSameNameDoNotUnify is the
// same-name-different-body case for the SIGNATURE table, stated as a property of
// identity rather than of a count.
//
// Both bodies declare `mk`, with different return types, and each body's own
// declaration is the one its calls must see. A signature table keyed on the bare
// name would keep whichever was built last and report a mismatch in the other
// body; keyed on the declaration's position, both are right at once.
func TestNestedFunc_TwoTestBodiesDeclaringTheSameNameDoNotUnify(t *testing.T) {
	src := `test "first" {
  fn mk(): Int {
    1
  }

  assert mk() == 1
}

test "second" {
  fn mk(): String {
    "s"
  }

  assert mk() == "s"
}`
	expectClean(t, checkWithStdlib(src))
}

// TestNestedFunc_SignaturesAreKeyedByPositionNotName reads the built table
// directly, so the property is asserted on the DATA rather than inferred from
// the absence of a diagnostic.
//
// Three declarations of `mk` — one at file scope and one in each test body —
// with three different return types. A name-keyed table has at most one entry
// for `mk` and cannot answer all three; the assertion is that each POSITION
// answers with its own declared return type.
func TestNestedFunc_SignaturesAreKeyedByPositionNotName(t *testing.T) {
	src := `fn mk(): Bool {
  true
}

test "first" {
  fn mk(): Int {
    1
  }

  assert mk() == 1
}

test "second" {
  fn mk(): String {
    "s"
  }

  assert mk() == "s"
}`
	tokens := lexer.Lex(withStdlibTestImports(src))
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	analysis.BuildTypes(fa, nodes)

	// Collect every definition named `mk` with a resolved function type, and
	// read back the return type each POSITION carries.
	returns := map[string]string{}
	for pos, sym := range fa.Definitions {
		if sym == nil || sym.Name != "mk" {
			continue
		}
		ft, ok := sym.Type.(*analysis.FuncType)
		if !ok || ft.Return == nil {
			t.Fatalf("`mk` at %d:%d has no resolved function type (%T)", pos.Line, pos.Col, sym.Type)
		}
		returns[ft.Return.String()] = ft.Return.String()
	}
	for _, want := range []string{"Bool", "Int", "String"} {
		if _, ok := returns[want]; !ok {
			got := make([]string, 0, len(returns))
			for r := range returns {
				got = append(got, r)
			}
			t.Fatalf("no `mk` resolved to %s; the three declarations collapsed to %d distinct return type(s): %s",
				want, len(returns), strings.Join(got, ", "))
		}
	}
}

// TestNestedFunc_BodyIsCheckedInEveryExpressionPosition: a `fn` declared in a
// block is checked wherever that block sits. BuildTypes' signature walk once
// enumerated the node kinds it descended through (blocks, bindings, `if`,
// `case`, lambdas, call arguments), so a block that was a list element, a
// struct field, an operand or an interpolation was never entered: its nested
// fn got no FuncType, checkFunc returned before the body, and these programs
// checked clean.
func TestNestedFunc_BodyIsCheckedInEveryExpressionPosition(t *testing.T) {
	cases := []struct {
		name, src string
	}{
		{"list element", `fn main() {
  xs = [{
    fn bad(): Int {
      "nope"
    }
    bad()
  }, 2]
  assert xs == [1, 2]
}`},
		{"tuple element", `fn main() {
  v = ({
    fn bad(): Int {
      "nope"
    }
    bad()
  }, 2)
  assert v == (1, 2)
}`},
		{"struct field", `struct P {
  x: Int
}

fn main() {
  p = P{x: {
    fn bad(): Int {
      "nope"
    }
    bad()
  }}
  assert p.x == 1
}`},
		{"operand", `fn main() {
  n = 1 + {
    fn bad(): Int {
      "nope"
    }
    bad()
  }
  assert n == 2
}`},
		{"field receiver", `struct P {
  x: Int
}

fn main() {
  n = {
    fn bad(): P {
      "nope"
    }
    bad()
  }.x
  assert n == 1
}`},
		{"lambda body in a list", `fn main() {
  fs = [|k: Int| {
    fn bad(): Int {
      "nope"
    }
    bad() + k
  }]
  assert Iter.count(fs) == 1
}`},
		{"then body", `fn main() {
  n = 1
    |> then |k| {
      fn bad(): Int {
        "nope"
      }
      bad() + k
    }
  assert n == 2
}`},
		{"pipe head", `fn main() {
  n = {
    fn bad(): List<Int> {
      "nope"
    }
    bad()
  }
    |> Iter.count()
  assert n == 2
}`},
		{"interpolation", `fn main() {
  s = "${{
    fn bad(): Int {
      "nope"
    }
    bad()
  }}"
  assert s == "1"
}`},
		{"case on the nested fn's call", `fn main() {
  xs = [{
    fn bad(): Maybe<Int> {
      "nope"
    }
    case bad() {
      Some(v) -> v
      None -> 0
    }
  }]
  assert xs == [1]
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectErrorContaining(t, checkWithStdlib(tc.src), "return type mismatch: expected ")
			expectErrorContaining(t, checkWithStdlib(tc.src), ", got String")
		})
	}
}

// TestNestedFunc_ExternIsRejectedInEveryExpressionPosition: the walk that
// rejects a nested `host` declaration enumerated node kinds the same way the
// signature walk did, so one in a block that was a list element was accepted.
func TestNestedFunc_ExternIsRejectedInEveryExpressionPosition(t *testing.T) {
	src := `fn main() {
  xs = [{
    host fn now(): Int
    now()
  }]
  assert xs == [1]
}`
	expectErrorContaining(t, checkWithStdlib(src), "extern declaration must be at the top level")
}
