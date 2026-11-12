package analysis

import "testing"

func TestDeferRequiresCall(t *testing.T) {
	src := `fn main()
  defer 1
end
`
	_, errs := checkSource(src)
	expectError(t, errs, "`defer` requires a function call")
}

func TestDeferRequiresUnitReturn(t *testing.T) {
	src := `fn close() -> Int
  1
end

fn main()
  defer close()
end
`
	_, errs := checkSource(src)
	expectError(t, errs, "`defer` call must return Unit")
}

func TestDeferWithUnitCall(t *testing.T) {
	src := `fn close() -> Unit end

fn main()
  defer close()
end
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}
