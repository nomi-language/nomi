package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Pattern coverage by constructor matrix (Maranget's usefulness check).
//
// A set of patterns over a type is exhaustive when no value escapes every
// pattern. The check works on a matrix: one row per pattern, one column per
// component still to be examined. At each step the first column's type
// decides how values split into constructors:
//
//   - An enum splits into its variants; a variant's payload (or its fields,
//     as an anonymous struct) becomes a new column.
//   - A tuple, struct or distinct type has one constructor whose components
//     are its elements, fields or wrapped value.
//   - A list splits by length: lengths 0 through M-1 exactly, and M or more,
//     where M is the longest length any pattern in the column tells apart.
//   - Every other type (Int, String, Codepoint, Map, ...) has no finite
//     constructor set, so a literal, a string prefix or a map pattern never
//     covers it: only rows with a catch-all there go on.
//
// When the column's patterns name every constructor, each constructor is
// checked on the rows that match it. Otherwise only the catch-all rows can
// cover the constructors nobody named. A pattern that does not fit its
// column's type is an error reported by checkPattern; here it counts as a
// catch-all so one mistake does not also report a coverage error.

// patCtor is one constructor of a column's type: what a pattern must name
// to match it, and the types of the components a match exposes.
type patCtor struct {
	// variant is the enum variant or distinct type name; listLen is a list
	// length class, with listOpen meaning "this length or more".
	variant  string
	listLen  int
	listOpen bool
	subTypes []Type
	render   func(subs []string) string
}

// uncoveredValue returns a value of the given column types that no row
// matches, rendered one string per column, and whether there is one.
func (c *checker) uncoveredValue(tys []Type, rows [][]ast.Node) ([]string, bool) {
	if len(tys) == 0 {
		return []string{}, len(rows) == 0
	}
	t := resolveTypeVar(tys[0])
	// An `as` name never changes what its pattern matches.
	rows = withoutAsColumn(rows)
	column := make([]ast.Node, len(rows))
	for i, r := range rows {
		column[i] = r[0]
	}
	ctors := c.patternCtors(t, column)
	if len(ctors) > 0 {
		used := make([]bool, len(ctors))
		anyUsed := false
		for i, ctor := range ctors {
			for _, p := range column {
				if isCatchAllPattern(p) {
					continue
				}
				if _, ok := c.specializePattern(t, ctor, p); ok {
					used[i] = true
					anyUsed = true
					break
				}
			}
		}
		allUsed := anyUsed
		for _, u := range used {
			allUsed = allUsed && u
		}
		if allUsed {
			for _, ctor := range ctors {
				var sub [][]ast.Node
				for _, r := range rows {
					args, ok := c.specializePattern(t, ctor, r[0])
					if !ok {
						continue
					}
					sub = append(sub, append(args, r[1:]...))
				}
				w, missing := c.uncoveredValue(append(append([]Type{}, ctor.subTypes...), tys[1:]...), sub)
				if missing {
					n := len(ctor.subTypes)
					return append([]string{ctor.render(w[:n])}, w[n:]...), true
				}
			}
			return nil, false
		}
		var def [][]ast.Node
		for _, r := range rows {
			if c.coversColumn(t, ctors, r[0]) {
				def = append(def, r[1:])
			}
		}
		w, missing := c.uncoveredValue(tys[1:], def)
		if !missing {
			return nil, false
		}
		head := "_"
		if anyUsed {
			for i, ctor := range ctors {
				if !used[i] {
					wild := make([]string, len(ctor.subTypes))
					for j := range wild {
						wild[j] = "_"
					}
					head = ctor.render(wild)
					break
				}
			}
		}
		return append([]string{head}, w...), true
	}
	var def [][]ast.Node
	for _, r := range rows {
		if !isUnenumerableConstraint(t, r[0]) {
			def = append(def, r[1:])
		}
	}
	w, missing := c.uncoveredValue(tys[1:], def)
	if !missing {
		return nil, false
	}
	return append([]string{"_"}, w...), true
}

// withoutAsColumn returns rows with each first pattern seen through its
// `as` names (ast.WithoutAs), copying only the rows that change.
func withoutAsColumn(rows [][]ast.Node) [][]ast.Node {
	var out [][]ast.Node
	for i, r := range rows {
		inner := ast.WithoutAs(r[0])
		if inner == r[0] {
			if out != nil {
				out[i] = r
			}
			continue
		}
		if out == nil {
			out = make([][]ast.Node, len(rows))
			copy(out, rows[:i])
		}
		out[i] = append([]ast.Node{inner}, r[1:]...)
	}
	if out == nil {
		return rows
	}
	return out
}

// coversColumn reports whether p, in a column whose type has the constructors
// ctors, matches every value: a catch-all, or a pattern that fits none of the
// constructors (a type error checkPattern reports).
func (c *checker) coversColumn(t Type, ctors []patCtor, p ast.Node) bool {
	if isCatchAllPattern(p) {
		return true
	}
	for _, ctor := range ctors {
		if _, ok := c.specializePattern(t, ctor, p); ok {
			return false
		}
	}
	return true
}

// isCatchAllPattern reports whether p matches every value at any type.
func isCatchAllPattern(p ast.Node) bool {
	switch ast.WithoutAs(p).(type) {
	case nil, *ast.WildcardPattern, *ast.IdentPattern, *ast.Placeholder:
		return true
	}
	return false
}

// isUnenumerableConstraint reports whether p tests a value of a type with no
// finite constructor set: a literal, a string prefix, or a map pattern (which
// fails when a key it names is absent).
func isUnenumerableConstraint(t Type, p ast.Node) bool {
	switch v := p.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit, *ast.StringLit, *ast.CodepointLit, *ast.Binary:
		return true
	case *ast.MapPattern:
		_, isMap := t.(*MapType)
		return isMap && v.TypeName == nil
	}
	return false
}

