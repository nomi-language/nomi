package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// shapeIface declares a required function and a default; squareImpl
// implements it for Square in another file, writing only the required one.
const shapeIface = `pub interface Shape {
    fn area(s: self) -> Int

    fn describe(s: self) -> String {
        "area ${area(s)}"
    }
}
`

const squareImpl = `import shape.Shape

pub struct Square {
    side: Int
}

impl Shape for Square {
    fn area(s: Square) -> Int {
        s.side * s.side
    }
}
`

// A default the type inherits is its member as a function its impl writes
// is: `Square.describe` checks in a file that imports Square and not Shape,
// as `Square.area` does, and its reference is the interface's function.
func TestInheritedDefaultReachedThroughTheTypeWithoutTheInterface(t *testing.T) {
	fa, errs := opaqueProject(t, map[string]string{
		"shape.nomi":  shapeIface,
		"square.nomi": squareImpl,
		"main.nomi": `import square.Square

fn main() {
    a: Int = Square.area(Square{side: 3})
    d: String = Square.describe(Square{side: 3})
    dbg (a, d)
}
`,
	})
	if len(errs) != 0 {
		t.Fatalf("the program does not check clean: %v", errs)
	}
	sym := fa.References[analysis.Pos{Line: 5, Col: 24}]
	if sym == nil {
		t.Fatalf("no reference at `describe`")
	}
	if sym.Kind != analysis.SymbolInterfaceMethod || sym.OwningInterface != "Shape" || sym.Name != "describe" {
		t.Fatalf("`describe` names kind %v %q of %q, want Shape's interface function", sym.Kind, sym.Name, sym.OwningInterface)
	}
	if !strings.HasSuffix(sym.SourceFile, "shape.nomi") {
		t.Fatalf("`describe` names a function in %q, want shape.nomi", sym.SourceFile)
	}
	if _, ok := fa.TypeDefaultFunctions("Square")["describe"]; !ok {
		t.Fatalf("TypeDefaultFunctions(Square) lacks describe: %v", fa.TypeDefaultFunctions("Square"))
	}
}

// The import rules are the ones for any name. An import of the interface
// used only by a type-qualified default call is unused, as one used only by
// `Square.area` is; an interface-qualified call needs the interface in scope.
func TestInheritedDefaultKeepsTheImportRules(t *testing.T) {
	cases := []struct {
		name, main, want string
	}{
		{
			name: "an import used only by a type-qualified default call is unused",
			main: `import square.Square
import shape.Shape

fn main() {
    dbg Square.describe(Square{side: 3})
}
`,
			want: "imported name 'Shape' is unused",
		},
		{
			name: "an interface-qualified call needs the interface in scope",
			main: `import square.Square

fn main() {
    dbg Shape.describe(Square{side: 3})
}
`,
			want: "undefined type or variant 'Shape'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := opaqueProject(t, map[string]string{
				"shape.nomi":  shapeIface,
				"square.nomi": squareImpl,
				"main.nomi":   tc.main,
			})
			if len(errs) == 0 {
				t.Fatalf("the program checks clean, want an error containing %q", tc.want)
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e.Message, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("errors %v, want one containing %q", errs, tc.want)
			}
		})
	}
}

// Two interfaces the type implements in other files, each declaring a
// default of one name, leave `Type.name` ambiguous whether or not the
// calling file imports either.
func TestInheritedDefaultsOfTwoUnimportedInterfacesAreAmbiguous(t *testing.T) {
	_, errs := opaqueProject(t, map[string]string{
		"shape.nomi": shapeIface,
		"label.nomi": `pub interface Label {
    fn name(s: self) -> String

    fn describe(s: self) -> String {
        "label ${name(s)}"
    }
}
`,
		"square.nomi": `import shape.Shape
import label.Label

pub struct Square {
    side: Int
}

impl Shape for Square {
    fn area(s: Square) -> Int {
        s.side * s.side
    }
}

impl Label for Square {
    fn name(_s: Square) -> String {
        "square"
    }
}
`,
		"main.nomi": `import square.Square

fn main() {
    dbg Square.describe(Square{side: 3})
}
`,
	})
	for _, e := range errs {
		if strings.Contains(e.Message, "is ambiguous") && strings.Contains(e.Message, "'Label' and 'Shape'") {
			return
		}
	}
	t.Fatalf("errors %v, want the type-qualified ambiguity naming Label and Shape", errs)
}

// A std file names a built-in type it imports with no module qualifier,
// while the impl's home file qualifies it with its declaring module; both
// are the one Int. std/sessions imports Int and not Discrete, and Int's
// Discrete impl in std/int is found from there, its `steps_between` among
// the defaults Int has through the type.
func TestInheritedDefaultsOfABuiltinReceiverFromAStdFile(t *testing.T) {
	fa := loadStd().Files["sessions"]
	if fa == nil || fa.ModuleScope == nil {
		t.Fatal("std/sessions is not analyzed")
	}
	if fa.ModuleScope.Lookup("Discrete") != nil {
		t.Fatal("std/sessions names Discrete, so the interface is not one it leaves unimported")
	}
	if fa.ModuleScope.Lookup("Int") == nil {
		t.Fatal("std/sessions does not name Int")
	}
	if _, ok := fa.TypeDefaultFunctions("Int")["steps_between"]; !ok {
		t.Fatalf("Int's defaults from std/sessions lack Discrete's steps_between: %v", fa.TypeDefaultFunctions("Int"))
	}
}
