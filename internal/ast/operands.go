package ast

import (
	"reflect"
	"slices"
)

// Which children of a node are evaluated whenever the node is.
//
// Two components ask it. The checker asks whether a statement always
// leaves its block: it does when an operand it always evaluates does
// (analysis, leaves), as in `close(if c then return 1 else return 2)`. The IR builder lifts such a region out of the construct that would
// read it (irbuild, exitingOperand). Both are right only for a child that
// is always evaluated: an arm or a lambda's body runs only sometimes. So
// every field of every node type that can hold a child (NodeChildFields)
// is given a class in operandFields, and EvaluatedOperands reads nothing
// else. A node type or field added to the AST has no class:
// TestEveryChildFieldHasAnOperandClass fails naming it, and
// EvaluatedOperands panics on it, until someone decides.

// operandClass says how a node evaluates the children in one of its fields.
type operandClass uint8

const (
	// operandNow: evaluated whenever the node is, before anything reads the
	// node's value. Searched for a region that leaves, and lowered for its
	// effects when it stands before one.
	operandNow operandClass = iota + 1
	// operandLater: evaluated only sometimes, or later, or once per case:
	// a body that runs when it is called, an arm, a binding's `else`, a
	// declaration's parts. Never searched. A field whose evaluation is not
	// settled belongs here: an operand left unsearched declines, and one
	// searched wrongly runs when it should not.
	operandLater
	// operandNames: names something and evaluates nothing: a type, a
	// pattern, a field's name, the app field a `with` overrides.
	operandNames
	// operandScoped: a block's statements, which run in the block's scope.
	// A block is read by its own statements, in order, by whoever asks.
	operandScoped
)

// operandRule is a field's class, or for a field whose class depends on
// the node, the function that answers it for one child.
type operandRule struct {
	class operandClass
	of    func(parent, child Node) operandClass
}

var (
	evalNow    = operandRule{class: operandNow}
	evalLater  = operandRule{class: operandLater}
	evalNames  = operandRule{class: operandNames}
	evalScoped = operandRule{class: operandScoped}

	// rightOperand is the right side of a binary: evaluated, except that
	// `and` and `or` evaluate it only for some left values, and a pipe's
	// stage is no expression until the source is spliced into it (the
	// stage call's arguments are the pipe's operands, EvaluatedOperands).
	rightOperand = operandRule{of: func(parent, _ Node) operandClass {
		switch parent.(*Binary).Op {
		case "and", "or", "|>":
			return operandLater
		}
		return operandNow
	}}

	// calleeOperand is a call's callee: a name (`f`, `io.print`,
	// `List.length`, `.Some`) says which function and evaluates nothing;
	// any other expression (`pick(x)(1)`, `(table.at(i))(1)`) is evaluated
	// before the arguments.
	calleeOperand = operandRule{of: func(_, child Node) operandClass {
		if plainName(child) {
			return operandNames
		}
		return operandNow
	}}
)

// plainName reports whether n is a name or a dotted name: nothing is
// evaluated to say what it names.
func plainName(n Node) bool {
	switch v := n.(type) {
	case *Ident, *TypeIdent, *SelfExpr, *DotVariant:
		return true
	case *FieldAccess:
		return v != nil && plainName(v.Object)
	}
	return false
}

