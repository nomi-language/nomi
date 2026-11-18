package stdlibbindings

import "testing"

// Every row decides whether a call to it is an outside call: a Pure row is
// inside by being Pure, and every other row says Inside or Outside. There
// is no default, so a new row that says neither fails here.
func TestEveryRowDecidesItsWorld(t *testing.T) {
	for _, rows := range [][]Binding{Funcs(), RtFuncs()} {
		for _, b := range rows {
			switch {
			case b.Pure && b.World == Outside:
				t.Errorf("%s is Pure and Outside; a Pure row acts on nothing outside the machine", b.Name)
			case b.Pure && b.World != 0:
				t.Errorf("%s is Pure and sets World; a Pure row is inside by being Pure", b.Name)
			case !b.Pure && b.World != Inside && b.World != Outside:
				t.Errorf("%s is not Pure and says neither Inside nor Outside (World %d)", b.Name, b.World)
			}
		}
	}
}

// A function a target can lack acts outside the machine, so every row with
// a capability class is an outside call. Outside is its own fact, set on
// the row: not derived from the class, and not from Pure.
func TestEveryClassedRowIsOutside(t *testing.T) {
	worlds := Worlds()
	for name, class := range Classes() {
		if !worlds[name] {
			t.Errorf("%s has class %s and is not Outside", name, class)
		}
	}
}
