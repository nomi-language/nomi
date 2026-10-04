package vm_test

import (
	"github.com/nomi-language/nomi/internal/expectation"
	"testing"
)

func TestVMPreludeValues_TourMatchesRecordedAnswer(t *testing.T) {
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", []string{"pattern-matching.md:L78"}, vmPathResolver(t, "tour"))
	if len(got.Cases) != 1 || len(refused) != 0 {
		t.Fatalf("prelude enum Tour program incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatalf("prelude enum Tour output differs: %v", diffs)
	}
}
