package analysis_test

import "testing"

// Two user files import each other, and each one's function signature names
// the other's generic enum. Whichever file BuildTypes reached first resolved
// its signature before the other file's enum had variants, so a `case` over
// that function's result was "variant Hit not found in enum Wrap". Both
// files' enums now carry their variants in every signature, in either order.
// This test drives the build order; vmhost's
// TestImportCycle_GenericSignaturesAcrossTheCycleRun runs the same program.
func TestDeclarationOrder_ImportCycleSignaturesCarryVariants(t *testing.T) {
	files := map[string]string{
		"a.nomi": `import b.Tag

pub enum Wrap<T> {
    Hit(T)
    Miss
}

pub fn tag_of(w: Wrap<Int>): Tag<Int> {
    case w {
        .Hit(n) -> Tag.Big(n)
        .Miss -> Tag.Small
    }
}
`,
		"b.nomi": `import a.Wrap

pub enum Tag<T> {
    Big(T)
    Small
}

pub fn wrap_of(t: Tag<Int>): Wrap<Int> {
    case t {
        .Big(n) -> Wrap.Hit(n)
        .Small -> Wrap.Miss
    }
}
`,
		"main.nomi": `import {
    std/io
    a.{Wrap, tag_of}
    b.{Tag, wrap_of}
}

fn main() {
    case tag_of(Wrap.Hit(4)) {
        .Big(n) -> io.print("big ${n}")
        .Small -> io.print("small")
    }
    case wrap_of(Tag.Small) {
        .Hit(n) -> io.print("hit ${n}")
        .Miss -> io.print("miss")
    }
}
`,
	}
	for _, order := range [][]string{{"", "a", "b"}, {"b", "a", ""}} {
		for file, errs := range checkOnceProject(t, files, order) {
			if len(errs) > 0 {
				t.Errorf("order %q: %s: %v", order, file, messages(errs))
			}
		}
	}
}

// An enum that embeds a wrapping distinct type takes the distinct type's
// inner type as the variant's payload. An enum built before the distinct
// type, above it in the file or in a file built earlier, read a shell with
// no inner type and took the variant for a zero-sized one: the program was
// "UserId wraps Int, so UserId(...) takes an Int; got UserId" at the enum,
// and `Shape.UserId(n)` bound a UserId rather than an Int. Every order now
// checks clean. vmhost's TestDeclarationOrder_EmbedsADistinctTypeDeclaredBelow
// runs the program.
const embedsDistinctUse = `fn main() {
    s: Shape = UserId(5)
    n = case s {
        Shape.UserId(k) -> k + 1
        Shape.Dot -> 0
    }
    dbg n
    dbg Shape.UserId(8)
}
`

func TestDeclarationOrder_EmbedsADistinctTypeInOneFile(t *testing.T) {
	enum := "enum Shape {\n    embeds UserId\n    Dot\n}\n\n"
	distinct := "type UserId Int\n\n"
	for name, src := range map[string]string{
		"distinct first": distinct + enum + embedsDistinctUse,
		"enum first":     enum + distinct + embedsDistinctUse,
	} {
		for file, errs := range checkOnceProject(t, map[string]string{"main.nomi": src}, []string{""}) {
			if len(errs) > 0 {
				t.Errorf("%s: %s: %v", name, file, messages(errs))
			}
		}
	}
}

// User files are built in the order of their module keys, so `ids` is
// built before `shape` and `zids` after it.
func TestDeclarationOrder_EmbedsADistinctTypeFromAnotherFile(t *testing.T) {
	for _, ids := range []string{"ids", "zids"} {
		files := map[string]string{
			"shape.nomi":  "import " + ids + ".UserId\n\npub enum Shape {\n    embeds UserId\n    Dot\n}\n",
			ids + ".nomi": "pub type UserId Int\n",
			"main.nomi":   "import {\n    shape.Shape\n    " + ids + ".UserId\n}\n\n" + embedsDistinctUse,
		}
		for file, errs := range checkOnceProject(t, files, []string{"", "shape", ids}) {
			if len(errs) > 0 {
				t.Errorf("%s: %s: %v", ids, file, messages(errs))
			}
		}
	}
}

// The enum's file and the distinct type's file import each other.
func TestDeclarationOrder_EmbedsADistinctTypeAcrossAnImportCycle(t *testing.T) {
	for _, ids := range []string{"ids", "zids"} {
		files := map[string]string{
			"shape.nomi": "import " + ids + ".UserId\n\npub enum Shape {\n    embeds UserId\n    Dot\n}\n",
			ids + ".nomi": "import shape.Shape\n\npub type UserId Int\n\n" +
				"pub fn bump(s: Shape): Int {\n    case s {\n        Shape.UserId(k) -> k + 1\n        Shape.Dot -> 0\n    }\n}\n",
			"main.nomi": "import {\n    shape.Shape\n    " + ids + ".{UserId, bump}\n}\n\n" +
				"fn main() {\n    dbg bump(UserId(5))\n    dbg Shape.UserId(8)\n}\n",
		}
		for file, errs := range checkOnceProject(t, files, []string{"", "shape", ids}) {
			if len(errs) > 0 {
				t.Errorf("%s: %s: %v", ids, file, messages(errs))
			}
		}
	}
}
