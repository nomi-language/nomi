package irbuild

import "testing"

// A `?` whose boundary's error type is an interface takes a concrete
// implementor's error (spec §11): the Err edge holds the error as the
// interface, a type std names its impl by and one the program declares
// alike.
func TestTry_IntoAnInterfaceErrorBoundary(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
struct Code
    n: Int
end
impl Display for Code
  fn to_string(c: Code) -> String
      "code ${c.n}"
  end
end
fn parse(ok: Bool) -> Result<Int, String>
    if ok then Ok(42) else Err("bad")
end
fn check(ok: Bool) -> Result<Int, Code>
    if ok then Ok(1) else Err(Code{n: 7})
end
fn both(a: Bool, b: Bool) -> Result<Int, Display>
  x = parse(a)?
  y = check(b)?
  Ok(x + y)
end
fn show(r: Result<Int, Display>) -> String
  case r
      Ok(n) -> "ok ${n}"
      Err(e) -> "err ${e}"
  end
end
fn main()
  io.print(show(both(True, True)))
  io.print(show(both(False, True)))
  io.print(show(both(True, False)))
end
`, "ok 43\nerr bad\nerr code 7\n")
}

// A program's own interface as the boundary's error type, taking a struct
// and an enum with an `embeds` variant, whose values enter it boxed.
func TestTry_IntoAProgramInterfaceErrorBoundary(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
interface Problem
  fn describe(p: self) -> String
end
struct Missing
    key: String
end
enum Failure
  embeds Missing
  Timeout
end
impl Problem for Missing
  fn describe(m: Missing) -> String
      "missing ${m.key}"
  end
end
impl Problem for Failure
  fn describe(f: Failure) -> String
    case f
        .Timeout -> "failure: timeout"
        _ -> "failure: other"
    end
  end
end
fn lookup(n: Int) -> Result<Int, Missing>
    if n > 0 then Ok(n) else Err(Missing{key: "n"})
end
fn wait(n: Int) -> Result<Int, Failure>
    if n < 10 then Ok(n * 2) else Err(.Timeout)
end
fn run(n: Int) -> Result<Int, Problem>
  a = lookup(n)?
  b = wait(a)?
  Ok(b)
end
fn show(r: Result<Int, Problem>) -> String
  case r
      Ok(n) -> "ok ${n}"
      Err(e) -> "err ${Problem.describe(e)}"
  end
end
fn main()
  io.print(show(run(3)))
  io.print(show(run(0)))
  io.print(show(run(20)))
end
`, "ok 6\nerr missing n\nerr failure: timeout\n")
}

// A `?` whose boundary's error type is an enum that embeds the operand's
// error type widens the error into the embedding variant, as a binding of
// the enum's type does.
func TestTry_IntoAnEmbeddingErrorBoundary(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
struct Missing
    key: String
end
enum Failure
  embeds Missing
  Timeout
end
fn lookup(n: Int) -> Result<Int, Missing>
    if n > 0 then Ok(n) else Err(Missing{key: "n"})
end
fn run(n: Int) -> Result<Int, Failure>
  a = lookup(n)?
  Ok(a)
end
fn main()
  io.inspect(run(3))
  widened: Failure = Missing{key: "n"}
  io.inspect(run(0) == Err(widened))
  case run(0)
    Err(.Timeout) -> io.print("timeout")
    Err(.Missing{key:}) -> io.print("missing ${key}")
    Ok(_) -> io.print("ok")
  end
end
`, "Ok(3)\nTrue\nmissing n\n")
}

// A `?` whose error is a structure the boundary's error type holds at wider
// type arguments rebuilds it there: a List of Ints as a List of Displays,
// a Maybe of an embedded struct as a Maybe of the enum.
func TestTry_IntoAWiderStructuredErrorBoundary(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
struct Missing
    key: String
end
enum Failure
  embeds Missing
  Timeout
end
fn codes(n: Int) -> Result<Int, List<Int>>
    if n > 0 then Ok(n) else Err([n, 1])
end
fn shown(n: Int) -> Result<Int, List<Display>>
  a = codes(n)?
  Ok(a)
end
fn lookup(n: Int) -> Result<Int, Maybe<Missing>>
    if n > 0 then Ok(n) else Err(Some(Missing{key: "k"}))
end
fn failed(n: Int) -> Result<Int, Maybe<Failure>>
  a = lookup(n)?
  Ok(a)
end
fn main()
  io.print(case shown(0)
      Ok(_) -> "ok"
      Err(es) -> "${es}"
  end)
  io.print(case shown(2)
      Ok(n) -> "ok ${n}"
      Err(es) -> "${es}"
  end)
  io.inspect(failed(0))
  io.inspect(failed(4))
end
`, "[0, 1]\nok 2\nErr(Some(Missing{key: \"k\"}))\nOk(4)\n")
}
