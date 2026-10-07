package irbuild

import (
	"testing"
)

func TestIRHostRef_StringCallbacks(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 trim = String.trim
 io.print(trim("  hello  "))

}
`, "hello\n")
}

func TestIRHostRef_IterationCallbacks(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 xs = ["ONE", "Two", "THREE"] |> Iter.map(String.to_lower) |> Iter.to_list()
 dbg xs
 lengths = ["café", "", "hello"] |> Iter.map(String.length) |> Iter.to_list()
 dbg lengths
 contains: (String, String) -> Bool = String.contains?
 io.print(contains("abc", "b"))
}
`, "dbg line 4: xs = [\"one\", \"two\", \"three\"]\ndbg line 6: lengths = [4, 0, 5]\nTrue\n")
}

// A generic host function named as a value runs: it lowers as a function
// whose body is the qualified call, typed at the instance the annotation
// solves.
func TestIRHostRef_GenericHostFunctionAsAValue(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 wrap: (Context, Int) -> Context = Context.with_value
 c = wrap(Context.root(), 7)
 io.inspect(Context.value(c, Int))
}
`, "Some(7)\n")
}

func TestIRHostRef_ReturnedAndCaptured(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn parser(): (String) -> Maybe<Int> { String.to_int }
fn main() {
 parse = parser()
 wrapped = |s: String| parse(s)
 io.inspect(wrapped("42"))
 io.inspect(wrapped("no"))
 io.inspect(wrapped("9223372036854775808"))
}
`, "Some(42)\nNone\nNone\n")
}
