package irbuild

import (
	"testing"
)

func TestIRResultMapErr_ConvertedTryLinks(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
enum ParseError
    NotANumber String
    OutOfRange Int
end
fn error_text(err: ParseError) -> String
 case err
  ParseError.NotANumber(s) -> "not a number: " + s
  ParseError.OutOfRange(n) -> "out of range: " + Int.to_string(n)
 end
end
fn parse_small(s: String) -> Result<Int, ParseError>
 case s
  "7" -> Ok(7)
  "999" -> Err(ParseError.OutOfRange(999))
  _ -> Err(ParseError.NotANumber(s))
 end
end
fn describe(s: String) -> Result<String, String>
 n = Result.map_err(parse_small(s), fn err: ParseError -> error_text(err))?
 Ok("parsed " + Int.to_string(n))
end
fn describe_twice(s: String) -> Result<String, String>
 first = describe(s)?
 Ok(first + " / " + first)
end
fn main()
 io.inspect(describe_twice("7"))
 io.inspect(describe_twice("abc"))
 io.inspect(describe_twice("999"))
end
`, "Ok(\"parsed 7 / parsed 7\")\nErr(\"not a number: abc\")\nErr(\"out of range: 999\")\n")
}

func TestIRResultMapErr_CallbackEffectsAndCapture(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn source(ok: Bool) -> Result<Int, String>
 io.print("source")
 if ok then Ok(7) else Err("bad")
end
fn main()
 suffix = "!"
 convert = fn s: String ->
     io.print("callback")
     s + suffix
 end
 io.inspect(Result.map_err(source(True), convert))
 io.inspect(Result.map_err(source(False), convert))
end
`, "source\nOk(7)\nsource\ncallback\nErr(\"bad!\")\n")
}

func TestIRResultMapErr_CallbackFault(t *testing.T) {
	verifyIterFault(t, `import std::io
fn source() -> Result<Int, Int>
    Err(0)
end
fn main()
 io.inspect(Result.map_err(source(), fn n: Int ->
  io.print("callback")
  1 / n
 end))
 io.print("after")
end
`, "line 8: division by zero", "callback\n")
}

func TestIRScalarText_ConcreteCalls(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn number() -> Int
    io.print("number")
    7
end
fn main()
 io.print(Int.to_string(number()) + Float.to_string(1.5))
 io.print(Bool.to_string(True))
 io.print(String.to_string("text"))
end
`, "number\n71.5\nTrue\ntext\n")
}

// A struct with a list field is a retained struct, so a map_err over it runs.
func TestIRResultMapErr_StructPayloadRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
struct Problem
    tags: List<String>
end
fn convert(r: Result<Int, Problem>) -> Result<Int, String>
 Result.map_err(r, fn _p: Problem -> "bad")
end
fn main()
    io.inspect(convert(Err(Problem{tags: ["x"]})))
end
`, "Err(\"bad\")\n")
}

func TestIRResultMapErr_ListPayload(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn failed() -> Result<List<Int>, String>
    Err("bad")
end
fn succeeded() -> Result<List<Int>, String>
    Ok([1, 2])
end
fn main()
 io.inspect(Result.map_err(failed(), fn e: String -> e + "!"))
 io.inspect(Result.map_err(succeeded(), fn e: String -> e + "!"))
end
`, "Err(\"bad!\")\nOk([1, 2])\n")
}

// Maybe.to_result is a host call to rt.MaybeToResult, prefix and piped
// under `?`, with an enum error payload beside the String one.
func TestIRMaybeToResult_PrefixAndPipedTry(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
enum Why
    Missing
    Empty
end
fn tenfold(m: Maybe<Int>) -> Result<Int, String>
 n =
  m
  |> Maybe.to_result("missing")?

 Ok(n * 10)
end
fn why(m: Maybe<String>) -> Result<String, Why>
 Maybe.to_result(m, Why.Missing)
end
fn main()
 io.inspect(tenfold(Some(4)))
 io.inspect(tenfold(None))
 io.inspect(Maybe.to_result(Some("a"), 0))
 io.inspect(why(None))
 io.inspect(why(Some("b")))
end
`, "Ok(40)\nErr(\"missing\")\nOk(\"a\")\nErr(Missing)\nOk(\"b\")\n")
}