// operandFields classifies every field of every AST node type that can
// hold a child, by the Go type's name and the field's.
var operandFields = map[string]map[string]operandRule{
	// Expressions.
	"Ascription":          {"Value": evalNow, "Type": evalNames},
	"Binary":              {"Left": evalNow, "Right": rightOperand},
	"Call":                {"Func": calleeOperand, "Args": evalNow, "TypeArgs": evalNames},
	"Capture":             {"Body": evalLater},
	"Case":                {"Value": evalNow, "Branches": evalLater},
	"ConcurrentBlock":     {"Body": evalLater},
	"Dbg":                 {"Expr": evalNow},
	"FieldAccess":         {"Object": evalNow, "Field": evalNames},
	"FieldAccessor":       {"Path": evalNames},
	"GroupedExpr":         {"Expr": evalNow},
	"If":                  {"Cond": evalNow, "CondPattern": evalNames, "Then": evalLater, "Else": evalLater},
	"Is":                  {"Value": evalNow, "Pattern": evalNames},
	"Lambda":              {"Params": evalLater, "Body": evalLater},
	"ListLit":             {"TypeName": evalNames, "Items": evalNow, "Spreads": evalNow},
	"MapLit":              {"TypeName": evalNames, "Entries": evalNow, "Spreads": evalNow},
	"NamedArg":            {"Value": evalNow},
	"RangeLit":            {"Start": evalNow, "End": evalNow},
	"StringInterp":        {"Parts": evalNow},
	"StructLit":           {"TypeName": evalNames, "Fields": evalNow, "Positional": evalNow, "Spread": evalNow},
	"TaggedString":        {"Parts": evalNow},
	"Tap":                 {"Lambda": evalLater, "Capture": evalLater, "Name": evalNames},
	"Then":                {"Lambda": evalLater, "Capture": evalLater},
	"Todo":                {"Reason": evalNames},
	"TryOp":               {"Expr": evalNow},
	"TupleLit":            {"Items": evalNow},
	"Unary":               {"Right": evalNow},
	"UncheckedRefinement": {"Value": evalNow},
	"Unreachable":         {"Reason": evalNames},
	"Unwrap":              {"Expr": evalNow},
	// What a generated `try_from` holds: the rule is a function, and only
	// one of the two parent checks applies.
	"RefinementRule":        {"Rule": evalLater, "Value": evalNow},
	"RefinementParentCheck": {"Parent": evalNames, "Refined": evalLater, "Plain": evalLater},

	// Statements.
	"Assertion":          {"Expr": evalNow},
	"Binding":            {"TypeAnnotation": evalNames, "Value": evalNow},
	"BindingElse":        {"Block": evalLater, "Arms": evalLater},
	"Block":              {"Stmts": evalScoped},
	"Break":              {"Value": evalNow},
	"Continue":           {"Value": evalNow},
	"Defer":              {"Call": evalNow},
	"ExprStmt":           {"Expr": evalNow},
	"PatternBinding":     {"Pattern": evalNames, "Value": evalNow, "Else": evalLater},
	"PatternDestructure": {"Pattern": evalNames, "Value": evalNow},
	"Return":             {"Value": evalNow},
	"With":               {"Target": evalNames, "Value": evalNow},
	// `subtests` runs its name and body once per row.
	"Subtests":      {"Rows": evalLater, "Pattern": evalNames, "Name": evalLater, "Body": evalLater},
	"GroupSubtests": {"Rows": evalLater, "Pattern": evalNames, "Name": evalLater},

	// Patterns.
	"AsPattern":     {"Pattern": evalNames},
	"EnumPattern":   {"Variant": evalNames, "Payload": evalNames},
	"ListPattern":   {"TypeName": evalNames, "Heads": evalNames, "TailSpread": evalNames, "Tails": evalNames},
	"MapPattern":    {"TypeName": evalNames, "Entries": evalNames},
	"OrPattern":     {"Alternatives": evalNames},
	"StructPattern": {"TypeName": evalNames, "Fields": evalNames},
	"TuplePattern":  {"Patterns": evalNames},

	// Types.
	"AnonStructType":     {"Fields": evalNames},
	"ConjunctionType":    {"Members": evalNames},
	"FuncType":           {"Params": evalNames, "Return": evalNames},
	"GenericType":        {"Params": evalNames},
	"QualifiedType":      {"Member": evalNames},
	"RefinementBaseType": {"Params": evalNames},

	// Declarations, which run nothing where they stand: what they name is
	// operandNames, and a body, a default, a rule or a test is
	// operandLater.
	"AttachedTest": {"Body": evalLater},
	"Decorator":    {"Args": evalLater, "Options": evalLater},
	"EmbedDecl":    {"Body": evalLater},
	"EnumDef": {"AttachedTests": evalLater, "Decorators": evalLater, "TypeParams": evalNames, "WhereClauses": evalNames,
		"Variants": evalLater},
	"ExternFunc": {"AttachedTests": evalLater, "TypeParams": evalNames, "Params": evalLater, "ReturnTypeExpr": evalNames,
		"WhereClauses": evalNames, "ImplIface": evalNames},
	"ExternType": {"TypeParams": evalNames, "WhereClauses": evalNames, "AttachedTests": evalLater, "Decorators": evalLater},
	"FuncDef": {"AttachedTests": evalLater, "Decorators": evalLater, "TypeParams": evalNames, "Params": evalLater,
		"ReturnTypeExpr": evalNames, "WhereClauses": evalNames, "Body": evalLater, "ImplIface": evalNames},
	"ImplBlock": {"AttachedTests": evalLater, "Interface": evalNames, "Receiver": evalNames, "Generics": evalNames,
		"WhereClauses": evalNames, "Items": evalLater},
	"ImplConformance": {"AttachedTests": evalLater, "Interface": evalNames, "Interfaces": evalNames, "Receiver": evalNames,
		"Options": evalLater, "Generics": evalNames, "WhereClauses": evalNames},
	"ImportBlock":  {"Entries": evalNames},
	"ImportStmt":   {"ModulePath": evalNames, "Names": evalNames, "Aliases": evalNames, "ModuleAlias": evalNames},
	"InterfaceDef": {"AttachedTests": evalLater, "TypeParams": evalNames, "WhereClauses": evalNames, "Methods": evalLater},
	"InterfaceMethod": {"TypeParams": evalNames, "Params": evalLater, "ReturnTypeExpr": evalNames, "Body": evalLater,
		"WhereClauses": evalNames},
	"KeywordDecl": {"Params": evalLater, "ReturnTypeExpr": evalNames, "Body": evalLater},
	"OnceBinding": {"AttachedTests": evalLater, "TypeAnnotation": evalNames, "Value": evalLater},
	"StructDef": {"AttachedTests": evalLater, "Decorators": evalLater, "TypeParams": evalNames, "WhereClauses": evalNames,
		"Fields": evalLater, "Rule": evalLater},
	"TestDecl": {"Subtests": evalLater, "Clock": evalLater, "Boot": evalLater, "Setup": evalLater, "ContextPattern": evalNames,
		"Body": evalLater},
	"TypeAlias": {"AttachedTests": evalLater, "TargetTypeExpr": evalNames, "Bounds": evalNames, "TypeParams": evalNames,
		"WhereClauses": evalNames},
	"TypeDef": {"AttachedTests": evalLater, "Decorators": evalLater, "TypeParams": evalNames, "InnerTypeExpr": evalNames,
		"MoreParents": evalNames, "WhereClauses": evalNames, "Rule": evalLater},
}

