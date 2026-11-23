package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
)

// An instance of a generic function is keyed and named by its arguments'
// kinds, and a refinement's kind is its base's, so the instance at a refinement is
// its base's: `Iter.map` over `List<NonNegativeInt>` is the instance over
// `List<Int>`. One whose type parameter is bounded by an interface that
// makes a value of its type and stands for a type that holds a refinement
// (analysis.ReadsRefinement) makes values of that refinement, which its base's
// instance does not check, so it is an instance of its own. Its reads are,
// per type parameter, where its argument holds the refinements it reads
// (analysis.RefinementShape), "" for a parameter that reads none. The reads
// are part of the instance's intern key (refinementReadsKey) and of its
// linkage name (writeInstArgs), so `Json.decode_as<{port: Port}>` and
// `Json.decode_as<{port: Int}>` are two functions, and every instance that
// reads no refinement keeps the key and the name its kinds give it.
//
// Every place that interns an instance of a function reads its reads here:
// a std generic function's, impl function's or interface default's
// (stdInstances.instantiateAt), a program generic function's
// (resolveMonoInstance), a generic impl function's own type parameters
// (methodInstance) and a generic nested fn's (nestedInstKey). An instance a
// path asks for with no checker types reads nothing; the paths that pass
// none ask for a function with no type parameters (a monomorphic std body
// built per program). A generic type's instance is one per kind, its impl
// functions with it, so the checker refuses a refinement where a `where` of
// such an impl would read one (analysis.ReadsRefinement).

// refinementReads is the reads of an instance whose type parameters names
// stand for the checker types types (nil where the path has none), bounded
// as bounds answers. bounds is asked only when an argument holds a
// refinement. Nil when no parameter reads one.
func refinementReads(names []string, types []analysis.Type, bounds func() map[string][]*analysis.InterfaceType) []string {
	var out []string
	var bs map[string][]*analysis.InterfaceType
	for i, name := range names {
		if i >= len(types) || !analysis.HoldsRefinement(types[i]) {
			continue
		}
		if bs == nil {
			bs = bounds()
		}
		if !analysis.ReadsRefinement(bs[name], types[i]) {
			continue
		}
		if out == nil {
			out = make([]string, len(names))
		}
		out[i] = analysis.RefinementShape(types[i])
	}
	return out
}

// refinementReadsKey is the part of an instance's intern key its reads make:
// "" for an instance that reads no refinement.
func refinementReadsKey(reads []string) string {
	if reads == nil {
		return ""
	}
	return "\x00reads " + strings.Join(reads, "\x00")
}

// writeInstArgs spells an instance's type arguments for its linkage name:
// each kind (writePortableKind), and after an argument whose parameter
// reads a refinement, `!` and where it holds them.
func writeInstArgs(b *strings.Builder, args []kind, reads []string) {
	for i, a := range args {
		if i > 0 {
			b.WriteByte(',')
		}
		writePortableKind(b, a)
		if i < len(reads) && reads[i] != "" {
			b.WriteString("!" + reads[i])
		}
	}
}

// instArgsNomi is an instance's type arguments as Nomi writes them, for
// its display name: an argument whose parameter reads a refinement is its
// checker type, and any other its kind.
func instArgsNomi(args []kind, types []analysis.Type, reads []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.nomi()
		if i < len(reads) && reads[i] != "" && i < len(types) && types[i] != nil {
			parts[i] = analysis.Elaborate(types[i]).String()
		}
	}
	return strings.Join(parts, ", ")
}
