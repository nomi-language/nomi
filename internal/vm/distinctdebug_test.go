package vm_test

import (
	"github.com/nomi-language/nomi/internal/expectation"
	"testing"
)

func TestVMDistinctDebug_TourMatchesRecordedAnswer(t *testing.T) {
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", []string{"structs-enums-distinct.md:L259", "generics.md:L106", "generics.md:L147"}, vmPathResolver(t, "tour"))
	if len(got.Cases) != 3 || len(refused) != 0 {
		t.Fatalf("distinct-debug Tour program incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatal(diffs)
	}
}