// EvaluatedOperands is the nodes directly beneath n that are evaluated
// whenever n is (operandNow in operandFields), in the order they are: the
// order written, which is Nomi's, except that a collection literal
// evaluates its spreads before its elements. A named
// argument is its value, and a pipe's operands are its source and then a
// stage call's own operands.
func EvaluatedOperands(n Node) []Node {
	if IsNilNode(n) {
		return nil
	}
	node := reflect.TypeOf(n).Elem().Name()
	fields := operandFields[node]
	var out []Node
	FieldChildren(n, func(field string, c Node) {
		rule, classified := fields[field]
		if !classified {
			panic("ast: " + node + "." + field + " has no operand class; add it to operandFields (operands.go)")
		}
		class := rule.class
		if rule.of != nil {
			class = rule.of(n, c)
		}
		if class != operandNow {
			return
		}
		if arg, isNamed := c.(*NamedArg); isNamed {
			// `f(b: x)`: the argument is its value.
			c = arg.Value
		}
		out = append(out, c)
	})
	out = inEvaluationOrder(n, out)
	if pipe, isPipe := n.(*Binary); isPipe && pipe.Op == "|>" {
		if stage, isCall := pipe.Right.(*Call); isCall {
			out = append(out, EvaluatedOperands(stage)...)
		}
	}
	return out
}

// inEvaluationOrder sorts the operands of n as n evaluates them: as
// written, a collection literal's spreads first.
func inEvaluationOrder(n Node, operands []Node) []Node {
	if len(operands) < 2 {
		return operands
	}
	var spreads []Spread
	switch v := n.(type) {
	case *ListLit:
		spreads = v.Spreads
	case *MapLit:
		spreads = v.Spreads
	}
	type placed struct {
		n         Node
		spread    bool
		line, col int
	}
	at := make([]placed, len(operands))
	for i, c := range operands {
		line, col := Position(c)
		at[i] = placed{n: c, line: line, col: col}
		for _, sp := range spreads {
			at[i].spread = at[i].spread || sp.Head == c
		}
	}
	slices.SortStableFunc(at, func(a, b placed) int {
		if a.spread != b.spread {
			if a.spread {
				return -1
			}
			return 1
		}
		if a.line != b.line {
			return a.line - b.line
		}
		return a.col - b.col
	})
	for i, p := range at {
		operands[i] = p.n
	}
	return operands
}

// IsNilNode reports whether n is nil or a nil pointer in a Node.
func IsNilNode(n Node) bool {
	if n == nil {
		return true
	}
	v := reflect.ValueOf(n)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

// Position is n's line, and its column when its type has a Col field.
func Position(n Node) (line, col int) {
	if n == nil {
		return 0, 0
	}
	line = n.LineNum()
	v := reflect.Indirect(reflect.ValueOf(n))
	if v.Kind() != reflect.Struct {
		return line, 0
	}
	if f := v.FieldByName("Col"); f.IsValid() && f.Kind() == reflect.Int {
		col = int(f.Int())
	}
	return line, col
}
