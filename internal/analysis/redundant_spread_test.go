package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

const redundantSpreadDecls = `struct Geo
  lat: Int
  lon: Int
end
struct Address
  city: String
  geo: Geo
end
struct Person
  name: String
  address: Address
end
fn load() -> Person
  Person{name: "Ada", address: Address{city: "Bath", geo: Geo{lat: 0, lon: 0}}}
end
`

// sourcePos is the 1-based line and byte column of the first needle in src.
func sourcePos(t *testing.T, src, needle string) (int, int) {
	t.Helper()
	i := strings.Index(src, needle)
	if i < 0 {
		t.Fatalf("%q is not in the source", needle)
	}
	line := strings.Count(src[:i], "\n") + 1
	return line, i - strings.LastIndex(src[:i], "\n")
}

// A nested update whose spread is the value its field already holds is an
// error at the spread, spanning it, with a fix that deletes the path after
// `..`. It fires through a bare `..` at any depth and in the literal patch
// of `Struct.update`.
func TestRedundantSpread_RefusesTheFieldsOwnValue(t *testing.T) {
	for _, tc := range []struct{ name, body, spread, path, field string }{
		{
			"one level",
			`_a = {..ada, address: {..ada.address, city: "NYC"}}`,
			"..ada.address", "ada.address", "address",
		},
		{
			"through a bare `..`",
			`_a = {..ada, address: {.., geo: {..ada.address.geo, lat: 1}}}`,
			"..ada.address.geo", "ada.address.geo", "geo",
		},
		{
			"through a named path",
			`_a = {..ada.address, geo: {..ada.address.geo, lat: 1}}`,
			"..ada.address.geo", "ada.address.geo", "geo",
		},
		{
			"in a Struct.update patch",
			`_a = Struct.update(ada, {address: {..ada.address, city: "NYC"}})`,
			"..ada.address", "ada.address", "address",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := redundantSpreadDecls + "fn main()\n  ada = load()\n  " + tc.body + "\nend\n"
			_, errs := checkSourceWithStdlib(src)
			if len(errs) != 1 {
				t.Fatalf("want one error, got %d: %v", len(errs), errs)
			}
			e := errs[0]
			want := "`.." + tc.path + "` is the value `" + tc.field + "` already holds here: write `..`"
			if e.Message != want {
				t.Fatalf("message %q, want %q", e.Message, want)
			}
			if e.Code != analysis.RedundantSpreadCode {
				t.Errorf("code %q, want %q", e.Code, analysis.RedundantSpreadCode)
			}
			line, col := sourcePos(t, src, tc.spread)
			if e.Line != line || e.Col != col || e.EndLine != line || e.EndCol != col+len(tc.spread) {
				t.Errorf("span %d:%d-%d:%d, want %d:%d-%d:%d", e.Line, e.Col, e.EndLine, e.EndCol, line, col, line, col+len(tc.spread))
			}
			if e.Fix == nil || e.Fix.Title != "Write `..`" || len(e.Fix.Edits) != 1 {
				t.Fatalf("fix %+v, want one edit titled Write `..`", e.Fix)
			}
			ed := e.Fix.Edits[0]
			if ed.Text != "" || ed.Start.Line != line || ed.Start.Col != col+2 || ed.End.Line != line || ed.End.Col != col+len(tc.spread) {
				t.Errorf("edit %+v, want the path after `..` deleted", ed)
			}
		})
	}
}

// A spread naming another value, and a base with no name path, a call,
// are values, not the field's own: a bare `..` under `..load()` would not
// run `load()` again.
func TestRedundantSpread_AcceptsOtherValues(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"another value", `_a = {..ada, address: {..bob.address, city: "NYC"}}`},
		{"another value through a bare `..`", `_a = {..ada, address: {.., geo: {..bob.address.geo, lat: 1}}}`},
		{"a call base", `_a = {..load(), address: {..load().address, city: "NYC"}}`},
		{"a bare `..` under a call base", `_a = {..load(), address: {.., geo: {..load().address.geo, lat: 1}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bindings := ""
			if strings.Contains(tc.body, "bob") {
				bindings = "ada = load()\n  bob = load()\n  "
			}
			src := redundantSpreadDecls + "fn main()\n  " + bindings + tc.body + "\nend\n"
			_, errs := checkSourceWithStdlib(src)
			if len(errs) != 0 {
				t.Fatalf("the front end rejects this: %v", errs)
			}
		})
	}
}
