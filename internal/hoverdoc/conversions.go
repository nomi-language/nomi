package hoverdoc

import (
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// What a covariant conversion costs at run time (spec §5 *Covariant
// conversion*), which nothing in the source shows: `wide: List<Display> =
// xs` reads the same whether `xs` is relabelled or rebuilt. The hover over
// the converted expression adds one line, read from the checker's record of
// the conversion (analysis.Conversion.Cost), the fact the IR builder obeys
// (internal/irbuild, irrelabel.go's relabelsAt), so the line says what the
// builder does. A free conversion says nothing.

// ConversionNote is the line a hover over pos adds when pos is on the head
// of an expression whose conversion does work at run time (its name, a
// field read's field, a call's callee), or "" when there is none.
func ConversionNote(fa *analysis.FileAnalysis, pos analysis.Pos) string {
	if fa == nil || len(fa.Conversions) == 0 {
		return ""
	}
	var at ast.Node
	var rec *analysis.Conversion
	for n, r := range fa.Conversions {
		if r.Cost == analysis.ConversionFree || !headHolds(n, pos) {
			continue
		}
		// A callee that is itself converted (`f` in `f(x)`, each recorded)
		// answers as the call's head only when it is the narrower node.
		if at == nil || spanSize(spanOf(n)) < spanSize(spanOf(at)) {
			at, rec = n, r
		}
	}
	if rec == nil {
		return ""
	}
	return conversionLine(rec)
}

// headHolds reports whether pos is on the token that heads n: a name, a
// field read's field, or a call's callee, read the same way.
func headHolds(n ast.Node, pos analysis.Pos) bool {
	switch e := n.(type) {
	case *ast.Ident:
		return e.Line == pos.Line && pos.Col >= e.Col && pos.Col < e.Col+len(e.Name)
	case *ast.FieldAccess:
		return e.Field != nil && headHolds(e.Field, pos)
	case *ast.Call:
		return headHolds(e.Func, pos)
	}
	return false
}

// conversionLine is the hover line for a conversion that does work.
func conversionLine(rec *analysis.Conversion) string {
	from, to := displayValueType(rec.From), displayValueType(rec.To)
	if rec.Cost == analysis.ConversionPerInstance {
		// The enclosing body's own type parameters, by name.
		from, to = rec.From.String(), rec.To.String()
	}
	how := rebuildWords(rec.To, "`"+from+"` as "+aType(to))
	if rec.Cost == analysis.ConversionPerInstance {
		return "*converts* with no copy where " + instanceParams(rec) + " is an `Int`, `Float`, `Bool`, `String` or declared type, and otherwise " + how
	}
	return "*converts* " + how
}

// rebuildWords says how a value converted to the type to is built again;
// what names the value and the type it becomes.
func rebuildWords(to analysis.Type, what string) string {
	switch t := analysis.ResolveTypeVar(to).(type) {
	case *analysis.FuncType:
		return "by wrapping the function: each call converts its arguments and result"
	case *analysis.ListType:
		return "by rebuilding the " + what + ", one pass over its elements"
	case *analysis.MapType:
		return "by rebuilding the " + what + ", one pass over its entries, rehashing each key"
	case *analysis.TupleType, *analysis.AnonStructType:
		return "by rebuilding the " + what + " field by field"
	case *analysis.StructType:
		if t.Name == "Set" && strings.HasPrefix(t.Origin, "std/") {
			return "by rebuilding the " + what + ", one pass over its elements, rehashing each one"
		}
		return "by rebuilding the " + what + " field by field"
	case *analysis.EnumType:
		return "by rebuilding the " + what + ", one match"
	case *analysis.DistinctType:
		if t.Name == "Vector" && strings.HasPrefix(t.Origin, "std/") {
			return "by rebuilding the " + what + ", one pass over its elements"
		}
		// A distinct type is rebuilt as its inner value is.
		if t.Inner != nil {
			return rebuildWords(t.Inner, what)
		}
	}
	return "by rebuilding the " + what
}

// instanceParams names the type parameters whose instances decide a
// conversion's cost: "`T`", "`K` or `V`".
func instanceParams(rec *analysis.Conversion) string {
	seen := map[string]bool{}
	var names []string
	for _, b := range rec.Boxes {
		if p, ok := analysis.ResolveTypeVar(b.Value).(*analysis.TypeParam_); ok && !seen[p.Name_] {
			seen[p.Name_] = true
			names = append(names, "`"+p.Name_+"`")
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "each type parameter"
	}
	if len(names) == 1 {
		return "each " + names[0]
	}
	return "each of " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
