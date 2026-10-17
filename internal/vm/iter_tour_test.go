package vm_test

import (
	"github.com/nomi-language/nomi/internal/expectation"
	"testing"
)

func TestVMIter_TourMatchesRecordedAnswer(t *testing.T) {
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", []string{tourBlock(t, "pipes.md", `|> Iter.map(fn n -> n * n)`), tourBlock(t, "iteration-and-loops.md", `if x == 3 { return 300 }`), tourBlock(t, "collections.md", `|> Iter.sum()`)}, vmPathResolver(t, "tour"))
	if len(got.Cases) != 3 || len(refused) != 0 {
		t.Fatalf("list map program incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatal(diffs)
	}
}
