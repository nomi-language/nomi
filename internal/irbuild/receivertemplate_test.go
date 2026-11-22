package irbuild

import "testing"

// An inherent block's owner is the type the checker recorded for its
// receiver (receiverTemplate, ownerBlockParams): an interface's owner
// functions, a generic interface's included, run as a generic type's do.
func TestReceiverTemplate_AnInterfaceOwnsItsInherentBlock(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io

interface Shape
    fn area(s: self) -> Int
end

impl Shape
    fn twice(s: Shape) -> Int
        Shape.area(s) * 2
    end

    fn unit() -> Int
        1
    end
end

interface Holder<T>
    fn get(h: self) -> T
end

impl Holder<T>
    fn label() -> String
        "holder"
    end
end

struct Sq
    side: Int
end

impl Shape for Sq
    fn area(s: Sq) -> Int
        s.side * s.side
    end
end

fn main()
    io.print("${Shape.twice(Sq{side: 3})} ${Shape.unit()} ${Holder.label()}")
end
`, "18 1 holder\n")
}
