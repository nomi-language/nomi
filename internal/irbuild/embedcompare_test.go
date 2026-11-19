package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// `==` and `!=` between an embedded type's value and the enum that embeds
// it run on either side, a value narrowed by a `case` arm included: the
// narrower value widens into the enum, and the two compare through the
// enum's Equatable, which the checker records. The golden set holds the
// program, so the operator verifier sees the widened comparison.
func TestEmbeddedComparison_RunsAtTheEnum(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	src := `import std::io

struct Circle
    radius: Float
end

enum Shape
    embeds Circle
    Point
end

derive Equatable for Circle

derive Equatable for Shape

fn same(value: Shape, other: Shape) -> String
    case value
        .Circle{radius: _} -> "${value == other} ${other != value}"
        .Point -> "point"
    end
end

fn main()
    c = Circle{radius: 1.0}
    s: Shape = Shape.Point
    io.print("${c == s} ${s != c} ${c == Circle{radius: 1.0}}")
    io.print(same(Circle{radius: 1.0}, Circle{radius: 1.0}))
    io.print(same(Circle{radius: 1.0}, Shape.Point))
end
`
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(path); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	const want = "False True True\nTrue False\nFalse True\n"
	got := vmReference(path)
	if got.stdout+got.stderr != want || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
	if golden := goldenReference(t, path); golden.stdout+golden.stderr != want || golden.exit != 0 {
		t.Errorf("golden record (exit %d):\n%s%s", golden.exit, golden.stdout, golden.stderr)
	}
}
