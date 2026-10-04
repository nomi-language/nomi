package rt

import (
	"strconv"
	"testing"
)

func stringSeq(items ...string) Seq[string] {
	var list *List[string]
	for i := len(items) - 1; i >= 0; i-- {
		list = Cons(items[i], list)
	}
	return ListSeq(list)
}

// The separator goes between elements and nowhere else, and an empty source
// joins to "".
func TestSeqJoin_SeparatorBetweenElements(t *testing.T) {
	cases := []struct {
		items []string
		sep   string
		want  string
	}{
		{[]string{"a", "b", "c"}, ", ", "a, b, c"},
		{nil, "-", ""},
		{[]string{"x"}, "-", "x"},
		{[]string{"he", "llo"}, "", "hello"},
		{[]string{"é", "", "🙂"}, "|", "é||🙂"},
	}
	for _, c := range cases {
		got, ok := SeqJoin(nil, stringSeq(c.items...), c.sep)
		if !ok || got != c.want {
			t.Errorf("SeqJoin(%q, %q) = %q, %v; want %q, true", c.items, c.sep, got, ok, c.want)
		}
	}
}

// The walk stops where the source stops: a bounded prefix of an unbounded
// source joins after asking for exactly that many elements.
func TestSeqJoin_DrivesThroughEachWhile(t *testing.T) {
	asked := 0
	text := SeqMap(SeqTake(countingSeq(t, &asked), 3), func(_ *Frame, n int64) string { return strconv.FormatInt(n, 10) })
	got, ok := SeqJoin(nil, text, "-")
	if !ok || got != "1-2-3" {
		t.Fatalf("SeqJoin = %q, %v; want \"1-2-3\", true", got, ok)
	}
	if asked != 3 {
		t.Fatalf("the source was asked for %d elements, want 3", asked)
	}
}

// The VM's sequences carry `any`; an element that is not a string stops the
// walk and reports it.
func TestSeqJoin_ReportsANonStringElement(t *testing.T) {
	src := Seq[any]{Run: func(fr *Frame, yield func(*Frame, any) bool) bool {
		return yield(fr, "a") && yield(fr, int64(1)) && yield(fr, "never")
	}}
	if _, ok := SeqJoin(nil, src, ","); ok {
		t.Fatal("SeqJoin over an Int element answered ok")
	}
}
