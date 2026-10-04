package ast

import (
	"strings"
	"unicode"
)

// IsPublic returns true if the name starts with an uppercase letter (Go-style visibility).
func IsPublic(name string) bool {
	if len(name) == 0 {
		return false
	}
	return unicode.IsUpper(rune(name[0]))
}

// IsDiscardName reports whether a binding-position name intentionally discards
// its value instead of entering scope. `_` is the shortest form; `_name` is a
// human-readable discard label. Double-underscore names are reserved for
// runtime/compiler internals and remain ordinary bindings.
func IsDiscardName(name string) bool {
	return name == "_" || (len(name) > 1 && name[0] == '_' && name[1] != '_')
}

// Node is the interface implemented by all AST nodes.
type Node interface {
	NodeType() string
	LineNum() int
}

// TriviaKind distinguishes comment trivia from blank-line trivia.
type TriviaKind int

const (
	TriviaComment TriviaKind = iota
	TriviaBlankLine
)

// Trivia is a non-semantic token (comment or blank line) preserved
// for source-faithful formatting. Position fields match the source token.
type Trivia struct {
	Kind TriviaKind
	Text string // raw lexeme, e.g. "// a note". Empty for BlankLine.
	Line int
	Col  int
}

// TriviaCarrier is embedded in every AST node to provide uniform trivia access.
type TriviaCarrier struct {
	Leading  []Trivia
	Trailing []Trivia
}

func (t *TriviaCarrier) GetLeading() []Trivia  { return t.Leading }
func (t *TriviaCarrier) GetTrailing() []Trivia { return t.Trailing }
func (t *TriviaCarrier) AddLeading(x Trivia)   { t.Leading = append(t.Leading, x) }
func (t *TriviaCarrier) AddTrailing(x Trivia)  { t.Trailing = append(t.Trailing, x) }

// Span is a node's source extent: its first token through one past its
// last, as 1-based lines and byte columns. A leading doc comment, `//!`
// attached test or trailing comment lies outside it. An expression's span
// starts at its first token even when its Line/Col name an operator (a
// Binary's operator, a Call's `(`). Zero on a node the parser did not
// build, and on an identifier the parser built as part of another node (a
// field name, a qualified member).
type Span struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
}

// IsZero reports whether the parser recorded no extent.
func (s Span) IsZero() bool { return s.EndLine == 0 }

// SpanCarrier is embedded in every node the parser builds from source:
// declarations and their members, statements, expressions, patterns and
// type expressions. It records the node's extent.
type SpanCarrier struct {
	Span Span
}

func (c *SpanCarrier) GetSpan() Span  { return c.Span }
func (c *SpanCarrier) SetSpan(s Span) { c.Span = s }

// HasSpan is implemented by the nodes that carry a Span.
type HasSpan interface {
	GetSpan() Span
	SetSpan(Span)
}

// HasTrivia is implemented by all AST nodes that carry leading/trailing trivia.
type HasTrivia interface {
	GetLeading() []Trivia
	GetTrailing() []Trivia
	AddLeading(Trivia)
	AddTrailing(Trivia)
}

// ---------------------------------------------------------------------------
// String interpolation parts
// ---------------------------------------------------------------------------

// StringPart is a piece of an interpolated string.
type StringPart interface {
	stringPart()
}

// StringText is a literal text segment inside an interpolated string.
type StringText struct {
	Value string
}

func (StringText) stringPart() {}

// StringExpr is an expression segment inside an interpolated string.
type StringExpr struct {
	Expr Node
}

func (StringExpr) stringPart() {}

// ---------------------------------------------------------------------------
// Type expressions — structured representation of type annotations
// ---------------------------------------------------------------------------

// TypeExpr is a type annotation node with source position.
type TypeExpr interface {
	Node
	typeExpr() // marker method
	// TypeString returns the string representation.
	TypeString() string
}

// SimpleType is a named type like Int, String, or Bool.
type SimpleType struct {
	TriviaCarrier
	SpanCarrier
	Name string
	Line int
	Col  int
}

func (*SimpleType) NodeType() string     { return "SimpleType" }
func (n *SimpleType) LineNum() int       { return n.Line }
func (*SimpleType) typeExpr()            {}
func (n *SimpleType) TypeString() string { return n.Name }

// QualifiedType is a module-qualified type like io.Reader or io.List<Int>.
// Module holds the qualifier ("io") with position; Member is the resolved type part.
type QualifiedType struct {
	TriviaCarrier
	SpanCarrier
	Module     string
	ModuleLine int
	ModuleCol  int
	Member     TypeExpr // SimpleType or GenericType for the member part
}

func (*QualifiedType) NodeType() string     { return "QualifiedType" }
func (n *QualifiedType) LineNum() int       { return n.ModuleLine }
func (*QualifiedType) typeExpr()            {}
func (n *QualifiedType) TypeString() string { return n.Module + "." + n.Member.TypeString() }

// GenericType is a parameterized type like List<Int> or Map<String, Int>.
type GenericType struct {
	TriviaCarrier
	SpanCarrier
	Name   string
	Params []TypeExpr
	Line   int
	Col    int
}

func (*GenericType) NodeType() string { return "GenericType" }
func (n *GenericType) LineNum() int   { return n.Line }
func (*GenericType) typeExpr()        {}
func (n *GenericType) TypeString() string {
	s := n.Name + "<"
	for i, p := range n.Params {
		if i > 0 {
			s += ", "
		}
		s += p.TypeString()
	}
	return s + ">"
}

// FuncType is a function type like (Int, String) -> Bool.
type FuncType struct {
	TriviaCarrier
	SpanCarrier
	Params []TypeExpr
	Return TypeExpr // nil for tuple types like (Int, String) with no ->
	Line   int
	Col    int
}

func (*FuncType) NodeType() string { return "FuncType" }
func (n *FuncType) LineNum() int   { return n.Line }
func (*FuncType) typeExpr()        {}
func (n *FuncType) TypeString() string {
	s := "("
	for i, p := range n.Params {
		if i > 0 {
			s += ", "
		}
		s += p.TypeString()
	}
	s += ")"
	if n.Return != nil {
		s += " -> " + n.Return.TypeString()
	}
	return s
}

// SelfType represents the self keyword used in type position inside interface signatures.
type SelfType struct {
	TriviaCarrier
	SpanCarrier
	Line int
	Col  int
}

func (*SelfType) NodeType() string     { return "SelfType" }
func (n *SelfType) LineNum() int       { return n.Line }
func (*SelfType) typeExpr()            {}
func (n *SelfType) TypeString() string { return "self" }

// AnonStructType is an anonymous struct type expression like
// `{name: String, age: Int}`. It can appear in any type-expression slot
// (function parameter type, struct field type, tuple element type, generic
// type argument, …) but not as the bare RHS of a top-level `type Foo`
// declaration — that form is reserved for the `struct` keyword. The
// analyzer maps this to `*analysis.AnonStructType`, which already
// impl structural equality.
//
// Field names follow the snake_case rule, validated by §4 naming
// conventions during analysis. Field types resolve recursively under the
// current type-param scope. Defaults are not allowed in type position
// (defaults are runtime concepts attached to nominal struct
// declarations); the parser rejects `=` after the type.
type AnonStructType struct {
	TriviaCarrier
	SpanCarrier
	Fields []StructField // Default left nil for every field
	// EndTrivia holds comments / blank-line trivia that sit between the
	// last field and the closing `}`. Captured by the parser so
	// `nomi fmt -w` preserves trailing in-body comments. Without this slot
	// the trivia has nowhere to live — TriviaCarrier.Trailing is reserved
	// for same-line trailing comments on the construct itself.
	EndTrivia []Trivia
	Line      int
	Col       int
}

func (*AnonStructType) NodeType() string { return "AnonStructType" }
func (n *AnonStructType) LineNum() int   { return n.Line }
func (*AnonStructType) typeExpr()        {}
func (n *AnonStructType) TypeString() string {
	s := "{"
	for i, f := range n.Fields {
		if i > 0 {
			s += ", "
		}
		s += f.Name + ": " + f.TypeAnnotation.TypeString()
	}
	return s + "}"
}

// DotVariantType is the TypeExpr produced by the leading-dot variant shorthand
// in TypeName positions of literal-attach forms and patterns. Examples:
//
//	StructLit{TypeName: &DotVariantType{Name: "Rect"}}  // .Rect{w: 4.0}
//	MapLit{TypeName: &DotVariantType{Name: "Obj"}}      // .Obj{"k" => v}
//	ListLit{TypeName: &DotVariantType{Name: "Arr"}}     // .Arr[1, 2, 3]
//	StructPattern{TypeName: &DotVariantType{Name: ...}} // case x { .Rect{...} -> ... }
//
// The analyzer treats this TypeExpr as "resolve via expected type at this
// position": at any expression / pattern position whose expected type is
// some enum E, it resolves to E's variant whose name matches Name. No
// expected enum context yields a "no expected type" diagnostic.
//
// Distinct from SimpleType in that it carries no scope-lookup semantics —
// it always resolves contextually, never via name lookup in the surrounding
// scope.
type DotVariantType struct {
	TriviaCarrier
	SpanCarrier
	Name string // variant name (e.g. "Red", "Obj", "Rect")
	Line int    // position of the leading `.`
	Col  int
	// ResolvedEnum is populated by the analyzer once context determines
	// which enum the leading dot resolves against — `Color` for `.Red`
	// in a `Color`-expecting position, etc. The IR builder reads this to
	// construct the right variant without re-deriving the expected-type
	// context. Empty when analysis hasn't fired or when the resolution
	// failed (in which case the analyzer emitted a diagnostic).
	ResolvedEnum string
}

func (*DotVariantType) NodeType() string     { return "DotVariantType" }
func (n *DotVariantType) LineNum() int       { return n.Line }
func (*DotVariantType) typeExpr()            {}
func (n *DotVariantType) TypeString() string { return "." + n.Name }

// ---------------------------------------------------------------------------
// Expressions
// ---------------------------------------------------------------------------

type IntLit struct {
	TriviaCarrier
	SpanCarrier
	Value int64
	// Lexeme is the original source text of the literal (e.g. "1_000_000",
	// "0xFF"), preserved so the formatter can emit it back verbatim instead
	// of normalising every literal to plain base-10. Empty for literals
	// synthesised outside the parser.
	Lexeme string
	Line   int
	Col    int
}

func (*IntLit) NodeType() string { return "IntLit" }
func (n *IntLit) LineNum() int   { return n.Line }

