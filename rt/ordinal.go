package rt

import "strconv"

// tupleOrdinals name a tuple's items in order: `t.first` reads item one,
// `t.second` item two (spec §4 *Tuples*). An item past the last has no name
// and is read by a pattern. The front end (internal/ast re-exports these)
// and the debugger's variable tree (Inspect) share this one list.
var tupleOrdinals = [...]string{
	"first", "second", "third", "fourth", "fifth",
	"sixth", "seventh", "eighth", "ninth", "tenth",
	"eleventh", "twelfth", "thirteenth", "fourteenth", "fifteenth",
	"sixteenth", "seventeenth", "eighteenth", "nineteenth", "twentieth",
}

// TupleOrdinalCount is how many items of a tuple have an ordinal name.
const TupleOrdinalCount = len(tupleOrdinals)

// TupleOrdinal is the name that reads the item at zero-based index i of a
// tuple: "first" for 0. ok is false past the last ordinal.
func TupleOrdinal(i int) (name string, ok bool) {
	if i < 0 || i >= len(tupleOrdinals) {
		return "", false
	}
	return tupleOrdinals[i], true
}

// TupleOrdinalIndex is the zero-based index the ordinal name reads: 0 for
// "first". ok is false for a name that is no ordinal.
func TupleOrdinalIndex(name string) (index int, ok bool) {
	for i, o := range tupleOrdinals {
		if o == name {
			return i, true
		}
	}
	return 0, false
}

// tupleItemLabel is the name a debugger's variable tree gives the item at
// zero-based index i of a tuple: its ordinal, or past the twentieth, which
// has none, its index in brackets (`[20]` for the twenty-first), as a List's
// elements are named, so it never reads as a field.
func tupleItemLabel(i int) string {
	if name, ok := TupleOrdinal(i); ok {
		return name
	}
	return "[" + strconv.Itoa(i) + "]"
}
