package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

const irMapPatternSource = `import std/io

enum JV {
  Num Int
  Obj Map<String, JV>
  Arr List<JV>
  Pos (Int, Int)
}

fn describe(m: Map<String, Int>): String {
  case m {
    {"a" => 1, "b" => b} -> "one and ${b}"
    {"a" => a} -> "a=${a}"
    _ -> "none"
  }
}

fn pick(m: Map<Int, String>): String {
  {1 => one, 2 => _} = m
  one
}

fn nested(x: Maybe<Map<Int, String>>): String {
  case x {
    Some({-2 => v}) -> v
    _ -> "no match"
  }
}

fn composite(m: Map<(Int, Int), String>, flags: Map<Bool, String>): String {
  a = case m {
    {(3, 4) => v} -> v
    _ -> "?"
  }
  b = case flags {
    {JV.Num(1) => v} -> v
    {True => v} -> v
    _ -> "?"
  }
  a + b
}

fn attached(j: JV): String {
  case j {
    .Obj{"k" => .Num(n)} -> "obj ${n}"
    .Arr[.Num(a), .Num(b), ..rest] -> "arr ${a} ${b} ${Iter.count(rest)}"
    .Pos(x, y) -> "pos ${x} ${y}"
    _ -> "other"
  }
}

fn main() {
  io.print(describe({"a" => 1, "b" => 7}))
  io.print(describe({"a" => 3}))
  io.print(describe({"z" => 3}))
  io.print(pick({1 => "x", 2 => "y"}))
  io.print(nested(Some({-2 => "neg"})))
  io.print(nested(None))
  io.print(composite({(1, 2) => "a", (3, 4) => "b"}, {True => "t"}))
  io.print(attached(JV.Obj{"k" => JV.Num(7)}))
  io.print(attached(JV.Arr[JV.Num(1), JV.Num(2), JV.Num(3)]))
  io.print(attached(JV.Arr[JV.Num(1)]))
  io.print(attached(JV.Pos(4, 5)))
  io.inspect(JV.Obj{"k" => JV.Arr[JV.Num(1), JV.Pos(2, 3)]})
}
`

// Map patterns and destructuring lower to Map.get, a Some test and the
// payload's own pattern, in entry order; keys may be composite values or of
// another type than the map's (a lookup that misses); literal-attach
// patterns over an enum read as the variant pattern; and a user enum may
// carry maps and lists of itself and a tuple payload.
func TestIRMapPattern_CaseDestructureAndAttach(t *testing.T) {
	names := irRetainedFuncNames(t, irMapPatternSource)
	for _, fn := range []string{"describe", "pick", "nested", "composite", "attached", "main"} {
		if !names[fn] {
			t.Errorf("%s was not retained", fn)
		}
	}
	verifyLambdaProgram(t, irMapPatternSource,
		"one and 7\na=3\nnone\nx\nneg\nno match\nbt\nobj 7\narr 1 2 1\nother\npos 4 5\n"+
			"Obj({\"k\" => Arr([Num(1), Pos(2, 3)])})\n")
}

// A destructured key the map lacks faults with rt.MapKeyMissingError's text
// at the statement's line, the key rendered by Display.
func TestIRMapPattern_MissingDestructuredKeyFaults(t *testing.T) {
	src := `import std/io

fn pick(m: Map<Int, String>): String {
  {1 => one, 2 => _} = m
  one
}

fn main() {
  io.print(pick({1 => "x"}))
}
`
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var entry *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			entry = m
		}
	}
	if entry == nil {
		t.Fatal("no module for the program")
	}
	var out bytes.Buffer
	booted, err := vm.NewProgram(entry, res.IRModules(), &out).Boot()
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := booted.Run("main")
	const want = "line 4: key 2 not found in map"
	if runErr == nil || !strings.Contains(runErr.Error(), want) {
		t.Fatalf("VM error %v; want %q", runErr, want)
	}
	if got := vmReference(path); got.exit == 0 || !strings.Contains(got.stderr, want) {
		t.Fatalf("vm command: %s; want %q", got, want)
	}
}

const irMapFuncsSource = `import std/io

type Key Int

fn main() {
  io.inspect(Map.get({Key(1) => 2}, Key(1)))
  m = {"q" => 1, "r" => 2}
  io.inspect(Map.keys(m))
  io.inspect(Map.values(Map.remove(m, "q")))
  io.inspect(Map.map_values(m, |v| v * 10))
  io.inspect(Map.contains_key?(m, "r"))
  io.inspect(Map.merge(m, {"r" => 9, "s" => 3}))
  io.inspect(Map.map_keys({"a" => 1, "b" => 2, "c" => 3}, |k| k == "c"))
  s: Set<Int> = Set.new()
  io.inspect(Set.size(s))
  io.inspect(#{1, 2} == #{2, 1})
}
`

// Map.keys, values, remove, contains_key?, merge and map_values are machine
// intrinsics over rt's map, in the keys' insertion order.
func TestIRMapFuncs_Intrinsics(t *testing.T) {
	verifyLambdaProgram(t, irMapFuncsSource,
		"Some(2)\n[\"q\", \"r\"]\n[2]\n{\"q\" => 10, \"r\" => 20}\nTrue\n{\"q\" => 1, \"r\" => 9, \"s\" => 3}\n{False => 2, True => 3}\n0\nTrue\n")
}

const irEnumFieldSource = `import std/io

struct Circle {
  radius: Float
}

enum Shape {
  Rect {height: Int, label: String = "r"}
  Wrap Circle
  embeds Circle2
  Num Int
  Point
}

struct Circle2 {
  radius: Float
  height: Int
}

fn height(s: Shape): Int {
  s.height
}

fn main() {
  r = Shape.Rect{height: 2}
  io.print(r.height)
  io.print(r.label)
  io.print(height(Shape.Circle2{radius: 1.0, height: 9}))
  w = Shape.Wrap(Circle{radius: 2.5})
  io.print(w.radius)
  io.print(height(w))
}
`

// A field read on an enum value whose variant is not known reads the
// struct-shaped variant's field, an embedded struct's or a positional struct
// payload's (ir.ProjEnumField); a variant that lacks it faults with rt's
// text, keyed on that variant.
func TestIREnumField_ReadsAndFaults(t *testing.T) {
	names := irRetainedFuncNames(t, irEnumFieldSource)
	for _, fn := range []string{"height", "main"} {
		if !names[fn] {
			t.Errorf("%s was not retained", fn)
		}
	}
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(irEnumFieldSource), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var entry *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			entry = m
		}
	}
	var out bytes.Buffer
	booted, err := vm.NewProgram(entry, res.IRModules(), &out).Boot()
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := booted.Run("main")
	const wantOut = "2\nr\n9\n2.5\n"
	const wantErr = "line 21: variant 'Wrap' has no field 'height'"
	if out.String() != wantOut || runErr == nil || !strings.Contains(runErr.Error(), wantErr) {
		t.Fatalf("VM printed %q and faulted %v; want %q and %q", out.String(), runErr, wantOut, wantErr)
	}
	if got := vmReference(path); got.stdout != wantOut || !strings.Contains(got.stderr, wantErr) {
		t.Fatalf("vm command: %s; want %q and %q", got, wantOut, wantErr)
	}
}
