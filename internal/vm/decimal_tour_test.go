package vm_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
)

func TestVMDecimal_TourMatchesRecordedAnswer(t *testing.T) {
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", []string{tourBlock(t, "scalars-and-strings.md", `19.99d + 5.00d + 2.50d + 0.99d`)}, vmPathResolver(t, "tour"))
	if len(got.Cases) != 1 || len(refused) != 0 {
		t.Fatalf("Decimal example incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatal(diffs)
	}
}