type FloatLit struct {
	TriviaCarrier
	SpanCarrier
	Value float64
	// Lexeme is the original source text of the literal (e.g. "1_000.123_456",
	// "1.0e10"), preserved so the formatter can round-trip it. Empty for
	// literals synthesised outside the parser.
	Lexeme string
	Line   int
	Col    int
}

func (*FloatLit) NodeType() string { return "FloatLit" }
func (n *FloatLit) LineNum() int   { return n.Line }

// DecimalLit is a `1.50d`-style exact base-10 literal. Unlike FloatLit it does
// NOT carry a parsed numeric value: a Float would lose precision/scale, so the
// full source text (including the trailing `d`) is preserved in Lexeme and
// parsed lazily by rt.ParseDecimalLexeme.
// Scale is significant — "1.50d" and "1.5d" are equal values but display
// differently — so round-tripping the lexeme is what keeps that fidelity.
type DecimalLit struct {
	TriviaCarrier
	SpanCarrier
	// Lexeme is the original source text incl. the trailing `d`
	// (e.g. "1.50d", "1_000.00d", "5d"). Always non-empty for parser-produced
	// nodes; rt.ParseDecimalLexeme strips the suffix and parses the rest.
	Lexeme string
	Line   int
	Col    int
}

func (*DecimalLit) NodeType() string { return "DecimalLit" }
func (n *DecimalLit) LineNum() int   { return n.Line }

// CodepointLit is an ASCII codepoint literal, `'a'` or `'\n'`, of type
// `Codepoint`. Value is the codepoint (0..=0x7F); Lexeme is the body between
// the quotes as written, escapes undecoded, so the formatter reproduces the
// spelling.
type CodepointLit struct {
	TriviaCarrier
	SpanCarrier
	Value  rune
	Lexeme string
	Line   int
	Col    int
}

func (*CodepointLit) NodeType() string { return "CodepointLit" }
func (n *CodepointLit) LineNum() int   { return n.Line }

// StringLit is a non-interpolated string literal.
//
// Triple distinguishes the source form: true when the literal was
// written as `"""..."""` (multi-line, no escape processing), false for
// the single-line `"..."` form. Both decode into the same Value (escapes
// processed for single-line, indent stripped for triple). The formatter
// uses this flag to round-trip the source form, rather than collapsing
// triple-quoted strings to `\n`-escaped single-line output.
//
// Raw distinguishes backtick raw forms from the regular forms. In raw form
// the lexer does no escape processing, no `${...}` interpolation parsing, and
// no `\${` escape — the body is verbatim text. Because raw bodies can never have
// interpolation slots, the raw form is always represented as a
// StringLit (never a StringInterp). The flag is source-form metadata
// for round-trip preservation only; runtime semantics are identical to
// a non-raw StringLit with the same Value.
type StringLit struct {
	TriviaCarrier
	SpanCarrier
	Value  string
	Triple bool
	Raw    bool
	Line   int
	Col    int
}

func (*StringLit) NodeType() string { return "StringLit" }
func (n *StringLit) LineNum() int   { return n.Line }

// StringInterp is an interpolated string literal. See StringLit.Triple
// for the meaning of Triple.
type StringInterp struct {
	TriviaCarrier
	SpanCarrier
	Parts  []StringPart
	Triple bool
	Line   int
	Col    int
}

func (*StringInterp) NodeType() string { return "StringInterp" }
func (n *StringInterp) LineNum() int   { return n.Line }

// TaggedString is a tagged string literal: a PascalCase type identifier
// immediately adjacent (no whitespace) to a string opener — for example
// `Sql"SELECT 1"`, a raw Regex backtick literal, or `Bash"""..."""`.
// Tag carries the identifier, Raw and Triple carry the source-form metadata,
// and Parts holds the body in the same
// Static/Dynamic interleave shape that StringInterp uses.
//
// For non-interpolated forms (regular tagged with no `${...}`, or any
// raw-tagged form, since raw bodies never interpolate) Parts is a
// single StringText. For interpolated tagged forms Parts alternates
// StringText and StringExpr just like StringInterp.
//
// Semantics are deferred to a later phase (Phase 8 type-checking,
// Phase 9 runtime); the analyzer currently rejects any TaggedString
// it walks with a "typed literals not yet implemented" error so the
// new surface is parseable but not yet usable.
type TaggedString struct {
	TriviaCarrier
	SpanCarrier
	Tag    string
	Raw    bool
	Triple bool
	Parts  []StringPart
	Line   int
	Col    int
}

func (*TaggedString) NodeType() string { return "TaggedString" }
func (n *TaggedString) LineNum() int   { return n.Line }

// ListLit represents a list literal: [item, item, ...].
//
// TypeName is non-nil for type-prefixed list literals like
// `Arr[1, 2, 3]` — the literal-attach construction form for
// list-payload variants (`Arr ([1, 2, 3])`) and list-distinct
// types (`Items([1, 2, 3])`). nil for anonymous list literals.
type ListLit struct {
	TriviaCarrier
	SpanCarrier
	TypeName TypeExpr // nil for anonymous list literals
	Items    []Node
	// EndTrivia holds trailing comments / blank lines between the last
	// item and the closing `]`. Mirrors StructLit.EndTrivia — captured
	// so `nomi fmt -w` doesn't silently drop `// comment` tokens sitting
	// before the closing bracket. Distinct from TriviaCarrier.Trailing
	// (reserved for same-line trailing comments on the literal itself).
	EndTrivia []Trivia
	// EmbedsEnum is analyzer-filled: the enum the list's elements have when
	// some of them are values of a type that enum `embeds` (`[c, Shape.Dot]`
	// with `c: Circle` is a List<Shape>). Those elements are widened into
	// the enum as the list is built. Empty otherwise.
	EmbedsEnum string
	Line       int
	Col        int
}

func (*ListLit) NodeType() string { return "ListLit" }
func (n *ListLit) LineNum() int   { return n.Line }

// VectorLit represents a vector literal: #[item, item, ...].
type VectorLit struct {
	TriviaCarrier
	SpanCarrier
	Items []Node
	// EndTrivia holds trailing comments / blank lines between the last
	// item and the closing `]`. Mirrors ListLit.EndTrivia.
	EndTrivia []Trivia
	Line      int
	Col       int
}

func (*VectorLit) NodeType() string { return "VectorLit" }
func (n *VectorLit) LineNum() int   { return n.Line }

// SetLit represents a set literal: #{item, item, ...}.
type SetLit struct {
	TriviaCarrier
	SpanCarrier
	Items []Node
	// EndTrivia holds trailing comments / blank lines between the last
	// item and the closing `}`. Mirrors ListLit.EndTrivia.
	EndTrivia []Trivia
	Line      int
	Col       int
}

func (*SetLit) NodeType() string { return "SetLit" }
func (n *SetLit) LineNum() int   { return n.Line }

// ListSpreadLit represents a list spread expression: [item, ..tail] or [a, b, ..tail]
type ListSpreadLit struct {
	TriviaCarrier
	SpanCarrier
	Heads      []Node // fixed elements before ..
	TailSpread Node   // expression after ..
	Line       int
	Col        int
}

func (*ListSpreadLit) NodeType() string { return "ListSpreadLit" }
func (n *ListSpreadLit) LineNum() int   { return n.Line }

// MapEntry represents a key-value pair in a map literal.
type MapEntry struct {
	Key   Node
	Value Node
}

// MapLit represents a map literal: {key => value, ...}
//
// TypeName is non-nil for type-prefixed map literals like
// `Kvs{"a" => 1}` — the literal-attach construction form for
// map-distinct types. nil for anonymous map literals `{"a" => 1}`.
type MapLit struct {
	TriviaCarrier
	SpanCarrier
	TypeName TypeExpr // nil for anonymous map literals
	Entries  []MapEntry
	// EndTrivia holds trailing comments / blank lines between the last
	// entry and the closing `}`. Mirrors StructLit.EndTrivia — captured
	// so `nomi fmt -w` accepts and round-trips trailing in-body comments
	// instead of erroring at the COMMENT token.
	EndTrivia []Trivia
	Line      int
	Col       int
}

func (*MapLit) NodeType() string { return "MapLit" }
func (n *MapLit) LineNum() int   { return n.Line }

type TupleLit struct {
	TriviaCarrier
	SpanCarrier
	Items []Node
	Line  int
	Col   int
}

func (*TupleLit) NodeType() string { return "TupleLit" }
func (n *TupleLit) LineNum() int   { return n.Line }

type Ident struct {
	TriviaCarrier
	SpanCarrier
	Name string
	Line int
	Col  int
}

func (*Ident) NodeType() string { return "Ident" }
func (n *Ident) LineNum() int   { return n.Line }

type TypeIdent struct {
	TriviaCarrier
	SpanCarrier
	Name string
	Line int
	Col  int
}

func (*TypeIdent) NodeType() string { return "TypeIdent" }
func (n *TypeIdent) LineNum() int   { return n.Line }

type Unary struct {
	TriviaCarrier
	SpanCarrier
	Op    string
	Right Node
	Line  int
	Col   int
}

func (*Unary) NodeType() string { return "Unary" }
func (n *Unary) LineNum() int   { return n.Line }

type Binary struct {
	TriviaCarrier
	SpanCarrier
	Left  Node
	Op    string
	Right Node
	// Wrapping marks an arithmetic node whose Int overflow should wrap
	// (two's complement) instead of trapping. Has no surface syntax — it is
	// set only by compiler-synthesized code that needs modular arithmetic,
	// e.g. the `@derive Hashable` hash-mix in analysis/derive_synthesis.go.
	Wrapping bool
	Line     int
	Col      int
}

func (*Binary) NodeType() string { return "Binary" }
func (n *Binary) LineNum() int   { return n.Line }

// GroupedExpr is an explicitly parenthesized expression. It is transparent
// for type checking and evaluation; the formatter keeps it when the grouping
// communicates structure and drops it around obvious atoms.
type GroupedExpr struct {
	TriviaCarrier
	SpanCarrier
	Expr Node
	Line int
	Col  int
}

func (*GroupedExpr) NodeType() string { return "GroupedExpr" }
func (n *GroupedExpr) LineNum() int   { return n.Line }

type If struct {
	TriviaCarrier
	SpanCarrier
	Cond        Node
	CondPattern Node // non-nil for `if Pattern = expr`
	Then        *Block
	Else        Node // *Block, *If, or nil
	Line        int
	Col         int
}

