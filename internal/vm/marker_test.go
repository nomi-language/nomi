package vm_test

import (
	"github.com/nomi-language/nomi/internal/expectation"
	"testing"
)

func TestVMMarker_TourMatchesRecordedAnswer(t *testing.T) {
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", []string{"structs-enums-distinct.md:L289"}, vmPathResolver(t, "tour"))
	if len(got.Cases) != 1 || len(refused) != 0 {
		t.Fatalf("marker Tour program incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatal(diffs)
	}
}
