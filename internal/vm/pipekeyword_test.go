package vm_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
)

func TestVMPipeKeyword_TourMatchesRecordedAnswer(t *testing.T) {
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	got, refused := vmSubsetOf(t, "tour", []string{tourBlock(t, "pipes.md", `|> if String.contains?("A")`)}, vmPathResolver(t, "tour"))
	if len(got.Cases) != 1 || len(refused) != 0 {
		t.Fatalf("keyword pipeline incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatal(diffs)
	}
}
