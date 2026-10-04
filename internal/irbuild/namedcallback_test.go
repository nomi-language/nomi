package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A named function passed where a function value is expected runs, whatever
// spelling names it: a top-level fn, an owner-qualified impl fn, a
// selectively imported or file-qualified sibling, and through a pipe.
// `Iter.reduce` with a function value has no seed, so the first element
// seeds the accumulator.
func TestNamedCallback_FunctionValuesRun(t *testing.T) {
	irBacklogProgramVM(t, map[string]string{
		"util.nomi": `pub fn plus(a: Int, b: Int): Int {
  a + b
}

pub fn small?(n: Int): Bool {
  n < 3
}
`,
		"main_test.nomi": `import util
import util.{plus}

struct Money {
  cents: Int
}

impl Money {
  fn sum(a: Money, b: Money): Money {
    Money{cents: a.cents + b.cents}
  }

  fn double(n: Int): Int {
    n * 2
  }
}

fn add(a: Int, b: Int): Int {
  a + b
}

fn longer(a: String, b: String): String {
  if String.length(b) > String.length(a) { b } else { a }
}

fn is_even(n: Int): Bool {
  n % 2 == 0
}

test "a top-level fn as a reduction" {
  assert Iter.reduce([1, 2, 3], add) == 6
  assert [1, 2, 3, 4] |> Iter.reduce(add) == 10
  assert Iter.reduce(["a", "ccc", "bb"], longer) == "ccc"
}

test "a top-level fn as an adapter" {
  assert Iter.filter([1, 2, 3, 4], is_even) |> Iter.to_list() == [2, 4]
}

test "an imported fn" {
  assert Iter.reduce([1, 2, 3], plus) == 6
  assert Iter.reduce([1, 2, 3], util.plus) == 6
  assert Iter.filter([1, 2, 3, 4], util.small?) |> Iter.to_list() == [1, 2]
}

test "an owner-qualified impl fn" {
  assert Iter.reduce([Money{cents: 1}, Money{cents: 2}], Money.sum).cents == 3
  assert Iter.map([1, 2], Money.double) |> Iter.to_list() == [2, 4]
}
`,
	}, "main_test.nomi", 4)
}

// A named function that uses `break` or `continue` (an iter-sensitive
// function) passed as the callback of the iteration operations that catch
// them. Its body is lowered in the signalling form its arity names: two
// parameters is a reduction's, one an adapter's or `Iter.each`'s.
func TestNamedCallback_IterSensitiveFunctionsRun(t *testing.T) {
	const src = `import std/io

fn skip_odd(n: Int): Int {
  if n % 2 == 1 {
    continue
  }
  n * 10
}

fn keep_until_big(n: Int): Bool {
  if n > 3 {
    break
  }
  n % 2 == 0
}

fn below_five(n: Int): Bool {
  if n >= 5 {
    break False
  }
  True
}

fn sum_small(total: Int, n: Int): Int {
  if n < 0 {
    continue
  }
  if total > 10 {
    break total
  }
  total + n
}

fn stop_at_zero(total: Int, n: Int): Int {
  if n == 0 {
    break
  }
  total + n
}

fn show_until_three(n: Int): Unit {
  if n == 3 {
    break
  }
  io.print("each ${n}")
}

fn double_until(n: Int): Int {
  if n > 20 {
    break
  }
  n * 2
}

test "map skips with continue" {
  assert Iter.map([1, 2, 3, 4], skip_odd) |> Iter.to_list() == [20, 40]
  assert [1, 2, 3, 4] |> Iter.map(skip_odd) |> Iter.to_list() == [20, 40]
}

test "filter stops with a bare break" {
  assert Iter.filter([1, 2, 3, 4, 5, 6], keep_until_big) |> Iter.to_list() == [2]
}

test "take_while stops with break False" {
  assert Iter.take_while([1, 2, 7, 3], below_five) |> Iter.to_list() == [1, 2]
}

test "reduce continues and breaks with the accumulator" {
  assert Iter.reduce([1, -5, 2, 3, 20, 4], sum_small) == 26
  assert Iter.reduce([1, 2, 0, 4], stop_at_zero) == 3
}

test "each stops" {
  Iter.each([1, 2, 3, 4], show_until_three)
  assert True
}

test "iterate stops" {
  assert Iter.iterate(1, double_until) |> Iter.to_list() == [1, 2, 4, 8, 16]
}
`
	out := irTestBodyVM(t, src, 6)
	if !strings.Contains(out, "each 1\neach 2\n") || strings.Contains(out, "each 3") {
		t.Errorf("Iter.each did not stop at the named callback's break:\n%s", out)
	}
}

// An iter-sensitive function called directly, even from inside a callback
// lambda the checker admits, has nowhere to send its signal: the builder
// declines the caller rather than leaking the signal as a value.
func TestNamedCallback_IterSensitiveDirectCallDeclines(t *testing.T) {
	const src = `import std/io

fn skip_odd(n: Int): Int {
  if n % 2 == 1 {
    continue
  }
  n
}

fn evens(xs: List<Int>): List<Int> {
  Iter.map(xs, |x| skip_odd(x)) |> Iter.to_list()
}

fn main() {
  io.print(evens([1, 2]))
}
`
	declined := irDeclinesOf(t, src)
	if got := declined["evens"]; !strings.Contains(got, "a direct call to a function that uses break or continue: skip_odd") {
		t.Fatalf("evens declined for %q; want the direct-call reason (all: %v)", got, declined)
	}
}

