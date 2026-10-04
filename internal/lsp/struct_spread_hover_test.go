package lsp

import (
	"strings"
	"testing"
)

// Hover inside a struct-update spread literal, `{..base, field: value}`.
//
// Two positions, and they reach the analyzer by different routes. The HEAD is
// an ordinary expression, so it hovers because analysis/builder.go walks
// `StructLit.Spread` — before that walk existed the head was not resolved at
// all and the checker reported `undefined variable`. Each FIELD LABEL hovers
// because the literal goes through `checkStructLitAgainstStruct`, whose
// `recordLitFieldLabelTypes` mints the field symbol; the spread form gets that
// for free precisely because it reuses the validator rather than carrying its
// own field walk.
func TestHover_StructSpread(t *testing.T) {
	const prog = `struct Point {
  x: Int
  y: Int
}

fn shift(): Point {
  origin = Point{x: 1, y: 2}
  MARK
}
`
	cases := []struct {
		name string
		line string
		want string
	}{
		{"spread head", "  {..▮origin, x: 9, y: 8}", "origin: Point"},
		{"first field label", "  {..origin, ▮x: 9, y: 8}", "x: Int"},
		{"second field label", "  {..origin, x: 9, ▮y: 8}", "y: Int"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, pos := hoverMarkerPosition(t, strings.Replace(prog, "  MARK", tc.line, 1))
			uri := "file:///struct_spread_hover.nomi"
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
			got := renderHover(sym)
			if !strings.Contains(got, tc.want) {
				t.Errorf("hover = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// An ANONYMOUS head. The field labels come from the head's own inferred shape,
// so this also pins that the anonymous case reaches the same validator.
func TestHover_StructSpreadAnonymousHead(t *testing.T) {
	const prog = `fn shift(): Int {
  base = {x: 1, y: 2}
  moved = {..base, ▮x: 9}
  moved.x
}
`
	input, pos := hoverMarkerPosition(t, prog)
	uri := "file:///struct_spread_anon_hover.nomi"
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
	if got := renderHover(sym); !strings.Contains(got, "x: Int") {
		t.Errorf("hover = %q, want it to contain %q", got, "x: Int")
	}
}
