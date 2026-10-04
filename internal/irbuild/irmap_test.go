package irbuild

import (
	"testing"
)

func TestIRMap_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn add(m: Map<String, Int>): Map<String, Int> { Map.put(m, "c", 3) }
fn main() {
  original = {"a" => 1, "b" => 2, "a" => 4}
  updated = add(original)
  io.print(Map.size(original))
  io.print(Map.size(updated))
  io.inspect(Map.get(original, "a"))
  io.inspect(Map.get(original, "c"))
  io.inspect(Map.get(updated, "c"))
  get = |key: String| Map.get(updated, key)
  io.inspect(get("b"))
  config = {"host" => "localhost", "port" => "8080"}
  io.inspect(Map.get(config, "host"))
  io.inspect(Map.get(config, "user"))
}
`, "2\n3\nSome(4)\nNone\nSome(3)\nSome(2)\nSome(\"localhost\")\nNone\n")
}

func TestIRMap_EvaluationOrder(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn key(s: String): String { io.print(s) s }
fn val(n: Int): Int { io.print(n) n }
fn mark(m: Map<String, Int>): Map<String, Int> { io.print("map") m }
fn main() {
  m = {key("a") => val(1), key("b") => val(2)}
  n = Map.put(mark(m), key("c"), val(3))
  io.inspect(Map.get(mark(n), key("c")))
  io.print(Map.size(m))
}
`, "a\n1\nb\n2\nmap\nc\n3\nmap\nc\nSome(3)\n2\n")
}

func TestIRMap_FloatAndBoolKeys(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  nan = 0.0 / 0.0
  m = {nan => "first", 0.0 / 0.0 => "last", -0.0 => "zero"}
  io.print(Map.size(m))
  io.inspect(Map.get(m, nan))
  io.inspect(Map.get(m, 0.0))
  flags = {True => 7, False => 8}
  io.inspect(Map.get(flags, False))
  nums = {1 => True, 2 => False}
  io.inspect(Map.get(nums, 2))
}
`, "2\nSome(\"last\")\nSome(\"zero\")\nSome(8)\nSome(False)\n")
}

func TestIRMap_MultilineAndPipelines(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn key(s: String): String { io.print(s) s }
fn val(n: Int): Int { io.print(n) n }
fn pick(b: Bool, a: Map<String, Int>, c: Map<String, Int>): Map<String, Int> {
  if b { a } else { c }
}
fn main() {
  a = {
    key("a") => val(1),
    key("b") => val(2)
  }
  b = a
    |> Map.put(key("c"), val(3))
  pick(False, a, b)
    |> Map.get(key("c"))
    |> io.inspect()
  a |> Map.size() |> io.print()
}
`, "a\n1\nb\n2\nc\n3\nc\nSome(3)\n2\n")
}

// A distinct key (`{Key(1) => 2}`) retains since map keys are structural
// values; TestIRMapFuncs_Intrinsics runs one.
func TestIRMap_DiscardedBuildKeepsNativeEmission(t *testing.T) {
	for _, src := range []string{
		`struct Point { x: Int }
struct Outer {
  p: List<Point>
  hooks: Map<String, (Int) -> Int> = Map.empty()
}
fn main() { m = {"a" => 1} dbg Outer{p: [Point{x: Map.size(m)}]} return }`,
		`fn main() { m = {"a" => [1, 2]} dbg Map.get(m, "a") return }`,
	} {
		p, err := AnalyzeSource("main.nomi", src)
		if err != nil {
			t.Fatalf("front end rejects boundary control: %v", err)
		}
		got, _, err := GenerateIR(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range got.IR {
			for _, f := range m.Funcs() {
				if f.Name() == "main" {
					t.Fatal("unsupported map/Debug body retained")
				}
			}
		}
	}
}
