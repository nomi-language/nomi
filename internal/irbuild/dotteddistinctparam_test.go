package irbuild

import "testing"

// A pattern head that names a namespaced distinct type, `Civil.Days(n)`,
// names that type, as `Days(n)` names a plain one, in a named function's
// parameter, a lambda's and a `case` arm. It used to be read as a variant of
// `Civil`, so the parameter took a `Civil` and the call declined.
func TestIRDottedDistinct_APatternHeadNamesTheNamespacedType(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Civil {}

type Civil.Days Int

fn days_value(Civil.Days(n)) -> Int {
    n
}

fn main() {
    io.print("${days_value(Civil.Days(3))}")
    f = fn Civil.Days(n) -> n + 1
    io.print("${f(Civil.Days(4))}")
    case Civil.Days(5) {
        Civil.Days(n) -> io.print("${n}")
    }
}
`, "3\n5\n5\n")
}
