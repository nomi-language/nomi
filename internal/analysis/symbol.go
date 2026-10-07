package analysis

import "github.com/nomi-language/nomi/internal/ast"

// Pos is a 1-based source position (matching token.Token).
//
// File is the absolute path of the source file the position belongs to,
// or "" when the recorder didn't have a file path in scope (single-file
// BuildFileWithStdlib callers, stdlib synth bodies where the file is
// the embedded stdlib source, etc.). Existing `map[Pos]*Symbol` users
// (References / Definitions, populated by the builder for one file at
// a time) leave File as the zero value because the map's containing
// FileAnalysis already names the file. The field is non-zero on the
// Recordings stored in FileAnalysis.ImplManifest, where positions
// from different files end up unioned at the project level and the
// missing-impl diagnostic needs the filename to render an actionable
// location.
type Pos struct {
	File string
	Line int
	Col  int
}

// SymbolKind classifies what a symbol represents.
type SymbolKind int

const (
	SymbolFunction SymbolKind = iota
	SymbolStruct
	SymbolEnum
	SymbolEnumVariant
	SymbolType
	SymbolTypeAlias
	SymbolInterface
	SymbolParam
	SymbolBinding
	SymbolField
	SymbolModule
	// SymbolOnce represents a `once name: T = expression` module-level
	// binding — lazy memoized value. Distinct from SymbolBinding (local,
	// block-scoped) so hover, completion, and semantic-tokens can render
	// it appropriately.
	SymbolOnce
	SymbolInterfaceMethod
	// SymbolArgHint is a synthetic, hover-only Symbol attached to a
	// call-argument position (literals, partial-application placeholders)
	// to surface "this fills slot <name>: <type>" without claiming the
	// position represents a real binding. The renderer dispatches on this
	// kind explicitly; semantic-tokens and other "real symbol" consumers
	// skip it.
	SymbolArgHint
	// SymbolAssertion is a synthetic, hover-only Symbol attached to the
	// `assert`, `refute`, or `check` keyword. Keywords are not ordinary
	// declarations, so the checker records this marker with per-site type
	// information for editor hover.
	SymbolAssertion
	// SymbolTestSetup is a synthetic, hover-only Symbol attached to a
	// contextual `setup` keyword inside a `tests` block.
	SymbolTestSetup
	// SymbolTestDecl is a synthetic, hover-only Symbol attached to a `test`
	// or `tests` keyword.
	SymbolTestDecl
	// SymbolTryOp is a synthetic, hover-only Symbol attached to the `try`
	// prefix keyword. Carries the operand's input type
	// (Result<T,E> or Maybe<T>) and a description of the boundary the `try`
	// unwinds to (enclosing fn name, or "lambda"), so hover can render
	// the success type, the propagated Err / None branch, and the
	// boundary in one place. See ast.TryOp and analysis.TryOpInfo.
	SymbolTryOp
	// SymbolControlFlow is a synthetic, hover-only Symbol attached to
	// expression control-flow keywords such as `if` and `case`.
	SymbolControlFlow
	// SymbolImplKeyword is a synthetic, hover-only Symbol attached to an
	// `impl` keyword in a top-level impl block.
	SymbolImplKeyword
	// SymbolLiteral is a synthetic, hover-only Symbol attached to a literal
	// whose type its spelling does not name: a codepoint literal `'a'` is a
	// `Codepoint`. Name is the literal as written.
	SymbolLiteral
)

// TryOpInfo holds the per-site data a SymbolTryOp needs to render hover
// content. Populated by checker.checkTryOp before unwrapping the operand's
// type; consumed by lsp/hover.go's renderHover.
type TryOpInfo struct {
	// InputTy is the operand's static type before the `try` strips a layer
	// — Result<T,E> or Maybe<T>. The hover renderer splits this into the
	// success type and the Err / None branch.
	InputTy Type
	// Boundary names the enclosing fn or lambda the `try` unwinds to on
	// failure: "fn name" for an enclosing FuncDef, "lambda" inside a
	// lambda body. `return` follows the same scoping rule: Nomi has no
	// non-local returns.
	Boundary string
}

