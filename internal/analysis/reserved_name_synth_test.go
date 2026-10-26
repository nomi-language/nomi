package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// frontEndErrors checks src as `nomi check` does and answers its
// diagnostics, one per line.
func frontEndErrors(t *testing.T, src string, mode frontend.Mode) []string {
	t.Helper()
	c := frontend.New(frontend.Config{})
	nodes, err := c.Prepare(src, mode)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if _, _, err = c.Analyze(nodes); err == nil {
		return nil
	}
	return strings.Split(err.Error(), "\n")
}

// A declaration named after a type the language reserves is refused with one
// error, worded for the declaration it is (an enum, a struct, ...), whose
// advice is a new name in the declaration the author wrote. The impls the
// compiler synthesizes for a declaration (the universal Debug, a derive)
// are not made for a refused one: the name still means the prelude's type,
// so they were orphan and duplicate impls of it, and one was reported at a
// line in the synthesized band (`main.nomi:1073872896:1`).
func TestReservedName_OneErrorWordedForTheDeclaration(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"enum Unit", "pub enum Unit {\n  Ohms\n}\n",
			"1:10: type name 'Unit' is reserved by the language and cannot be redeclared as an enum; pick a different name (e.g. `enum MyUnit { ... }`)"},
		{"enum Maybe", "enum Maybe {\n  Nope\n}\n",
			"1:6: type name 'Maybe' is reserved by the language and cannot be redeclared as an enum; pick a different name (e.g. `enum MyMaybe { ... }`)"},
		{"enum Ordering", "enum Ordering {\n  Less\n}\n",
			"1:6: type name 'Ordering' is reserved by the language and cannot be redeclared as an enum; pick a different name (e.g. `enum MyOrdering { ... }`)"},
		{"struct Int", "struct Int {\n  x: Float\n}\n",
			"1:8: type name 'Int' is reserved by the language and cannot be redeclared as a struct; pick a different name (e.g. `struct MyInt { ... }`)"},
		{"struct List", "struct List {\n  y: Float\n}\n",
			"1:8: type name 'List' is reserved by the language and cannot be redeclared as a struct; pick a different name (e.g. `struct MyList { ... }`)"},
		{"struct Bytes", "struct Bytes {\n  y: Float\n}\n",
			"1:8: type name 'Bytes' is reserved by the language and cannot be redeclared as a struct; pick a different name (e.g. `struct MyBytes { ... }`)"},
		{"distinct type", "type String Float\n",
			"1:6: type name 'String' is reserved by the language and cannot be redeclared as a distinct type; pick a different name (e.g. `type MyString ...`)"},
		{"typealias", "typealias Unit Int\n",
			"1:11: type name 'Unit' is reserved by the language and cannot be redeclared as a typealias; pick a different name (e.g. `typealias MyUnit ...`)"},
		{"interface", "interface Display {\n  fn show(value: self) -> String\n}\n",
			"1:11: type name 'Display' is reserved by the language and cannot be redeclared as an interface; pick a different name (e.g. `interface MyDisplay { ... }`)"},
		{"enum with a derive", "enum Unit {\n  Ohms\n}\n\nderive Equatable for Unit\n",
			"1:6: type name 'Unit' is reserved by the language and cannot be redeclared as an enum; pick a different name (e.g. `enum MyUnit { ... }`)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := frontEndErrors(t, tc.src+"\nfn main() {\n}\n", frontend.Mode{})
			if len(errs) == 0 {
				t.Fatalf("the front end accepts a declaration of a reserved name:\n%s", tc.src)
			}
			if len(errs) != 1 || !strings.HasSuffix(errs[0], tc.want) {
				t.Fatalf("want the one error\n  %s\ngot:\n  %s", tc.want, strings.Join(errs, "\n  "))
			}
		})
	}
}
