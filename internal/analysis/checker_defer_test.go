package analysis

import "testing"

func TestDeferRequiresCall(t *testing.T) {
	src := `fn main() {
  defer 1
}
`
	_, errs := checkSource(src)
	expectError(t, errs, "`defer` requires a function call")
}

func TestDeferRequiresUnitReturn(t *testing.T) {
	src := `fn close(): Int {
  1
}

fn main() {
  defer close()
}
`
	_, errs := checkSource(src)
	expectError(t, errs, "`defer` call must return Unit")
}

func TestDeferWithUnitCall(t *testing.T) {
	src := `fn close(): Unit {}

fn main() {
  defer close()
}
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}
