package vm_test

import (
	"github.com/nomi-language/nomi/internal/expectation"
	"testing"
)

func TestVMStdMethodCall_TourMatchesRecordedAnswer(t *testing.T) {
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", []string{"dates-and-times.md:L101"}, vmPathResolver(t, "tour"))
	if len(got.Cases) != 1 || len(refused) != 0 {
		t.Fatalf("instant/duration Tour program incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatalf("instant/duration output differs: %v", diffs)
	}
}

func TestVMStringHosts_TourMatchesRecordedAnswers(t *testing.T) {
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"pipes.md:L144"}
	got, refused := vmSubsetOf(t, "tour", ids, vmPathResolver(t, "tour"))
	if len(got.Cases) != len(ids) || len(refused) != 0 {
		t.Fatalf("string host Tour programs incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatal(diffs)
	}
}
