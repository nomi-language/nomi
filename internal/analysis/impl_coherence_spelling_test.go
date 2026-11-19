package analysis_test

import (
	"testing"
)

// Two impls of one interface for one type are a duplicate however each
// header spells the interface: `impl ToJson` with std's `ToJson` imported
// and `impl json.ToJson` name one declaration. Coherence keyed the pair by
// the header as written, so the two were filed apart and the program was
// accepted, and then refused at lowering.
func TestImplCoherence_TwoSpellingsOfOneInterfaceAreADuplicate(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `import std::json
import std::json.{Json, ToJson}

struct User
    name: String
end

impl ToJson for User
    fn to_json(value: User) -> Json
        Json.String(value.name)
    end
end

impl json.ToJson for User
    fn to_json(value: User) -> Json
        Json.String("q ${value.name}")
    end
end

fn main() end
`, nil)
	expectRejected(t, errs, "duplicate impl: `ToJson` is implemented for `User` by modules <project entry> (function `to_json`)")
}

// Two interfaces of one name, one a sibling's named through its module,
// each implemented for one type, are two impls: the qualifier left out of
// the key does not make them one.
func TestImplCoherence_TwoInterfacesOfOneNameAreNotADuplicate(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `import std::json.{Json, ToJson}
import a

struct User
    name: String
end

impl ToJson for User
    fn to_json(value: User) -> Json
        Json.String(value.name)
    end
end

impl a.ToJson for User
    fn to_json(value: User) -> String
        "a ${value.name}"
    end
end

fn main()
    dbg (a.ToJson.to_json(User{name: "x"}), Json.encode_as(User{name: "y"}))
end
`, map[string]string{"a": "pub interface ToJson\n    fn to_json(value: self) -> String\nend\n"})
	expectSelectionClean(t, errs)
}