func (*If) NodeType() string { return "If" }
func (n *If) LineNum() int   { return n.Line }

// With replaces one application field from this statement to the end of
// the enclosing block, as a `defer` lasts to the end of its block:
//
//	with MyApp.logger = Silent
//	run_import()
//
// The replacement reaches every function called after it in the block, and
// however the block exits the previous value is restored. A `with` is a
// statement and has no value. Target is the application field it replaces,
// spelled as a read is (`MyApp.logger`, `app.MyApp.logger`); an application
// field is read as an ordinary FieldAccess on the application type, and the
// checker identifies those reads.
type With struct {
	TriviaCarrier
	SpanCarrier
	Target *FieldAccess
	Value  Node
	Line   int // the `with` keyword
	Col    int
}

func (*With) NodeType() string { return "With" }
func (n *With) LineNum() int   { return n.Line }

// Defer registers a Unit-returning call to run when the current block exits.
type Defer struct {
	TriviaCarrier
	SpanCarrier
	Call Node
	Line int
	Col  int
}

func (*Defer) NodeType() string { return "Defer" }
func (n *Defer) LineNum() int   { return n.Line }

// TestDecl is either `test "name" { ... }` or `tests "name" { ... }`.
// Test declarations are inert during normal program execution and are run by
// the test runner.
type TestDecl struct {
	TriviaCarrier
	SpanCarrier
	Name  string
	Group bool
	// Clock is the expression in a `clock …` declaration, naming a
	// `testing.Clock` variant: `.Virtual` runs the group's tests in a bubble
	// where time only advances when every task is blocked, `.System` the
	// real clock. nil means the real clock.
	//
	// An expression rather than a keyword so the declaration is the same
	// shape as its neighbours `boot` and `setup` — and so a typo is an
	// ordinary "unknown variant" diagnostic, and editors highlight it
	// like any other enum access.
	//
	// Group declarations only.
	Clock     Node
	ClockLine int
	ClockCol  int
	// ClockLeading / ClockTrailing carry the comments around the `clock`
	// line. It is a bare declaration rather than a node, so it has no
	// TriviaCarrier of its own and the formatter would otherwise drop a
	// comment written above it.
	ClockLeading  []Trivia
	ClockTrailing []Trivia
	// Boot is the call on a group's `boot` line, `boot server.boot(startup)`:
	// a call to an entry file's boot with one Startup argument, which each
	// test in the group runs before its setup and body. nil when the group
	// boots nothing.
	Boot Node
	// Setup is the group's `setup` body, run before each test's body; its
	// value is what a test binds with ContextPattern.
	Setup Node
	// ContextPattern is a test's irrefutable pattern after the name,
	// `test "x", db { ... }`, binding its group's setup value.
	ContextPattern Node
	Body           *Block
	Line           int
	Col            int
	NameLine       int
	NameCol        int
	BootLine       int
	BootCol        int
	SetupLine      int
	SetupCol       int
}

func (*TestDecl) NodeType() string { return "TestDecl" }
func (n *TestDecl) LineNum() int   { return n.Line }

// Assertion is `assert expr` or `refute expr`. Result and Maybe subjects
// validate their shape and return the original value on success. Custom
// Assertable subjects also return the original value. `assert` and `refute`
// propagate an assertion failure otherwise. The Check flag is used internally
// by testing.check so it can reuse assertion evaluation and rendering.
type Assertion struct {
	TriviaCarrier
	SpanCarrier
	Check  bool
	Refute bool
	Expr   Node
	Line   int
	Col    int
}

func (*Assertion) NodeType() string { return "Assertion" }
func (n *Assertion) LineNum() int   { return n.Line }

// Dbg is `dbg expr`, a debug-print expression that returns the evaluated value.
// Expr is nil only for the pipe-stage form `value |> dbg`, where the piped
// value is supplied by the pipe's checker and IR builder.
type Dbg struct {
	TriviaCarrier
	SpanCarrier
	Expr Node
	Line int
	Col  int
}

func (*Dbg) NodeType() string { return "Dbg" }
func (n *Dbg) LineNum() int   { return n.Line }

// Todo is `todo` or `todo "reason"`, a placeholder for code not yet written.
// It produces no value: reaching it stops the program with
// `todo reached at <file>:<line>` plus `: <reason>` when there is one, so the
// checker gives it whatever type its position expects. Reason is nil for the
// bare form; otherwise it is a string literal with no interpolation (the
// parser rejects any other reason), kept as a node so the formatter can
// reproduce its source form.
type Todo struct {
	TriviaCarrier
	SpanCarrier
	Reason *StringLit
	Line   int
	Col    int
}

func (*Todo) NodeType() string { return "Todo" }
func (n *Todo) LineNum() int   { return n.Line }

// ReasonText is the reason's text, or "" for a bare `todo`.
func (n *Todo) ReasonText() string {
	if n.Reason == nil {
		return ""
	}
	return n.Reason.Value
}

// ConcurrentBlock is `concurrent { body }` — the structured-
// concurrency scope from spec §20 (layer 1). The body type-checks
// like a lambda body (last expression is the value; `return` / `try`
// unwind to the block boundary, not the enclosing fn). At runtime
// `Task.spawn(|| ...)` calls inside this block spawn goroutines that
// must all be awaited (or explicitly discarded via `_ = Task.await(t)`)
// before the block exits; the runtime cancels any in-flight
// siblings on non-normal exit.
type ConcurrentBlock struct {
	TriviaCarrier
	SpanCarrier
	Body *Block
	Line int
	Col  int
}

func (*ConcurrentBlock) NodeType() string { return "ConcurrentBlock" }
func (n *ConcurrentBlock) LineNum() int   { return n.Line }

type Block struct {
	TriviaCarrier
	SpanCarrier
	Stmts   []Node
	Line    int // opening `{`
	Col     int
	EndLine int // closing `}`; zero on synthesized blocks (e.g. lambda bodies)
	EndCol  int
}

func (*Block) NodeType() string { return "Block" }
func (n *Block) LineNum() int   { return n.Line }

// Contains reports whether (line, col) falls strictly between the
// block's opening `{` and closing `}`. False when end positions are
// unset (synthesized blocks).
func (n *Block) Contains(line, col int) bool {
	if n == nil || n.EndLine == 0 {
		return false
	}
	if line < n.Line || line > n.EndLine {
		return false
	}
	if line == n.Line && col <= n.Col {
		return false
	}
	if line == n.EndLine && col >= n.EndCol {
		return false
	}
	return true
}

type Call struct {
	TriviaCarrier
	SpanCarrier
	Func                   Node
	Args                   []Node
	TypeArgs               []TypeExpr // explicit turbofish type args (`f<Int>(x)`); nil when omitted
	InferredDispatchTarget string     // analyzer-filled target for dispatch calls inferred from expected type
	IsTailCall             bool       // set by analysis/tail_position.go
	Line                   int
	Col                    int
}

func (*Call) NodeType() string { return "Call" }
func (n *Call) LineNum() int   { return n.Line }

// DotVariant is the expression-position dot-leading variant shorthand,
// the no-payload (`.Red`) and call-form (`.Circle(1.0)`) cases.
//
// Bare `.X` is a *DotVariant directly; `.X(args)` is a Call whose Func is a
// *DotVariant. The literal-attach forms (`.X{...}`, `.X[...]`, `.X{"k" => v}`)
// produce StructLit / ListLit / MapLit with `TypeName: &DotVariantType{...}`
// rather than wrapping them in a DotVariant — those literal nodes already
// carry the payload directly, and they own their own TypeName slot.
//
// The analyzer resolves the variant against the expected enum type at the
// expression's position, the same as DotVariantType in TypeName slots.
type DotVariant struct {
	TriviaCarrier
	SpanCarrier
	Name string // variant name (e.g. "Red")
	Line int    // position of the leading `.`
	Col  int
	// ResolvedEnum is populated by the analyzer once context determines
	// which enum the leading dot resolves against. See DotVariantType's
	// matching field for the rationale and lifecycle.
	ResolvedEnum string
}

func (*DotVariant) NodeType() string { return "DotVariant" }
func (n *DotVariant) LineNum() int   { return n.Line }

// FieldAccessor is the field accessor shorthand `.name`: a function that
// reads field `name` from its one argument. A chain `.address.city` reads
// each field in turn, and a tuple index reads an element (`.0`). Path holds
// the segments in order, each at its own position.
//
// It stands only where a function is expected. The analyzer types it from
// that expected function type's parameter, as it resolves a `.Variant`
// against the expected enum.
type FieldAccessor struct {
	TriviaCarrier
	SpanCarrier
	Path []*Ident
	Line int // position of the leading `.`
	Col  int
}

func (*FieldAccessor) NodeType() string { return "FieldAccessor" }
func (n *FieldAccessor) LineNum() int   { return n.Line }

// Spelling is the accessor as written: `.address.city`.
func (n *FieldAccessor) Spelling() string {
	var b strings.Builder
	for _, seg := range n.Path {
		b.WriteString(".")
		b.WriteString(seg.Name)
	}
	return b.String()
}

type FieldAccess struct {
	TriviaCarrier
	SpanCarrier
	Object Node
	Field  *Ident
	Line   int
	Col    int
}

func (*FieldAccess) NodeType() string { return "FieldAccess" }
func (n *FieldAccess) LineNum() int   { return n.Line }

// ---------------------------------------------------------------------------
// Structs
// ---------------------------------------------------------------------------

type StructField struct {
	SpanCarrier
	Name           string
	TypeAnnotation TypeExpr // structured type annotation
	Default        Node     // nil if no default
	// LeadingComments holds COMMENT / BLANK_LINE trivia that sits
	// immediately above this field in the source (between this field
	// and either the opening `{` or the previous field's comma). The
	// formatter renders these lines above the field so inter-field
	// comments round-trip cleanly. Distinct from a TriviaCarrier:
	// StructField is a value type (not a Node); comments on their own
	// lines after the last field belong to the enclosing EndTrivia.
	LeadingComments []Trivia
	// Trailing holds a `// comment` on the line the field ends on
	// (`x: Int // note`, or `x: Int, // note` in a comma-separated list).
	// The formatter keeps it on that line.
	Trailing []Trivia
	// Doc holds `///` doc-comment text attached to the field. The parser
	// (parseStructBody) collects doc comments before every body member,
	// so docs attach to keyword `field` items and old-form bare fields
	// alike. Empty when no doc comment precedes the field.
	Doc  string
	Line int
	Col  int
}

