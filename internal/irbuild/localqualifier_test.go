package irbuild

import (
	"strings"
	"testing"
)

// A test body's binding named `testing` is the binding: `testing.check(x)`
// calls its field, not std/testing's check, which would record a failed
// assertion for `False`.
func TestLocalQualifier_ALocalNamedTestingIsNotStdTesting(t *testing.T) {
	out := irTestBodyVM(t, `import std::testing

struct Checker
    check: (Bool) -> Bool
end

fn yes(_b: Bool) -> Bool
    True
end

test "a local named testing"
    testing = Checker{check: yes}
    assert testing.check(False)
end

test "std's testing once the local is out of scope"
    assert testing.check(True)
end
`, 2)
	if !strings.Contains(out, "2 passed, 0 failed") {
		t.Fatalf("the cases do not all pass:\n%s", out)
	}
}

// A binding named like a std import is the binding where it is in scope:
// `io.print(s)` over a local `io` calls the local's field, in the body and
// inside a lambda, and a function value taken from it is that field. What
// the qualifier names is the checker's reference at it (identRef), never
// its spelling.
func TestLocalQualifier_ALocalNamedLikeAStdImportIsTheLocal(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

struct Printer
    print: (String) -> Unit
end

fn say(s: String)
    io.print("custom: ${s}")
end

fn main()
    io = Printer{print: say}
    io.print("hello")
    each = fn n: Int -> io.print("in lambda ${n}")
    each(1)
    f = io.print
    f("as value")
end
`, "custom: hello\ncustom: in lambda 1\ncustom: as value\n")
}

// A local `base64` is the local, whatever its field's type: a field of the
// std function's type is called, not std's `encode`, and one of another
// type lowers at the type the checker gave it, where the std function's
// type stopped the build.
func TestLocalQualifier_ALocalNamedLikeAStdModuleWhoseFunctionItsFieldShadows(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
import std::base64

struct Codec
    encode: (Bytes) -> String
end

struct Sizer
    encode: (Bytes) -> Int
end

fn fake(_b: Bytes) -> String
    "fake"
end

fn size(b: Bytes) -> Int
    Bytes.length(b)
end

fn same_type()
    base64 = Codec{encode: fake}
    io.print(base64.encode(String.to_bytes("hi")))
    run = fn -> base64.encode(String.to_bytes("hi"))
    io.print(run())
end

fn other_type()
    base64 = Sizer{encode: size}
    n = base64.encode(String.to_bytes("hello"))
    io.print(n + 1)
end

fn main()
    same_type()
    other_type()
    io.print(base64.encode(String.to_bytes("hi")))
end
`, "fake\nfake\n6\naGk=\n")
}