// AssertionInfo holds the per-site data a SymbolAssertion needs to render
// hover content. Populated by checker.checkAssertion after the subject type is
// known and before assertion/check success is returned.
type AssertionInfo struct {
	// Keyword is "assert", "refute", or "check".
	Keyword string
	// InputTy is the subject's static type before assertion-specific success
	// unwrapping.
	InputTy Type
	// SuccessTy is the value type produced when the assertion succeeds.
	SuccessTy Type
	// ResultTy is the full expression type. For `check`, this is
	// Result<SuccessTy, AssertionFailure>; for assert/refute it matches
	// SuccessTy.
	ResultTy Type
	// Boundary names the enclosing fn or lambda assert/refute unwind to on
	// failure. Empty for `check`, which returns Result.Err instead.
	Boundary string
}

// TestSetupInfo holds the per-site data a SymbolTestSetup needs to render
// hover content. InputTy is Unit when the setup expression does not declare a
// parent-context pattern.
type TestSetupInfo struct {
	InputTy  Type
	OutputTy Type
	Group    string
}

// TestDeclInfo holds the per-site data a SymbolTestDecl needs to render hover
// content.
type TestDeclInfo struct {
	Keyword    string
	Name       string
	InputTy    Type
	OutputTy   Type
	Group      bool
	HasSetup   bool
	HasContext bool
}

// ControlFlowInfo holds the per-site data a SymbolControlFlow needs to render
// hover content.
type ControlFlowInfo struct {
	Keyword      string
	InputTy      Type
	OutputTy     Type
	HasInput     bool
	PatternInput bool
}

// ImplInfo holds the per-site data a SymbolImplKeyword needs to render hover
// content without making `impl` behave like a regular definition/reference.
type ImplInfo struct {
	Interfaces      []string
	Receiver        string
	GenericHeader   string
	WhereClause     string
	FunctionName    string
	Extern          bool
	Inherent        bool
	SourceBlock     bool
	SourceQualified bool
}

