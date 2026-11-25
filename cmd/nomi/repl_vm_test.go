package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/vmhost"
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
	replExpect(t, "fn double(n: Int) -> Int\n  n * 2\nend\ndouble(21)\nx = double(2)\nx\n",
		"42\n4\n")
}

func TestReplVM_ATypeDefinedEarlierIsUsedLater(t *testing.T) {
	replExpect(t, "struct Point\n  x: Int\n  y: Int\nend\n"+
		"impl Point\n  fn sum(p: Point) -> Int\n    p.x + p.y\n  end\nend\n"+
		"p = Point{x: 1, y: 2}\nPoint.sum(p)\np.y\n",
		"3\n2\n")
}

// A Uuid made in one input is the same type in every later input, though
// each input's program lowers std/uuid again: equal to a literal written
// later, a key a later input finds, and ordered and rendered by std's impls.
func TestReplVM_AStdUuidCarriesBetweenInputs(t *testing.T) {
	replExpect(t, "import std::uuid.Uuid\n"+
		"id = Uuid\"0190A0B2-7F3C-7C3E-8A1B-1F2E3D4C5B6A\"\n"+
		"m = {id => 1}\n"+
		"id == Uuid\"0190a0b2-7f3c-7c3e-8a1b-1f2e3d4c5b6a\"\n"+
		"Map.get(m, Uuid\"0190a0b2-7f3c-7c3e-8a1b-1f2e3d4c5b6a\")\n"+
		"Comparable.compare(id, Uuid.nil())\n"+
		"[id, Uuid.nil()] |> Iter.sort() |> Iter.to_list()\n"+
		"\"${id}\"\n",
		"True\nSome(1)\nGreater\n[Uuid\"00000000-0000-0000-0000-000000000000\", Uuid\"0190a0b2-7f3c-7c3e-8a1b-1f2e3d4c5b6a\"]\n"+
			"\"0190a0b2-7f3c-7c3e-8a1b-1f2e3d4c5b6a\"\n")
}