// TypeParam represents a type parameter with position info (e.g., T in Pair<T, U>).
// Bounds carries internal/synthesized interface constraints for this
// parameter. Source-written constraints live in WhereClauses; nil/empty means
// "unbounded."
type TypeParam struct {
	Name   string
	Bounds []TypeExpr
	Line   int
	Col    int
}

// StructDef is a nominal record type declared with the `struct` keyword:
//
//	[pub ][opaque ]struct Name[<TypeParams>]{ field1: T, field2: U, ... }
//
// No space between the type name (or type-params) and the opening `{` is a
// formatter convention, not a parser requirement. The optional `opaque`
// modifier publishes the type name while keeping its construction surface
// (constructor, destructuring, field access) module-private — the field
// set IS the representation. See spec §15.3 *Opaque distinct types*.
type StructDef struct {
	TriviaCarrier
	SpanCarrier
	AttachedTests []AttachedTest // prompt tests attached to this declaration
	Decorators    []Decorator    // `derive` decorators the derive lowering adds; nil when none
	Name          string
	Public        bool
	// Opaque is set when the parser saw `opaque` before `struct`. It marks
	// the type as opaque per spec §15.3: outside the owning module the
	// type name is usable (signatures, generic args) but the field set is
	// hidden — no `Name{...}` construction, no destructuring, no field
	// access. The flag flows through to the symbol and StructType during
	// analysis so the opacity boundary is enforced at use sites.
	Opaque     bool
	TypeParams []TypeParam
	// WhereClauses holds declaration-level constraints for type params, e.g.
	// `struct Range<T> where T: Comparable { ... }`.
	WhereClauses []WhereConstraint
	Fields       []StructField
	// Items holds the non-field body items of the type-body item syntax:
	// once bindings and named tests, in source order relative to each other.
	// Fields land in Fields, not here.
	Items []Node
	// EndTrivia holds trailing comments / blank lines that sit between the
	// last field and the closing `}`. The formatter renders these inside
	// the braces so `nomi fmt -w` doesn't drop or refuse them. Kept distinct
	// from TriviaCarrier.Trailing (which is for same-line trailing comments
	// on the StructDef as a whole).
	EndTrivia []Trivia
	Doc       string
	Line      int
	Col       int
}

func (*StructDef) NodeType() string { return "StructDef" }
func (n *StructDef) LineNum() int   { return n.Line }

type StructFieldVal struct {
	Name  string
	Value Node
	// LeadingComments holds COMMENT / BLANK_LINE trivia between this
	// field and either the opening `{` or the previous field's comma.
	// Mirrors StructField.LeadingComments — the enclosing StructLit
	// owns trailing in-body trivia via its EndTrivia slot.
	LeadingComments []Trivia
	Line            int // position of the field name token (0 if unknown)
	Col             int
}

type StructLit struct {
	TriviaCarrier
	SpanCarrier
	TypeName TypeExpr
	Fields   []StructFieldVal
	// Spread holds the head expression of a struct-update literal,
	// `{..base, field: value}` — the `base`. Nil for every other struct
	// literal. It is only ever set on the anonymous form (TypeName == nil):
	// the head carries the result type, so `Cfg{..base}` has no meaning and
	// the parser does not produce it. Fields then hold only the overrides,
	// and "missing field" stops being an error because the spread supplies
	// every field (analysis/checker.go's checkStructLitAgainstStruct).
	Spread Node
	// SpreadLine / SpreadCol are the position of the `..` token, used for
	// the position diagnostics and for hover on the spread head.
	SpreadLine int
	SpreadCol  int
	// EndTrivia holds trailing comments / blank lines between the last
	// field and the closing `}`. Covers both nominal struct literals
	// (`Foo{a: 1, // comment\n}`) and the anonymous literal form
	// (`{a: 1, // comment\n}`) since they share this AST node.
	EndTrivia []Trivia
	Line      int
	Col       int
}

func (*StructLit) NodeType() string { return "StructLit" }
func (n *StructLit) LineNum() int   { return n.Line }

// ---------------------------------------------------------------------------
// Enums
// ---------------------------------------------------------------------------

type EnumVariant struct {
	SpanCarrier
	Name             string
	Kind             string        // "bare", "positional", "struct", or "embedded"
	DataTypeExpr     TypeExpr      // structured type annotation for positional
	Fields           []StructField // fields for struct variants (reuses StructField from struct defs)
	EmbeddedTypeExpr TypeExpr      // type for embedded variants
	// EndTrivia holds trailing comments / blank lines between the last
	// struct-variant field and the closing `}` (struct-shaped variants only).
	// Bare / positional / embedded variants don't have a body to attach
	// trivia to.
	EndTrivia []Trivia
	// LeadingComments holds COMMENT / BLANK_LINE trivia between this
	// variant and the previous variant's `|` separator (or the opening
	// `{` for the first variant). Lets inter-variant comments round-trip
	// cleanly. The enclosing EnumDef.EndTrivia still owns trailing
	// in-body trivia.
	LeadingComments []Trivia
	// Trailing holds a `// comment` on the line the variant ends on
	// (`ToJson // note`). The formatter keeps it on that line.
	Trailing []Trivia
	// Doc holds `///` doc-comment text attached to a keyword `variant`
	// item (`/// docs` lines above `variant Name ...` in a type body).
	// Empty for old-form `|`-separated variants, which never supported
	// doc comments.
	Doc  string
	Line int
	Col  int
}

// EnumDef is a sum type declared with the `enum` keyword:
//
//	[pub ][opaque ]enum Name[<TypeParams>] { Variant1 | Variant2 | ... }
//
// First variant is bare; subsequent variants are pipe-prefixed (`|` is a
// binary separator, not a per-variant prefix). Single-variant enums have
// no `|` at all. The optional `opaque` modifier publishes the type name
// while keeping its variant set module-private — the variant set IS the
// representation. See spec §15.3 *Opaque distinct types*.
type EnumDef struct {
	TriviaCarrier
	SpanCarrier
	AttachedTests []AttachedTest // prompt tests attached to this declaration
	Decorators    []Decorator    // `derive` decorators the derive lowering adds; nil when none
	Name          string
	Public        bool
	// Opaque is set when the parser saw `opaque` before `enum`. It marks
	// the type as opaque per spec §15.3: outside the owning module the
	// type name is usable (signatures, generic args) but the variant set
	// is hidden — no construction of variants, no `case` destructuring,
	// no payload access. The flag flows through to the symbol and EnumType
	// during analysis so the opacity boundary is enforced at use sites.
	Opaque     bool
	TypeParams []TypeParam
	// WhereClauses holds declaration-level constraints for type params, e.g.
	// `enum Box<T> where T: Display { ... }`.
	WhereClauses []WhereConstraint
	Variants     []EnumVariant
	// Items holds the non-variant body items of the type-body item syntax:
	// once bindings and named tests, in source order relative to each other.
	// Variants land in Variants, not here.
	Items []Node
	// EndTrivia holds trailing comments / blank lines between the last
	// variant and the closing `}`. Mirrors StructDef.EndTrivia.
	EndTrivia []Trivia
	Doc       string
	Line      int
	Col       int
}

func (*EnumDef) NodeType() string { return "EnumDef" }
func (n *EnumDef) LineNum() int   { return n.Line }

// ---------------------------------------------------------------------------
// Distinct types
// ---------------------------------------------------------------------------

// TypeDef defines a distinct-type wrapper or a zero-sized marker:
//
//	type Name InnerType   → distinct over InnerType (primitive/tuple/generic)
//	type Name             → zero-sized marker
//
// Struct (`struct Name {...}`) and enum (`enum Name { A | B }`) shapes
// have their own AST nodes (StructDef, EnumDef) and are not produced by
// this declaration. `opaque type Name InnerType` makes the inner
// representation module-private; opaque on a zero-sized type is rejected
// at analysis time.
type TypeDef struct {
	TriviaCarrier
	SpanCarrier
	AttachedTests []AttachedTest // prompt tests attached to this declaration
	Decorators    []Decorator    // `derive` decorators the derive lowering adds; nil when none
	Name          string
	Public        bool
	Opaque        bool     // true for `pub opaque type Name InnerType` — name exported, construction surface module-private. See spec §15.3.
	InnerTypeExpr TypeExpr // structured inner type, nil for zero-sized
	// HasBody/Items are retained for compiler-synthesized or legacy internal
	// nodes. Source `type` declarations do not carry bodies; type-qualified
	// functions and once bindings live in sibling `impl Type { ... }` blocks.
	HasBody bool
	Items   []Node
	// EndTrivia holds trailing comments / blank lines between the last
	// body item and the closing `}`. Mirrors StructDef.EndTrivia. Empty
	// when HasBody is false.
	EndTrivia []Trivia
	Doc       string
	Line      int
	Col       int
	EndLine   int // closing `}` when HasBody; zero otherwise
	EndCol    int
}

func (*TypeDef) NodeType() string { return "TypeDef" }
func (n *TypeDef) LineNum() int   { return n.Line }

// ---------------------------------------------------------------------------
// Type aliases
// ---------------------------------------------------------------------------

// RangeLit is a range literal in expression position. The six syntactic
// forms (`1..5`, `1..=5`, `..5`, `..=5`, `1..`, `..`) all map to one
// node — Start nil means "implicit 0" and End nil means "unbounded."
// Inclusive distinguishes `..` (false) from `..=` (true). When End is
// nil, Inclusive is canonically false (no upper bound to be inclusive
// of).
type RangeLit struct {
	TriviaCarrier
	SpanCarrier
	Start     Node
	End       Node
	Inclusive bool
	Line      int
	Col       int
}

func (*RangeLit) NodeType() string { return "RangeLit" }
func (n *RangeLit) LineNum() int   { return n.Line }

// TypeAlias defines a transparent type synonym (`typealias Name Target`)
// or a bound alias (`typealias Name A and B`). Exactly one of
// TargetTypeExpr or Bounds is populated:
//
//   - TargetTypeExpr non-nil, Bounds empty   → regular type alias
//   - TargetTypeExpr nil, Bounds non-empty   → bound alias
//
// A bound alias is usable only in `where` bounds (`where T: Name`) and in
// other bound-alias definitions; using it as a value type is rejected by the
// checker.
type TypeAlias struct {
	TriviaCarrier
	SpanCarrier
	AttachedTests  []AttachedTest // prompt tests attached to this declaration
	Name           string
	Public         bool
	TargetTypeExpr TypeExpr   // structured target type (single-type alias)
	Bounds         []TypeExpr // bound list when RHS is `A and B` (bound alias)
	Doc            string
	Line           int
	Col            int
}

