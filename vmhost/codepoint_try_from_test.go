package vmhost_test

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// `Codepoint.try_from` from a program outside std is an error: Codepoint is
// opaque, and the `try_from` the compiler provides for it builds one, so it
// is construction, private to std::code_points as `Codepoint(n)` is.
func TestLoad_CodepointTryFromOutsideStdIsRejected(t *testing.T) {
	for _, src := range []string{
		"fn main()\n    dbg Codepoint.try_from(65)\nend\n",
		"fn main()\n    f = Codepoint.try_from\n    dbg f(65)\nend\n",
		"fn main()\n    dbg Codepoint.unchecked(65)\nend\n",
	} {
		_, err := vmhost.Load(writeProgram(t, src))
		if err == nil {
			t.Fatalf("the front end admits a provided conversion of Codepoint outside std:\n%s", src)
		}
		if want := "constructs the opaque type 'Codepoint', whose construction is private to its defining module"; !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q, want it to contain %q\n%s", err, want, src)
		}
	}
}

// Inside std/code_points, Codepoint's provided `try_from` checks the type's
// rule, not only Int64's range: `Some` for a Unicode scalar value, `None`
// for a negative Int, a surrogate and a value past 0x10FFFF.
func TestStdlib_CodepointTryFromChecksTheRule(t *testing.T) {
	body := `assert Option.map(Codepoint.try_from(65), Codepoint.to_int) == Some(65)
assert Option.map(Codepoint.try_from(0), Codepoint.to_int) == Some(0)
assert Option.map(Codepoint.try_from(0xD7FF), Codepoint.to_int) == Some(0xD7FF)
assert Option.map(Codepoint.try_from(0xE000), Codepoint.to_int) == Some(0xE000)
assert Option.map(Codepoint.try_from(0x10FFFF), Codepoint.to_int) == Some(0x10FFFF)
assert Codepoint.try_from(-1) == None
assert Codepoint.try_from(0xD800) == None
assert Codepoint.try_from(0xDFFF) == None
assert Codepoint.try_from(0x110000) == None
`
	cases, err := vmhost.StdlibReference("code_points", body, "", io.Discard)
	if err != nil {
		t.Fatalf("the test does not load in std/code_points: %v", err)
	}
	if len(cases) != 1 || cases[0].Err != nil || cases[0].Blocked != nil {
		t.Fatalf("want one passing case, got %+v", cases)
	}
}

// `Int.to_codepoint` is the way in from an Int: `Some` for a Unicode scalar
// value, `None` for a surrogate, a value past 0x10FFFF and a negative one.
func TestRun_IntToCodepointChecksTheScalarValue(t *testing.T) {
	out, err := runVM(t, `import std::io

fn main()
    a = Int.to_codepoint(65)!
    io.print(a == 'A')
    io.print(Codepoint.to_int(a))
    io.print(Int.to_codepoint(0xD800) == None)
    io.print(Int.to_codepoint(0x110000) == None)
    io.print(Int.to_codepoint(-1) == None)
end
`)
	if err != nil {
		t.Fatalf("the program does not run: %v\n%s", err, out)
	}
	if want := "True\n65\nTrue\nTrue\nTrue\n"; out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}