// patternCtors returns the constructors of t, or nil when t has no finite
// set of them (or is not known). column is the patterns over t, which fix
// how many list lengths need telling apart.
func (c *checker) patternCtors(t Type, column []ast.Node) []patCtor {
	switch tt := t.(type) {
	case *EnumType:
		if len(tt.Variants) == 0 {
			return nil
		}
		subs := enumTypeArgSubs(tt)
		ctors := make([]patCtor, 0, len(tt.Variants))
		for _, v := range tt.Variants {
			name := v.Name
			payload := variantPayloadType(v, subs)
			ctor := patCtor{variant: name}
			switch {
			case payload == nil:
				ctor.render = func([]string) string { return name }
			case IsZeroSized(payload):
				// `True`, or an `embeds` of a zero-sized type: written bare.
				ctor.subTypes = []Type{payload}
				ctor.render = func([]string) string { return name }
			case v.DataType == nil:
				ctor.subTypes = []Type{payload}
				ctor.render = func(s []string) string {
					if s[0] == "_" {
						return name + "{..}"
					}
					return name + s[0]
				}
			default:
				ctor.subTypes = []Type{payload}
				ctor.render = func(s []string) string { return name + "(" + s[0] + ")" }
			}
			ctors = append(ctors, ctor)
		}
		return ctors
	case *DistinctType:
		name := tt.Name
		ctor := patCtor{variant: name}
		if tt.Inner == nil {
			ctor.render = func([]string) string { return name }
		} else {
			ctor.subTypes = []Type{tt.Inner}
			ctor.render = func(s []string) string {
				if s[0] == "_" {
					return "_"
				}
				return name + "(" + s[0] + ")"
			}
		}
		return []patCtor{ctor}
	case *TupleType:
		return []patCtor{{
			subTypes: tt.Elems,
			render: func(s []string) string {
				if allWild(s) {
					return "_"
				}
				return "(" + strings.Join(s, ", ") + ")"
			},
		}}
	case *StructType:
		var subs map[*TypeParam_]Type
		if len(tt.TypeParamDefs) > 0 && len(tt.TypeArgs) == len(tt.TypeParamDefs) {
			subs = make(map[*TypeParam_]Type, len(tt.TypeParamDefs))
			for i, def := range tt.TypeParamDefs {
				subs[def] = tt.TypeArgs[i]
			}
		}
		return []patCtor{structCtor(tt.Name, tt.Fields, subs)}
	case *AnonStructType:
		return []patCtor{structCtor("", tt.Fields, nil)}
	case *ListType:
		longest := -1
		for _, p := range column {
			lp, ok := p.(*ast.ListPattern)
			if !ok || lp.TypeName != nil {
				continue
			}
			n := len(lp.Heads)
			if lp.TailSpread == nil {
				n++
			}
			if n > longest {
				longest = n
			}
		}
		if longest < 0 {
			return nil
		}
		ctors := make([]patCtor, 0, longest+1)
		for n := 0; n <= longest; n++ {
			elems := make([]Type, n)
			for i := range elems {
				elems[i] = tt.Elem
			}
			open := n == longest
			ctors = append(ctors, patCtor{
				listLen:  n,
				listOpen: open,
				subTypes: elems,
				render: func(s []string) string {
					parts := append([]string{}, s...)
					if open {
						parts = append(parts, "..")
					}
					return "[" + strings.Join(parts, ", ") + "]"
				},
			})
		}
		return ctors
	}
	return nil
}

