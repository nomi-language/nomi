package irbuild

import "testing"

// A generic call in an argument position (`Err("no")` as
// `Result.with_default`'s first argument) takes its open type arguments from
// that call, not from the enclosing function's return type, which says
// nothing about a value that is not returned. In a function returning
// `Result<Int, String>`, the `Err` here is a `Result<(String, Bool), String>`.
func TestReturnInfer_AnArgumentTakesItsCallsType(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn ok<T>(x: T): Result<T, String> {
    Ok(x)
}

fn under_try(): Result<Int, String> {
    t = try ok({
        f: ((String, Bool)) -> Int = |x| x.0 |> String.length()
        f(Result.with_default(Err("no"), ("bc", True)))
    })
    Ok(t)
}

fn bound(): Result<Int, String> {
    v = Result.with_default(Err("no"), ("abc", True))
    Ok(String.length(v.0))
}

fn tail(): Result<Int, String> {
    Ok(5)
}

fn main() {
    io.inspect(under_try())
    io.inspect(bound())
    io.inspect(tail())
}
`, "Ok(2)\nOk(3)\nOk(5)\n")
}
