package ast

import (
	"math"
	"strconv"
	"strings"
)

// A group's `subtests` rows are read, not evaluated, when the tests are
// collected: each row's tests are cases of their own, named with the row, so
// the front end's case list (internal/frontend) and the IR builder's
// (internal/irbuild) both need the rows and their labels before anything
// runs. The rule lives here, once, because the two lists are matched by name
// and two copies could name one case two ways. The analyzer holds the rows to
// GroupRowNonLiteral first.

// GroupRows is the rows of a group's `subtests` block: the items of its list
// literal, or nil when the rows are not one (the analyzer reports that).
func GroupRows(gs *GroupSubtests) []Node {
	if gs == nil {
		return nil
	}
	list, ok := gs.Rows.(*ListLit)
	if !ok || list.TypeName != nil {
		return nil
	}
	return list.Items
}

// GroupRowNonLiteral answers the first part of row that is not a literal
// value, or nil when the whole row is one: a String without interpolation, an
// Int or Float (negated or not), True or False, or a tuple of them.
func GroupRowNonLiteral(row Node) Node {
	switch v := row.(type) {
	case *StringLit, *IntLit, *FloatLit:
		return nil
	case *Unary:
		switch v.Right.(type) {
		case *IntLit, *FloatLit:
			if v.Op == "-" {
				return nil
			}
		}
		return v
	case *TypeIdent:
		if v.Name == "True" || v.Name == "False" {
			return nil
		}
		return v
	case *TupleLit:
		for _, item := range v.Items {
			if bad := GroupRowNonLiteral(item); bad != nil {
				return bad
			}
		}
		return nil
	case *GroupedExpr:
		return GroupRowNonLiteral(v.Expr)
	}
	return row
}

// GroupRowColumn answers the position, among a tuple pattern's elements, of
// the name the row's name reads, -1 when the pattern binds the whole row to
// it, and false when the pattern binds no such name.
func GroupRowColumn(gs *GroupSubtests) (int, bool) {
	name, ok := gs.Name.(*Ident)
	if !ok {
		return 0, false
	}
	switch p := gs.Pattern.(type) {
	case *IdentPattern:
		return -1, p.Name == name.Name
	case *TuplePattern:
		for i, elem := range p.Patterns {
			if ip, isIdent := elem.(*IdentPattern); isIdent && ip.Name == name.Name {
				return i, true
			}
		}
	}
	return 0, false
}

// GroupRowLabels is each row's label, as a case's group segment carries it in
// brackets (`todo api [sqlite] / creates a todo`). A row with a name column
// is labelled by that column's String; a String row is its own label; any
// other row reads as a `values:` row renders it, so a tuple row is
// `("memory", 1)`. A row that is not a literal, which the analyzer rejects,
// is labelled by its position.
func GroupRowLabels(gs *GroupSubtests) []string {
	rows := GroupRows(gs)
	labels := make([]string, len(rows))
	col, named := GroupRowColumn(gs)
	for i, row := range rows {
		if GroupRowNonLiteral(row) != nil {
			labels[i] = "row " + strconv.Itoa(i)
			continue
		}
		cell := unparen(row)
		if named && col >= 0 {
			tuple, ok := cell.(*TupleLit)
			if !ok || col >= len(tuple.Items) {
				labels[i] = "row " + strconv.Itoa(i)
				continue
			}
			cell = unparen(tuple.Items[col])
		}
		if s, ok := cell.(*StringLit); ok {
			labels[i] = s.Value
			continue
		}
		labels[i] = renderRowLiteral(cell)
	}
	return labels
}

// GroupRowSegment is a group's name with a row's label, the segment the
// row's cases are named under.
func GroupRowSegment(group, label string) string { return group + " [" + label + "]" }

func unparen(n Node) Node {
	for {
		g, ok := n.(*GroupedExpr)
		if !ok {
			return n
		}
		n = g.Expr
	}
}

// renderRowLiteral reads a literal row as a `values:` row renders the value.
func renderRowLiteral(n Node) string {
	switch v := unparen(n).(type) {
	case *StringLit:
		return quoteRowString(v.Value)
	case *IntLit:
		if v.Big != nil {
			return v.Big.String()
		}
		return strconv.FormatInt(v.Value, 10)
	case *FloatLit:
		return renderRowFloat(v.Value)
	case *Unary:
		return "-" + renderRowLiteral(v.Right)
	case *TypeIdent:
		return v.Name
	case *TupleLit:
		parts := make([]string, len(v.Items))
		for i, item := range v.Items {
			parts[i] = renderRowLiteral(item)
		}
		return "(" + strings.Join(parts, ", ") + ")"
	}
	return "?"
}

func renderRowFloat(f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

func quoteRowString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
