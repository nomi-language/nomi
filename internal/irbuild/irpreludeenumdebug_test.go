package irbuild

import "testing"

func TestIRPreludeEnumDebug_TransparentAndOrdered(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn get(): Maybe<Int> { io.print("get") Some(42) }
fn read(m: Maybe<Int>): Int { case m { Some(n) -> n None -> 0 } }
fn main() {
  v = dbg get()
  io.print(read(v))
  io.inspect((v, True))
}
`, "get\ndbg line 5: get() = Some(42)\n42\n(Some(42), True)\n")
}

// A custom Debug impl on a user enum is called through its declaration; it
// is never replaced by structural rendering.
func TestIRPreludeEnumDebug_CustomImplIsCalled(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
enum Answer { Ok Int }
impl Debug for Answer { fn inspect(_a: Answer): String { "custom" } }
fn main() { io.inspect(Answer.Ok(42)) }`, "custom\n")
}

func TestIRPreludeEnumDebug_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import { std/io std/literals.Fragment }
fn maybe(ok: Bool): Maybe<String> { if ok { Some("a\\\"b") } else { None } }
fn result(ok: Bool): Result<Int, String> { if ok { Ok(42) } else { Err("bad") } }
fn main() {
  io.inspect(maybe(True))
  io.inspect(maybe(False))
  io.inspect(result(True))
  io.inspect(result(False))
  io.inspect(Fragment.Static<Int>("text"))
  io.inspect(Fragment.Dynamic(7))
}
`, "Some(\"a\\\\\\\"b\")\nNone\nOk(42)\nErr(\"bad\")\nStatic(\"text\")\nDynamic(7)\n")
}
