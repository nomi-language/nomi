package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A read of a binding a check narrowed shows the std subtype it holds
// there (spec §4 *Narrowing*), and a read outside the check its own type.
func TestHover_NarrowedBinding(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"in the branch", "fn half(n: Int, d: Int) -> Int\n  if d != 0 then Int.divide(n, ▮d) else 0\nend\n", "NonZeroInt"},
		{"after an early return", "fn half(n: Int, d: Int) -> Int\n  if d == 0 then return 0\n  Int.divide(n, ▮d)\nend\n", "NonZeroInt"},
		{"outside the check", "fn half(n: Int, d: Int) -> Int\n  k = if d != 0 then Int.divide(n, d) else 0\n  k + ▮d\nend\n", "Int"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, pos := hoverMarkerPosition(t, tc.src)
			s := NewServer()
			doc := s.docs.Open("file:///narrow.nomi", src)
			if doc == nil || doc.Analysis == nil {
				t.Fatal("no analysis")
			}
			if len(doc.Errors) > 0 || len(doc.Analysis.TypeErrors) > 0 {
				t.Fatalf("the program does not check: %v %v", doc.Errors, doc.Analysis.TypeErrors)
			}
			got, _ := hoverAt(t, src, protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col - 1)})
			if !strings.Contains(got, tc.want) || (tc.want == "Int" && strings.Contains(got, "NonZeroInt")) {
				t.Errorf("hover = %q, want %s", got, tc.want)
			}
		})
	}
}

// A pipeline over a narrowed binding shows the subtype its stages keep.
func TestPipeHints_NarrowedHead(t *testing.T) {
	src := `fn f(n: Int) -> Int
    if n <= 0 then return 0

    k = Some(n)
    |> Maybe.map(fn v -> v)
    |> Maybe.with_default(1)
    k
end
`
	wantPipeHints(t, src,
		"4:16 Maybe<PositiveInt>",
		"6:29 PositiveInt",
	)
}
