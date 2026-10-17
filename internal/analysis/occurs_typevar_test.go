package analysis_test

import "testing"

// FuzzFormatKeepsMeaning laid out spec.md's Iter.loop example with the
// tuple parentheses gone. Checking it bound an inference variable to a tuple
// that contained the variable, and rendering that type in the next
// diagnostic overflowed the stack. Unification now refuses the binding, and
// the program is rejected with ordinary errors.
func TestTypeVarIsNotBoundToATypeContainingIt(t *testing.T) {
	_, errs := checkSourceWithStdlib(`fn f(input: Int) -> Int {
  #(_, acc) = Iter.loop(fn n, acc: #(Int, Int) = input, 1 -> {
    if n <= 1 { break #(n, acc) }
    #(n - 1, n * acc)
  })
  acc
}
`)
	if len(errs) == 0 {
		t.Fatal("the front end admits this program")
	}
	for _, e := range errs {
		if len(e.Message) > 1000 {
			t.Fatalf("a diagnostic renders a runaway type: %.200s...", e.Message)
		}
	}
}
