package hoverdoc

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Hover on an operator: what it does at that site. An arithmetic operator
// shows its operand and result types, what the operator does on them (Int
// is exact at any size, Decimal has no division) and the impl the
// checker chose (FileAnalysis.OperatorImpls), named with its file. `==` and
// `!=` say whether equality is structural or an `Equatable` impl's, the
// ordering operators name the `Comparable` impl, `and` and `or` say when
// their right side runs, a range's `..` or `..=` the range it builds, and
// a unary `-` or `!` its type.
//
// Every type shown is one the checker recorded. An operand with no type
// shows as `…`, and an operator neither of whose operands has a type shows
// nothing.

// operator claims the operator token of b.
func (s *shapeSearch) operator(b *ast.Binary) {
	if b.Line <= 0 || b.Col <= 0 || operatorWords[b.Op] == "" {
		return
	}
	s.claim(b.Line, b.Col, len(b.Op), nodeSize(b), func() string { return s.renderOperator(b) })
}

// operatorWords are the binary operators that have a hover, by the kind of
// hover they get.
var operatorWords = map[string]string{
	"+": "arithmetic", "-": "arithmetic", "*": "arithmetic", "/": "arithmetic", "%": "arithmetic",
	"==": "equality", "!=": "equality",
	"<": "ordering", "<=": "ordering", ">": "ordering", ">=": "ordering",
	"and": "logical", "or": "logical",
}

// nodeSize is a node's extent for claim, or a large one when the parser
// recorded none.
func nodeSize(n ast.Node) int {
	if size := spanSize(spanOf(n)); size > 0 {
		return size
	}
	return 1 << 30
}

// unary claims the `-` or `!` of u.
func (s *shapeSearch) unary(u *ast.Unary) {
	if u.Line <= 0 || u.Col <= 0 || (u.Op != "-" && u.Op != "!") {
		return
	}
	s.claim(u.Line, u.Col, 1, nodeSize(u), func() string { return s.renderUnary(u) })
}

// rangeOp claims the `..` or `..=` of r.
func (s *shapeSearch) rangeOp(r *ast.RangeLit) {
	if r.Line <= 0 || r.Col <= 0 {
		return
	}
	n := 2
	if r.Inclusive {
		n = 3
	}
	s.claim(r.Line, r.Col, n, nodeSize(r), func() string { return s.renderRange(r) })
}

func (s *shapeSearch) renderRange(r *ast.RangeLit) string {
	ty := s.pipeType(s.fa.ExprTypes[r])
	if ty == "" {
		return ""
	}
	if r.Inclusive {
		return fence(ty) + "\n\nInclusive: from the start through the end."
	}
	return fence(ty) + "\n\nExclusive: from the start up to, not including, the end."
}

func (s *shapeSearch) renderUnary(u *ast.Unary) string {
	operand := analysis.ResolveTypeVar(s.fa.ExprTypes[u.Right])
	spelled := s.pipeType(operand)
	if spelled == "" {
		return ""
	}
	if u.Op == "!" {
		return "Not: `" + spelled + "` → `Bool`"
	}
	out := "Negation: `" + spelled + "` → `" + spelled + "`"
	if operand == analysis.TypeInt {
		out += "\n\nExact at any size."
	}
	return out
}

