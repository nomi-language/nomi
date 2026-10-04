package irbuild

import "testing"

func TestIRFloatCompare_NaNAndInfinity(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn show(a: Float, b: Float) {
 io.inspect(a == b)
 io.inspect(a != b)
 io.inspect(a < b)
 io.inspect(a <= b)
 io.inspect(a > b)
 io.inspect(a >= b)
}
fn main() {
 nan = 0.0 / 0.0
 inf = 1.0 / 0.0
 show(nan, 1.0)
 show(1.0, nan)
 show(nan, nan)
 show(inf, inf)
 show(-inf, inf)
 show(-0.0, 0.0)
}`, "False\nTrue\nFalse\nFalse\nFalse\nFalse\n"+
		"False\nTrue\nFalse\nFalse\nFalse\nFalse\n"+
		"True\nFalse\nFalse\nFalse\nFalse\nFalse\n"+
		"True\nFalse\nFalse\nTrue\nFalse\nTrue\n"+
		"False\nTrue\nTrue\nTrue\nFalse\nFalse\n"+
		"True\nFalse\nFalse\nTrue\nFalse\nTrue\n")
}
