package lsp

import (
	"strings"
	"testing"
)

// Hover and completion inside a map update, `{..base, key => value}`. The
// base is an ordinary expression, so it hovers because analysis/builder.go
// walks `MapLit.Spread`.
func TestHover_MapSpread(t *testing.T) {
	const prog = `fn shift(): Map<String, Int> {
  scores = {"a" => 1}
  bonus = 5
  MARK
}
`
	cases := []struct {
		name string
		line string
		want string
	}{
		{"spread head", `  {..▮scores, "b" => bonus}`, "scores: Map<String, Int>"},
		{"entry value", `  {..scores, "b" => ▮bonus}`, "bonus: Int"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, pos := hoverMarkerPosition(t, strings.Replace(prog, "  MARK", tc.line, 1))
			uri := "file:///map_spread_hover.nomi"
			s := NewServer()
			s.docs.Open(uri, input)
			doc := s.docs.Get(uri)
			if doc == nil {
				t.Fatal("doc not found after Open")
			}
			sym := doc.Analysis.SymbolAt(pos)
			if sym == nil {
				t.Fatalf("no symbol at %v in:\n%s", pos, input)
			}
			if got := renderHover(sym); !strings.Contains(got, tc.want) {
				t.Errorf("hover = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// Completion offers the function's locals at the spread's head and at an
// entry's value.
func TestCompletion_MapSpread(t *testing.T) {
	cases := []struct {
		name string
		line string // the body's third line, the caret at ‸
	}{
		{"spread head", `  {..sc‸, "b" => 2}`},
		{"entry value", `  {..scores, "b" => bo‸}`},
		{"second entry value", `  {..scores, "b" => 1, "c" => bo‸}`},
		// The slot after the spread holds a key, not a field label: it
		// parses as the struct form, and the map base makes it an
		// expression.
		{"first key", `  {..scores, ‸}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn shift(): Map<String, Int> {\n  scores = {\"a\" => 1}\n  bonus = 5\n" + tc.line + "\n}\n"
			char := strings.Index(tc.line, cursorMark)
			src = strings.Replace(src, cursorMark, "", 1)
			uri := "file:///map_spread_completion.nomi"
			s := NewServer()
			s.docs.Open(uri, src)
			labels := completionLabels(t, s, uri, 3, char)
			want := "bonus"
			if tc.name == "spread head" {
				want = "scores"
			}
			if !labels[want] {
				t.Errorf("completion at %q lacks %q; got %v", tc.line, want, labels)
			}
		})
	}
}
