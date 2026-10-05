package vm_test

import (
	"github.com/nomi-language/nomi/internal/expectation"
	"testing"
)

func TestVMInterfaceCall_TourMatchesRecordedAnswer(t *testing.T) {
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", []string{tourBlock(t, "interfaces-and-dispatch.md", `io.print(Speech.speak(rex))`)}, vmPathResolver(t, "tour"))
	if len(got.Cases) != 1 || len(refused) != 0 {
		t.Fatalf("interface-call Tour program incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatal(diffs)
	}
}

func TestVMInterfaceCall_InheritedDefaultsMatchRecordedAnswers(t *testing.T) {
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", []string{tourBlock(t, "interfaces-and-dispatch.md", `open fn brief(value: self)`), tourBlock(t, "interfaces-and-dispatch.md", `io.print(HasName.greet(alice))`)}, vmPathResolver(t, "tour"))
	if len(got.Cases) != 2 || len(refused) != 0 {
		t.Fatalf("inherited default programs incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatal(diffs)
	}
}
