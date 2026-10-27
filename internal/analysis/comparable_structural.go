package analysis

// Comparable over tuples.
//
// A tuple has no declaration to hang an impl on, and it is Comparable
// exactly when every element is: it compares lexicographically, the first
// elements first and the next on a tie (the spec's Comparable section), as
// it is Equatable and Hashable structurally. An anonymous struct is not
// Comparable: its fields have no order a reader can see at the use site.
// Every Comparable question the checker asks about a tuple goes through the
// function below: an ordering operator or an interface-typed argument
// (recordConformanceRecording), a `where T: Comparable` bound
// (typeImplementsInterface), and a bound inside a generic impl's receiver
// (`List<#(Int, String)>` against `impl<T> Comparable for List<T> where T:
// Comparable`, typeSatisfiesInterfaceForImplMatch). The IR builder lowers
// the comparison (internal/irbuild/irtuplecompare.go).

// structuralComparable answers whether t is Comparable when t is a tuple, by
// asking part for each element; decided is false for every other type.
func structuralComparable(t Type, part func(Type) bool) (implements, decided bool) {
	tup, ok := resolveTypeVar(t).(*TupleType)
	if !ok {
		return false, false
	}
	for _, p := range tup.Elems {
		if !part(p) {
			return false, true
		}
	}
	return true, true
}
