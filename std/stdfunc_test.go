package std

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// TestStdFuncsAreDistinct: no two declarations of a std module share a
// std identity (analysis.StdFunc), so sameDecl, which takes two nodes with
// one identity for one function in two parses of the module, never takes
// two functions for one. A receiver's impls of one interface at different
// type arguments (`impl Add<Days, Date> for Date`, `impl Add<Weeks, Date>
// for Date`) and an inherent function beside an impl's of the same name
// are the pairs the identity's Impl tells apart.
func TestStdFuncsAreDistinct(t *testing.T) {
	lib := All()
	total, impls := 0, 0
	for _, fa := range lib.Files {
		seen := map[analysis.StdFunc]ast.Node{}
		for n, id := range fa.StdFuncs() {
			total++
			if id.Impl != "" {
				impls++
			}
			if prev, dup := seen[id]; dup {
				pl, _ := ast.Position(prev)
				nl, _ := ast.Position(n)
				t.Errorf("%s: lines %d and %d declare two functions with one identity %+v", fa.Origin, pl, nl, id)
			}
			seen[id] = n
		}
	}
	if total == 0 || impls == 0 {
		t.Fatalf("read %d std functions, %d of them in an impl, so this test checks nothing", total, impls)
	}
}