func (s *shapeSearch) renderOperator(b *ast.Binary) string {
	leftType := analysis.ResolveTypeVar(s.fa.ExprTypes[b.Left])
	left, right := s.pipeType(leftType), s.pipeType(s.fa.ExprTypes[b.Right])
	if left == "" && right == "" {
		return ""
	}
	head := operandText(left) + " " + b.Op + " " + operandText(right)
	if left != "" && right != "" {
		if out := s.pipeType(s.fa.ExprTypes[b]); out != "" {
			head += " → `" + out + "`"
		}
	}
	lines := []string{head}
	add := func(line string) {
		if line != "" {
			lines = append(lines, line)
		}
	}
	impl := s.fa.OperatorImpls[b]
	switch operatorWords[b.Op] {
	case "arithmetic":
		add(arithmeticNote(b.Op, leftType))
		if impl != nil {
			add(s.implLine(impl))
		} else {
			add(s.boundLine(b, leftType))
		}
	case "equality":
		add(s.equalityNote(leftType, impl))
	case "ordering":
		if impl != nil {
			add(s.implLine(impl))
		} else {
			add(s.boundLine(b, leftType))
		}
		if leftType == analysis.TypeFloat {
			add("IEEE: `nan <= nan` is `False`.")
		}
	case "logical":
		if b.Op == "and" {
			add("Short-circuits: the right side runs only when the left is `True`.")
		} else {
			add("Short-circuits: the right side runs only when the left is `False`.")
		}
	}
	return strings.Join(lines, "\n\n")
}

// operandText is an operand's type in the summary line, or `…` for one the
// checker gave none.
func operandText(t string) string {
	if t == "" {
		return "…"
	}
	return "`" + t + "`"
}

// arithmeticNote is what an arithmetic operator does on a scalar or a
// built-in collection left operand t.
func arithmeticNote(op string, t analysis.Type) string {
	switch t {
	case analysis.TypeInt:
		switch op {
		case "/":
			return "Truncates toward zero; traps on division by zero."
		case "%":
			return "Remainder: takes the dividend's sign; `Int.modulo` takes the divisor's. Traps on division by zero."
		}
		return "Exact at any size: never overflows."
	case analysis.TypeFloat:
		if op == "%" {
			return "Remainder: takes the dividend's sign."
		}
		return "IEEE 754: never traps."
	case analysis.TypeDecimal:
		if op == "/" {
			return "Decimal has no `/`; use `Decimal.divide` to round or `Decimal.divide_exact`."
		}
		return "Exact: never rounds."
	case analysis.TypeString:
		if op == "+" {
			return "Concatenates."
		}
	}
	if _, ok := t.(*analysis.ListType); ok && op == "+" {
		return "Concatenates."
	}
	return ""
}

// operatorBounds are the interfaces through which an operator on a type
// parameter dispatches.
var operatorBounds = map[string]string{
	"+": "Add", "-": "Subtract", "*": "Multiply", "/": "Divide",
	"<": "Comparable", "<=": "Comparable", ">": "Comparable", ">=": "Comparable",
}

// boundLine names the bound through which the operator b on the type
// parameter t dispatches, as the function around b writes it: "Through
// the bound `T: Add<T, T>`.".
func (s *shapeSearch) boundLine(b *ast.Binary, t analysis.Type) string {
	tp, ok := t.(*analysis.TypeParam_)
	if !ok {
		return ""
	}
	want := operatorBounds[b.Op]
	var fn *ast.FuncDef
	for _, n := range s.nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDef)
			if !ok {
				return true
			}
			sp := spanOf(fd)
			before := sp.StartLine < b.Line || sp.StartLine == b.Line && sp.StartCol <= b.Col
			after := sp.EndLine > b.Line || sp.EndLine == b.Line && sp.EndCol > b.Col
			if before && after {
				fn = fd // the innermost function holding b is found last
			}
			return true
		})
	}
	if fn == nil {
		return ""
	}
	find := func(name string, bounds []ast.TypeExpr) string {
		if name != tp.Name_ {
			return ""
		}
		for _, bound := range bounds {
			if bound != nil && analysis.TypeExprBaseName(bound) == want {
				return "Through the bound `" + name + ": " + bound.TypeString() + "`."
			}
		}
		return ""
	}
	for _, p := range fn.TypeParams {
		if line := find(p.Name, p.Bounds); line != "" {
			return line
		}
	}
	for _, w := range fn.WhereClauses {
		if line := find(w.Name, w.Bounds); line != "" {
			return line
		}
	}
	return ""
}

