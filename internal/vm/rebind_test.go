package vm_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
)

func TestVMRebind_TourMatchesRecordedAnswer(t *testing.T) {
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", []string{tourBlock(t, "bindings-and-expressions.md", `name = String.to_lower(name)`)}, vmPathResolver(t, "tour"))
	if len(got.Cases) != 1 || len(refused) != 0 {
		t.Fatalf("binding refinement incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatal(diffs)
	}
}
