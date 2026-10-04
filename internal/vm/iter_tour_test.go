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
	got, refused := vmSubsetOf(t, "tour", []string{"pipes.md:L40", "iteration-and-loops.md:L113", "collections.md:L68"}, vmPathResolver(t, "tour"))
	if len(got.Cases) != 3 || len(refused) != 0 {
		t.Fatalf("list map program incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatal(diffs)
	}
}
