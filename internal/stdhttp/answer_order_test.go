package stdhttp

import (
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// What the mux or a Go helper answers carries its headers sorted by name,
// each name's values in the order Go set them, so the same call answers
// the same list every time: Go's Header is a map and keeps no order.
func TestAnswers_HeadersComeInAStableOrder(t *testing.T) {
	only := NewMux(listOf([]string{"GET /users/{id}", "/static/"})).Ok
	set := listOf([]string{"X-Trace", "a", "Cache-Control", "no-store", "ETag", `"v1"`, "X-Trace", "b"})
	calls := map[string]func() *rt.List[string]{
		"route 405": func() *rt.List[string] { return Route(only, "DELETE", "x.com", "/users/7") },
		"route 404": func() *rt.List[string] { return Route(only, "GET", "x.com", "/nothing") },
		"route 301": func() *rt.List[string] { return Route(only, "GET", "x.com", "/static") },
		"content": func() *rt.List[string] {
			return AnswerContent("GET", "/f", nil, "f.txt", rt.SmallInt(0), set, rt.Bytes("hello"))
		},
		"content range": func() *rt.List[string] {
			return AnswerContent("GET", "/f", listOf([]string{"Range", "bytes=1-2"}), "f.txt", rt.SmallInt(-1), set, rt.Bytes("hello"))
		},
		"redirect": func() *rt.List[string] { return AnswerRedirect("GET", "/a/b", nil, "c", rt.SmallInt(302)) },
	}
	for name, call := range calls {
		first := strs(call())
		if first[0] != "answer" || len(first) < 7 {
			t.Fatalf("%s: %q answers fewer than two headers", name, first)
		}
		headers := first[3:]
		var names []string
		for i := 0; i < len(headers); i += 2 {
			names = append(names, headers[i])
		}
		if !slices.IsSorted(names) {
			t.Errorf("%s: header names %q are not sorted", name, names)
		}
		for range 20 {
			if again := strs(call()); !slices.Equal(again, first) {
				t.Fatalf("%s: answered %q, then %q", name, strings.Join(first, " "), strings.Join(again, " "))
			}
		}
	}
	// Two values of one name keep the order they were set in.
	got := strings.Join(strs(calls["content"]()), " ")
	if !strings.Contains(got, "X-Trace a X-Trace b") {
		t.Errorf("X-Trace values out of order: %q", got)
	}
}