// equalityNote says how `==` compares values of type t: through a
// hand-written `Equatable` impl, which it names, or structurally.
func (s *shapeSearch) equalityNote(t analysis.Type, impl *analysis.OperatorImpl) string {
	switch t {
	case analysis.TypeFloat:
		return "Compares the values; `nan == nan` is `True`."
	case analysis.TypeDecimal:
		return "Compares the values, ignoring scale: `1.50d == 1.5d`."
	}
	if _, ok := t.(*analysis.PrimitiveType); ok {
		return "Compares the values."
	}
	if impl != nil && !impl.Derived() {
		return "Uses " + s.implLine(impl)
	}
	parts := structuralParts(t)
	if parts == "" {
		return ""
	}
	if impl != nil {
		return "Derived `Equatable`: compares " + parts + "."
	}
	return "Structural: compares " + parts + "."
}

// structuralParts says what structural equality compares in a value of
// type t.
func structuralParts(t analysis.Type) string {
	switch v := t.(type) {
	case *analysis.EnumType:
		return "the variant and its payload"
	case *analysis.AnonStructType:
		return "field by field"
	case *analysis.StructType:
		switch v.Name {
		case "Vector", "Set", "Map", "Range":
			return "element by element"
		}
		return "field by field"
	case *analysis.ListType, *analysis.MapType, *analysis.TupleType:
		return "element by element"
	case *analysis.DistinctType:
		return "the value it wraps"
	}
	return ""
}

// implLine names the impl an operator uses and its file: "`impl Add<Days,
// Date> for Date` in `std/calendar`.", or the derive that made it.
func (s *shapeSearch) implLine(impl *analysis.OperatorImpl) string {
	receiver := s.pipeType(impl.Receiver)
	if receiver == "" {
		return ""
	}
	if impl.Derived() {
		line := "Derived: `derive " + impl.Interface + " for " + receiver + "`"
		if _, ok := analysis.ResolveTypeVar(impl.Receiver).(*analysis.StructType); ok && impl.Interface == "Comparable" {
			line += ", which compares field by field in declaration order"
		}
		return line + "."
	}
	header := impl.Interface
	if len(impl.Args) > 0 {
		args := make([]string, len(impl.Args))
		for i, a := range impl.Args {
			if args[i] = s.pipeType(a); args[i] == "" {
				args[i] = "_"
			}
		}
		header += "<" + strings.Join(args, ", ") + ">"
	}
	line := "`impl " + header + " for " + receiver + "`"
	if file := s.implFile(impl.Method); file != "" {
		line += " in " + file
	}
	return line + "."
}

// implFile names the file of an impl's function: "this file", "`std/int`",
// "`money.nomi`", or "" when the program does not know it.
func (s *shapeSearch) implFile(m ast.ImplMethodDecl) string {
	for _, n := range s.nodes {
		if blk, ok := n.(*ast.ImplBlock); ok {
			for _, item := range blk.Items {
				if item == m {
					return "this file"
				}
			}
		}
	}
	key, ok := s.fa.Program().ImplMethodFile(m)
	switch {
	case !ok:
		return ""
	case key == "":
		return "the entry file"
	case strings.HasPrefix(key, "std/"):
		return "`" + key + "`"
	}
	return "`" + key + ".nomi`"
}

// OperatorImplAt is the impl function the operator at pos uses: the `fn
// add` of the impl the checker chose, a std scalar's `host fn`, or the
// `compare` or `equal` of a hand-written impl. It is nil anywhere else,
// and for a derived impl, which has no function in the source.
func OperatorImplAt(fa *analysis.FileAnalysis, nodes []ast.Node, pos analysis.Pos) ast.ImplMethodDecl {
	if fa == nil || len(fa.OperatorImpls) == 0 {
		return nil
	}
	var found ast.ImplMethodDecl
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			b, ok := n.(*ast.Binary)
			if !ok || found != nil {
				return found == nil
			}
			if b.Line == pos.Line && pos.Col >= b.Col && pos.Col < b.Col+len(b.Op) {
				if impl := fa.OperatorImpls[b]; impl != nil && !impl.Derived() {
					found = impl.Method
				}
			}
			return true
		})
	}
	return found
}
