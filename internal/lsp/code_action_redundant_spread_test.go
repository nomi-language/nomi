package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// "Write `..`" turns a nested spread of the field's own value into a bare
// `..`, at one level and through a bare `..`, and the fixed program checks.
func TestRedundantSpreadFix_WritesTheBareSpread(t *testing.T) {
	const decls = "struct Geo\n    lat: Int\n    lon: Int\nend\n\nstruct Address\n    city: String\n    geo: Geo\nend\n\nstruct Person\n    name: String\n    address: Address\nend\n\n"
	const ada = "    ada = Person{name: \"Ada\", address: Address{city: \"Bath\", geo: Geo{lat: 0, lon: 0}}}\n"
	for _, tc := range []struct{ name, line, want string }{
		{
			"one level",
			"    dbg {..ada, address: {..ada.address, city: \"NYC\"}}\n",
			"    dbg {..ada, address: {.., city: \"NYC\"}}\n",
		},
		{
			"through a bare `..`",
			"    dbg {..ada, address: {.., geo: {..ada.address.geo, lat: 1}}}\n",
			"    dbg {..ada, address: {.., geo: {.., lat: 1}}}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "fn main()\n" + ada + tc.line + "end\n"
			want := decls + "fn main()\n" + ada + tc.want + "end\n"
			s := NewServer()
			actions := releaseActions(t, s, src, "Write `..`")
			if len(actions) != 1 {
				t.Fatalf("got %d fixes titled Write `..`, want 1", len(actions))
			}
			got := applyTextEditsLastFirst(src, actions[0].Edit.Changes[protocol.DocumentUri(releaseURI)])
			if got != want {
				t.Fatalf("got\n%s\nwant\n%s", got, want)
			}
			s.docs.Open(releaseURI, got)
			if errs := s.docs.Snapshot(releaseURI).Analysis.TypeErrors; len(errs) > 0 {
				t.Fatalf("the fixed program has errors: %v", errs)
			}
		})
	}
}
