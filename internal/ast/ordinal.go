package ast

import "github.com/nomi-language/nomi/rt"

// The ordinal names that read a tuple's items (`t.first`, spec §4 *Tuples*)
// are listed once, in rt, which the debugger's variable tree reads too; the
// front end reaches them here.

// TupleOrdinalCount is how many items of a tuple have an ordinal name.
const TupleOrdinalCount = rt.TupleOrdinalCount

// TupleOrdinal is the name that reads the item at zero-based index i of a
// tuple: "first" for 0. ok is false past the last ordinal.
func TupleOrdinal(i int) (name string, ok bool) { return rt.TupleOrdinal(i) }

// TupleOrdinalIndex is the zero-based index the ordinal name reads: 0 for
// "first". ok is false for a name that is no ordinal.
func TupleOrdinalIndex(name string) (index int, ok bool) { return rt.TupleOrdinalIndex(name) }