func (*TypeAlias) NodeType() string { return "TypeAlias" }
func (n *TypeAlias) LineNum() int   { return n.Line }

// ---------------------------------------------------------------------------
// Statements
// ---------------------------------------------------------------------------

type Binding struct {
	TriviaCarrier
	SpanCarrier
	Name           string
	TypeAnnotation TypeExpr // structured type annotation, nil if absent
	Value          Node
	Line           int
	Col            int
}

func (*Binding) NodeType() string { return "Binding" }
func (n *Binding) LineNum() int   { return n.Line }

// TupleDestructure binds tuple elements to names: (x, y) = expr
type TupleDestructure struct {
	TriviaCarrier
	SpanCarrier
	Bindings []*Ident // binding identifiers, nil for wildcard "_"
	Value    Node
	Line     int
	Col      int
}

func (*TupleDestructure) NodeType() string { return "TupleDestructure" }
func (n *TupleDestructure) LineNum() int   { return n.Line }

// StructDestructure binds struct fields to names: {x, y} = expr or {x: a, y: b} = expr
type StructDestructure struct {
	TriviaCarrier
	SpanCarrier
	Fields []StructPatternField // reuse existing type: Name, Binding, Pattern
	Value  Node
	Line   int
	Col    int
}

func (*StructDestructure) NodeType() string { return "StructDestructure" }
func (n *StructDestructure) LineNum() int   { return n.Line }

// MapDestructure binds map values to names: {"key" => name} = expr
type MapDestructure struct {
	TriviaCarrier
	SpanCarrier
	Entries []MapPatternEntry // key => binding pairs
	Value   Node
	Line    int
	Col     int
}

func (*MapDestructure) NodeType() string { return "MapDestructure" }
func (n *MapDestructure) LineNum() int   { return n.Line }

// PatternDestructure binds a general pattern using assertion semantics:
//
//	assert pattern = expr
//
// The RHS is evaluated as an ordinary expression; `assert` marks the pattern
// match itself as the assertion. On mismatch, evaluation produces an
// AssertionFailure instead of an ordinary destructuring error.
type PatternDestructure struct {
	TriviaCarrier
	SpanCarrier
	Pattern    Node
	Value      Node
	AssertLine int
	AssertCol  int
	Line       int
	Col        int
}

func (*PatternDestructure) NodeType() string { return "PatternDestructure" }
func (n *PatternDestructure) LineNum() int   { return n.Line }

// PatternBinding binds a pattern none of the destructure shapes above
// spells, optionally followed by an `else` branch that runs when the value
// does not match:
//
//	Some(email) = user.email else { return Err("no email") }
//	Ok(user) = load(id) else {
//	    Err(e) -> return Err("loading: ${e}")
//	}
//
// The names the pattern binds are in scope for the rest of the enclosing
// block. A destructure shape followed by `else` (`Some(x) = v else { ... }`,
// which without `else` is a DistinctDestructure) is a PatternBinding too.
type PatternBinding struct {
	TriviaCarrier
	SpanCarrier
	Pattern Node
	Value   Node
	Else    *BindingElse // nil when the binding has no `else`
	Line    int
	Col     int
}

func (*PatternBinding) NodeType() string { return "PatternBinding" }
func (n *PatternBinding) LineNum() int   { return n.Line }

// ElseNodes returns what the binding's else runs, in source order: its block,
// or each arm's guard (when present) and body. Arm patterns are not included.
// It is empty when the binding has no else.
func (n *PatternBinding) ElseNodes() []Node {
	if n.Else == nil {
		return nil
	}
	if n.Else.Block != nil {
		return []Node{n.Else.Block}
	}
	var out []Node
	for _, arm := range n.Else.Arms {
		if arm.Guard != nil {
			out = append(out, arm.Guard)
		}
		if arm.Body != nil {
			out = append(out, arm.Body)
		}
	}
	return out
}

// BindingElse is the `else { ... }` of a PatternBinding. Exactly one of
// Block and Arms is set: Block for a plain block, Arms when the braces hold
// case arms matched against the value the pattern did not match.
//
// Line/Col locate the `else` keyword. LBraceLine/LBraceCol and
// EndLine/EndCol locate the arms form's braces; trivia after the last arm
// is the BindingElse's own trailing trivia, as it is a Case's.
type BindingElse struct {
	TriviaCarrier
	SpanCarrier
	Block      *Block
	Arms       []CaseBranch
	Line       int
	Col        int
	LBraceLine int
	LBraceCol  int
	EndLine    int
	EndCol     int
}

func (*BindingElse) NodeType() string { return "BindingElse" }
func (n *BindingElse) LineNum() int   { return n.Line }

// DistinctDestructure unwraps a distinct type: Id(x) = expr
type DistinctDestructure struct {
	TriviaCarrier
	SpanCarrier
	TypeName     string   // "Id" or "Civil.Days"
	TypeNameExpr TypeExpr // structured type name when source used a qualified prefix
	Binding      *Ident   // bound variable, or nil for "_"
	Value        Node     // RHS expression
	Line         int
	Col          int
}

func (*DistinctDestructure) NodeType() string { return "DistinctDestructure" }
func (n *DistinctDestructure) LineNum() int   { return n.Line }

type ExprStmt struct {
	TriviaCarrier
	SpanCarrier
	Expr Node
	Line int
	Col  int
}

func (*ExprStmt) NodeType() string { return "ExprStmt" }
func (n *ExprStmt) LineNum() int   { return n.Line }

// ErrorNode stands in for a run of source the parser could not read. It
// holds the span of the tokens that were skipped and the message the
// failing production produced.
//
// Only parser.ParseResilient creates one. parser.Parse and
// parser.ParseWithRecovery never do, so an ErrorNode cannot reach the IR
// builder: `nomi run` and `nomi test` parse strictly and refuse a file with
// a syntax error before any AST exists.
//
// The purpose is the LSP. An ErrorNode inside a function body lets the
// enclosing declaration survive the parse, so the builder still records
// the function's parameters and locals and completion has scopes to
// offer at the cursor. See docs/roadmap.md Track 3, "Resilient parsing".
type ErrorNode struct {
	TriviaCarrier
	SpanCarrier
	Message string
	Line    int
	Col     int
	EndLine int
	EndCol  int
}

func (*ErrorNode) NodeType() string { return "ErrorNode" }
func (n *ErrorNode) LineNum() int   { return n.Line }

// ---------------------------------------------------------------------------
// Interfaces
// ---------------------------------------------------------------------------

// InterfaceMethod represents a method signature in an interface definition.
//
// Open is set when the parser saw the `open` modifier before `fn` on a
// default method (a method with a non-nil Body). It marks the default as
// an extension point — `impl Iface for T { fn name(...) }` or a type-body
// method satisfying `Iface` is allowed to override it. Default methods are
// final by default; the analyzer rejects
// overrides of a non-Open default. Required methods (Body == nil) ignore
// this flag — they always need an implementation and `open` is meaningless on
// them (the parser rejects `open fn name(...): T` without a body).
type InterfaceMethod struct {
	TriviaCarrier
	SpanCarrier
	Name string
	// TypeParams holds the explicit method-local generic header:
	// `fn prefer<K>(...)`. Interface method-local type variables must be
	// declared here; unlike free functions, interface methods do not invent
	// implicit method-local generics from bare PascalCase names in the
	// signature.
	TypeParams []TypeParam
	// Params reuses the regular function-parameter node, so interface methods
	// support the same param surface as a free `fn` (type annotations, trailing
	// default values, destructuring) through the one shared param parser —
	// rather than a bespoke, perpetually-lagging copy.
	Params         []Param
	ReturnTypeExpr TypeExpr // structured return type annotation, nil if absent
	Body           Node     // nil for abstract methods, *Block for default implementations
	Open           bool     // `open fn name(...) { ... }` / `open host fn ...` — default may be overridden
	// Extern marks a host-backed default: `host fn name(...): T` (no Nomi
	// body — the host provides the implementation, shared by every
	// implementor). A contract method like a Nomi default, but supplied by
	// the runtime rather than an interface body block. Combines with Open
	// (`open host fn` — an overridable host-backed default).
	Extern bool
	// Doc holds `///` doc-comment text attached to the method (the lines
	// immediately above `fn` / `open fn` in the interface body). The
	// formatter re-emits it above the signature so `nomi fmt -w` doesn't
	// delete user docs.
	Doc string
	// WhereClauses carries a method-level `where T: Comparable, K: Hashable`
	// clause, parsed after the return type. It constrains a type variable
	// already in scope — the interface's own type param (`T`) or an explicit
	// method-local (`K`) — for this one method, without re-declaring it.
	// This is the mechanism behind constrained interface defaults and
	// constrained required signatures.
	WhereClauses []WhereConstraint
	Line         int
	Col          int
}

// WhereConstraint is one `Name: Bound [and Bound]*` entry in a `where` clause.
// Name refers to a type variable in scope, and Bounds are the interfaces it
// must implement.
type WhereConstraint struct {
	Name   string
	Bounds []TypeExpr
	Line   int
	Col    int
}

func (*InterfaceMethod) NodeType() string { return "InterfaceMethod" }
func (n *InterfaceMethod) LineNum() int   { return n.Line }

// InterfaceField represents a `field name: Type` requirement inside an
// interface body. A struct implementing the interface is required to
// declare a field with the same name and exact type. Fields are
// required-only — interfaces never supply defaults at the field level
// (defaults live on the implementing struct's field declaration).
type InterfaceField struct {
	SpanCarrier
	Name           string
	TypeAnnotation TypeExpr
	// LeadingComments holds COMMENT / BLANK_LINE trivia that sits
	// immediately above this field requirement in the interface body.
	// Mirrors StructField.LeadingComments — the enclosing
	// InterfaceDef.EndTrivia owns end-of-body trivia.
	LeadingComments []Trivia
	// Trailing holds a `// comment` on the requirement's own line
	// (`field name: String // note`). The formatter keeps it on that line.
	Trailing []Trivia
	// Doc holds `///` doc-comment text attached to the field requirement
	// (the lines immediately above `field name: T`). The formatter
	// re-emits it above the requirement so `nomi fmt -w` doesn't delete
	// user docs.
	Doc  string
	Line int
	Col  int
}

