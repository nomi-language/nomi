package irbuild

import (
	"testing"
)

func TestIRResultMapErr_ConvertedTryLinks(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
enum ParseError { NotANumber String; OutOfRange Int }
fn error_text(err: ParseError): String {
 case err {
  ParseError.NotANumber(s) -> "not a number: " + s
  ParseError.OutOfRange(n) -> "out of range: " + Int.to_string(n)
 }
}
fn parse_small(s: String): Result<Int, ParseError> {
 case s {
  "7" -> Ok(7)
  "999" -> Err(ParseError.OutOfRange(999))
  _ -> Err(ParseError.NotANumber(s))
 }
}
fn describe(s: String): Result<String, String> {
 n = try Result.map_err(parse_small(s), |err: ParseError| error_text(err))
 Ok("parsed " + Int.to_string(n))
}
fn describe_twice(s: String): Result<String, String> {
 first = try describe(s)
 Ok(first + " / " + first)
}
fn main() {
 io.inspect(describe_twice("7"))
 io.inspect(describe_twice("abc"))
 io.inspect(describe_twice("999"))
}
`, "Ok(\"parsed 7 / parsed 7\")\nErr(\"not a number: abc\")\nErr(\"out of range: 999\")\n")
}

func TestIRResultMapErr_CallbackEffectsAndCapture(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn source(ok: Bool): Result<Int, String> {
 io.print("source")
 if ok { Ok(7) } else { Err("bad") }
}
fn main() {
 suffix = "!"
 convert = |s: String| { io.print("callback"); s + suffix }
 io.inspect(Result.map_err(source(True), convert))
 io.inspect(Result.map_err(source(False), convert))
}
`, "source\nOk(7)\nsource\ncallback\nErr(\"bad!\")\n")
}

func TestIRResultMapErr_CallbackFault(t *testing.T) {
	verifyIterFault(t, `import std/io
fn source(): Result<Int, Int> { Err(0) }
fn main() {
 io.inspect(Result.map_err(source(), |n: Int| {
  io.print("callback")
  1 / n
 }))
 io.print("after")
}
`, "line 6: division by zero", "callback\n")
}

func TestIRScalarText_ConcreteCalls(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn number(): Int { io.print("number"); 7 }
fn main() {
 io.print(Int.to_string(number()) + Float.to_string(1.5))
 io.print(Bool.to_string(True))
 io.print(String.to_string("text"))
}
`, "number\n71.5\nTrue\ntext\n")
}

// A struct with a list field is a retained struct, so a map_err over it runs.
func TestIRResultMapErr_StructPayloadRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Problem { tags: List<String> }
fn convert(r: Result<Int, Problem>): Result<Int, String> {
 Result.map_err(r, |_p: Problem| "bad")
}
fn main() { io.inspect(convert(Err(Problem{tags: ["x"]}))) }
`, "Err(\"bad\")\n")
}

func TestIRResultMapErr_ListPayload(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn failed(): Result<List<Int>, String> { Err("bad") }
fn succeeded(): Result<List<Int>, String> { Ok([1, 2]) }
fn main() {
 io.inspect(Result.map_err(failed(), |e: String| e + "!"))
 io.inspect(Result.map_err(succeeded(), |e: String| e + "!"))
}
`, "Err(\"bad!\")\nOk([1, 2])\n")
}

// Maybe.to_result is a host call to rt.MaybeToResult, prefix and piped
// under `try`, with an enum error payload beside the String one.
func TestIRMaybeToResult_PrefixAndPipedTry(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
enum Why { Missing; Empty }
fn tenfold(m: Maybe<Int>): Result<Int, String> {
 n =
  m
  |> try Maybe.to_result("missing")

 Ok(n * 10)
}
fn why(m: Maybe<String>): Result<String, Why> {
 Maybe.to_result(m, Why.Missing)
}
fn main() {
 io.inspect(tenfold(Some(4)))
 io.inspect(tenfold(None))
 io.inspect(Maybe.to_result(Some("a"), 0))
 io.inspect(why(None))
 io.inspect(why(Some("b")))
}
`, "Ok(40)\nErr(\"missing\")\nOk(\"a\")\nErr(Missing)\nOk(\"b\")\n")
}