// A value is computed once, when its input runs. Reading it later reads the
// stored value: the effect in its initializer does not repeat.
func TestReplVM_AnEffectHappensOnce(t *testing.T) {
	replExpect(t, "import std::io\n"+
		"fn noisy() -> Int\n  io.print(\"computed\")\n  7\nend\n"+
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
	replExpect(t, "k = 10\nadd = fn n: Int -> n + k\nk = 100\nadd(1)\ng = add\ng(0)\nk\n",
		"11\n10\n100\n")
}

// An input that fails to check or faults prints its error and changes
// nothing; the session goes on.
func TestReplVM_AnErrorDoesNotPoisonTheSession(t *testing.T) {
	replExpect(t, "x = 5\nundefined_name + 1\nx + 1\ny = x / NonZeroInt.try_from(x - x)!\ny\nx\n",
		"6\n5\n",
		"undefined variable 'undefined_name'", "`NonZeroInt.try_from(x - x)` found nothing", "undefined variable 'y'")
}

// A redefinition applies to later inputs only: an earlier function that
// calls the old definition keeps calling it, whatever the new one's type.
func TestReplVM_RedefinitionsApplyToLaterInputs(t *testing.T) {
	replExpect(t, "fn f() -> Int\n  1\nend\nfn g() -> Int\n  f() + 1\nend\n"+
		"fn f() -> String\n  \"s\"\nend\ng()\nf()\nfn f() -> Int\n  10\nend\ng()\n"+
		"fn h() -> Int\n  f() + 1\nend\nh()\n",
		"2\n\"s\"\n2\n11\n")
}

// A redefinition the front end rejects is reported and the earlier
// definition stays.
func TestReplVM_ARejectedRedefinitionChangesNothing(t *testing.T) {
	replExpect(t, "fn f() -> Int\n  1\nend\nfn f() -> Int\n  \"s\"\nend\nf()\n",
		"1\n",
		"line 2, col 3: return type mismatch: expected Int, got String")
}

// A function reads the value a name had when the function was entered, as
// in a file: rebinding the name later reaches later inputs only.
func TestReplVM_AFunctionKeepsTheBindingItWasWrittenAgainst(t *testing.T) {
	replExpect(t, "x = 5\nfn add(a: Int) -> Int\n  a + x\nend\nadd(1)\nx = 100\nadd(1)\nx\n"+
		"fn add2(a: Int) -> Int\n  a + x\nend\nadd2(1)\nadd(1)\n",
		"6\n6\n100\n101\n6\n")
}

// A recursive function redefined keeps its old version's recursion for an
// earlier caller.
func TestReplVM_ARecursiveFunctionIsVersionedWhole(t *testing.T) {
	replExpect(t, "fn count(n: Int) -> Int\n  if n == 0 then\n    0\n  else\n    1 + count(n - 1)\n  end\nend\n"+
		"fn use_count() -> Int\n  count(3)\nend\n"+
		"fn count(n: Int) -> Int\n  if n == 0 then\n    0\n  else\n    10 + count(n - 1)\n  end\nend\n"+
		"use_count()\ncount(3)\n",
		"3\n30\n")
}

// A function value held in a session value is callable from a later
// function, and rebinding the name does not reach that function.
func TestReplVM_AFunctionValuedBindingIsUsedByALaterFunction(t *testing.T) {
	replExpect(t, "inc = fn n: Int -> n + 1\nfn twice(n: Int) -> Int\n  inc(inc(n))\nend\ntwice(1)\n"+
		"inc = fn n: Int -> n + 100\ntwice(1)\ninc(1)\nop = twice\nop(0)\n",
		"3\n3\n101\n2\n")
}

// A once runs its initializer on first use, in whichever input that is, and
// never again.
func TestReplVM_AOnceIsForcedOnFirstUse(t *testing.T) {
	replExpect(t, "import std::io\n"+
		"fn noisy() -> Int\n  io.print(\"computed\")\n  7\nend\n"+
		"once o = noisy()\n1\nfn plus(n: Int) -> Int\n  n + o\nend\nplus(1)\no\nonce o = 100\no\nplus(1)\n"+
		"once f: (Int) -> Int = plus\nf(2)\n",
		"1\ncomputed\n8\n7\n100\n8\n9\n")
}

// A type redeclared with a different declaration is a new type: an earlier
// value, function, once and type that name the old one keep it and still
// work together, and a later input that says the name gets the new one.
func TestReplVM_ARedeclaredTypeIsANewVersion(t *testing.T) {
	replExpect(t, "struct P\n  x: Int\nend\nfn px(p: P) -> Int\n  p.x\nend\nstruct Line\n  a: P\nend\n"+
		"once origin: P = P{x: 0}\np = P{x: 1}\nps = [P{x: 2}]\n"+
		"struct P\n  y: Int\nend\npx(p)\np\nps\nLine{a: origin}\nq = P{y: 5}\nq.y\nr = p\npx(r)\n",
		"1\nP{x: 1}\n[P{x: 2}]\nLine{a: P{x: 0}}\n5\n1\n")
}

// An input's declarations and onces carry the modifiers the user wrote and
// no others. An unmarked once of an unmarked type is internal to the
// session's module, which every later input is in, and a `pub` once that
// names it is refused as a file would refuse it.
func TestReplVM_TheUsersModifiersAreTheOnesJudged(t *testing.T) {
	replExpect(t, "struct P\n  x: Int\nend\nonce a: P = P{x: 1}\na\npub once b: P = P{x: 2}\npub once c: Int = 3\nc\n",
		"P{x: 1}\n3\n",
		"public once binding exposes internal type P")
}

// Mixing the old and the new version of a type is a type error that names
// each by the input that declared it.
func TestReplVM_MixingTypeVersionsNamesEachInput(t *testing.T) {
	replExpect(t, "struct P\n  x: Int\nend\nfn px(p: P) -> Int\n  p.x\nend\nstruct P\n  x: Int\n  y: Int\nend\n"+
		"px(P{x: 1, y: 2})\n",
		"",
		"argument 1: expected P (input 1), got P (input 3)")
}

// An impl written for the old version stays with it, and the new version
// starts with none; a redefined impl for the new one replaces only its own.
func TestReplVM_AnImplStaysWithItsTypeVersion(t *testing.T) {
	replExpect(t, "struct P\n  x: Int\nend\n"+
		"impl Display for P\n  fn to_string(p: P) -> String\n    \"old ${p.x}\"\n  end\nend\np = P{x: 1}\n"+
		"struct P\n  y: Int\nend\n\"${p}\"\n\"${P{y: 2}}\"\n"+
		"impl Display for P\n  fn to_string(p: P) -> String\n    \"new ${p.y}\"\n  end\nend\n\"${P{y: 3}}\"\n\"${p}\"\n"+
		"impl Display for P\n  fn to_string(p: P) -> String\n    \"newer ${p.y}\"\n  end\nend\n\"${P{y: 4}}\"\n\"${p}\"\n",
		"\"old 1\"\n\"new 3\"\n\"old 1\"\n\"newer 4\"\n\"old 1\"\n",
		"no impl of `Display` for `P`")
}

// An identical redeclaration keeps the type, so earlier values, functions
// and impls go on working with values a later input makes.
func TestReplVM_AnIdenticalRedeclarationKeepsTheType(t *testing.T) {
	replExpect(t, "struct P\n  x: Int\nend\nfn px(p: P) -> Int\n  p.x\nend\np = P{x: 1}\n"+
		"struct P\n  x: Int\nend\npx(P{x: 2})\n[p, P{x: 3}]\n",
		"2\n[P{x: 1}, P{x: 3}]\n")
}

// replBlockedDecls declares `count`, which the builder declines under
// plantDecline at its call to `planted_decline`.
const replBlockedDecls = `fn planted_decline(n: Int) -> Int
  n
end
fn count(n: Int, acc: Int) -> Int
  if n == 0 then
    acc
  else
    planted_decline(n) + 3
  end
end
`

// An input the VM cannot run prints its BLOCKED lines before any effect and
// binds nothing.
func TestReplVM_ABlockedInputChangesNothing(t *testing.T) {
	plantDecline(t)
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
	replExpect(t, "import std::io\nio.print(\"hi\")\nx = 2\nio.print(Int.to_string(x))\n",
		"hi\n2\n")
}

// replPlantedDecl declares `planted_decline`, every call to which the builder
// declines under plantDecline.
const replPlantedDecl = "fn planted_decline(n: Int) -> Int\n  n\nend\n"

// An input whose `main` the builder declines, and which therefore lowers no
// entry module at all, is reported BLOCKED and commits nothing. It crashed
// the session's machine with a nil-pointer dereference when the input came
// after an earlier one, because Session.Run linked the missing entry before
// asking whether `main` was retained. An input that binds a call to
// `planted_decline` is such an input under plantDecline.
func TestReplVM_AnInputWithNoRetainedEntryIsBlocked(t *testing.T) {
	plantDecline(t)
	cases := []struct {
		name, script string
		wantOut      string
		wantErr      string
	}{
		{"after an earlier input", replPlantedDecl + "x = 1\nv = planted_decline(1)\nx\n",
			"1\n", "[main] not retained"},
		{"value stored", replPlantedDecl + "v = planted_decline(1)\nv\nimport std::io\nio.print(\"after\")\n",
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
	s.eval("import std::duration.Duration\nd = Duration.seconds(2)", &out, &errOut)
	s.eval("Duration.as_millis(d)", &out, &errOut)
	s.eval("import std::duration\nduration.seconds(1)", &out, &errOut)
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
	replExpect(t, "import std::duration.{seconds}\nx = 1\nx\n",
		"1\n",
		"file 'std::duration' has no exported name 'seconds'")
}

// A value whose std type the input's names do not bring into scope is kept:
// a type from a module the session imported whole (`Date` through
// `calendar`), and one a range types (`Duration.seconds(2)` is a
// PositiveDuration, which only std::duration names), are written through
// their modules in the files that keep and read them.
func TestReplVM_AValueOfAStdTypeOutOfScopeIsKept(t *testing.T) {
	replExpect(t, "import std::calendar\nd = calendar.Date.new(2020, 1, 2)\nx = 1\nx\nd\n",
		"1\nOk(2020-01-02)\n")
	replExpect(t, "import std::duration.Duration\nd = Duration.seconds(2)\nd\nDuration.as_millis(d)\n",
		"2s\n2000\n")
}

// A lazy Iter is a value like any other: it is kept, and each later input
// runs it from its start.
func TestReplVM_ALazyIterIsKept(t *testing.T) {
	replExpect(t, "it = [1, 2] |> Iter.map(fn v -> v + 1)\nIter.to_list(it)\nIter.to_list(it)\n",
		"[2, 3]\n[2, 3]\n")
}

// Error text names an older definition by the user's name, not by the
// versioned one a program spells it with.
func TestReplVM_ErrorsNameOlderDefinitionsByTheUsersName(t *testing.T) {
	src := &replSource{}
	got := src.explainText("BLOCKED <repl> [nomi_repl_12_count] not retained: a call to nomi_repl_3_f through nomi_repl_get_4", false)
	want := "BLOCKED <repl> [count] not retained: a call to f through nomi_repl_get_4"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// An error names the input's own line, not the generated program's.
func TestReplVM_ErrorsNameTheInputsLine(t *testing.T) {
	_, errOut := replRun(t, "x = 1\nfn f() -> Int\n  1\n  missing_name\nend\n")
	if !strings.Contains(errOut, "line 3, col 3: undefined variable 'missing_name'") {
		t.Errorf("stderr:\n%s", errOut)
	}
}

// A `todo` reached in an input is reported at the line of the input it was
// typed on, one in an earlier input's function as a fault is, and the
// session goes on. Each input reads k, so its file holds lines the session
// wrote above what the user typed.
func TestReplVM_ATodoIsReportedAndTheSessionGoesOn(t *testing.T) {
	replExpect(t, "k = 1\nn: Int = begin\n  _ = k\n  todo \"now\"\nend\n"+
		"fn f(n: Int) -> Int\n  _ = k\n  todo \"later\"\nend\nf(1)\n2 + 2\n",
		"4\n",
		"todo reached at repl.nomi:3: now",
		"todo reached at repl.nomi:3 (an earlier input's declaration): later")
}

// Every position the REPL shows is a line of what the user typed, counted
// from the input's first line, though each input's file holds imports and
// declarations the session wrote above it: a `dbg` in the input and in an
// earlier input's function, a failed assertion's line, and a diagnostic's
// line and column, in a one-line and in a multi-line input.
func TestReplVM_PositionsAreTheLinesTheUserTyped(t *testing.T) {
	out, errOut := replRun(t, "import std::assertions.AssertionFailure\nk = 1\n"+
		"dbg k\n"+
		"m = begin\n  a = k + 1\n  dbg a\nend\n"+
		"fn traced(n: Int) -> Int\n  t = n + k\n  dbg t\nend\ntraced(5)\n"+
		"fn checked(n: Int) -> Result<Int, AssertionFailure>\n  _ = k\n  assert n > k\n  Ok(n)\nend\n"+
		"Result.is_err(checked(0))\n"+
		"case checked(0)\n  Ok(_) -> 0\n  Err(e) -> e.line\nend\n"+
		"fn bad() -> Int\n  _ = k\n  missing_name\nend\n"+
		"k + missing_name\n")
	if want := "dbg line 1: k = 1\n1\ndbg line 3: a = 2\ndbg line 3: t = 6\n6\nTrue\n3\n"; out != want {
		t.Errorf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", out, want, errOut)
	}
	for _, want := range []string{
		"line 3, col 3: undefined variable 'missing_name'",
		"line 1, col 5: undefined variable 'missing_name'",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
}

// A session reads to its user as one file: a later input calls an earlier
// input's private function, from a statement and from a function of its
// own, takes it as a value, reads its private once, keeps a value of its
// private type and calls a private function of its inherent impl, and
// builds, reads and destructures its opaque struct, distinct type and enum.
func TestReplVM_PrivateAndOpaqueReachLaterInputs(t *testing.T) {
	replExpect(t, "private fn secret(n: Int) -> Int\n  n * 2\nend\n"+
		"secret(4)\nf = secret\nf(5)\nfn g() -> Int\n  secret(1)\nend\ng()\n"+
		"private once p: Int = 5\np\n"+
		"private struct P\n  x: Int\nend\nprivate once origin: P = P{x: 9}\norigin.x\nq = P{x: 4}\nq\n"+
		"impl P\n  private fn twice(p: P) -> Int\n    p.x * 2\n  end\nend\nP.twice(origin)\n"+
		"opaque struct Box\n  v: Int\nend\nb = Box{v: 3}\nb.v\nBox{v:} = b\nv\n"+
		"opaque type Meters Int\nm = Meters(4)\nMeters(k) = m\nk\n"+
		"opaque enum Shape\n  Sq(Int)\n  Dot\nend\ns = Shape.Sq(2)\ncase s\n  Shape.Sq(n) -> n\n  Shape.Dot -> 0\nend\n",
		"8\n10\n2\n5\n9\nP{x: 4}\n18\n3\n3\n4\n2\n")
}

// A later input that leaves out a defaulted parameter of an earlier
// input's function gets the default, whatever the function's level and
// though nothing called it when its input was lowered: by position, past a
// named argument, from a later function, a default that calls an earlier
// function and reads an earlier once, a generic function's and an
// inherent impl function's.
func TestReplVM_ALaterInputLeavesAnEarlierDefaultOut(t *testing.T) {
	for _, mod := range []string{"", "pub ", "private "} {
		replExpect(t, mod+"fn e(a: Int, b: Int = 10, c: Int = 100) -> Int\n  a + b + c\nend\n"+
			"e(1)\ne(1, c: 5)\nfn h() -> Int\n  e(2)\nend\nh()\n"+
			"once t: Int = 7\nfn ten() -> Int\n  10\nend\n"+
			mod+"fn f(a: Int, b: Int = ten() + t) -> Int\n  a + b\nend\nf(1)\n"+
			mod+"fn g<T>(a: T, b: Int = 10) -> (T, Int)\n  (a, b)\nend\ng(\"x\")\n"+
			mod+"struct P\n  x: Int\nend\nimpl P\n  "+mod+"fn add(p: P, b: Int = 10) -> Int\n    p.x + b\n  end\nend\nP.add(P{x: 1})\n",
			"111\n16\n112\n18\n(\"x\", 10)\n11\n")
	}
}

// The session's files are one file only in a session. The same
// declarations in a file of a project stay that file's: another file of
// the project names none of them and does not reach inside the opaque type.
func TestReplVM_TwoFilesOfAProjectStayApart(t *testing.T) {
	earlier := "private fn secret(n: Int) -> Int\n  n * 2\nend\n\nprivate once p: Int = 5\n\n" +
		"opaque struct Box\n  v: Int\nend\n\nfn box() -> Box\n  Box{v: 1}\nend\n"
	entry := "import earlier.{secret, p, Box}\nimport earlier\n\nfn main()\n  _ = secret(4)\n  _ = p\n" +
		"  b = Box{v: 3}\n  _ = b.v\n  Box{v:} = earlier.box()\n  _ = v\nend\n"
	_, err := vmhost.LoadSource("main", entry, vmhost.WithVirtualFiles(map[string]string{"earlier": earlier}))
	if err == nil {
		t.Fatal("the front end admits a private declaration and an opaque type's inside named from another file of a project")
	}
	var ds vmhost.Diagnostics
	if !errors.As(err, &ds) {
		t.Fatalf("the rejection is not diagnostics: %v", err)
	}
	var got []string
	for _, d := range ds {
		got = append(got, fmt.Sprintf("%d %s %s", d.Line, d.Code, d.Message))
	}
	for _, want := range []string{
		"1 " + analysis.PrivateDeclarationCode + " `secret` is private to `earlier.nomi`",
		"1 " + analysis.PrivateDeclarationCode + " `p` is private to `earlier.nomi`",
		"7 " + analysis.OpaqueConstructionCode + " constructor of opaque type 'Box' is private to `earlier.nomi`",
		"8  cannot access field 'v' of opaque type 'Box' outside `earlier.nomi`",
		"9  cannot destructure opaque type 'Box' outside `earlier.nomi`",
	} {
		found := false
		for _, g := range got {
			found = found || strings.HasPrefix(g, want)
		}
		if !found {
			t.Errorf("no diagnostic %q among:\n%s", want, strings.Join(got, "\n"))
		}
	}
}

// The prompt reads a declaration after `private` to its `end`, as it reads
// one after `pub` or with no modifier, so each declaration below is one
// input that checks and runs.
func TestReplVM_APrivateDeclarationIsReadToItsEnd(t *testing.T) {
	for _, mod := range []string{"", "pub ", "private "} {
		replExpect(t, mod+"fn f() -> Int\n  1\nend\n"+
			mod+"struct S\n  x: Int\nend\n"+
			mod+"once o: Int =\n  2\n"+
			mod+"once p: Int = begin\n  3\nend\n",
			"")
	}
}
