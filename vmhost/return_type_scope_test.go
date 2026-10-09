package vmhost_test

import "testing"

// A generic constructor bound in a non-result position takes its type from
// its own arguments and the other arm, not from the enclosing function's
// return type. Before, `Err("bad")` in g's bound case was typed
// `Result<Unit, String>` (g's return) and the case was rejected as
// mismatching f's `Result<Int, String>`.
func TestReturnTypeScope_BoundArmsRun(t *testing.T) {
	got := runSourceOutput(t, `import std/io

fn f(): Result<Int, String> { Ok(1) }

fn g(s: String): Result<Unit, String> {
    r = case s {
        "a" -> f()
        _ -> Err("bad")
    }
    io.print(Debug.inspect(r))
    Ok(Unit)
}

fn k(s: String): Result<Int, Int> {
    r = if s == "a" { f() } else { Ok(2) }
    io.print(Debug.inspect(r))
    Ok(1)
}

fn c(): Result<Unit, String> {
    e = Err("late")
    r: Result<Int, String> = e
    io.print(Debug.inspect(r))
    Ok(Unit)
}

fn main() {
    _a = g("a")
    _b = g("b")
    _c = k("a")
    _d = k("b")
    _e = c()
}
`)
	want := "Ok(1)\nErr(\"bad\")\nOk(1)\nOk(2)\nErr(\"late\")\n"
	if got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
}
