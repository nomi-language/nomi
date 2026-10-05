package vm_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
)

func TestVMImpl_TourProgramsMatchRecordedAnswers(t *testing.T) {
	ids := []string{
		tourBlock(t, "interfaces-and-dispatch.md", `"${c.name} says meow"`),
		tourBlock(t, "structs-enums-distinct.md", `User.rename(alice, "Augusta")`),
	}
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", ids, vmPathResolver(t, "tour"))
	if len(got.Cases) != len(ids) || len(refused) != 0 {
		t.Fatalf("completed %d of %d impl programs; refusals: %v", len(got.Cases), len(ids), refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatalf("impl programs disagree with recorded answers: %v", diffs)
	}
}
