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
	got, refused := vmSubsetOf(t, "tour", []string{tourBlock(t, "structs-enums-distinct.md", `type Email String`), tourBlock(t, "generics.md", `dbg advance(Counter(10), 5)`), tourBlock(t, "generics.md", `dbg plus(Day(10), Days(4))`)}, vmPathResolver(t, "tour"))
	if len(got.Cases) != 3 || len(refused) != 0 {
		t.Fatalf("distinct-debug Tour program incomplete: %v", refused)
	}
	if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
		t.Fatal(diffs)
	}
}
