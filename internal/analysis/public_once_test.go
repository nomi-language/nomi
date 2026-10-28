package analysis

import "testing"

// An unannotated `pub once` exposes its value's type as an annotated one
// does, so a private type or a type declared inside a body in it is the
// visibility error the annotation would get.
func TestCheck_PublicOnceInferredTypeVisibility(t *testing.T) {
	_, errs := checkSource(`
struct Hidden {
    x: Int
}
pub struct Shown {
    x: Int
}
pub once hidden = [#Hidden{x: 1}]
pub once shown = #Shown{x: 1}
once private_hidden = #Hidden{x: 2}
pub once boxes = {
    struct Box {
        inner: Int
    }

    [#Box{inner: 1}]
}
`)
	expectErrorCount(t, errs, "public once binding exposes private type Hidden", 1)
	expectErrorCount(t, errs, "public once binding exposes private type Box, which is declared inside a body", 1)
	expectErrorCount(t, errs, "exposes private type", 2)
}
