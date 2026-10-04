package irbuild

import (
	"testing"
)

func TestIRRetainedTry_DeclinedBuildKeepsNativeEmission(t *testing.T) {
	const src = `import std/io
struct Point { x: Int }
struct Outer {
  p: List<Point>
  hooks: Map<String, (Int) -> Int> = Map.empty()
}
fn get(): Result<Int, String> { Ok(42) }
fn work(): Result<Int, String> {
  n = try get()
  dbg Outer{p: [Point{x: n}]}
  Ok(n)
}
fn main() { io.inspect(work()) }`
	p, err := AnalyzeSource("main.nomi", src)
	if err != nil {
		t.Fatal(err)
	}
	retained, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range retained.IR {
		for _, fn := range mod.Funcs() {
			if fn.Name() == "work" {
				t.Fatal("nominal Debug must decline after try lowering")
			}
		}
	}
}

func TestIRRetainedTry_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn get(ok: Bool): Result<Int, String> { if ok { Ok(42) } else { Err("bad") } }
fn transform(ok: Bool): Result<String, String> {
  n = try get(ok)
  io.print("success")
  Ok("value ${n}")
}

fn show(ok: Bool): String {
  case transform(ok) { Ok(s) -> s Err(e) -> e }
}
fn main() { io.print(show(True)) io.print(show(False)) }
`, "success\nvalue 42\nbad\n")
}

func TestIRRetainedTry_MaybeAndPipes(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn get(ok: Bool): Maybe<Int> { if ok { Some(42) } else { None } }
fn transform(ok: Bool): Maybe<String> {
  n = ok
    |> get()
    |> try
  io.print("success")
  Some("value ${n}")
}
fn show(ok: Bool): String {
  case transform(ok) { Some(s) -> s None -> "empty" }
}
fn main() { io.print(show(True)) io.print(show(False)) }
`, "success\nvalue 42\nempty\n")
}

func TestIRRetainedTry_OperandOrderAndNestedCalls(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn mark(n: Int): Int { io.print(n) return n }
fn get(ok: Bool): Result<Int, String> {
  io.print("get")
  if ok { Ok(20) } else { Err("bad") }
}
fn add(a: Int, b: Int, c: Int): Int { a + b + c }
fn inner(ok: Bool): Result<Int, String> {
  Ok(add(mark(1), try get(ok), mark(3)))
}
fn outer(ok: Bool): Result<Int, String> {
  n = try inner(ok)
  io.print("outer")
  Ok(n + 1)
}
fn show(ok: Bool): String {
  case outer(ok) { Ok(n) -> "value ${n}" Err(e) -> e }
}
fn main() { io.print(show(True)) io.print(show(False)) }
`, "1\nget\n3\nouter\nvalue 25\n1\nget\nbad\n")
}

func TestIRRetainedTry_PureAndConditionalOperands(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn transform(r: Result<Int, String>, flag: Bool): Result<Int, String> {
  if flag { Ok(try r) } else { Ok(try Ok(7)) }
}
fn maybe(m: Maybe<Int>): Maybe<Int> { Some(m |> try) }
fn show(r: Result<Int, String>): String { case r { Ok(n) -> "${n}" Err(e) -> e } }
fn read(m: Maybe<Int>): Int { case m { Some(n) -> n None -> 0 } }
fn main() {
  io.print(show(transform(Ok(42), True)))
  io.print(show(transform(Err("bad"), True)))
  io.print(show(transform(Err("ignored"), False)))
  io.print(read(maybe(Some(9))))
  io.print(read(maybe(None)))
}
`, "42\nbad\n7\n9\n0\n")
}