// `random.Generator.bool()` names a std module's type whose member is
// instantiated per program; it lowers as `Generator.bool()` does.
func TestNamedCallback_StdModuleTypeQualifiedGenericMember(t *testing.T) {
	const src = `import std/random
import std/random.{Generator, Seed}

test "the module-qualified spelling" {
  seed = random.Seed.from_int(7)
  (a, _) = random.Generator.step(random.Generator.bool(), seed)
  (b, _) = Generator.step(Generator.bool(), Seed.from_int(7))
  assert a == b
  (n, _) = random.Generator.step(random.Generator.constant(3), seed)
  assert n == 3
}
`
	irTestBodyVM(t, src, 1)
}

// A std type imported under an alias is that type as an owner qualifier.
func TestNamedCallback_StdOwnerImportedUnderAnAlias(t *testing.T) {
	const src = `import std/iter.Iter as It
import std/strings.String as Str

test "aliased owners" {
  assert It.count([1, 2]) == 2
  assert It.map([1, 2], |x| x + 1) |> It.to_list() == [2, 3]
  assert Str.length("abc") == 3
}
`
	irTestBodyVM(t, src, 1)
}

// `derive FromJson` decodes a `Maybe<T>` field with `Maybe<T>.from_json`,
// a call qualified by an applied generic type.
func TestNamedCallback_DeriveFromJsonMaybeField(t *testing.T) {
	const src = `import std/json.{Json, FromJson}

struct User {
  name: String
  age: Maybe<Int>
  nick: Maybe<String>
}

derive FromJson for User

fn decode(src: String): Result<User, String> {
  case Json.decode(src) {
    Ok(j) -> Result.map_err(User.from_json(j), |e| Json.ShapeError.to_string(e))
    Err(_) -> Err("bad json")
  }
}

test "present, absent and mistyped optional fields" {
  assert Ok(a) = decode("{\"name\": \"a\", \"age\": 3}")
  assert a.age == Some(3)
  assert a.nick == None
  assert Ok(b) = decode("{\"name\": \"b\", \"nick\": \"bee\"}")
  assert b.nick == Some("bee")
  assert decode("{\"name\": \"c\", \"age\": \"old\"}") == Err("age: expected int, got string")
}
`
	irTestBodyVM(t, src, 1)
}

// A program's own `Date` beside std/calendar's: an owner-qualified call that
// omits a default, or names an argument past an omitted one, inside an
// assertion subject is the program's `Date.new`, never std's.
func TestNamedCallback_LocalOwnerSharingAStdNameWithDefaults(t *testing.T) {
	const src = `struct Date {
  year: Int
  month: Int
  day: Int
}

impl Date {
  fn new(year: Int, month: Int = 1, day: Int = 1): Date {
    Date{year, month, day}
  }

  fn same?(a: Date, b: Date, strict: Bool = True): Bool {
    a.year == b.year and (!strict or a.day == b.day)
  }
}

test "an omitted default in an assertion subject" {
  assert Date.new(2026).month == 1
}

test "a named argument past an omitted default" {
  assert Date.new(2026, day: 5).day == 5
  assert Date.new(2026, day: 5).month == 1
}

test "a failing named call reports the written arguments" {
  y = 2026
  d = 4
  assert Date.same?(Date.new(y, day: d), Date.new(y), strict: True)
}
`
	out := irTestBodyVM(t, src, 3)
	if !strings.Contains(out, "Date.new(y, day: d)\n        = Date{day: 4, month: 1, year: 2026}") {
		t.Errorf("the failing case does not report the named call's value:\n%s", out)
	}
}

// A non-scalar `${x}` hole in a std body renders as it does in user code, so
// the body is retained. This isolates the std-side gate with a structural
// value, which needs no impl lookup in the one-module std this harness builds.
func TestNamedCallback_StdBodyInterpolatesAStructuralValue(t *testing.T) {
	const body = "pub fn label(pair: (Int, String)): String {\n  \"pair ${pair}\"\n}\n"
	var c irFuncRetentionCount
	defer c.observe(t, irFromStd)()
	lowerStdSourceOnly(t, body)
	if !slicesContain(c.retained, "irstdcall.label") {
		t.Fatalf("a std body interpolating a tuple was not retained: retained %v, walked %v",
			c.retained, c.walked)
	}
}

// irDeclinesOf lowers src as a one-file program and answers the first decline
// reason of each body the builder declined. The front end must accept src.
func irDeclinesOf(t *testing.T, src string) map[string]string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects this, so the decline is not what is tested: %v", err)
	}
	declined := map[string]string{}
	prev := IRDeclineObserved
	IRDeclineObserved = func(fn, reason string) { declined[fn] = reason }
	defer func() { IRDeclineObserved = prev }()
	if _, _, err := GenerateIR(p); err != nil {
		t.Fatal(err)
	}
	return declined
}
