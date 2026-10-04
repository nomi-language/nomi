package rt

import (
	"fmt"
	"os"
)

// Typed conveniences the tests in this package use to build and read the live
// kernels' inputs and outputs. None of them is reached from a program: the VM
// works over erased values and calls the `*Cells` kernels and `any`-typed
// entry points directly, so these live beside the tests rather than in the
// package.

func FormatString(v string) string { return v }

func InspectInt(v int64) string { return FormatInt(v) }

func FormatList[T any](xs *List[T], render func(T) string) string {
	return FormatListCells[T, List[T]](xs, render)
}

func ListEqual[T any](a, b *List[T], eq func(T, T) bool) bool {
	return ListCellsEqual[T, List[T]](a, b, eq)
}

func HashList[T any](xs *List[T], h func(T) uint64) uint64 {
	return HashListCells[T, List[T]](xs, h)
}

func ListSeq[T any](xs *List[T]) Seq[T] { return ListCellSeq[T](xs) }

func SeqToList[T any](fr *Frame, src Seq[T]) *List[T] {
	return SeqToListCells[T, List[T]](fr, src)
}

func TypeOf[T any](tid *TypeID) Type[T] { return Type[T]{TID: tid} }

// ScopedField reads one application field through LookupScopedField, the
// VM's read, typed for the test's convenience.
func ScopedField[T any](fr *Frame, name string) T {
	v, _ := LookupScopedField(fr, name)
	t, ok := v.(T)
	if !ok {
		Trap("application field " + name + " is unavailable")
	}
	return t
}

// Dbg writes DbgText for os.Stdout to os.Stdout, the way the VM writes it to
// its own destination.
func Dbg(line int, expr, rendered string) {
	fmt.Fprint(os.Stdout, DbgText(os.Stdout, line, expr, rendered))
}
