package analysis_test

import "testing"

// A container's type arguments compare exactly (spec §5 *Assignability*):
// a `Maybe<Int>` is not a `Maybe<Display>`, nor a `List<Int>` a
// `List<Display>`, though each Int is a Display. An Iter's element is the
// exception, since an Iter only hands its elements out: a `List<Int>`
// passes where an `Iter<Display>` is expected.
func TestTypeArgumentVariance_ContainersCompareExactly(t *testing.T) {
	for name, src := range map[string]string{
		"Maybe": `fn main() {
  x: Maybe<Int> = Some(1)
  y: Maybe<Display> = x
  dbg y
}`,
		"List": `fn main() {
  xs: List<Int> = [1]
  ys: List<Display> = xs
  dbg ys
}`,
		"argument": `fn show(xs: List<Display>) -> Int { Iter.count(xs) }
fn main() {
  xs: List<Int> = [1]
  dbg show(xs)
}`,
	} {
		t.Run(name, func(t *testing.T) {
			expectErrorContaining(t, checkWithStdlib(src), "type arguments compare exactly")
		})
	}
	t.Run("an Iter's element", func(t *testing.T) {
		expectNoErrorsT(t, checkWithStdlib(`fn count(xs: Iter<Display>) -> Int { Iter.count(xs) }
fn main() {
  xs: List<Int> = [1, 2]
  dbg count(xs)
}`))
	})
	t.Run("a literal checks each element", func(t *testing.T) {
		expectNoErrorsT(t, checkWithStdlib(`fn main() {
  ys: List<Display> = [1, 2]
  y: Maybe<Display> = Some(1)
  dbg ys
  dbg y
}`))
	})
}
