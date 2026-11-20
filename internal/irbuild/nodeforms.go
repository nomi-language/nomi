package irbuild

import (
	"reflect"

	"github.com/nomi-language/nomi/internal/ast"
)

// Where the builder meets each AST node type.
//
// lowerNode switches over the expression nodes and leadStatement over a
// block's statements: those before its last, and a last one that is no
// value (formsOf), after which the block answers Unit. Both switches are written by hand, and a node
// type neither names would fall out of one: so every node type of
// internal/ast is given its forms here, and TestEveryNodeTypeHasAForm holds
// the table to the AST and each switch to the table. A node type added to
// the AST fails there, by name, until someone decides what it is, and one
// classed a value or a statement fails until its switch has the arm. What
// falls out of either switch is then a node the table says cannot be there,
// and it panics.

// nodeForm is the set of places a node type appears in a checked tree.
type nodeForm uint

const (
	// formValue is an expression: lowerNode has an arm for it.
	formValue nodeForm = 1 << iota
	// formStatement is a statement before a block's last: irScalarBlock has
	// an arm for it.
	formStatement
	// formLeaves is `return`, `break` or `continue`: a block's last
	// statement or an arm's body. The parser admits one nowhere else
	// ("unexpected `return`"), and the checker rejects a statement after
	// one ("unreachable code after return").
	formLeaves
	// formPattern is a pattern, which the pattern tests and the
	// destructures read.
	formPattern
	// formType is a type expression, which typeOf reads.
	formType
	// formDecl is a declaration at file level or in an impl, a group or an
	// interface. The checker rejects one in a body ("an `impl` block must
	// appear at module top-level"), and the parser reads `embed` and
	// `phrase` there as names.
	formDecl
	// formPart is a part of another node, read by that node's lowering: a
	// named argument, a pipe's `tap` or `then` stage, a capture's
	// placeholder, a binding's `else`.
	formPart
	// formUnchecked is a node no checked tree holds: a parse error's.
	formUnchecked
)

// nodeForms gives every node type of internal/ast its forms, by the Go
// type's name.
var nodeForms = map[string]nodeForm{
	"Ascription":            formValue,
	"Assertion":             formValue | formStatement,
	"Binary":                formValue,
	"Block":                 formValue | formStatement,
	"Call":                  formValue,
	"Capture":               formValue,
	"Case":                  formValue,
	"CodepointLit":          formValue,
	"ConcurrentBlock":       formValue,
	"CurrentValue":          formValue,
	"Dbg":                   formValue,
	"DecimalLit":            formValue,
	"DotVariant":            formValue,
	"EmbeddedFiles":         formValue,
	"FieldAccess":           formValue,
	"FieldAccessor":         formValue,
	"FloatLit":              formValue,
	"GroupedExpr":           formValue,
	"Ident":                 formValue,
	"If":                    formValue,
	"IntLit":                formValue,
	"Is":                    formValue,
	"Lambda":                formValue,
	"ListLit":               formValue,
	"MapLit":                formValue,
	"RangeLit":              formValue,
	"RefinementParentCheck": formValue,
	"RefinementRule":        formValue,
	"SelfExpr":              formValue,
	"StringInterp":          formValue,
	"StringLit":             formValue,
	"StructLit":             formValue,
	"TaggedString":          formValue,
	"Todo":                  formValue,
	"TryOp":                 formValue,
	"TupleLit":              formValue,
	"TypeIdent":             formValue,
	"Unary":                 formValue,
	"UncheckedRefinement":   formValue,
	"Unreachable":           formValue,
	"Unwrap":                formValue,
	"With":                  formValue | formStatement,

	"Binding":            formStatement,
	"Defer":              formStatement,
	"ExprStmt":           formStatement,
	"PatternBinding":     formStatement,
	"PatternDestructure": formStatement,
	"Subtests":           formStatement,

	// Declared in a body as at file level.
	"EnumDef":      formStatement | formDecl,
	"FuncDef":      formStatement | formDecl,
	"ImportStmt":   formStatement | formDecl,
	"InterfaceDef": formStatement | formDecl,
	"StructDef":    formStatement | formDecl,
	"TypeAlias":    formStatement | formDecl,
	"TypeDef":      formStatement | formDecl,

	"Break":    formLeaves,
	"Continue": formLeaves,
	"Return":   formLeaves,

	"AsPattern":       formPattern,
	"EnumPattern":     formPattern,
	"IdentPattern":    formPattern,
	"ListPattern":     formPattern,
	"MapPattern":      formPattern,
	"OrPattern":       formPattern,
	"StructPattern":   formPattern,
	"TuplePattern":    formPattern,
	"WildcardPattern": formPattern,

	"AnonStructType":     formType,
	"ConjunctionType":    formType,
	"DotVariantType":     formType,
	"FuncType":           formType,
	"GenericType":        formType,
	"QualifiedType":      formType,
	"RefinementBaseType": formType,
	"SelfType":           formType,
	"SimpleType":         formType,

	"AttachedTest":    formDecl,
	"EmbedDecl":       formDecl,
	"ExternFunc":      formDecl,
	"ExternPackage":   formDecl,
	"ExternType":      formDecl,
	"GroupSubtests":   formDecl,
	"ImplBlock":       formDecl,
	"ImplConformance": formDecl,
	"ImportBlock":     formDecl,
	"InterfaceMethod": formDecl,
	"KeywordDecl":     formDecl,
	"OnceBinding":     formDecl,
	"TestDecl":        formDecl,

	"BindingElse":      formPart,
	"FilePath":         formPart,
	"NamedArg":         formPart,
	"Placeholder":      formPart,
	"PlaceholderParam": formPart,
	"Tap":              formPart,
	"Then":             formPart,

	"Decorator": formUnchecked,
	"ErrorNode": formUnchecked,
}

// formsOf is n's forms, by its Go type's name as nodeForms is keyed.
func formsOf(n ast.Node) nodeForm {
	return nodeForms[reflect.TypeOf(n).Elem().Name()]
}
