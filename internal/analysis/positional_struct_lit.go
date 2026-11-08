package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/parser"
)

// Positional struct literal elements.
//
// `Student{&1.first, &1.second}` gives a struct's values with no field
// names. Nomi never builds a struct by position: reordering a struct's
// fields would then silently swap two values of one type. The parser keeps
// such values (ast.StructLit.Positional) so the checker, which knows the
// fields, can write the literal the way Nomi spells it.

// checkPositionalStructLit reports lit, a literal of a type whose fields
// are fields, and checks every value it holds. It returns result, the type
// the literal names, so the error does not spread to what uses it.
func (c *checker) checkPositionalStructLit(lit *ast.StructLit, fields []FieldDef, result Type) Type {
	c.reportPositionalStructLit(lit, fields)
	for _, f := range lit.Fields {
		c.checkNode(f.Value)
	}
	for _, f := range lit.Positional {
		c.checkNode(f.Value)
	}
	return result
}

// reportPositionalStructLit is the error for lit's values without names,
// once per literal. When the values are all positional, their count fits
// the fields (every field without a default given, in declaration order)
// and each was written on one line, its help writes the literal with each
// value under its field's name, and its fix inserts the names. Otherwise it
// says how many values the literal gives and names the fields. fields is
// nil when the literal's type is unknown.
func (c *checker) reportPositionalStructLit(lit *ast.StructLit, fields []FieldDef) {
	if c.positionalReported == nil {
		c.positionalReported = map[*ast.StructLit]bool{}
	}
	if c.positionalReported[lit] {
		return
	}
	c.positionalReported[lit] = true
	name := typeExprText(lit.TypeName)
	e := TypeError{Line: lit.Line, Col: lit.Col}
	if fields == nil {
		e.Message = fmt.Sprintf("a struct literal names its fields; write each value as `field: value` in `%s{...}`", name)
		c.report(e)
		return
	}
	if named, ok := positionalNamedForm(lit, fields); ok {
		e.Message = fmt.Sprintf("a struct literal names its fields; write `%s`", named)
		e.Fix = positionalFix(lit, fields)
		c.report(e)
		return
	}
	given := len(lit.Fields) + len(lit.Positional)
	list := make([]string, len(fields))
	for i, f := range fields {
		list[i] = "`" + f.Name + "`"
		if f.HasDefault {
			list[i] += " (with a default)"
		}
	}
	gives := fmt.Sprintf("%d value%s without names", given, plural(given))
	if len(lit.Fields) > 0 {
		gives = fmt.Sprintf("%d value%s, %d without a name", given, plural(given), len(lit.Positional))
	}
	e.Message = fmt.Sprintf("a struct literal names its fields; `%s` has %s, and this literal gives %s",
		name, countedFields(len(fields)), gives)
	e = e.WithHint(fmt.Sprintf("its fields are %s; write each value as `field: value`", joinAnd(list)))
	c.report(e)
}

// positionalNamedForm is lit written with names, when its values map onto
// fields in declaration order: every value positional, written on one
// line, no more of them than fields, and every field past them defaulted.
func positionalNamedForm(lit *ast.StructLit, fields []FieldDef) (string, bool) {
	if len(lit.Fields) > 0 || len(lit.Positional) > len(fields) {
		return "", false
	}
	for _, f := range fields[len(lit.Positional):] {
		if !f.HasDefault {
			return "", false
		}
	}
	parts := make([]string, len(lit.Positional))
	for i, p := range lit.Positional {
		if p.Source == "" {
			return "", false
		}
		parts[i] = fields[i].Name + ": " + p.Source
	}
	return typeExprText(lit.TypeName) + "{" + strings.Join(parts, ", ") + "}", true
}

// positionalFix inserts each field's name before the value positionalNamedForm
// gives it.
func positionalFix(lit *ast.StructLit, fields []FieldDef) *parser.Fix {
	fix := &parser.Fix{Title: "Name the fields"}
	for i, p := range lit.Positional {
		at := parser.Pos{Line: p.Line, Col: p.Col}
		fix.Edits = append(fix.Edits, parser.Edit{Start: at, End: at, Text: fields[i].Name + ": "})
	}
	return fix
}

func countedFields(n int) string {
	return fmt.Sprintf("%d field%s", n, plural(n))
}
