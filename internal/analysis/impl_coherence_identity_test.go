package analysis_test

import (
	"testing"
)

// Two files that each declare an `Error`, as an SQL driver and an HTTP
// client each do.
var twoErrors = map[string]string{
	"a": "pub struct Error\n    text: String\nend\n",
	"b": "pub struct Error\n    code: Int\nend\n",
}

// A type argument is its declaration, not its name: impls of one interface
// for one type at two files' `Error` are two instantiations, wherever the
// name appears in the arguments and however the header reaches it.
// Coherence keyed the arguments by their rendering, which prints both as
// `Error`, and refused `impl From<a.Error>` beside `impl From<b.Error>` as
// a duplicate.
func TestImplCoherence_SameNamedTypeArgumentsAreTwoInstantiations(t *testing.T) {
	for _, tc := range []struct {
		name, src string
	}{
		{"two files' types", `import
    a
    b
end

enum Failure
    A a.Error
    B b.Error
end

impl From<a.Error> for Failure
    fn from(e: a.Error) -> Failure
        Failure.A(e)
    end
end

impl From<b.Error> for Failure
    fn from(e: b.Error) -> Failure
        Failure.B(e)
    end
end

fn main()
    dbg Failure.from(a.Error{text: "x"})
    dbg Failure.from(b.Error{code: 1})
end
`},
		{"inside a type argument", `import
    a
    b
end

enum Failure
    A List<a.Error>
    B List<b.Error>
end

impl From<List<a.Error>> for Failure
    fn from(e: List<a.Error>) -> Failure
        Failure.A(e)
    end
end

impl From<List<b.Error>> for Failure
    fn from(e: List<b.Error>) -> Failure
        Failure.B(e)
    end
end

fn main()
    dbg Failure.from([a.Error{text: "x"}])
end
`},
		{"std's type beside the file's own", `import std::json

struct Json
    raw: String
end

enum Failure
    Std json.Json
    Own Json
end

impl From<json.Json> for Failure
    fn from(e: json.Json) -> Failure
        Failure.Std(e)
    end
end

impl From<Json> for Failure
    fn from(e: Json) -> Failure
        Failure.Own(e)
    end
end

fn main()
    dbg Failure.from(Json{raw: "x"})
end
`},
		{"through import aliases", `import
    a as first
    b as second
end

enum Failure
    A first.Error
    B second.Error
end

impl From<first.Error> for Failure
    fn from(e: first.Error) -> Failure
        Failure.A(e)
    end
end

impl From<second.Error> for Failure
    fn from(e: second.Error) -> Failure
        Failure.B(e)
    end
end

fn main()
    dbg Failure.from(second.Error{code: 1})
end
`},
		{"as receivers", `import
    a
    b
end

interface Show
    fn show(value: self) -> String
end

impl Show for a.Error
    fn show(e: a.Error) -> String
        e.text
    end
end

impl Show for b.Error
    fn show(e: b.Error) -> String
        "${e.code}"
    end
end

fn main()
    dbg Show.show(b.Error{code: 1})
end
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectSelectionClean(t, buildProjectExpectingErrors(t, tc.src, twoErrors))
		})
	}
}

// One instantiation at a file's `Error` twice is still the duplicate, one
// error, and it names the `Error` by its file, since the program implements
// the interface at the other file's `Error` too.
func TestImplCoherence_ASameNamedTypeArgumentTwiceIsADuplicate(t *testing.T) {
	errs := buildProjectExpectingErrors(t, `import
    a
    b
end

enum Failure
    A a.Error
    B b.Error
end

impl From<a.Error> for Failure
    fn from(e: a.Error) -> Failure
        Failure.A(e)
    end
end

impl From<b.Error> for Failure
    fn from(e: b.Error) -> Failure
        Failure.B(e)
    end
end

impl From<a.Error> for Failure
    fn from(e: a.Error) -> Failure
        Failure.A(e)
    end
end

fn main()
end
`, twoErrors)
	expectRejected(t, errs, "duplicate impl: `From<a.Error>` is implemented for `Failure` twice in this module; a type implements an interface once at each set of type arguments")
	if n := len(errorsBesidesUnused(errs)); n != 1 {
		t.Errorf("%d errors, want the one duplicate: %v", n, errs)
	}
	e := duplicateImplError(t, errs)
	if e.Line != 23 || e.Col != 1 {
		t.Errorf("the duplicate is reported at %d:%d, want the third block's header, 23:1", e.Line, e.Col)
	}
	if len(e.Related) != 1 || e.Related[0].Line != 11 {
		t.Errorf("the duplicate's related location is %+v, want the first block's header at line 11", e.Related)
	}
}
