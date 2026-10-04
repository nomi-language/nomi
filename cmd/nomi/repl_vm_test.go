package main

import (
	"bytes"
	"strings"
	"testing"
)

// replRun runs script, one REPL input per line (an incomplete line continues
// onto the next, as at the prompt), in one session, and answers what it
// printed to stdout and stderr.
func replRun(t *testing.T, script string) (string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	replScript(strings.NewReader(script), &out, &errOut)
	return out.String(), errOut.String()
}

func replExpect(t *testing.T, script, wantOut string, wantErrs ...string) {
	t.Helper()
	out, errOut := replRun(t, script)
	if out != wantOut {
		t.Errorf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", out, wantOut, errOut)
	}
	for _, want := range wantErrs {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	if len(wantErrs) == 0 && errOut != "" {
		t.Errorf("unexpected stderr:\n%s", errOut)
	}
}

func TestReplVM_BindingsCarryBetweenInputs(t *testing.T) {
	replExpect(t, "x = 5\nx + 1\n(a, b) = (x, \"two\")\nb\na * 2\n[1, 2, 3]\n",
		"6\n\"two\"\n10\n[1, 2, 3]\n")
}

func TestReplVM_AFunctionDefinedEarlierIsCalledLater(t *testing.T) {
	replExpect(t, "fn double(n: Int): Int {\n  n * 2\n}\ndouble(21)\nx = double(2)\nx\n",
		"42\n4\n")
}

func TestReplVM_ATypeDefinedEarlierIsUsedLater(t *testing.T) {
	replExpect(t, "struct Point {\n  x: Int\n  y: Int\n}\n"+
		"impl Point {\n  pub fn sum(p: Point): Int {\n    p.x + p.y\n  }\n}\n"+
		"p = Point{x: 1, y: 2}\nPoint.sum(p)\np.y\n",
		"3\n2\n")
}

// A value is computed once, when its input runs. Reading it later reads the
// stored value: the effect in its initializer does not repeat.
func TestReplVM_AnEffectHappensOnce(t *testing.T) {
	replExpect(t, "import std/io\n"+
		"fn noisy(): Int {\n  io.print(\"computed\")\n  7\n}\n"+
		"v = noisy()\nv + 1\nv + 2\nonce o = noisy() * 10\no\no + 1\n",
		"computed\n8\n9\ncomputed\n70\n71\n")
}

func TestReplVM_ABindingShadowsForLaterInputs(t *testing.T) {
	replExpect(t, "x = 1\nx = x + 1\nx\nx = \"now a string\"\nx\n",
		"2\n\"now a string\"\n")
}

// A closure keeps the value it captured when it was built; rebinding the
// name later does not reach it.
func TestReplVM_AClosureKeepsItsCapturedValue(t *testing.T) {
	replExpect(t, "k = 10\nadd = |n: Int| n + k\nk = 100\nadd(1)\ng = add\ng(0)\nk\n",
		"11\n10\n100\n")
}

// An input that fails to check or faults prints its error and changes
// nothing; the session goes on.
func TestReplVM_AnErrorDoesNotPoisonTheSession(t *testing.T) {
	replExpect(t, "x = 5\nundefined_name + 1\nx + 1\ny = x / 0\ny\nx\n",
		"6\n5\n",
		"undefined variable 'undefined_name'", "division by zero", "undefined variable 'y'")
}

// A redefinition applies to later inputs only: an earlier function that
// calls the old definition keeps calling it, whatever the new one's type.
func TestReplVM_RedefinitionsApplyToLaterInputs(t *testing.T) {
	replExpect(t, "fn f(): Int {\n  1\n}\nfn g(): Int {\n  f() + 1\n}\n"+
		"fn f(): String {\n  \"s\"\n}\ng()\nf()\nfn f(): Int {\n  10\n}\ng()\n"+
		"fn h(): Int {\n  f() + 1\n}\nh()\n",
		"2\n\"s\"\n2\n11\n")
}

// A redefinition the front end rejects is reported and the earlier
// definition stays.
func TestReplVM_ARejectedRedefinitionChangesNothing(t *testing.T) {
	replExpect(t, "fn f(): Int {\n  1\n}\nfn f(): Int {\n  \"s\"\n}\nf()\n",
		"1\n",
		"line 2, col 3: return type mismatch: expected Int, got String")
}

// A function reads the value a name had when the function was entered, as
// in a file: rebinding the name later reaches later inputs only.
func TestReplVM_AFunctionKeepsTheBindingItWasWrittenAgainst(t *testing.T) {
	replExpect(t, "x = 5\nfn add(a: Int): Int {\n  a + x\n}\nadd(1)\nx = 100\nadd(1)\nx\n"+
		"fn add2(a: Int): Int {\n  a + x\n}\nadd2(1)\nadd(1)\n",
		"6\n6\n100\n101\n6\n")
}

// A recursive function redefined keeps its old version's recursion for an
// earlier caller.
func TestReplVM_ARecursiveFunctionIsVersionedWhole(t *testing.T) {
	replExpect(t, "fn count(n: Int): Int {\n  if n == 0 {\n    0\n  } else {\n    1 + count(n - 1)\n  }\n}\n"+
		"fn use_count(): Int {\n  count(3)\n}\n"+
		"fn count(n: Int): Int {\n  if n == 0 {\n    0\n  } else {\n    10 + count(n - 1)\n  }\n}\n"+
		"use_count()\ncount(3)\n",
		"3\n30\n")
}

// A function value held in a session value is callable from a later
// function, and rebinding the name does not reach that function.
func TestReplVM_AFunctionValuedBindingIsUsedByALaterFunction(t *testing.T) {
	replExpect(t, "inc = |n: Int| n + 1\nfn twice(n: Int): Int {\n  inc(inc(n))\n}\ntwice(1)\n"+
		"inc = |n: Int| n + 100\ntwice(1)\ninc(1)\nop = twice\nop(0)\n",
		"3\n3\n101\n2\n")
}

// A once runs its initializer on first use, in whichever input that is, and
// never again.
func TestReplVM_AOnceIsForcedOnFirstUse(t *testing.T) {
	replExpect(t, "import std/io\n"+
		"fn noisy(): Int {\n  io.print(\"computed\")\n  7\n}\n"+
		"once o = noisy()\n1\nfn plus(n: Int): Int {\n  n + o\n}\nplus(1)\no\nonce o = 100\no\nplus(1)\n"+
		"once f: (Int) -> Int = plus\nf(2)\n",
		"1\ncomputed\n8\n7\n100\n8\n9\n")
}

// Redeclaring a type drops the functions that name it, and the definitions
// that use those; an identical redeclaration drops nothing.
func TestReplVM_RedeclaringATypeDropsItsFunctions(t *testing.T) {
	replExpect(t, "struct P {\n  x: Int\n}\nfn px(p: P): Int {\n  p.x\n}\nfn one(): Int {\n  px(P{x: 1})\n}\n"+
		"fn other(): Int {\n  2\n}\n"+
		"struct P {\n  x: Int\n}\none()\nstruct P {\n  y: Int\n}\nother()\none()\n",
		"1\n2\n",
		"note: one is no longer in scope", "note: px is no longer in scope", "undefined")
}

// Redeclaring a type drops the values of its old layout.
func TestReplVM_RedeclaringATypeDropsItsValues(t *testing.T) {
	replExpect(t, "struct P {\n  x: Int\n}\np = P{x: 1}\nn = 3\nstruct P {\n  y: Int\n}\nn\np\n",
		"3\n",
		"note: p is no longer in scope", "undefined variable 'p'")
}

const replBlockedDecls = `struct Node {
  f: Map<String, (Int) -> Int>
}
fn weigh(_d: Node): Int {
  0
}
fn count(n: Int, acc: Int): Int {
  if n == 0 {
    acc
  } else {
    weigh(Node{f: Map.empty()}) + 3
  }
}
`

// An input the VM cannot run prints its BLOCKED lines before any effect and
// binds nothing.
func TestReplVM_ABlockedInputChangesNothing(t *testing.T) {
	out, errOut := replRun(t, replBlockedDecls+"z = 1\nw = count(3, 0)\nw\nz + 1\n")
	if out != "2\n" {
		t.Errorf("stdout %q, want %q\nstderr:\n%s", out, "2\n", errOut)
	}
	for _, want := range []string{"BLOCKED <repl> [count] not retained", "the VM cannot run this program",
		"undefined variable 'w'"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
}

func TestReplVM_AnImportIsUsedByLaterInputs(t *testing.T) {
	replExpect(t, "import std/io\nio.print(\"hi\")\nx = 2\nio.print(Int.to_string(x))\n",
		"hi\n2\n")
}

// An input whose `main` the builder declines, and which therefore lowers no
// entry module at all, is reported BLOCKED and commits nothing. It crashed
// the session's machine with a nil-pointer dereference when the input came
// after an earlier one, because Session.Run linked the missing entry before
// asking whether `main` was retained. Binding a file API object to a name is
// such an input: the front end accepts it and the builder declines it.
func TestReplVM_AnInputWithNoRetainedEntryIsBlocked(t *testing.T) {
	cases := []struct {
		name, script string
		wantOut      string
		wantErr      string
	}{
		{"after an earlier input", "x = 1\nimport std/io\nm = io\nx\n",
			"1\n", "[main] not retained"},
		{"file object stored", "import std/io\nm = io\nm\nio.print(\"after\")\n",
			"after\n", "[main] not retained"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, errOut := replRun(t, c.script)
			if out != c.wantOut {
				t.Errorf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", out, c.wantOut, errOut)
			}
			if !strings.Contains(errOut, c.wantErr) {
				t.Errorf("stderr lacks %q:\n%s", c.wantErr, errOut)
			}
			if strings.Contains(errOut, "runtime error") || strings.Contains(errOut, "internal error") {
				t.Errorf("stderr reports a crash:\n%s", errOut)
			}
		})
	}
}

// The same input may import a module and call through it; the canonical
// spellings run, and the file-qualified spelling of a type-owned function is
// an analysis error naming the owner, not a crash.
func TestReplVM_AnInputImportsAndCallsTogether(t *testing.T) {
	var out, errOut bytes.Buffer
	s := newReplSession(&out, &errOut)
	s.eval("import std/duration.Duration\nd = Duration.seconds(2)", &out, &errOut)
	s.eval("Duration.as_millis(d)", &out, &errOut)
	s.eval("import std/duration\nduration.seconds(1)", &out, &errOut)
	s.eval("Duration.as_seconds(d)", &out, &errOut)
	if got, want := out.String(), "2000\n2\n"; got != want {
		t.Errorf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", got, want, errOut.String())
	}
	if e := errOut.String(); !strings.Contains(e, "call it as Duration.seconds(...)") || strings.Contains(e, "runtime error") {
		t.Errorf("stderr:\n%s", e)
	}
}

// An inherent function is not a file export, so importing it by name is an
// analysis error, not a crash.
func TestReplVM_ImportingAnInherentFunctionByNameIsRejected(t *testing.T) {
	replExpect(t, "import std/duration.{seconds}\nx = 1\nx\n",
		"1\n",
		"file 'std.duration' has no exported name 'seconds'")
}

// A value the session cannot carry is reported and the input still runs.
func TestReplVM_AValueThatCannotBeKeptIsReported(t *testing.T) {
	// A type from a module the session imported whole is spelled short
	// (`Date`), which does not resolve in a later input.
	replExpect(t, "import std/calendar\nd = calendar.Date.new(2020, 1, 2)\nx = 1\nx\n",
		"1\n",
		"note: d is not kept for later inputs")
}

// A lazy Iter is a value like any other: it is kept, and each later input
// runs it from its start.
func TestReplVM_ALazyIterIsKept(t *testing.T) {
	replExpect(t, "it = [1, 2] |> Iter.map(|v| v + 1)\nIter.to_list(it)\nIter.to_list(it)\n",
		"[2, 3]\n[2, 3]\n")
}

// Error text names an older definition by the user's name, not by the
// versioned one a program spells it with.
func TestReplVM_ErrorsNameOlderDefinitionsByTheUsersName(t *testing.T) {
	src := &replSource{}
	got := src.explainText("BLOCKED <repl> [nomi_repl_12_count] not retained: a call to nomi_repl_3_f through nomi_repl_get_4")
	want := "BLOCKED <repl> [count] not retained: a call to f through nomi_repl_get_4"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// An error names the input's own line, not the generated program's.
func TestReplVM_ErrorsNameTheInputsLine(t *testing.T) {
	_, errOut := replRun(t, "x = 1\nfn f(): Int {\n  1\n  missing_name\n}\n")
	if !strings.Contains(errOut, "line 3, col 3: undefined variable 'missing_name'") {
		t.Errorf("stderr:\n%s", errOut)
	}
}

// A `todo` reached in an input is reported at the input's line, one in an
// earlier input's function as a fault is, and the session goes on.
func TestReplVM_ATodoIsReportedAndTheSessionGoesOn(t *testing.T) {
	replExpect(t, "n: Int = todo \"now\"\nfn f(n: Int): Int {\n  todo \"later\"\n}\nf(1)\n2 + 2\n",
		"4\n",
		"todo reached at repl.nomi:1: now",
		"todo reached at repl.nomi:3 (an earlier input's declaration): later")
}
