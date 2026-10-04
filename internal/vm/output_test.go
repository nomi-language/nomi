package vm_test

import (
	"github.com/nomi-language/nomi/internal/expectation"
	"testing"
)

func TestVMOutput_TourRecordedAnswer(t *testing.T) {
	ids := []string{"bindings-and-expressions.md:L32"}
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", ids, vmPathResolver(t, "tour"))
	if len(got.Cases) != len(ids) || len(refused) != 0 {
		t.Fatalf("completed %d; refused: %v", len(got.Cases), refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatal(diffs)
	}
}
