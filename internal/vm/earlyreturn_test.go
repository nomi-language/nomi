package vm_test

import (
	"github.com/nomi-language/nomi/internal/expectation"
	"testing"
)

func TestVMEarlyReturn_TourRecordedAnswer(t *testing.T) {
	ids := []string{"functions-and-lambdas.md:L17"}
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