// structCtor is the one constructor of a struct type with the given fields.
func structCtor(name string, fields []FieldDef, subs map[*TypeParam_]Type) patCtor {
	types := make([]Type, len(fields))
	for i, f := range fields {
		types[i] = f.Type
		if subs != nil && f.Type != nil {
			types[i] = Substitute(f.Type, subs)
		}
	}
	return patCtor{
		subTypes: types,
		render: func(s []string) string {
			if allWild(s) {
				return "_"
			}
			var parts []string
			for i, w := range s {
				if w != "_" {
					parts = append(parts, fields[i].Name+": "+w)
				}
			}
			if len(parts) < len(fields) {
				parts = append(parts, "..")
			}
			return name + "{" + strings.Join(parts, ", ") + "}"
		},
	}
}

func allWild(s []string) bool {
	for _, w := range s {
		if w != "_" {
			return false
		}
	}
	return true
}

// variantPayloadType is the type a pattern on variant v matches its payload
// against, with the enum's type arguments substituted: the declared payload,
// an anonymous struct of a struct variant's fields, or nil for a bare variant.
func variantPayloadType(v VariantDef, subs map[*TypeParam_]Type) Type {
	switch {
	case v.DataType != nil:
		if subs != nil {
			return Substitute(v.DataType, subs)
		}
		return v.DataType
	case len(v.Fields) > 0:
		fields := make([]FieldDef, len(v.Fields))
		for i, f := range v.Fields {
			fields[i] = f
			if subs != nil && f.Type != nil {
				fields[i].Type = Substitute(f.Type, subs)
			}
		}
		return &AnonStructType{Fields: fields}
	}
	return nil
}

// specializePattern returns the component patterns p matches ctor's
// components against, when p matches values built by ctor. A catch-all
// matches every constructor with catch-all components.
func (c *checker) specializePattern(t Type, ctor patCtor, p ast.Node) ([]ast.Node, bool) {
	if isCatchAllPattern(p) {
		return make([]ast.Node, len(ctor.subTypes)), true
	}
	switch tt := t.(type) {
	case *EnumType:
		payload, ok := c.variantPatternPayload(p, ctor.variant)
		if !ok {
			return nil, false
		}
		if len(ctor.subTypes) == 0 {
			return nil, true
		}
		return []ast.Node{payload}, true
	case *DistinctType:
		var inner ast.Node
		switch v := p.(type) {
		case *ast.EnumPattern:
			if !distinctTypeNameMatches(c.variantNameOfPattern(v), tt.Name) &&
				!distinctTypeNameMatches(v.Variant.TypeString(), tt.Name) {
				return nil, false
			}
			inner = payloadOfVariantPattern(v)
		case *ast.ListPattern:
			stripped := *v
			stripped.TypeName = nil
			inner = &stripped
		case *ast.MapPattern:
			stripped := *v
			stripped.TypeName = nil
			inner = &stripped
		default:
			return nil, false
		}
		if len(ctor.subTypes) == 0 {
			return nil, true
		}
		return []ast.Node{inner}, true
	case *TupleType:
		tp, ok := p.(*ast.TuplePattern)
		if !ok || len(tp.Patterns) != len(tt.Elems) {
			return nil, false
		}
		return append([]ast.Node{}, tp.Patterns...), true
	case *StructType:
		return structPatternFields(p, tt.Fields)
	case *AnonStructType:
		return structPatternFields(p, tt.Fields)
	case *ListType:
		lp, ok := p.(*ast.ListPattern)
		if !ok || lp.TypeName != nil {
			return nil, false
		}
		heads := len(lp.Heads)
		if lp.TailSpread == nil {
			// An exact-length pattern matches its own length only.
			if heads != ctor.listLen || ctor.listOpen {
				return nil, false
			}
		} else if heads > ctor.listLen {
			return nil, false
		}
		args := make([]ast.Node, ctor.listLen)
		copy(args, lp.Heads)
		return args, true
	}
	return nil, false
}

// structPatternFields lines a struct pattern's field patterns up with the
// declared fields; a field the pattern leaves out, or binds by name, is a
// catch-all.
func structPatternFields(p ast.Node, fields []FieldDef) ([]ast.Node, bool) {
	sp, ok := p.(*ast.StructPattern)
	if !ok {
		return nil, false
	}
	args := make([]ast.Node, len(fields))
	for _, f := range sp.Fields {
		for i, fd := range fields {
			if fd.Name == f.Name {
				args[i] = f.Pattern
				break
			}
		}
	}
	return args, true
}
