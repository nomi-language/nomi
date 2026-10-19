package irbuild

import "testing"

// A type parameter named `Struct` is the instance's argument in a qualified
// call (`Struct.update(p, ...)` calls P's Up impl), while `Struct.update`
// outside the generic is still std's struct update.
func TestTypeParamNamedStruct_CallsTheInstancesImpl(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

interface Up {
    fn update(a: self, b: #{x: Int}) -> self
}

struct P {
    x: Int
}

impl Up for P {
    fn update(_a: P, b: #{x: Int}) -> P {
        #P{x: b.x + 10}
    }
}

fn bump<Struct>(p: Struct) -> Struct where Struct: Up {
    Struct.update(p, #{x: 2})
}

fn main() {
    io.print(bump(#P{x: 1}).x)
    io.print(Struct.update(#P{x: 1}, #{x: 5}).x)
}
`, "12\n5\n")
}