// Symbol represents a named definition in the source.
type Symbol struct {
	Name   string
	Kind   SymbolKind
	Pos    Pos
	Public bool
	// Opaque is true when a distinct type was declared with the inline
	// `pub opaque type` (or bare `opaque type`) qualifier. The type name
	// is visible outside the owning module but the construction surface
	// (constructor, unwrap, destructuring) is not.
	Opaque     bool
	Doc        string             // from /// comments
	Node       ast.Node           // the AST node that defined this symbol
	SourceFile string             // file path where this symbol is defined (set for cross-file imports)
	Resolved   *Symbol            // for selective imports: the real symbol in the imported module
	Type       Type               // resolved type (filled by type resolution)
	Members    map[string]*Symbol // child symbols (e.g., interface methods)
	// DefinitionFile/Line/Col/Span override go-to-definition for symbols whose
	// Nomi declaration is synthetic but whose true editable source lives
	// elsewhere, such as generated host declarations for Go bindings.
	DefinitionFile string
	DefinitionLine int
	DefinitionCol  int
	DefinitionSpan int
	// TypeParamBounds holds the base names of a generic type parameter's
	// interface bounds (`["Display"]` for `where T: Display`). Set on the
	// type-param symbol in defineTypeParams; lets a `T.method` access record
	// its method-field reference against the bound interface's method symbol
	// (so hover / go-to-def work, the same as the interface-qualified form).
	TypeParamBounds []string
	// OwningInterface is the interface a SymbolInterfaceMethod belongs to
	// (`"Display"` for `Display.to_string`). Set on the method symbol in
	// defineInterface; hover surfaces it so a reader knows which interface a
	// dispatched method is contracted by.
	OwningInterface string
	// AppFieldOf is the application type an application-field read
	// (`MyApp.logger`) reads from. Set only on the per-site copy the checker
	// records at the field name.
	AppFieldOf      Type
	CallType        Type     // instantiated function type at a generic call site
	CallReceiver    string   // left-hand type owner for a type-qualified call site, e.g. `String` in `String.to_string`.
	ModuleScope     *Scope   // for SymbolModule: the qualified member scope used to resolve `alias.Member`
	Span            int      // hover-range length in source columns; 0 means use len(Name) for backward compatibility
	ParamNames      []string // parallel to Type.(*FuncType).Params; non-nil when the binding came from a partial application and we want hover to surface the original param names. Empty string for unknown slots.
	ReceiverDisplay string   // impl-block receiver spelling for hovers, e.g. Maybe<T>.
	// VariantType is the instantiated enum type of a payload-free variant of
	// a generic enum at a value site (`Tree.Leaf` in `[Tree.Leaf,
	// Tree.Node(2)]`), set on the per-site copy the checker records there.
	// Its type arguments are inference variables that resolve as the
	// enclosing expression is checked, so a reader reads it after checking.
	VariantType Type
	// ParamOf is the function, lambda or interface method whose DESTRUCTURED
	// parameter introduced this binding (`|(word, count)| count`), and nil for
	// every other symbol. The unused-binding check reports such a name as a
	// parameter.
	ParamOf ast.Node
	// PatternBody is the body a `case` arm, `else` arm, `if` condition or test
	// setup pattern that bound this name scopes over, and nil for every other
	// symbol. The unused-binding check does not report a name whose body has a
	// `todo` in it, as it does not for an unfinished function's parameter.
	PatternBody ast.Node
	// OwningType is the receiver type name for an impl-block function. Non-empty marks a type-promoted member:
	// the function is addressable through receiver-specific lookup and may also
	// be surfaced through the owning module's flattened API. Empty for ordinary
	// module-top-level functions.
	// The type-method table (FileAnalysis.TypeMethods)
	// is keyed by (OwningType, Name); this field lets a symbol reached by any
	// other path report which type it belongs to.
	OwningType string
	// IsImplMethod marks the symbol as an interface method registered by
	// the block form (`impl Iface for Type { fn name ... }`). Spec §13 allows multiple
	// types in the same module to implement the same interface — the
	// compiler dispatches based on the first argument's type. Same-name
	// impl methods at module level therefore must NOT trigger the
	// redeclaration check (they are dispatched via Impls, not by scope
	// lookup). checkRedeclareInScope consults this flag to allow such
	// overloads when the prior local symbol is also an impl method.
	IsImplMethod bool
	// ImplInterface names the implemented interface for a type-owned impl
	// method, e.g. "Display" for `impl Display for Int { fn to_string ... }`.
	// Empty for inherent methods.
	ImplInterface string
	// TryOp carries the per-site metadata for SymbolTryOp entries
	// (operand input type, boundary description). Nil for every other
	// symbol kind.
	TryOp *TryOpInfo
	// Assertion carries the per-site metadata for SymbolAssertion entries.
	// Nil for every other symbol kind.
	Assertion *AssertionInfo
	// TestSetup carries the per-site metadata for SymbolTestSetup entries.
	// Nil for every other symbol kind.
	TestSetup *TestSetupInfo
	// TestDecl carries the per-site metadata for SymbolTestDecl entries.
	// Nil for every other symbol kind.
	TestDecl *TestDeclInfo
	// ControlFlow carries the per-site metadata for SymbolControlFlow entries.
	// Nil for every other symbol kind.
	ControlFlow *ControlFlowInfo
	// Impl carries the per-site metadata for SymbolImplKeyword entries.
	// Nil for every other symbol kind.
	Impl *ImplInfo
	// DispatchImpl is the concrete impl method an interface-qualified
	// call (`Display.to_string(x)`) statically resolves to, recorded by
	// the checker on a per-call-site PROXY of the interface-method
	// reference when the receiver type is concrete and exactly one impl
	// matches. It is the impl method (`fn` or `host fn`) the runtime
	// dispatch will actually invoke. The LSP prefers it for both go-to-def
	// (jump to the impl's source) and hover (render the impl's concrete
	// signature) so the two agree on where the call lands; both fall back to
	// the interface method contract when it is nil (generic / existential
	// receiver, ambiguous match, or a `@derive`-synthesized impl with no real
	// source position). Never set on the shared interface-method symbol —
	// that would leak the last call site's target to every other site.
	DispatchImpl     ast.ImplMethodDecl
	DispatchReceiver Type // concrete receiver type paired with DispatchImpl; used for hovers.
	// ReturnSelf is the type `self` was solved to at an interface-qualified
	// call whose method declares self only in its return
	// (`FromJson.from_json(json)` at `Result<List<Note>, _>`), when no type
	// argument named it. No argument carries that type, so this is the only
	// record of which implementation the call names. Set on the per-site
	// copy at the method's position, never on the shared symbol.
	ReturnSelf Type
}
