package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// A 3-segment qualifier names a namespaced type (`Probe.Reading.Spike(3)`,
// `Json.DecodeError.to_string(e)`) or a stdlib module's type
// (`calendar.Date.parse`). Each resolves to the owner the 2-segment spelling
// would, and runs on the VM.
func TestIRBacklogCalls_ThreeSegmentQualifier(t *testing.T) {
	const src = `import std/calendar
import std/json.Json

pub enum Probe.Reading {
  Steady
  Spike Int
}

fn severity(r: Probe.Reading): Int {
  case r {
    Probe.Reading.Steady -> 0
    Probe.Reading.Spike(n) -> n
  }
}

fn decode_error(src: String): String {
  case Json.decode(src) {
    Ok(_) -> "parsed"
    Err(e) -> Json.DecodeError.to_string(e)
  }
}

test "a namespaced variant, bare and with a payload" {
  assert severity(Probe.Reading.Steady) == 0
  assert severity(Probe.Reading.Spike(3)) == 3
}

test "a namespaced type's impl function" {
  assert decode_error("not json") == "json decode error at line 1, col 2: unexpected character 'o'"
}

test "a stdlib module's type-owned function" {
  got = case calendar.Date.parse("05/04/2026") {
    Ok(_) -> "parsed"
    Err(_) -> "rejected"
  }
  assert got == "rejected"
}
`
	irTestBodyVM(t, src, 3)
}

// A direct call in a test body may omit defaulted parameters, name its
// arguments, or route a trailing function past a defaulted slot. The failing
// cases pin the `values:` rows: positional arguments first against their own
// position's slot, then named ones, and a slot the callee defaulted records
// nothing (recordCallSlots).
func TestIRBacklogCalls_DefaultsAndNamedArgumentsInATestBody(t *testing.T) {
	const src = `fn greet(name: String, greeting: String = "Hello", mark: String = "!"): String {
  greeting + ", " + name + mark
}

fn skip(req: Int, opt: String = "a", scale: Int = 2, cb: (Int) -> Int): Int {
  cb(req) * scale + String.length(opt)
}

fn double(n: Int): Int {
  n * 2
}

test "an omitted default" {
  assert greet("World") == "Hello, World!"
  assert greet("World", "Hi") == "Hi, World!"
}

test "named arguments and a trailing function" {
  assert greet("Ada", mark: "?") == "Hello, Ada?"
  assert skip(5, double) == 21
  assert skip(5, scale: 3, double) == 31
  assert skip(req: 5, |n| n + 1) == 13
}

test "a failing short call reports its written arguments" {
  who = "World"
  assert greet(who) == "Hi, World!"
}

test "a failing named call reports positional rows then named rows" {
  n = 5
  s = 3
  assert skip(n, scale: s, double) == 0
}
`
	irTestBodyVM(t, src, 4)
}

// irBacklogProgramVM writes files into one temp directory, lowers entry, and
// runs its cases on the VM against the golden record, as irTestBodyVM does
// for a single file.
func irBacklogProgramVM(t *testing.T, files map[string]string, entry string, wantCases int) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, entry)
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	prev := IRDeclineObserved
	IRDeclineObserved = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	IRDeclineObserved = prev
	if err != nil {
		t.Fatal(err)
	}
	var module *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			module = m
		}
	}
	if module == nil || len(module.Tests()) != wantCases {
		t.Fatalf("retained %v; want %d case(s) (declines: %v)", module, wantCases, declined)
	}
	var out bytes.Buffer
	exit, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path)
	if reason != "" {
		t.Fatalf("the VM could not run the cases: %s", reason)
	}
	golden := goldenReference(t, path)
	if out.String() != golden.stdout || exit != golden.exit {
		t.Errorf("VM and golden output differ:\nVM (exit %d):\n%s\ngolden (exit %d):\n%s",
			exit, out.String(), golden.exit, golden.stdout)
	}
}

// A bare call to a selectively imported sibling `fn`, aliased or not, links to
// the declaring file's body; a bare `loop` imported from std/iter is Iter.loop.
func TestIRBacklogCalls_BareImportedCallees(t *testing.T) {
	irBacklogProgramVM(t, map[string]string{
		"api.nomi": `pub fn make(name: String): String {
  "widget:" + name
}

pub fn twice(n: Int): Int {
  n * 2
}
`,
		"main_test.nomi": `import {
  api.{make, twice as double}
  std/iter.Iter.loop
}

fn countdown(from: Int): Int {
  loop(|n = from| {
    if n == 0 { break n }
    n - 1
  })
}

test "a selectively imported sibling fn" {
  assert make("Ada") == "widget:Ada"
  assert double(21) == 42
}

test "a bare loop" {
  assert countdown(5) == 0
}

test "a failing bare sibling call reports its argument" {
  who = "Bo"
  assert make(who) == "widget:Ada"
}
`,
	}, "main_test.nomi", 3)
}

// A call to an erased generic function, bounded or not, runs a
// monomorphic instance on the VM (irMonoTemplate), from a named function or a
// test body; a failing assertion records the call's argument row.
func TestIRBacklogCalls_ErasedGenericCallees(t *testing.T) {
	const src = `interface Showable {
  fn show(value: self): String
}

struct User {
  name: String
}

impl Showable for User {
  fn show(u: User): String {
    "user:" + u.name
  }
}

fn announce<T>(value: T): String where T: Showable {
  "announce(" + Showable.show(value) + ")"
}

fn same<T>(value: T): T {
  value
}

fn via(): String {
  announce(User{name: "Alice"})
}

test "from a named function" {
  assert via() == "announce(user:Alice)"
}

test "from a test body" {
  assert announce(User{name: "Bob"}) == "announce(user:Bob)"
  assert same(3) == 3
  assert same("x") == "x"
}

test "a failing generic call reports its argument" {
  n = 4
  assert same(n) == 5
}
`
	irTestBodyVM(t, src, 3)
}

// The struct call form with a literal argument builds the brace literal:
// written fields in order, then the omitted fields' defaults.
func TestIRBacklogCalls_StructCallFormWithALiteral(t *testing.T) {
	const src = `struct Defaulted {
  a: Int
  b: String = "default"
}

test "a literal argument" {
  d = Defaulted({a: 7})
  e = Defaulted({a: 1, b: "x"})
  assert d.a == 7
  assert d.b == "default"
  assert e.b == "x"
}
`
	irTestBodyVM(t, src, 1)
}
