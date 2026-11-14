package irbuild

import "testing"

// A std body that calls a function of a std module lowered after its own
// runs. std/strings imports std/regex, so the shared lowering lowers regex
// first, and Regex's Debug calls `String.to_typed_literal` in strings: the
// shared lowering does not retain that body, and the program builds it
// (otherModuleCallee). Reached directly, through a generic function's
// Debug bound, inside a list and inside a Maybe.
func TestIRStdBodyCallingALaterModule_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io
import std::regex.Regex

fn show<T>(x: T) -> String where T: Debug
    Debug.inspect(x)
end

fn main()
    r = Regex` + "`\\d+`" + `
    io.print(Debug.inspect(Regex"a+"))
    io.print(show(r))
    io.print(Debug.inspect([Regex"a", r]))
    io.print(Debug.inspect(Some(r)))
end
`
	irRunSource(t, src, "Regex\"a+\"\nRegex`\\d+`\n[Regex\"a\", Regex`\\d+`]\nSome(Regex`\\d+`)\n")
}
