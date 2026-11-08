package lsp

import (
	"slices"
	"testing"
)

// `t.0` is the parse error whose quick fix writes the ordinal, and the
// fixed program checks clean.
func TestTupleOrdinal_QuickFixWritesTheOrdinal(t *testing.T) {
	s := NewServer()
	src := "pub fn f(t: (Int, (String, Int))) -> Int {\n    t.0 + t.1.1\n}\n"
	want := "pub fn f(t: (Int, (String, Int))) -> Int {\n    t.first + t.second.second\n}\n"
	got := src
	for _, title := range []string{"Write `.first`", "Write `.second.second`"} {
		actions := parseFixActions(t, s, got, title)
		if len(actions) != 1 {
			t.Fatalf("got %d fixes titled %q, want 1", len(actions), title)
		}
		got = applyAllEdits(got, actions[0])
	}
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	s.docs.Open(parseFixURI, got)
	snap := s.docs.Snapshot(parseFixURI)
	if diags := buildPublishedDiagnostics(parseFixURI, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors); len(diags) != 0 {
		t.Errorf("fixed program has diagnostics: %v", diags)
	}
}

// After a dot, a tuple and a named tuple offer their items' ordinals, as
// many as they have, and an accessor over tuples offers them too. A struct
// offers its fields, a `first` among them as an ordinary field.
func TestTupleOrdinal_Completion(t *testing.T) {
	decls := "type Pair (Int, String)\n\nstruct Holder {\n    first: Int\n}\n\n"
	for _, tc := range []struct {
		name, src string
		want, not []string
	}{
		{"tuple", "fn f(t: (Int, String, Bool)) -> Int {\n    t." + cursorMark + "\n}\n", []string{"first", "second", "third"}, []string{"fourth"}},
		{"named tuple", decls + "fn f(p: Pair) -> Int {\n    p." + cursorMark + "\n}\n", []string{"first", "second"}, []string{"third"}},
		{"accessor", "fn f(ps: List<(Int, String)>) -> List<String> {\n    ps |> Iter.map(." + cursorMark + ") |> Iter.to_list()\n}\n", []string{"first", "second"}, []string{"third"}},
		{"struct", decls + "fn f(h: Holder) -> Int {\n    h." + cursorMark + "\n}\n", []string{"first"}, []string{"second"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			labels := itemLabels(complete(t, tc.src))
			for _, w := range tc.want {
				if !slices.Contains(labels, w) {
					t.Errorf("completion lacks %q: %v", w, labels)
				}
			}
			for _, n := range tc.not {
				if slices.Contains(labels, n) {
					t.Errorf("completion offers %q: %v", n, labels)
				}
			}
		})
	}
}