func (*InterfaceField) NodeType() string { return "InterfaceField" }
func (n *InterfaceField) LineNum() int   { return n.Line }

// InterfaceDef represents an interface definition: Interface Name { methods... }
type InterfaceDef struct {
	TriviaCarrier
	SpanCarrier
	AttachedTests []AttachedTest // prompt tests attached to this declaration
	Name          string
	Public        bool
	TypeParams    []TypeParam
	WhereClauses  []WhereConstraint
	Methods       []InterfaceMethod
	Fields        []InterfaceField
	// EndTrivia holds trailing comments / blank lines between the last
	// member and the closing `}`. Mirrors StructDef.EndTrivia.
	EndTrivia []Trivia
	Doc       string
	Line      int
	Col       int
	EndLine   int // closing `}`; for ScopeAt span of the type-param scope
	EndCol    int
}

func (*InterfaceDef) NodeType() string { return "InterfaceDef" }
func (n *InterfaceDef) LineNum() int   { return n.Line }

// ImplBlock is the core impl IR node. It is produced two ways:
//
//   - Directly by the parser for the `impl Iface for Type { ... }` surface
//     form:
//
//     impl Display for Money { fn to_string(m: Money): String { ... } }
//     impl<T> Iter for Box<T> { fn next(b: Box<T>): Maybe<(T, Box<T>)> { ... } }
//
//     The interface is named once before `for`; methods inside are plain
//     `fn` / `host fn` because the header names the single interface.
//     Two interfaces on one type = two separate blocks.
//
//   - By LowerDerives, which synthesizes receiver/interface blocks for
//     structural derives.
//
// Items inside the block define functions on the receiver type (and, for
// interface impls, satisfy the named interface). Concrete impl blocks spell
// the receiver type explicitly in signatures; `self` is only a placeholder in
// interface declarations.
//
// Interface is nil for inherent blocks; non-nil (the type-expr written before
// `for`) for interface impls. Receiver is the type-expr after `for` (or the
// lone type-expr for an inherent block) — non-nil for every top-level block.
// Generics holds the optional `<T, U>` header clause. WhereClauses holds the
// optional block-level constraints for those params. Items are the body
// declarations — each is a *FuncDef or *ExternFunc (the only two forms the body
// admits), carrying its own leading doc comments and decorators via the
// embedded node.
type ImplBlock struct {
	TriviaCarrier
	SpanCarrier
	AttachedTests []AttachedTest // prompt tests attached to this declaration
	Interface     TypeExpr       // nil for inherent blocks; the interface type-expr before `for` otherwise
	Receiver      TypeExpr       // the receiver type-expr (after `for`, or the lone type-expr for inherent)
	Generics      []TypeParam
	WhereClauses  []WhereConstraint
	Items         []Node // each is a *FuncDef or *ExternFunc
	// InferInterfaceMethods marks legacy/synthesized blocks whose items are
	// candidates for the interface instead of direct members of the block.
	// Source `impl Iface for Type { ... }` blocks leave this false because the
	// header already names the one interface for every item.
	InferInterfaceMethods bool
	// EndTrivia holds trailing comments / blank lines between the last
	// item and the closing `}`. Mirrors StructDef.EndTrivia / InterfaceDef.EndTrivia.
	EndTrivia []Trivia
	Doc       string
	Line      int
	Col       int
	EndLine   int // closing `}`; for ScopeAt span of the self/generics scope
	EndCol    int
	// SynthOriginLine / SynthOriginCol are the source position of the
	// declaration this block was SYNTHESIZED for (`@derive`, or the
	// universal-default Debug). Zero on every hand-written block.
	//
	// A synthesized block's own Line/Col live in the analyzer's synth-band
	// (analysis.IsSynthesizedLine) and name a position no programmer can
	// navigate to and no editor can resolve, so a diagnostic about one must
	// report HERE instead. Recorded on the node rather than recovered from
	// the position, because the position is deliberately not invertible.
	SynthOriginLine int
	SynthOriginCol  int
}

func (*ImplBlock) NodeType() string { return "ImplBlock" }
func (n *ImplBlock) LineNum() int   { return n.Line }

// ImplConformance is one derive declaration:
//
//	derive Equatable for Point
//
// Manual implementations are written as `impl Iface for Type { ... }` blocks.
// Interface is the base-name interface type-expr (`Iter`, never `Iter<T>` —
// type args are rejected at parse). Interfaces is retained for internal callers
// that construct legacy multi-interface nodes; source derive declarations carry
// exactly one interface. Receiver is the type being derived for. Derive
// requests the structurally-synthesized impl and has no method body. Options is
// the optional compile-time codegen configuration expression in
// `derive Iface for Type with Options{...}`.
type ImplConformance struct {
	TriviaCarrier
	SpanCarrier
	AttachedTests []AttachedTest // prompt tests attached to this declaration
	Interface     TypeExpr       // base-name interface type-expr
	Interfaces    []TypeExpr     // optional internal list; source uses one interface
	Receiver      TypeExpr       // receiver type in `derive ... for Receiver`
	Options       Node           // optional expression after `with`
	Generics      []TypeParam    // optional derive header generics
	WhereClauses  []WhereConstraint
	Derive        bool // true for `derive Iface`
	Doc           string
	Line          int
	Col           int
}

func (*ImplConformance) NodeType() string { return "ImplConformance" }
func (n *ImplConformance) LineNum() int   { return n.Line }

// Decorator is a compiler-internal directive on a type declaration. Source
// has no decorators (the parser rejects `@name`); the derive lowering
// (analysis.LowerDerives) turns each `derive Iface for Type` declaration into
// a `derive` Decorator on its type, whose Args are the interface TypeExprs.
type Decorator struct {
	TriviaCarrier
	SpanCarrier
	Name    string // The decorator name without the leading `@` ("derive")
	Args    []Node // Each arg is a TypeExpr or *Ident depending on the decorator
	Options Node   // internal derive options carried from `derive ... with ...`
	Line    int
	Col     int
}

func (*Decorator) NodeType() string { return "Decorator" }
func (n *Decorator) LineNum() int   { return n.Line }

// AttachedTest is a prompt test associated with the declaration that follows
// it. It is a real runnable test body, not a doc-comment convention.
type AttachedTest struct {
	TriviaCarrier
	SpanCarrier
	Body                 *Block
	Kind                 string // "test", "assert", or "refute"
	Inline               bool
	DocAfter             string // doc comments written after this attached test and before the declaration
	After                []AttachedTestAfter
	TrailingPromptBlanks int
	Line                 int
	Col                  int
	EndLine              int
	EndCol               int
}

func (*AttachedTest) NodeType() string { return "AttachedTest" }
func (n *AttachedTest) LineNum() int   { return n.Line }

type AttachedTestAfter struct {
	IsDoc  bool
	Doc    string
	Trivia Trivia
}

// TestClockVariant reads the variant out of a `clock …` declaration on a
// `tests` group, accepting every spelling that names one: the `.Virtual`
// shorthand and the qualified `Clock.Virtual` / `testing.Clock.Virtual`.
//
// Reading it syntactically is safe because the analyzer has already
// type-checked the expression against `testing.Clock` (checker.checkTestClock)
// — so a qualified spelling needs std/testing imported, a misspelled variant
// is an unknown-variant error, and a non-clock expression is a type mismatch,
// all before this runs. What is left here is extracting a name the checker has
// already validated.
//
// It is READ rather than EVALUATED because the clock has to be known before a
// case runs, while `boot` and `setup` run inside it. Nothing evaluates the
// clause expression, so a builder that does not lower it is complete rather
// than partial.
//
// It lives here, and not beside either caller, because there are two callers
// and this rule decides observable behaviour: `internal/frontend` and
// `internal/irbuild` both read a group's clock from it. Two copies could
// disagree about which clock a group runs under, which is a wrong ANSWER and
// not a refusal. rt links no front end, so the rule cannot live there.
func TestClockVariant(n Node) (string, bool) {
	var name string
	switch v := n.(type) {
	case *DotVariant:
		name = v.Name
	case *FieldAccess:
		if v.Field == nil {
			return "", false
		}
		name = v.Field.Name
	default:
		return "", false
	}
	if name != "Virtual" && name != "System" {
		return "", false
	}
	return name, true
}

