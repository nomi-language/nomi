package vm

import (
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// zip has a direct cursor for the view of a List, a Vector and a Range over
// Ints, and none for any other sequence: a Range of another element, a Maybe
// view, an adapter (no Src), which it pulls through a coroutine.
func TestZip_ADirectCursorOnlyForListsVectorsAndIntRanges(t *testing.T) {
	xs := rt.Cons[any](int64(1), rt.Cons[any](int64(2), nil))
	vec := rt.VectorOf[any]([]any{"a", "b", "c"})
	ints := rangeDesc.Make(int64(5), some(int64(7)), true)
	chars := rangeDesc.Make(rt.Byte('a'), some(rt.Byte('c')), false)
	walk := func(src any) []any {
		cursor := directCursor(rt.Seq[any]{Src: src})
		if cursor == nil {
			return nil
		}
		var out []any
		next := cursor()
		for v, more := next(); more; v, more = next() {
			out = append(out, v)
		}
		return out
	}
	for name, tc := range map[string]struct {
		src  any
		want []any
	}{
		"list":   {xs, []any{int64(1), int64(2)}},
		"vector": {vec, []any{"a", "b", "c"}},
		"ints":   {ints, []any{int64(5), int64(6), int64(7)}},
	} {
		got := walk(tc.src)
		if len(got) != len(tc.want) {
			t.Errorf("%s: cursor walks %v, want %v", name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: cursor walks %v, want %v", name, got, tc.want)
			}
		}
	}
	for name, src := range map[string]any{"bytes range": chars, "maybe": some(int64(1)), "adapter": nil} {
		if directCursor(rt.Seq[any]{Src: src}) != nil {
			t.Errorf("%s: zip steps it directly, but only a List, a Vector or an Int range steps so", name)
		}
	}
}
