package vmhost_test

import "testing"

// A program holds values of the types a shared std module declares in Nomi
// with no spec row (std/json's `Json.Case` and options, std/testing's
// `Clock`): it builds them, compares them with their derived `Equatable`,
// renders them with their derived `Display`, reads a field and
// keeps them in a list. Each is one def for the process (irbuild's
// stdtypes.go), so the shared lowering's impls take the program's values.
func TestStdTypes_AProgramHoldsAStdTypeDeclaredInNomi(t *testing.T) {
	got := runSourceOutput(t, `import std::io
import std::json.{Json, ToJson, FromJson}
import std::testing

fn main()
    c = Json.Case.Camel
    io.print(Display.to_string(c))
    io.print(c == Json.Case.Snake)
    to = ToJson.Options{rename_all: c}
    io.print(to == ToJson.Options{rename_all: Json.Case.Camel})
    from = FromJson.Options{rename_all: Json.Case.Kebab}
    io.print(from.rename_all)
    cases: List<Json.Case> = [Json.Case.Snake, c]
    io.print(cases)
    io.print(testing.Clock.Virtual == testing.Clock.System)
end
`)
	want := "Camel\nFalse\nTrue\nKebab\n[Snake, Camel]\nFalse\n"
	if got != want {
		t.Errorf("output\n%s\nwant\n%s", got, want)
	}
}
