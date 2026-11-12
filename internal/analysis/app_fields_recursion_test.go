package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// onlyAppFieldError requires errs to be exactly one diagnostic, containing
// want.
func onlyAppFieldError(t *testing.T, errs []analysis.TypeError, want string) {
	t.Helper()
	if len(errs) != 1 || !strings.Contains(errs[0].Message, want) {
		t.Fatalf("want exactly one error containing %q, got %v", want, errs)
	}
}

const appFieldsRecursionPrefix = `
struct App
    context: Context
    n: Int
end
fn boot() -> Result<App, String>
    Ok(App{context: Context.root(), n: 1})
end
fn main()
    _ = 1
end
`

// A lambda that calls the function holding it reaches that function's body,
// and so the same lambda, again. The walk inspects each body once, so it
// ends.
func TestAppFields_SelfRecursiveLambdaEnds(t *testing.T) {
	// This overflowed the Go stack in appReadWalker.
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
fn later(x: Int) -> (Int) -> Int
  fn n -> later(x)(n)
end
fn main()
    _ = later(1)(2)
end
`))
}

// A read reached only through a self-recursive function's lambda is still
// found.
func TestAppFields_ReadThroughASelfRecursiveFunctionsLambda(t *testing.T) {
	onlyAppFieldError(t, buildProjectForDefaultConfig(appFieldsRecursionPrefix+`fn later(x: Int) -> (Int) -> Int
  fn k -> if k == 0 then App.n else later(x)(k - 1)
end
test "default"
    assert later(1)(0) == 1
end
`), "this test has no boot, but code it runs reads `App.n` (line 13)")
}

// The spec's shape (§7 *Tail-call optimization*, tail position does not cross a lambda): the
// function recurses inside the callback it hands to Iter.reduce, and the
// callback reads the field.
func TestAppFields_ReadThroughARecursiveReduceCallback(t *testing.T) {
	onlyAppFieldError(t, buildProjectForDefaultConfig(appFieldsRecursionPrefix+`fn total(xs: List<Int>) -> Int
  Iter.reduce(xs, 0, fn acc, _ -> acc + App.n + total([]))
end
test "default"
    assert total([1]) == 1
end
`), "this test has no boot, but code it runs reads `App.n` (line 13)")
}

// Mutual recursion through lambdas: ping's lambda calls pong, whose lambda
// calls ping and reads the field.
func TestAppFields_ReadThroughMutuallyRecursiveLambdas(t *testing.T) {
	onlyAppFieldError(t, buildProjectForDefaultConfig(appFieldsRecursionPrefix+`fn ping(x: Int) -> (Int) -> Int
  fn k -> pong(x)(k)
end
fn pong(x: Int) -> (Int) -> Int
  fn k -> if k == 0 then App.n else ping(x)(k - 1)
end
test "default"
    assert ping(1)(0) == 1
end
`), "this test has no boot, but code it runs reads `App.n` (line 16)")
}

// A stdlib function's body is walked against the stdlib file that declares
// it. `Iter.count` asks `Iter.known_count`, which dispatches to the program's
// impls, and this one reads the field. The walk used to look the stdlib
// body's positions up in the calling file's references, where they name
// nothing, and found no read.
func TestAppFields_ReadThroughAStdlibFunction(t *testing.T) {
	onlyAppFieldError(t, buildProjectForDefaultConfig(appFieldsRecursionPrefix+`struct Bag
    items: List<Int>
end
impl Iter<Int> for Bag
  fn each_while(collection: Bag, yield: (Int) -> Bool) -> Bool
      Iter.each_while(collection.items, yield)
  end
  fn known_count(_collection: Bag) -> Maybe<Int>
      Some(App.n)
  end
end
test "default"
    assert Iter.count(Bag{items: [1]}) == 1
end
`), "this test has no boot, but code it runs reads `App.n` (line 20)")
}
