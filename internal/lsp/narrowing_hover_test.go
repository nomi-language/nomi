package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A read of a binding a check narrowed shows the std refinement it holds
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

// A binding with no type written shows its literal's type, as a number's
// shows Int, though it passes where a NonEmptyList is expected; a `once`
// shows the refinement its value proves, as `once k = 60 * 60` shows
// PositiveInt (spec §4 *Value ranges*).
func TestHover_NonEmptyBinding(t *testing.T) {
	const decls = "once words = [\"a\"]\nonce k = 60 * 60\nonce title = \"nomi\"\nfn first(xs: NonEmptyList<String>) -> String\n  NonEmptyList.first(xs)\nend\n"
	for _, tc := range []struct{ name, src, want, not string }{
		{"a local list", "fn f() -> Int\n  local = [\"b\"]\n  _ = first(local)\n  List.length(▮local)\nend\n", "List<String>", "NonEmptyList"},
		{"a local number", "fn f() -> Int\n  n = 3\n  ▮n + 1\nend\n", "Int", "PositiveInt"},
		{"a once list", "fn f() -> Int\n  List.length(▮words)\nend\n", "NonEmptyList<String>", ""},
		{"a once number", "fn f() -> Int\n  ▮k + 1\nend\n", "PositiveInt", ""},
		{"a local string", "fn f() -> Int\n  local = \"b\"\n  String.length(▮local)\nend\n", "String", "NonEmptyString"},
		{"a once string", "fn f() -> Int\n  String.length(▮title)\nend\n", "NonEmptyString", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, pos := hoverMarkerPosition(t, decls+tc.src)
			s := NewServer()
			doc := s.docs.Open("file:///nonempty.nomi", src)
			if doc == nil || doc.Analysis == nil {
				t.Fatal("no analysis")
			}
			if len(doc.Errors) > 0 || len(doc.Analysis.TypeErrors) > 0 {
				t.Fatalf("the program does not check: %v %v", doc.Errors, doc.Analysis.TypeErrors)
			}
			got, _ := hoverAt(t, src, protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col - 1)})
			if !strings.Contains(got, tc.want) || (tc.not != "" && strings.Contains(got, tc.not)) {
				t.Errorf("hover = %q, want %s", got, tc.want)
			}
		})
	}
}

// A pipeline over a narrowed binding shows the refinement its stages keep.
func TestPipeHints_NarrowedHead(t *testing.T) {
	src := `fn f(n: Int) -> Int
    if n <= 0 then return 0

    k = Some(n)
    |> Option.map(fn v -> v)
    |> Option.with_default(1)
    k
end
`
	wantPipeHints(t, src,
		"4:16 Option<PositiveInt>",
		"6:30 PositiveInt",
	)
}
