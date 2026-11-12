package irbuild

import "testing"

// A named interface call that skips a defaulted slot before a supplied one
// fills it from the impl's own default, when the impl is another file's:
// the interface and the impl both in a sibling file, or the interface here
// and the impl beside its type in a sibling file.
func TestSiblingIfaceGapDefaults_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const shapes = `pub interface Shape
    open fn area(_s: self, scale: Int = 1, extra: Int = 0) -> Int
        scale + extra
    end
end

pub struct Sq
    side: Int
end

impl Shape for Sq
    fn area(s: Sq, scale: Int = 2, extra: Int = 0) -> Int
        s.side * scale + extra
    end
end
`
	t.Run("interface and impl in another file", func(t *testing.T) {
		verifyLambdaProgram(t, `import std::io
import shapes.{Shape, Sq}

fn main()
    io.print(Shape.area(Sq{side: 3}, extra: 1))
    io.print(Shape.area(extra: 4, Sq{side: 3}))
end
`, "7\n10\n", map[string]string{"shapes.nomi": shapes})
	})
	t.Run("impl in another file", func(t *testing.T) {
		verifyLambdaProgram(t, `import std::io
import sq.Sq

pub interface Shape
    open fn area(_s: self, scale: Int = 1, extra: Int = 0) -> Int
        scale + extra
    end
end

fn main()
    io.print(Shape.area(Sq{side: 3}, extra: 1))
end
`, "7\n", map[string]string{"sq.nomi": `import main.Shape

pub struct Sq
    side: Int
end

impl Shape for Sq
    fn area(s: Sq, scale: Int = 2, extra: Int = 0) -> Int
        s.side * scale + extra
    end
end
`})
	})
}
