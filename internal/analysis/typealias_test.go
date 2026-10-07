package analysis

import "testing"

func TestCheck_TypeAlias(t *testing.T) {
	_, errs := checkSource(`
typealias Mapper (Int) -> Int
fn apply(f: Mapper, x: Int): Int { f(x) }
fn demo(): Int { apply(|n| n + 1, 5) }
`)
	expectNoErrors(t, errs)
}
