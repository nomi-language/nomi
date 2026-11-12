package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// captureGenericDefaultSrc captures an inherited generic interface default
// through the implementing type and through the interface.
const captureGenericDefaultSrc = `import std::io

struct Box
    value: Int
end

interface Pairs
    fn pair<U>(b: self, u: U) -> (self, U)

    fn second<U>(_b: self, u: U) -> U
        u
    end
end

impl Pairs for Box
    fn pair<U>(b: Box, u: U) -> (Box, U)
        (b, u)
    end
end

fn main()
    b = Box{ value: 1 }
    f = &Box.second(b, &1)
    io.inspect(f(True))
    g = &Pairs.second(b, &1)
    io.inspect(g("x"))
end
`

// A capture of a generic interface default a type inherits runs, named
// through the type or the interface, instantiated at the type the
// checker gave the open slot.
func TestCaptureOfInheritedGenericDefault_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, captureGenericDefaultSrc, "True\n\"x\"\n")
}

// The front end accepts the program above, so the test must fail if the
// checker starts to reject it rather than pass vacuously.
func TestCaptureOfInheritedGenericDefault_FrontEndAccepts(t *testing.T) {
	if _, err := frontend.New(frontend.Config{}).CheckSource("main", captureGenericDefaultSrc, frontend.Mode{}); err != nil {
		t.Fatalf("the front end rejects this, so the lowering test above says nothing: %v", err)
	}
}
