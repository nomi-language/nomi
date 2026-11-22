package stdlibbindings

import "testing"

// Every row is exactly one of Pure, Held, or an effect an effects list
// names, and every row that is not Pure decides whether a call to it acts
// outside the machine (World): a Pure row is inside by being Pure, a Held row is Inside,
// and every other row says Inside or Outside. There is no default, so a new
// row that says neither fails here.
func TestEveryRowDecidesItsWorld(t *testing.T) {
	for _, rows := range [][]Binding{Funcs(), RtFuncs()} {
		for _, b := range rows {
			switch {
			case b.Pure && b.Held:
				t.Errorf("%s is Pure and Held; a Held row acts through a value it is given, which is an effect", b.Name)
			case b.Pure && b.World == Outside:
				t.Errorf("%s is Pure and Outside; a Pure row acts on nothing outside the machine", b.Name)
			case b.Pure && b.World != 0:
				t.Errorf("%s is Pure and sets World; a Pure row is inside by being Pure", b.Name)
			case b.Held && b.World != Inside:
				t.Errorf("%s is Held and not Inside (World %d); obtaining a value outside is Outside, using one is Inside", b.Name, b.World)
			case !b.Pure && b.World != Inside && b.World != Outside:
				t.Errorf("%s is not Pure and says neither Inside nor Outside (World %d)", b.Name, b.World)
			}
		}
	}
}

// Unbounded says what a pure function's one call costs, so only a Pure row
// carries it: a function with an effect is refused for that already.
func TestEveryUnboundedRowIsPure(t *testing.T) {
	n := 0
	for _, rows := range [][]Binding{Funcs(), RtFuncs()} {
		for _, b := range rows {
			if b.Unbounded {
				n++
				if !b.Pure {
					t.Errorf("%s is Unbounded and not Pure", b.Name)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("no row is marked Unbounded")
	}
}

// A function a target can lack acts outside the machine, so every row with
// an effect class is Outside. Outside is its own fact, set on
// the row: not derived from the class, and not from Pure.
func TestEveryClassedRowIsOutside(t *testing.T) {
	for _, rows := range [][]Binding{Funcs(), RtFuncs()} {
		for _, b := range rows {
			if b.Class != "" && b.World != Outside {
				t.Errorf("%s has class %s and is not Outside", b.Name, b.Class)
			}
		}
	}
}
