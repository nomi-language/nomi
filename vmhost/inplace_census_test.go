package vmhost

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// inPlaceCensus counts, over a population of functions, the struct updates
// and the collection operations that may write their operand in place, and
// the direct calls that hand an argument on.
type inPlaceCensus struct {
	updates, always, owned  int // updates; in place unconditionally; when a caller hands a parameter over
	local                   int // updates ir.InPlaceUpdates finds alone, knowing no callee
	collOps, takes, ownTake int // Vector.set/push, Map.put/remove, Set.insert/remove; in place; of them when handed over
	calls, moving           int // direct calls; those that hand an argument on
	folds, foldsOwned       int // Iter.reduce and Iter.map_reduce; those that may hand the accumulator on
	fields, fieldsOwned     int // field reads of a struct, record, tuple or collection; those that own it
}

// collectionWrites are the crossings that may write an owned receiver.
var collectionWrites = map[string]bool{
	"Vector.set": true, "Vector.push": true, "Map.put": true, "Map.remove": true,
	"Set.insert": true, "Set.remove": true,
}

// add counts f and every lambda body inside it, as the machine m answers
// their ownership.
func (c *inPlaceCensus) add(m interface {
	Ownership(*ir.Func) *ir.Ownership
}, f *ir.Func) {
	o := m.Ownership(f)
	if o == nil {
		o = &ir.Ownership{}
	}
	local := ir.InPlaceUpdates(f)
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			switch x := in.(type) {
			case *ir.Make:
				if x.Kind() != ir.MakeUpdate {
					continue
				}
				c.updates++
				if local[x] {
					c.local++
				}
				if need, ok := o.Updates[x]; ok {
					if need == 0 {
						c.always++
					} else {
						c.owned++
					}
				}
			case *ir.Call:
				if x.Crosses() {
					if x.Callee() != nil && collectionWrites[x.Callee().Name()] {
						c.collOps++
						if need, ok := o.Takes[x]; ok {
							c.takes++
							if need != 0 {
								c.ownTake++
							}
						}
					}
					continue
				}
				if x.Form() == ir.CalleeDirect {
					c.calls++
					if len(o.Moves[x]) > 0 {
						c.moving++
					}
				}
			case *ir.FuncValue:
				c.add(m, x.Body())
			case *ir.Iter:
				if x.Op() == ir.IterReduce || x.Op() == ir.IterMapReduce {
					c.folds++
					if _, ok := o.Folds[x]; ok {
						c.foldsOwned++
					}
				}
			case *ir.Proj:
				switch x.Kind() {
				case ir.ProjField, ir.ProjRecordField, ir.ProjSlot:
				default:
					continue
				}
				vt := f.TempType(x.Dst())
				if vt == nil {
					continue
				}
				switch vt.Kind() {
				case ir.KindStruct, ir.KindRecord, ir.KindTuple, ir.KindVector, ir.KindMap, ir.KindSet:
					c.fields++
					if _, ok := o.Fields[x]; ok {
						c.fieldsOwned++
					}
				}
			}
		}
	}
}

func (c *inPlaceCensus) String() string {
	pct := func(a, b int) string {
		if b == 0 {
			return "-"
		}
		return fmt.Sprintf("%.0f%%", 100*float64(a)/float64(b))
	}
	return fmt.Sprintf("struct updates %d: in place %d (%s), %d of them only when a caller hands the base over, "+
		"%d by the function alone (no callee's answer, no parameter); "+
		"collection writes %d: in place %d (%s), %d of them when handed over; direct calls %d: %d hand an argument on (%s); "+
		"folds %d: %d hand the accumulator on (%s); field reads of composite values %d: %d own the value (%s)",
		c.updates, c.always+c.owned, pct(c.always+c.owned, c.updates), c.owned, c.local,
		c.collOps, c.takes, pct(c.takes, c.collOps), c.ownTake,
		c.calls, c.moving, pct(c.moving, c.calls),
		c.folds, c.foldsOwned, pct(c.foldsOwned, c.folds),
		c.fields, c.fieldsOwned, pct(c.fieldsOwned, c.fields))
}

// TestInPlaceCensus logs, for the corpus's programs and the benchmarks, how
// many struct updates and collection writes the machine writes in place,
// how many folds may hand their accumulator to their callback, and how
// many field reads own the value they read (ir.Owns, as
// vm.Machine.Ownership answers it), counting each program's own functions
// and the lambdas inside them. It asserts no fraction: it
// records where the analysis stands. A program the front end rejects or
// that does not lower is skipped.
//
//	go test ./vmhost -run '^TestInPlaceCensus$' -count=1 -v
func TestInPlaceCensus(t *testing.T) {
	if testing.Short() {
		t.Skip("loads every corpus program; -short")
	}
	for _, pop := range []struct{ name, dir string }{
		{"corpus", filepath.Join("..", "tests")},
		{"benchmarks", filepath.Join("..", "benchmarks")},
	} {
		var c inPlaceCensus
		programs := 0
		err := filepath.WalkDir(pop.dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".nomi") {
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(src), "fn ") {
				return err
			}
			p, err := Load(path, WithOutput(io.Discard), WithErrorOutput(io.Discard))
			if err != nil || p.entry == nil || p.Unsupported() != nil {
				return nil
			}
			m := p.machine(io.Discard)
			for _, f := range p.entry.Funcs() {
				if !f.Pos().Stdlib() {
					c.add(m, f)
				}
			}
			for _, tc := range p.entry.Tests() {
				c.add(m, tc.Fn())
			}
			programs++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s, %d programs: %s", pop.name, programs, &c)
	}
}
