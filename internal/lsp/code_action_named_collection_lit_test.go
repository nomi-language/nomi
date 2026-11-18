package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// "Remove `List`" and "Remove `Map`" turn `List[...]` and `Map{...}` into
// the unnamed literals, and the fixed program checks.
func TestNamedCollectionLitFix_RemovesTheName(t *testing.T) {
	for _, tc := range []struct{ name, title, src, want string }{
		{"a list", "Remove `List`", "fn main()\n    xs = List[1, 2]\n    dbg xs\nend\n", "fn main()\n    xs = [1, 2]\n    dbg xs\nend\n"},
		{"a map", "Remove `Map`", "fn main()\n    m = Map{\"a\" => 1}\n    dbg m\nend\n", "fn main()\n    m = {\"a\" => 1}\n    dbg m\nend\n"},
		{"an empty map", "Remove `Map`", "fn main()\n    m: Map<String, Int> = Map{}\n    dbg m\nend\n", "fn main()\n    m: Map<String, Int> = {}\n    dbg m\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer()
			actions := releaseActions(t, s, tc.src, tc.title)
			if len(actions) != 1 {
				t.Fatalf("got %d fixes titled %s, want 1", len(actions), tc.title)
			}
			got := applyTextEditsLastFirst(tc.src, actions[0].Edit.Changes[protocol.DocumentUri(releaseURI)])
			if got != tc.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tc.want)
			}
			s.docs.Open(releaseURI, got)
			if errs := s.docs.Snapshot(releaseURI).Analysis.TypeErrors; len(errs) > 0 {
				t.Fatalf("the fixed program has errors: %v", errs)
			}
		})
	}
}
