package irbuild

import "testing"

const moduleQualifiedValueOther = `pub interface Shape {
    fn area(value: self) -> Int
}

pub fn helper(x: Int) -> Int {
    x * 10
}
`

// `other.Shape.area` and `other.helper` used as values rather than called:
// passed to Iter.map and stored in a binding.
func TestIRModuleQualifiedIface_AsAValue(t *testing.T) {
	t.Run("no local interface", func(t *testing.T) {
		verifyLambdaProgram(t, `import { other std::io }

struct Sq {
    side: Int
}

impl other.Shape for Sq {
    fn area(value: Sq) -> Int {
        value.side * value.side
    }
}

fn main() {
    shapes = [Sq{side: 3}, Sq{side: 2}]
    io.print(shapes |> Iter.map(other.Shape.area) |> Iter.sum())
    area: (Sq) -> Int = other.Shape.area
    io.print(area(Sq{side: 4}))
}
`, "13\n16\n", map[string]string{"other.nomi": moduleQualifiedValueOther})
	})
	t.Run("beside a local interface of its name", func(t *testing.T) {
		verifyLambdaProgram(t, `import { other std::io }

interface Shape {
    fn label(value: self) -> String
}

struct Sq {
    side: Int
}

impl other.Shape for Sq {
    fn area(value: Sq) -> Int {
        value.side * value.side
    }
}

impl Shape for Sq {
    fn label(_: Sq) -> String {
        "square"
    }
}

fn main() {
    shapes = [Sq{side: 3}, Sq{side: 2}]
    io.print(shapes |> Iter.map(other.Shape.area) |> Iter.sum())
    area: (Sq) -> Int = other.Shape.area
    io.print(area(Sq{side: 4}))
    label: (Sq) -> String = Shape.label
    io.print(label(Sq{side: 1}))
}
`, "13\n16\nsquare\n", map[string]string{"other.nomi": moduleQualifiedValueOther})
	})
	t.Run("a module function", func(t *testing.T) {
		verifyLambdaProgram(t, `import { other std::io }

fn main() {
    io.print([1, 2, 3] |> Iter.map(other.helper) |> Iter.sum())
    h = other.helper
    io.print(h(4))
}
`, "60\n40\n", map[string]string{"other.nomi": moduleQualifiedValueOther})
	})
}