// AttachedTestsOf returns the attached tests a declaration carries, or nil for
// a node that cannot carry any.
//
// The field is repeated across every declaration kind rather than living on a
// shared embedded type, so reading it is a type switch. This is that switch,
// once: the analyzer, the doc generator, and the IR builder all need the same
// answer, and separate copies would drift the moment a declaration kind gained
// attached-test support.
func AttachedTestsOf(n Node) []AttachedTest {
	switch v := n.(type) {
	case *FuncDef:
		return v.AttachedTests
	case *ExternFunc:
		return v.AttachedTests
	case *StructDef:
		return v.AttachedTests
	case *EnumDef:
		return v.AttachedTests
	case *TypeDef:
		return v.AttachedTests
	case *ExternType:
		return v.AttachedTests
	case *TypeAlias:
		return v.AttachedTests
	case *InterfaceDef:
		return v.AttachedTests
	case *ImplBlock:
		return v.AttachedTests
	case *ImplConformance:
		return v.AttachedTests
	case *OnceBinding:
		return v.AttachedTests
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// Imports
// ---------------------------------------------------------------------------

// ImportStmt represents an import statement.
// ModulePath contains positioned nodes for each slash-separated path segment.
// Names contains positioned nodes for explicitly imported items. An empty Names
// slice is a file import: `import std/io` binds an imported file API object
// under the last path segment (`io`) unless ModuleAlias supplies a local alias.
// Import aliasing:
//   - ModuleAlias is set by file aliases: `import std/io as console`.
//   - Aliases is a parallel slice to Names (same length) used for selective
//     aliases: `import mod.{Name as Alias}`. Aliases[i] is nil when the i-th
//     selected name has no alias.
//
// Re-export modifiers:
//   - ExportAll is true when a line-level `export` follows a selective import:
//     `import path: a, b export` re-exports every selected item under its
//     imported name.
//   - ExportAlias is kept for recovery/internal compatibility but is not
//     produced by valid source syntax; line-level `export as` is rejected.
//   - ExportFlags is a parallel slice to Names (same length) for per-item
//     re-exports: `import mod.{a export, b}` sets ExportFlags[0] = true.
//   - ExportAliases is a parallel slice to Names (same length) for per-item
//     re-export renames: `import mod.{a export as A, b}` sets
//     ExportAliases[0] to the `A` ident. Nil entry = no rename on re-export.
type ImportStmt struct {
	TriviaCarrier
	SpanCarrier
	ModulePath          []Node // *Ident or *TypeIdent for each path segment
	Names               []Node // *Ident or *TypeIdent for each imported name
	Aliases             []Node // parallel to Names; nil entry = no alias for that name
	ModuleAlias         Node   // alias for empty-name file imports
	Extern              bool   // true for Go import entries: `go [alias] "import/path"`
	ExternPath          string // Go import path for Extern entries
	ExternPathLine      int
	ExternPathCol       int
	ExternAlias         string // inferred package handle, explicit alias, or "_" for side-effect imports
	ExternAliasLine     int
	ExternAliasCol      int
	ExternAliasExplicit bool
	ExportAll           bool   // true for line-level `export` on a selective import
	ExportAlias         Node   // not produced by valid source syntax
	ExportFlags         []bool // parallel to Names; true when item carries `export`
	ExportAliases       []Node // parallel to Names; nil entry = no rename on re-export
	// Braced is set for the selective-list forms — `mod.{A, B}` and the
	// drill-through `mod.Owner.{A, B}`. It distinguishes them from the
	// plain `mod: Owner.Name`, which names one thing rather than
	// navigating to it: with braces the owner segment is a path step the
	// reader can hover on its own, without them the whole dotted string
	// is a single name.
	Braced bool
	// IncludeParent is set when the selective brace list carries the
	// `self` marker — `std/foo.{self, X, Y}` lifts X and Y AND binds
	// the LHS (`foo` as module owner, or `Maybe` as enum for the drill-through
	// form `std/maybe.Maybe.{self, Some, None}`). `{self}` alone is allowed
	// and imports only that parent binding.
	IncludeParent bool
	// SelfLine / SelfCol record the source position of the `self` token
	// when IncludeParent is true. The analyzer registers a definition
	// entry at this position so LSP hover and go-to-definition on `self`
	// route to the LHS module/enum, mirroring the trailing path segment.
	// Both are zero when IncludeParent is false.
	SelfLine int
	SelfCol  int
	Line     int
	Col      int
}

func (*ImportStmt) NodeType() string { return "ImportStmt" }
func (n *ImportStmt) LineNum() int   { return n.Line }

// ImportBlock represents `import { entry1, entry2, ... }` — a block-form
// import that bundles multiple import entries into one statement. Each
// entry is an *ImportStmt with the same shape it would have if written
// standalone (module path plus explicit item selectors). The
// analyzer and runtime iterate Entries and process each like a regular
// import; the formatter preserves the block shape on output.
type ImportBlock struct {
	TriviaCarrier
	SpanCarrier
	Entries []*ImportStmt
	Go      bool
	// EndTrivia holds comments / blank-line trivia between the last entry and
	// the closing `}` (the analog of AnonStructType.EndTrivia). Without it a
	// trailing in-block comment has nowhere to live and would be dropped.
	EndTrivia []Trivia
	Line      int
	Col       int
}

func (*ImportBlock) NodeType() string { return "ImportBlock" }
func (n *ImportBlock) LineNum() int   { return n.Line }

// GoBlock is a top-level raw Go prelude for source-level FFI wrappers:
//
//	go {
//	  type Conn struct { db *sql.DB }
//	}
//
// Package handles use ExternPackage (`gopkg ...`). Any declarations left in a
// GoBlock are emitted beside inline wrapper functions.
type GoBlock struct {
	TriviaCarrier
	SpanCarrier
	Body     string
	BodyLine int
	BodyCol  int
	Line     int
	Col      int
}

func (*GoBlock) NodeType() string { return "GoBlock" }
func (n *GoBlock) LineNum() int   { return n.Line }

// ExternPackage declares a compile-time Go package handle for source-level
// FFI bindings:
//
//	gopkg "example.com/app/ffi" as ffi
//
// The alias is compile-time-only; it may be referenced by Go binding
// selectors, but it is not a runtime Nomi value.
type ExternPackage struct {
	TriviaCarrier
	SpanCarrier
	ImportPath     string
	ImportPathLine int
	ImportPathCol  int
	Alias          string
	Line           int
	Col            int
	AliasLine      int
	AliasCol       int
}

func (*ExternPackage) NodeType() string { return "ExternPackage" }
func (n *ExternPackage) LineNum() int   { return n.Line }

// ImportNodeName extracts the string name from an import node (*Ident or *TypeIdent).
func ImportNodeName(n Node) string {
	switch v := n.(type) {
	case *Ident:
		return v.Name
	case *TypeIdent:
		return v.Name
	default:
		return ""
	}
}

// ---------------------------------------------------------------------------
// Once bindings
// ---------------------------------------------------------------------------

// OnceBinding represents a module- or container-level `once name: T = expression`.
// The RHS is evaluated lazily on first access and cached forever.
type OnceBinding struct {
	TriviaCarrier
	SpanCarrier
	AttachedTests  []AttachedTest // prompt tests attached to this declaration
	Name           string
	Public         bool
	TypeAnnotation TypeExpr
	Value          Node
	Doc            string
	Line           int
	Col            int
}

func (*OnceBinding) NodeType() string { return "OnceBinding" }
func (n *OnceBinding) LineNum() int   { return n.Line }

// ---------------------------------------------------------------------------
// Functions
// ---------------------------------------------------------------------------

// Param represents a function parameter with optional type annotation and default.
type Param struct {
	Name           string
	Destructure    Node     // non-nil for destructuring params: pattern node (TuplePattern, StructPattern, etc.)
	TypeAnnotation TypeExpr // structured type annotation, nil if absent
	Default        Node     // default value expression, nil if absent
	Line           int
	Col            int
}

// FuncDef defines a named function.
type FuncDef struct {
	TriviaCarrier
	SpanCarrier
	AttachedTests  []AttachedTest // prompt tests attached to this declaration
	Decorators     []Decorator    // always nil: source has no decorators, and the derive lowering decorates types only
	Name           string
	Public         bool
	TypeParams     []TypeParam
	Params         []Param
	ReturnTypeExpr TypeExpr // structured return type annotation, nil if absent
	WhereClauses   []WhereConstraint
	Body           *Block
	Doc            string
	// AutoSynth marks an `impl Debug` synthesized by the universal-default
	// Debug pass (SynthesizeUniversalDebug → synthesizeAutoDebug), as opposed
	// to a hand-written `impl` or an explicit `@derive`. It lets the coherence
	// check resolve a `(Debug, T)` clash by precedence
	// — an explicit impl always wins over the auto one — so a type whose Debug
	// impl lives in a different file than its declaration doesn't collide with
	// the auto impl the per-file synthesis pass would otherwise emit.
	AutoSynth bool
	// ImplFunction is retained for legacy/synthesized ASTs. Source
	// implementation functions now live as plain `fn` items inside
	// `impl Iface for Type { ... }` blocks.
	ImplFunction bool
	// ImplLine / ImplCol record the `impl` keyword for ImplFunction nodes.
	// Line / Col still point at the function name, so editor hover needs this
	// separate source position to attach to the marker keyword itself.
	ImplLine int
	ImplCol  int
	// ImplIface records the interface this function implements, filled by
	// analysis for items inside `impl Iface for Type { ... }` blocks.
	ImplIface TypeExpr
	// ImplIfaceInferred is true when analysis filled ImplIface from a
	// synthesized/legacy inferred block.
	ImplIfaceInferred bool
	// ImplIfaceSourceQualified is retained for legacy ASTs. Source no longer
	// accepts qualified implementation function headers.
	ImplIfaceSourceQualified bool
	Line                     int
	Col                      int
}

func (*FuncDef) NodeType() string { return "FuncDef" }
func (n *FuncDef) LineNum() int   { return n.Line }

// ---------------------------------------------------------------------------
// Extern declarations (runtime-provided)
// ---------------------------------------------------------------------------

// ExternFunc declares a function provided by the runtime — no body.
type ExternFunc struct {
	TriviaCarrier
	SpanCarrier
	Name           string
	Public         bool
	AttachedTests  []AttachedTest // prompt tests attached to this declaration
	TypeParams     []TypeParam    // explicit `<T, U>` clause after the name; empty for the implicit-only form
	Params         []Param
	ReturnTypeExpr TypeExpr // structured return type annotation, nil if absent
	WhereClauses   []WhereConstraint
	Doc            string
	// ImplFunction mirrors FuncDef.ImplFunction for legacy/synthesized ASTs.
	ImplFunction bool
	// ImplLine / ImplCol mirror FuncDef.ImplLine / ImplCol.
	ImplLine int
	ImplCol  int
	// ImplIface records the interface this extern function implements. Mirrors
	// FuncDef.ImplIface.
	ImplIface TypeExpr
	// ImplIfaceInferred mirrors FuncDef.ImplIfaceInferred.
	ImplIfaceInferred bool
	// ImplIfaceSourceQualified mirrors FuncDef.ImplIfaceSourceQualified.
	ImplIfaceSourceQualified bool
	Line                     int
	Col                      int
	DefinitionFile           string
	DefinitionLine           int
	DefinitionCol            int
	DefinitionSpan           int
	ForeignAlias             string
	ForeignName              string
	ForeignAliasLine         int
	ForeignAliasCol          int
	ForeignNameLine          int
	ForeignNameCol           int
	GoBody                   string
	GoBodyLine               int
	GoBodyCol                int
}

func (*ExternFunc) NodeType() string { return "ExternFunc" }
func (n *ExternFunc) LineNum() int   { return n.Line }

// ImplMethodDecl is an impl-block method item — either a `fn` (*FuncDef) or an
// `host fn` (*ExternFunc). Both can be the concrete target an interface- or
// module-qualified call statically resolves to, so go-to-def / hover navigate
// to the impl's source rather than the abstract interface method. The marker
// keeps Symbol.DispatchImpl honest about which of the two it holds; consumers
// type-switch to read the (Name, Params, …) each exposes.
type ImplMethodDecl interface {
	Node
	isImplMethodDecl()
}

func (*FuncDef) isImplMethodDecl()    {}
func (*ExternFunc) isImplMethodDecl() {}

// ExternType declares a primitive type provided by the runtime. It may
// carry an optional `{ ... }` item body (type-body item syntax):
//
//	pub host type List<T> {
//	  fn map(list: List<T>, f: (T) -> U): List<U> { ... }
//	}
//
//	impl Iter for List<T> {
//	  host fn next(s: List<T>): Maybe<(T, List<T>)>
//	}
//
// A bodiless declaration (`pub host type Unit`) is unchanged.
type ExternType struct {
	TriviaCarrier
	SpanCarrier
	Name          string
	Public        bool
	Opaque        bool
	TypeParams    []TypeParam
	WhereClauses  []WhereConstraint
	AttachedTests []AttachedTest // prompt tests attached to this declaration
	Decorators    []Decorator    // `derive` decorators the derive lowering adds; nil when none
	// HasBody/Items are retained for compiler-synthesized or legacy internal
	// nodes. Source `host type` declarations do not carry bodies;
	// type-qualified functions and once bindings live in sibling
	// `impl Type { ... }` blocks.
	HasBody bool
	Items   []Node
	// EndTrivia holds trailing comments / blank lines between the last
	// body item and the closing `}`. Empty when HasBody is false.
	EndTrivia        []Trivia
	Doc              string
	Line             int
	Col              int
	EndLine          int // closing `}` when HasBody; zero otherwise
	EndCol           int
	DefinitionFile   string
	DefinitionLine   int
	DefinitionCol    int
	DefinitionSpan   int
	ForeignAlias     string
	ForeignName      string
	ForeignAliasLine int
	ForeignAliasCol  int
	ForeignNameLine  int
	ForeignNameCol   int
	GoBody           string
	GoBodyLine       int
	GoBodyCol        int
}

func (*ExternType) NodeType() string { return "ExternType" }
func (n *ExternType) LineNum() int   { return n.Line }

// Lambda is an anonymous function: { params -> body }
type Lambda struct {
	TriviaCarrier
	SpanCarrier
	Params  []Param
	Body    *Block
	Line    int
	Col     int
	EndLine int // one past the last token of the body; for ScopeAt span (fixes synthesized single-expr Body.EndLine == 0)
	EndCol  int
}

func (*Lambda) NodeType() string { return "Lambda" }
func (n *Lambda) LineNum() int   { return n.Line }

// Return is a return statement: return expr
type Return struct {
	TriviaCarrier
	SpanCarrier
	Value Node // nil for bare return
	Line  int
	Col   int
}

func (*Return) NodeType() string { return "Return" }
func (n *Return) LineNum() int   { return n.Line }

// Break is a break statement: break [expr]
type Break struct {
	TriviaCarrier
	SpanCarrier
	Value Node // nil for bare break
	Line  int
	Col   int
}

func (*Break) NodeType() string { return "Break" }
func (n *Break) LineNum() int   { return n.Line }

// Continue is a continue statement: continue
//
// Value is an expression written after `continue` on the same line. The
// language gives it no meaning (`continue` takes no value) and the checker
// rejects it; it is parsed so that `continue (i + 1, s)` is an error rather
// than a `continue` followed by an unreachable statement.
type Continue struct {
	TriviaCarrier
	SpanCarrier
	Value Node // nil for a well-formed continue
	Line  int
	Col   int
}

func (*Continue) NodeType() string { return "Continue" }
func (n *Continue) LineNum() int   { return n.Line }

// TryOp is the 'try' keyword. Expr is nil only for the pipe-stage form
// `value |> try`, where the piped value is supplied by the pipe's checker
// and IR builder.
type TryOp struct {
	TriviaCarrier
	SpanCarrier
	Expr Node
	Line int
	Col  int
}

func (*TryOp) NodeType() string { return "TryOp" }
func (n *TryOp) LineNum() int   { return n.Line }

// Placeholder represents _ in expressions (partial application).
type Placeholder struct {
	TriviaCarrier
	SpanCarrier
	Line int
	Col  int
}

func (*Placeholder) NodeType() string { return "Placeholder" }
func (n *Placeholder) LineNum() int   { return n.Line }

// NamedArg represents a named argument at a call site: name: expr
type NamedArg struct {
	TriviaCarrier
	SpanCarrier
	Name  string
	Value Node
	Line  int
	Col   int
}

func (*NamedArg) NodeType() string { return "NamedArg" }
func (n *NamedArg) LineNum() int   { return n.Line }

// ---------------------------------------------------------------------------
// Pattern matching
// ---------------------------------------------------------------------------

// Case is a case expression with optional match value.
type Case struct {
	TriviaCarrier
	SpanCarrier
	Value    Node // nil for ad-hoc conditionals
	Branches []CaseBranch
	Line     int
	Col      int
}

func (*Case) NodeType() string { return "Case" }
func (n *Case) LineNum() int   { return n.Line }

// CaseBranch is one branch of a case expression.
// It does NOT implement Node — it's a data struct used inside Case.
// It embeds TriviaCarrier so trivia can attach to the branch itself (for
// formatter-faithful output), without leaking formatter concerns into Pattern/Body.
type CaseBranch struct {
	TriviaCarrier
	SpanCarrier
	Pattern Node // For value matching: literal node, IdentPattern, WildcardPattern
	// For ad-hoc: an expression (the condition)
	Guard Node // when condition, nil if absent
	Body  Node // result expression
	Line  int
	Col   int
	// EndLine/EndCol mark one past the last token of the branch body, for the
	// ScopeAt span of the branch's pattern-binding scope.
	EndLine int
	EndCol  int
}

// WildcardPattern represents _ in a case pattern.
type WildcardPattern struct {
	TriviaCarrier
	SpanCarrier
	Line int
	Col  int
}

func (*WildcardPattern) NodeType() string { return "WildcardPattern" }
func (n *WildcardPattern) LineNum() int   { return n.Line }

// IdentPattern represents an identifier binding in a case pattern.
type IdentPattern struct {
	TriviaCarrier
	SpanCarrier
	Name string
	Line int
	Col  int
}

func (*IdentPattern) NodeType() string { return "IdentPattern" }
func (n *IdentPattern) LineNum() int   { return n.Line }

// EnumPattern matches an enum variant, optionally binding the inner value.
// Simple payload forms (ident binding, wildcard) populate Binding/BindingCol as a fast path.
// Complex nested patterns (tuple destructure, literal, nested variant) populate Payload instead.
// Invariant: at most one of {Binding,Payload} is set.
type EnumPattern struct {
	TriviaCarrier
	SpanCarrier
	Variant    TypeExpr // always present — SimpleType for bare or dotted variant names
	Binding    string   // variable name for inner value, empty for bare or when Payload is set
	BindingCol int      // 1-based column of the binding identifier
	Payload    Node     // nested pattern for destructure (TuplePattern, IntLit, EnumPattern, etc.)
	Line       int
	Col        int
}

func (*EnumPattern) NodeType() string { return "EnumPattern" }
func (n *EnumPattern) LineNum() int   { return n.Line }

// StructPatternField describes one field in a struct pattern.
type StructPatternField struct {
	Name        string // field name
	NameLine    int    // 1-based line of the field name token (0 if unknown)
	NameCol     int    // 1-based column of the field name token
	Binding     string // variable to bind to (same as Name for punning)
	BindingLine int    // 1-based line of the binding identifier
	BindingCol  int    // 1-based column of the binding identifier
	Pattern     Node   // nil for binding, literal node for value matching
}

// StructPattern matches a struct value by type name and field patterns.
type StructPattern struct {
	TriviaCarrier
	SpanCarrier
	TypeName TypeExpr // nil for anonymous struct patterns
	Fields   []StructPatternField
	Line     int
	Col      int
}

func (*StructPattern) NodeType() string { return "StructPattern" }
func (n *StructPattern) LineNum() int   { return n.Line }

// TuplePattern matches a tuple value in a case branch: (pattern, pattern, ...)
//
// Flat is true when the parser synthesized this TuplePattern from the
// flat-destructure shorthand `Foo(a, b, ...)` (no inner parens around the
// comma list). The comma-separated patterns sit directly inside a variant's
// argument list. This flag lets the type-checker accept the shape only for
// tuple-distinct constructors of arity ≥ 2 (where the parallel construction
// form is the literal-attach `Foo(1, "x")`) and reject it for variant
// patterns with tuple payloads, where `Foo((a, b))` remains the canonical
// form. For explicit `(a, b)` patterns Flat is false.
type TuplePattern struct {
	TriviaCarrier
	SpanCarrier
	Patterns []Node // each can be a literal, IdentPattern, WildcardPattern
	Flat     bool
	Line     int
	Col      int
}

func (*TuplePattern) NodeType() string { return "TuplePattern" }
func (n *TuplePattern) LineNum() int   { return n.Line }

// ListPattern matches a list in a case branch: [], [a], [a, b], [head, ..tail], [a, b, ..rest]
//
// TypeName is non-nil for type-prefixed list patterns like
// `Arr[1, 2, 3]` — the literal-attach destructure form for
// list-payload variants. nil for anonymous list patterns.
type ListPattern struct {
	TriviaCarrier
	SpanCarrier
	TypeName   TypeExpr // nil for anonymous list patterns
	Heads      []Node   // fixed element patterns before ..
	TailSpread Node     // pattern after .., nil if no spread (exact-length match)
	// EndTrivia holds trailing comments / blank lines between the last
	// head pattern and the closing `]`. Mirrors ListLit.EndTrivia.
	EndTrivia []Trivia
	Line      int
	Col       int
}

func (*ListPattern) NodeType() string { return "ListPattern" }
func (n *ListPattern) LineNum() int   { return n.Line }

// MapPatternEntry is one key => pattern pair in a map pattern.
type MapPatternEntry struct {
	Key     Node // literal key: IntLit, FloatLit, StringLit
	Pattern Node // value pattern: IdentPattern, WildcardPattern, literal, nested
}

// MapPattern matches a map in a case branch: {"key" => val, 0 => x}
// Matching is partial — extra keys in the map are ignored.
//
// TypeName is non-nil for type-prefixed map patterns like
// `Kvs{"a" => v}` — the literal-attach destructure form for
// map-distinct types. nil for anonymous map patterns.
type MapPattern struct {
	TriviaCarrier
	SpanCarrier
	TypeName TypeExpr // nil for anonymous map patterns
	Entries  []MapPatternEntry
	// EndTrivia holds trailing comments / blank lines between the last
	// entry and the closing `}`. Mirrors MapLit.EndTrivia.
	EndTrivia []Trivia
	Line      int
	Col       int
}

func (*MapPattern) NodeType() string { return "MapPattern" }
func (n *MapPattern) LineNum() int   { return n.Line }
