package irbuild

import (
	"testing"
)

// A `?` runs before a struct whose field holds a Map of function values
// is built from its value.
func TestIRRetainedTry_BesideAFunctionMapFieldRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
struct Point
    x: Int
end
struct Outer
  p: List<Point>
  hooks: Map<String, (Int) -> Int> = {}
end
fn get() -> Result<Int, String>
    Ok(42)
end
fn work() -> Result<Int, String>
  n = get()?
  io.inspect(Outer{p: [Point{x: n}]})
  Ok(n)
end
fn main()
    io.inspect(work())
end
`, "Outer{p: [Point{x: 42}], hooks: {}}\nOk(42)\n")
}

func TestIRRetainedTry_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn get(ok: Bool) -> Result<Int, String>
    if ok then Ok(42) else Err("bad")
end
fn transform(ok: Bool) -> Result<String, String>
  n = get(ok)?
  io.print("success")
  Ok("value ${n}")
end

fn show(ok: Bool) -> String
  case transform(ok)
      Ok(s) -> s
      Err(e) -> e
  end
end
fn main()
    io.print(show(True))
    io.print(show(False))
end
`, "success\nvalue 42\nbad\n")
}

func TestIRRetainedTry_MaybeAndPipes(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn get(ok: Bool) -> Maybe<Int>
    if ok then Some(42) else None
end
fn transform(ok: Bool) -> Maybe<String>
  n = ok
    |> get()?
  io.print("success")
  Some("value ${n}")
end
fn show(ok: Bool) -> String
  case transform(ok)
      Some(s) -> s
      None -> "empty"
  end
end
fn main()
    io.print(show(True))
    io.print(show(False))
end
`, "success\nvalue 42\nempty\n")
}

func TestIRRetainedTry_OperandOrderAndNestedCalls(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    return n
end
fn get(ok: Bool) -> Result<Int, String>
  io.print("get")
  if ok then Ok(20) else Err("bad")
end
fn add(a: Int, b: Int, c: Int) -> Int
    a + b + c
end
fn inner(ok: Bool) -> Result<Int, String>
  Ok(add(mark(1), get(ok)?, mark(3)))
end
fn outer(ok: Bool) -> Result<Int, String>
  n = inner(ok)?
  io.print("outer")
  Ok(n + 1)
end
fn show(ok: Bool) -> String
  case outer(ok)
      Ok(n) -> "value ${n}"
      Err(e) -> e
  end
end
fn main()
    io.print(show(True))
    io.print(show(False))
end
`, "1\nget\n3\nouter\nvalue 25\n1\nget\nbad\n")
}

func TestIRRetainedTry_PureAndConditionalOperands(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn transform(r: Result<Int, String>, flag: Bool) -> Result<Int, String>
  if flag then Ok(r?) else Ok(Ok(7)?)
end
fn maybe(m: Maybe<Int>) -> Maybe<Int>
    Some(m?)
end
fn show(r: Result<Int, String>) -> String
    case r
        Ok(n) -> "${n}"
        Err(e) -> e
    end
end
fn read(m: Maybe<Int>) -> Int
    case m
        Some(n) -> n
        None -> 0
    end
end
fn main()
  io.print(show(transform(Ok(42), True)))
  io.print(show(transform(Err("bad"), True)))
  io.print(show(transform(Err("ignored"), False)))
  io.print(read(maybe(Some(9))))
  io.print(read(maybe(None)))
end
`, "42\nbad\n7\n9\n0\n")
}
