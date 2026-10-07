package analysis

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"strings"
	"testing"
)

func TestBuildScopes_Function(t *testing.T) {
	input := `pub fn add(x: Int, y: Int): Int { x + y }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	sym := file.ModuleScope.Lookup("add")
	if sym == nil {
		t.Fatal("expected to find symbol add in module scope")
	}
	if sym.Kind != SymbolFunction {
		t.Errorf("expected SymbolFunction, got %v", sym.Kind)
	}
	if !sym.Public {
		t.Error("expected add to be public")
	}
}

func TestBuildScopes_FunctionParams(t *testing.T) {
	input := `fn add(x: Int, y: Int): Int { x + y }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	// Params should NOT be in module scope
	if file.ModuleScope.LookupLocal("x") != nil {
		t.Error("x should not be in module scope")
	}
}

func TestBuildScopes_Binding(t *testing.T) {
	input := `fn main(): Unit {
    x = 42
    y = x + 1
    y
}`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	if file.ModuleScope.Lookup("main") == nil {
		t.Fatal("expected main in module scope")
	}
	if file.ModuleScope.LookupLocal("x") != nil {
		t.Error("x should not be in module scope")
	}
}

func TestBuildScopes_Struct(t *testing.T) {
	input := `struct User { name: String; age: Int }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	sym := file.ModuleScope.Lookup("User")
	if sym == nil {
		t.Fatal("expected to find User")
	}
	if sym.Kind != SymbolStruct {
		t.Errorf("expected SymbolStruct, got %v", sym.Kind)
	}
}

func TestBuildScopes_Enum(t *testing.T) {
	input := `enum Shape { Circle Float; Rectangle{width: Float, height: Float}; Point }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	sym := file.ModuleScope.Lookup("Shape")
	if sym == nil {
		t.Fatal("expected to find Shape")
	}
	if sym.Kind != SymbolEnum {
		t.Errorf("expected SymbolEnum, got %v", sym.Kind)
	}

	// Variants should be in module scope per Nomi spec
	for _, name := range []string{"Circle", "Rectangle", "Point"} {
		v := file.ModuleScope.Lookup(name)
		if v == nil {
			t.Errorf("expected variant %s in module scope", name)
		} else if v.Kind != SymbolEnumVariant {
			t.Errorf("expected SymbolEnumVariant for %s, got %v", name, v.Kind)
		}
	}
}

func TestBuildScopes_TypeDef(t *testing.T) {
	input := `type UserId Int`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	sym := file.ModuleScope.Lookup("UserId")
	if sym == nil {
		t.Fatal("expected to find UserId")
	}
	if sym.Kind != SymbolType {
		t.Errorf("expected SymbolType, got %v", sym.Kind)
	}
}

func TestBuildScopes_Interface(t *testing.T) {
	input := `interface Display { fn to_string(value: self): String }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	sym := file.ModuleScope.Lookup("Display")
	if sym == nil {
		t.Fatal("expected to find Display")
	}
	if sym.Kind != SymbolInterface {
		t.Errorf("expected SymbolInterface, got %v", sym.Kind)
	}
}

func TestBuildScopes_InterfaceMethodMembers(t *testing.T) {
	input := "interface Display {\n" +
		"  /// Renders the value for a human.\n" +
		"  fn to_string(value: self): String\n" +
		"}"
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	ifaceSym := file.ModuleScope.Lookup("Display")
	if ifaceSym == nil {
		t.Fatal("expected to find Display")
	}
	if ifaceSym.Members == nil {
		t.Fatal("expected Members map on interface symbol")
	}
	methodSym, ok := ifaceSym.Members["to_string"]
	if !ok {
		t.Fatal("expected to find to_string in Members")
	}
	if methodSym.Kind != SymbolInterfaceMethod {
		t.Errorf("expected SymbolInterfaceMethod, got %v", methodSym.Kind)
	}
	if _, ok := methodSym.Node.(*ast.InterfaceMethod); !ok {
		t.Errorf("expected Node to be *ast.InterfaceMethod, got %T", methodSym.Node)
	}
	// Symbol.Doc is this package's recorded doc for a declaration, and
	// interface methods were the one kind that left it empty while the
	// parser kept the text on the node. Nothing downstream read it through
	// the symbol at the time this line was added — hover and signature help
	// both reach for the AST node — so this is the assertion that keeps the
	// field honest for the kind rather than a proxy for a consumer.
	if methodSym.Doc != "Renders the value for a human." {
		t.Errorf("expected the method's /// doc on its symbol, got %q", methodSym.Doc)
	}
}

func TestBuildScopes_InterfaceQualifiedReference(t *testing.T) {
	input := `interface Greeting {
	fn greet(value: self): String
}
fn main() {
	Greeting.greet("hello")
}`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	// The "greet" in Greeting.greet should be a reference to the method symbol
	foundRef := false
	for _, sym := range file.References {
		if sym.Name == "greet" && sym.Kind == SymbolInterfaceMethod {
			foundRef = true
			break
		}
	}
	if !foundRef {
		t.Error("expected a reference to interface method 'greet'")
	}
}

func TestBuildScopes_InterfaceQualifiedReference_CrossModule(t *testing.T) {
	// Simulate cross-module: Greeting is an imported symbol with Resolved pointing
	// to the real interface symbol that has Members.
	realIfaceSrc := `interface Greeting {
	fn greet(value: self): String
}`
	realTokens := lexer.Lex(realIfaceSrc)
	realNodes, _ := parser.ParseWithRecovery(realTokens)
	realFile := BuildFile(realNodes)
	realSym := realFile.ModuleScope.LookupLocal("Greeting")
	if realSym == nil {
		t.Fatal("expected real Greeting symbol")
	}

	// Build the "importing" file with a primitives scope containing the imported symbol
	importScope := NewScope(nil)
	importedSym := &Symbol{
		Name:     "Greeting",
		Kind:     SymbolInterface, // inherits kind from real
		Pos:      Pos{Line: 1, Col: 1},
		Resolved: realSym,
	}
	importScope.Define(importedSym)

	callSrc := `fn main() {
	Greeting.greet("hello")
}`
	callTokens := lexer.Lex(callSrc)
	callNodes, _ := parser.ParseWithRecovery(callTokens)
	file := BuildFileWithStdlib(callNodes, importScope, nil, "", nil)

	foundRef := false
	for _, sym := range file.References {
		if sym.Name == "greet" && sym.Kind == SymbolInterfaceMethod {
			foundRef = true
			break
		}
	}
	if !foundRef {
		t.Error("expected a reference to interface method 'greet' via imported interface")
	}
}

func TestBuildScopes_References(t *testing.T) {
	input := `fn double(x: Int): Int { x + x }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	// Should have references to "x" that resolve to the parameter
	foundRef := false
	for _, sym := range file.References {
		if sym.Name == "x" && sym.Kind == SymbolParam {
			foundRef = true
			break
		}
	}
	if !foundRef {
		t.Error("expected at least one reference to param x")
	}
}

func TestBuildScopes_ForwardReference(t *testing.T) {
	// helper is defined after use — should still resolve
	input := `fn run(): Int { helper() }
fn helper(): Int { 42 }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	// "helper" used in Use's body should resolve
	foundRef := false
	for _, sym := range file.References {
		if sym.Name == "helper" && sym.Kind == SymbolFunction {
			foundRef = true
			break
		}
	}
	if !foundRef {
		t.Error("expected forward reference to helper to resolve")
	}
}

func TestScope_AllVisible(t *testing.T) {
	parent := NewScope(nil)
	parent.Define(&Symbol{Name: "a", Kind: SymbolBinding})
	parent.Define(&Symbol{Name: "b", Kind: SymbolBinding})

	child := NewScope(parent)
	child.Define(&Symbol{Name: "c", Kind: SymbolBinding})
	child.Define(&Symbol{Name: "a", Kind: SymbolBinding}) // shadows parent's "a"

	visible := child.AllVisible()
	names := make(map[string]bool)
	for _, sym := range visible {
		names[sym.Name] = true
	}

	if len(visible) != 3 {
		t.Errorf("expected 3 visible symbols, got %d", len(visible))
	}
	for _, name := range []string{"a", "b", "c"} {
		if !names[name] {
			t.Errorf("expected %s to be visible", name)
		}
	}
}

func TestPrimitivesScope_SymbolsVisible(t *testing.T) {
	primitives := NewScope(nil)
	primitives.Define(&Symbol{Name: "Int", Kind: SymbolType, Pos: Pos{Line: 1, Col: 1}})
	primitives.Define(&Symbol{Name: "Some", Kind: SymbolEnumVariant, Pos: Pos{Line: 10, Col: 1}})

	input := `x = Some(42)`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlib(nodes, primitives, nil, "", nil)

	found := false
	for _, sym := range file.References {
		if sym.Name == "Some" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected Some to resolve through primitives scope")
	}
}

func TestModuleQualifiedResolution(t *testing.T) {
	ioScope := NewScope(nil)
	ioScope.Define(&Symbol{
		Name: "inspect",
		Kind: SymbolFunction,
		Pos:  Pos{Line: 5, Col: 1},
	})
	modules := map[string]*Scope{"io": ioScope}

	// Mirror an explicit/prelude whole-file import: `io` is reachable
	// through the file's parent scope as a SymbolModule. Qualified access in
	// user code requires scope-chain visibility — `b.modules` is no longer a
	// silent fallback.
	primitives := NewScope(nil)
	primitives.Define(&Symbol{
		Name:        "io",
		Kind:        SymbolModule,
		ModuleScope: ioScope,
		Pos:         Pos{Line: 1, Col: 1},
	})

	input := `io.inspect(42)`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlib(nodes, primitives, modules, "", nil)

	found := false
	for _, sym := range file.References {
		if sym.Name == "inspect" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected io.inspect to resolve to stdlib symbol")
	}
}

func TestImplicitSelfModuleAliasResolvesFileMembers(t *testing.T) {
	input := `
pub fn answer(): Int { 42 }

fn demo(): Int {
  sample.answer()
}`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlibAtPath(nodes, nil, nil, "", nil, "/tmp/sample.nomi")

	self := file.ModuleScope.LookupLocal("sample")
	if self == nil {
		t.Fatal("expected implicit self module alias")
	}
	if self.Kind != SymbolModule {
		t.Fatalf("expected self alias to be a module symbol, got %v", self.Kind)
	}
	if self.ModuleScope != file.ModuleScope {
		t.Fatal("expected self alias to resolve through the file module scope")
	}

	found := false
	for _, sym := range file.References {
		if sym.Name == "answer" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected sample.answer to resolve to the file member")
	}
}

func TestImplicitSelfModuleAliasYieldsToTopLevelDeclaration(t *testing.T) {
	input := `fn main(): Unit { Unit }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlibAtPath(nodes, nil, nil, "", nil, "/tmp/main.nomi")

	sym := file.ModuleScope.LookupLocal("main")
	if sym == nil {
		t.Fatal("expected main function symbol")
	}
	if sym.Kind != SymbolFunction {
		t.Fatalf("expected main to remain the function declaration, got %v", sym.Kind)
	}
}

func TestExternFuncInScope(t *testing.T) {
	input := `host fn print(value: String) -> Unit`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	sym := file.ModuleScope.Lookup("print")
	if sym == nil {
		t.Fatal("expected print in scope")
	}
	if sym.Kind != SymbolFunction {
		t.Errorf("expected SymbolFunction, got %v", sym.Kind)
	}
}

func TestExternTypeInScope(t *testing.T) {
	input := `host type Int`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	sym := file.ModuleScope.Lookup("Int")
	if sym == nil {
		t.Fatal("expected Int in scope")
	}
	if sym.Kind != SymbolType {
		t.Errorf("expected SymbolType, got %v", sym.Kind)
	}
}

func TestTypeExprResolution_StructField(t *testing.T) {
	input := "host type Int\nstruct Point { x: Int; y: Int }"
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	count := 0
	for _, sym := range file.References {
		if sym.Name == "Int" {
			count++
		}
	}
	if count < 2 {
		t.Errorf("expected at least 2 Int references in struct fields, got %d", count)
	}
}

func TestTypeExprResolution_GenericType(t *testing.T) {
	primitives := NewScope(nil)
	primitives.Define(&Symbol{Name: "List", Kind: SymbolType, Pos: Pos{Line: 1, Col: 1}})
	primitives.Define(&Symbol{Name: "Int", Kind: SymbolType, Pos: Pos{Line: 2, Col: 1}})

	input := "fn foo(x: List<Int>): Int { x }"
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlib(nodes, primitives, nil, "", nil)

	foundList := false
	intCount := 0
	for _, sym := range file.References {
		if sym.Name == "List" {
			foundList = true
		}
		if sym.Name == "Int" {
			intCount++
		}
	}
	if !foundList {
		t.Error("expected List reference in generic type")
	}
	if intCount < 2 {
		t.Errorf("expected at least 2 Int references (param + return), got %d", intCount)
	}
}

func TestTypeExprResolution_FuncType(t *testing.T) {
	primitives := NewScope(nil)
	primitives.Define(&Symbol{Name: "Int", Kind: SymbolType, Pos: Pos{Line: 1, Col: 1}})
	primitives.Define(&Symbol{Name: "String", Kind: SymbolType, Pos: Pos{Line: 2, Col: 1}})

	input := "fn apply(f: (Int) -> String): String { f(1) }"
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlib(nodes, primitives, nil, "", nil)

	foundInt := false
	stringCount := 0
	for _, sym := range file.References {
		if sym.Name == "Int" {
			foundInt = true
		}
		if sym.Name == "String" {
			stringCount++
		}
	}
	if !foundInt {
		t.Error("expected Int reference inside function type param")
	}
	if stringCount < 2 {
		t.Errorf("expected at least 2 String references (fn return + outer return), got %d", stringCount)
	}
}

func TestTypeExprResolution_EnumVariantField(t *testing.T) {
	input := "enum Bool { True; False }\nenum Error {\n    HttpError{retryable: Bool}\n}"
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	found := false
	for _, sym := range file.References {
		if sym.Name == "Bool" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected Bool reference in struct variant field type")
	}
}

func TestEnumPatternVariantReference(t *testing.T) {
	primitives := NewScope(nil)
	primitives.Define(&Symbol{Name: "Some", Kind: SymbolEnumVariant, Pos: Pos{Line: 10, Col: 1}})
	primitives.Define(&Symbol{Name: "None", Kind: SymbolEnumVariant, Pos: Pos{Line: 11, Col: 1}})

	input := "x = case Some(1) {\n  Some(v) -> v\n  None -> 0\n}"
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlib(nodes, primitives, nil, "", nil)

	// The variant name "Some" in the pattern should be a reference
	foundSome := false
	foundNone := false
	for pos, sym := range file.References {
		if sym.Name == "Some" && pos.Line == 2 {
			foundSome = true
		}
		if sym.Name == "None" && pos.Line == 3 {
			foundNone = true
		}
	}
	if !foundSome {
		t.Error("expected Some variant in case pattern to register as reference")
	}
	if !foundNone {
		t.Error("expected None variant in case pattern to register as reference")
	}
}

func TestBuildScopes_ImportSelective(t *testing.T) {
	input := `import models.{User, Point}`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	user := file.ModuleScope.Lookup("User")
	if user == nil {
		t.Fatal("expected to find User in module scope")
	}
	if user.Kind != SymbolBinding {
		t.Errorf("expected SymbolBinding for User, got %v", user.Kind)
	}

	point := file.ModuleScope.Lookup("Point")
	if point == nil {
		t.Fatal("expected to find Point in module scope")
	}

	// Module path should NOT be defined in scope (models is not bound)
	if file.ModuleScope.LookupLocal("models") != nil {
		t.Error("module path 'models' should not be in scope for selective imports")
	}
}

func TestCrossFile_NamespaceImportResolvesQualifiedAccess(t *testing.T) {
	mathSrc := `/// Doubles a number.
pub fn double(n: Int): Int { n + n }`
	mathTokens := lexer.Lex(mathSrc)
	mathNodes, _ := parser.ParseWithRecovery(mathTokens)

	loader := func(root string, modPath []string) ([]ast.Node, error) {
		if len(modPath) == 1 && modPath[0] == "math" {
			return mathNodes, nil
		}
		return nil, fmt.Errorf("module not found: %s", strings.Join(modPath, "."))
	}

	src := "import math\nfn main() { math.double(21) }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	var doubleSym *Symbol
	for _, sym := range file.References {
		if sym.Name == "double" {
			doubleSym = sym
			break
		}
	}
	if doubleSym == nil {
		t.Fatal("reference to double not found")
	}
	if _, ok := doubleSym.Node.(*ast.FuncDef); !ok {
		t.Errorf("expected double to resolve to *ast.FuncDef, got %T", doubleSym.Node)
	}
	if doubleSym.Doc != "Doubles a number." {
		t.Errorf("expected doc 'Doubles a number.', got %q", doubleSym.Doc)
	}
}

func TestCrossFile_SelectiveImportResolvesKindAndDoc(t *testing.T) {
	mathSrc := "/// Doubles a number.\nfn double(n: Int): Int { n + n }"
	mathTokens := lexer.Lex(mathSrc)
	mathNodes, _ := parser.ParseWithRecovery(mathTokens)

	loader := func(root string, modPath []string) ([]ast.Node, error) {
		if len(modPath) == 1 && modPath[0] == "math" {
			return mathNodes, nil
		}
		return nil, fmt.Errorf("module not found: %s", strings.Join(modPath, "."))
	}

	src := "import math.{double}"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	sym := file.ModuleScope.Lookup("double")
	if sym == nil {
		t.Fatal("expected double in module scope")
	}
	if sym.Resolved == nil {
		t.Fatal("expected Resolved to point to real symbol")
	}
	if sym.Resolved.Kind != SymbolFunction {
		t.Errorf("expected SymbolFunction, got %v", sym.Resolved.Kind)
	}
	if sym.Resolved.Doc != "Doubles a number." {
		t.Errorf("expected doc 'Doubles a number.', got %q", sym.Resolved.Doc)
	}
}

func TestCrossFile_CycleDetection(t *testing.T) {
	// Module A imports module B which imports module A — should not infinite loop
	loader := func(root string, modPath []string) ([]ast.Node, error) {
		name := strings.Join(modPath, ".")
		var src string
		switch name {
		case "a":
			src = "import b\npub fn from_a(): Int { 1 }"
		case "b":
			src = "import a\npub fn from_b(): Int { 2 }"
		default:
			return nil, fmt.Errorf("not found: %s", name)
		}
		tokens := lexer.Lex(src)
		nodes, _ := parser.ParseWithRecovery(tokens)
		return nodes, nil
	}

	src := "import a\nfn main() { a.from_a() }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	// Should not hang or panic
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)
	if file == nil {
		t.Fatal("expected non-nil file analysis")
	}
}

func TestCrossFile_NilLoaderFallsBack(t *testing.T) {
	src := "import math: math\nfn main() { Unit }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	// nil loader — should behave like before (no resolution, no crash)
	file := BuildFileWithStdlib(nodes, nil, nil, "", nil)
	if file == nil {
		t.Fatal("expected non-nil file analysis")
	}
}

func TestCrossFile_TransitiveImport(t *testing.T) {
	helpersSrc := "pub fn square(n: Int): Int { n * n }"
	helpersTokens := lexer.Lex(helpersSrc)
	helpersNodes, _ := parser.ParseWithRecovery(helpersTokens)

	mathSrc := "import helpers\npub fn double(n: Int): Int { n + n }"
	mathTokens := lexer.Lex(mathSrc)
	mathNodes, _ := parser.ParseWithRecovery(mathTokens)

	loader := func(root string, modPath []string) ([]ast.Node, error) {
		switch modPath[0] {
		case "math":
			return mathNodes, nil
		case "helpers":
			return helpersNodes, nil
		}
		return nil, fmt.Errorf("not found")
	}

	src := "import math\nfn main() { math.double(21) }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	var doubleSym *Symbol
	for _, sym := range file.References {
		if sym.Name == "double" {
			doubleSym = sym
			break
		}
	}
	if doubleSym == nil {
		t.Fatal("double not resolved through transitive import")
	}
	if _, ok := doubleSym.Node.(*ast.FuncDef); !ok {
		t.Errorf("expected *ast.FuncDef, got %T", doubleSym.Node)
	}
}

func TestCrossFile_CacheHit(t *testing.T) {
	callCount := 0
	mathSrc := "fn double(n: Int): Int { n + n }"
	mathTokens := lexer.Lex(mathSrc)
	mathNodes, _ := parser.ParseWithRecovery(mathTokens)

	loader := func(root string, modPath []string) ([]ast.Node, error) {
		if modPath[0] == "math" {
			callCount++
			return mathNodes, nil
		}
		return nil, fmt.Errorf("not found")
	}

	src := "import math\nimport math.{double}"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	if callCount != 1 {
		t.Errorf("expected loader called once, got %d", callCount)
	}
}

// Same-file bare struct-variant construction is rejected just like
// positional / nullary forms. The qualified-variant rule applies
// uniformly: `enum Error { HttpError{status: Int} | ... }` requires
// `Error.HttpError{status: 404}`, never bare `HttpError{status: 404}`.
// Without this check, the StructLit path slipped past the existing
// TypeIdent-position rule and the user got a confusing "(Int, String)
// -> Error" function-type mismatch instead of the qualified-form hint.
func TestSameFile_BareStructVariantConstructionRejected(t *testing.T) {
	src := `enum Error {
  HttpError{status: Int, message: String}
  Timeout
}

fn make(): Error { HttpError{status: 404, message: "x"} }`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)
	BuildTypes(file, nodes)
	errs := CheckTypes(file, nodes)

	all := append([]TypeError{}, file.TypeErrors...)
	all = append(all, errs...)

	found := false
	for _, e := range all {
		if strings.Contains(e.Message, "HttpError") &&
			strings.Contains(e.Message, "Error.HttpError") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an error suggesting Error.HttpError for bare HttpError{...}, got: %v", all)
	}
}

// Same-file bare variant construction is rejected with a clear error
// that suggests the qualified form. Defining `enum Shape { Circle Float; variant ... }`
// in the same file as `c = Circle(5.0)` does NOT make `Circle`
// a bare-callable constructor. Variants are reachable only through the
// enum (`Shape.Circle(5.0)`) or via drill-through import. Prelude
// variants (Some/None/Ok/Err/True/False) remain bare anywhere.
func TestSameFile_BareVariantConstructionRejected(t *testing.T) {
	src := `enum Shape { Circle Float
  Empty }

fn make(): Shape { Circle(5.0) }`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)
	BuildTypes(file, nodes)
	errs := CheckTypes(file, nodes)

	all := append([]TypeError{}, file.TypeErrors...)
	all = append(all, errs...)

	found := false
	for _, e := range all {
		if strings.Contains(e.Message, "Circle") &&
			strings.Contains(e.Message, "Shape.Circle") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an error suggesting Shape.Circle for bare Circle, got: %v", all)
	}
}

// Drill-through import: `import shape.Shape.{Circle, Empty}` lifts
// the named variants into the consumer's scope as bindings whose
// Resolved fields point at the real variant symbols. The trailing
// `Shape` segment is the enum (a TypeIdent) within the `shape` module,
// not a sub-module path.
func TestImport_DrillThroughLiftsVariants(t *testing.T) {
	shapeSrc := `pub enum Shape { Circle Float
  Empty }`
	shapeTokens := lexer.Lex(shapeSrc)
	shapeNodes, _ := parser.ParseWithRecovery(shapeTokens)

	loader := func(root string, modPath []string) ([]ast.Node, error) {
		if len(modPath) == 1 && modPath[0] == "shape" {
			return shapeNodes, nil
		}
		return nil, fmt.Errorf("not found: %s", strings.Join(modPath, "."))
	}

	src := `import shape.Shape.{Circle, Empty}

once a = Circle(1.0)
once b = Empty`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	if len(file.TypeErrors) != 0 {
		t.Fatalf("unexpected TypeErrors: %v", file.TypeErrors)
	}

	circleSym := file.ModuleScope.Lookup("Circle")
	if circleSym == nil {
		t.Fatal("expected `Circle` to be defined in module scope")
	}
	if circleSym.Resolved == nil {
		t.Fatal("expected `Circle` to resolve to a real symbol")
	}
	if circleSym.Resolved.Kind != SymbolEnumVariant {
		t.Errorf("expected Circle.Resolved.Kind == SymbolEnumVariant, got %v", circleSym.Resolved.Kind)
	}
	emptySym := file.ModuleScope.Lookup("Empty")
	if emptySym == nil {
		t.Fatal("expected `Empty` to be defined")
	}
	if emptySym.Resolved == nil || emptySym.Resolved.Kind != SymbolEnumVariant {
		t.Error("expected `Empty` to resolve to a variant symbol")
	}
}

func TestImport_DrillThroughLiftsVariantsFromDottedOwner(t *testing.T) {
	jsonSrc := `pub enum Json { Null }
pub enum Json.Case { Camel
  Snake }`
	jsonNodes, _ := parser.ParseWithRecovery(lexer.Lex(jsonSrc))

	loader := func(root string, modPath []string) ([]ast.Node, error) {
		if len(modPath) == 1 && modPath[0] == "json" {
			return jsonNodes, nil
		}
		return nil, fmt.Errorf("not found: %s", strings.Join(modPath, "."))
	}

	src := `import json.Json.{self, Case.Camel}

once j = Json.Null
once c = Camel`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	for _, err := range file.TypeErrors {
		if err.Code == UnusedImportCode || strings.Contains(err.Message, "imported name 'Case.Camel' is unused") {
			continue
		}
		t.Fatalf("unexpected TypeError: %v", err)
	}

	camelSym := file.ModuleScope.Lookup("Camel")
	if camelSym == nil {
		t.Fatal("expected `Camel` to be defined in module scope")
	}
	if camelSym.Resolved == nil {
		t.Fatal("expected `Camel` to resolve to a real symbol")
	}
	if camelSym.Resolved.Kind != SymbolEnumVariant {
		t.Errorf("expected Camel.Resolved.Kind == SymbolEnumVariant, got %v", camelSym.Resolved.Kind)
	}
	jsonSym := file.ModuleScope.Lookup("Json")
	if jsonSym == nil {
		t.Fatal("expected `Json` to be defined by self in module scope")
	}
	if jsonSym.Resolved == nil || jsonSym.Resolved.Kind != SymbolEnum {
		t.Fatalf("expected `Json` self import to resolve to enum, got %#v", jsonSym.Resolved)
	}
}

func TestImport_SelectsModuleFunction(t *testing.T) {
	widgetSrc := `pub struct Widget {
  n: Int
}

pub fn make(n: Int): Widget { Widget{n} }
`
	widgetTokens := lexer.Lex(widgetSrc)
	widgetNodes, _ := parser.ParseWithRecovery(widgetTokens)

	loader := func(root string, modPath []string) ([]ast.Node, error) {
		if len(modPath) == 1 && modPath[0] == "widget" {
			return widgetNodes, nil
		}
		return nil, fmt.Errorf("not found: %s", strings.Join(modPath, "."))
	}

	src := `import widget.make

once w = make(1)`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	if len(file.TypeErrors) != 0 {
		t.Fatalf("unexpected TypeErrors: %v", file.TypeErrors)
	}

	makeSym := file.ModuleScope.Lookup("make")
	if makeSym == nil {
		t.Fatal("expected `make` to be defined in module scope")
	}
	if makeSym.Resolved == nil {
		t.Fatal("expected `make` to resolve to a real symbol")
	}
	if makeSym.Resolved.Kind != SymbolFunction || makeSym.Resolved.OwningType != "" {
		t.Fatalf("expected widget.make function, got kind=%v owner=%q", makeSym.Resolved.Kind, makeSym.Resolved.OwningType)
	}
}

// Flat selective import of an enum variant — `import shape.{Circle}` —
// is not a permitted form. The variant must come through its enum, either
// as `import shape.{Shape}` then `Shape.Circle` at the call site, or via
// the drill-through `import shape.Shape.{Circle}` form. Importing the
// variant flat hides which enum it belongs to and reintroduces the same
// silent-collision risk that motivates qualified construction. The
// builder reports this as a TypeError.
func TestImport_FlatSelectiveVariantRejected(t *testing.T) {
	shapeSrc := `enum Shape { Circle Float
  Empty }`
	shapeTokens := lexer.Lex(shapeSrc)
	shapeNodes, _ := parser.ParseWithRecovery(shapeTokens)

	loader := func(root string, modPath []string) ([]ast.Node, error) {
		if len(modPath) == 1 && modPath[0] == "shape" {
			return shapeNodes, nil
		}
		return nil, fmt.Errorf("not found: %s", strings.Join(modPath, "."))
	}

	src := `import shape.{Circle}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	if len(file.TypeErrors) == 0 {
		t.Fatal("expected a TypeError rejecting the flat selective variant import")
	}
	found := false
	for _, e := range file.TypeErrors {
		if strings.Contains(e.Message, "Circle") &&
			strings.Contains(e.Message, "variant") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about variant 'Circle', got: %v", file.TypeErrors)
	}
}

// A selective import of a name that doesn't exist in the source module
// should produce a diagnostic at the import site. Without this, a typo
// like `clock.{SystemClok}` silently binds the name locally and only
// surfaces as a downstream "undefined" error at the use site (or no
// error at all, if the import-bound symbol shadows the lookup).
func TestImport_SelectiveNameNotExportedDiagnoses(t *testing.T) {
	clockSrc := `pub struct FakeClock { at: Int }`
	clockNodes, _ := parser.ParseWithRecovery(lexer.Lex(clockSrc))

	loader := func(root string, modPath []string) ([]ast.Node, error) {
		if len(modPath) == 1 && modPath[0] == "clock" {
			return clockNodes, nil
		}
		return nil, fmt.Errorf("not found: %s", strings.Join(modPath, "."))
	}

	src := `import clock.{NonexistentClock}`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	if len(file.TypeErrors) != 1 {
		t.Fatalf("expected exactly one TypeError, got %d: %v", len(file.TypeErrors), file.TypeErrors)
	}
	got := file.TypeErrors[0].Message
	if !strings.Contains(got, "NonexistentClock") || !strings.Contains(got, "clock") {
		t.Errorf("expected error mentioning 'NonexistentClock' and 'clock', got: %q", got)
	}
}

// Drill-through form: `import shape.Shape.{Bogus}` where the variant
// doesn't exist on the enum should likewise diagnose at the variant
// position.
func TestImport_DrillThroughMissingVariantDiagnoses(t *testing.T) {
	shapeSrc := `pub enum Shape { Circle Float
  Empty }`
	shapeNodes, _ := parser.ParseWithRecovery(lexer.Lex(shapeSrc))

	loader := func(root string, modPath []string) ([]ast.Node, error) {
		if len(modPath) == 1 && modPath[0] == "shape" {
			return shapeNodes, nil
		}
		return nil, fmt.Errorf("not found: %s", strings.Join(modPath, "."))
	}

	src := `import shape.Shape.{Bogus}`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	if len(file.TypeErrors) != 1 {
		t.Fatalf("expected exactly one TypeError, got %d: %v", len(file.TypeErrors), file.TypeErrors)
	}
	got := file.TypeErrors[0].Message
	if !strings.Contains(got, "Bogus") || !strings.Contains(got, "shape.Shape") {
		t.Errorf("expected error mentioning 'Bogus' and 'shape.Shape', got: %q", got)
	}
}

// Cross-file qualified-variant access: when an enum is selectively imported
// (`import ast.{Expr}`) but its variants are not, a reference like
// `Expr.Let(...)` should still register a Reference at the variant's source
// position so hover and go-to-def light up. The variant is looked up via the
// resolved enum's Members map.
func TestCrossFile_QualifiedEnumVariantResolves(t *testing.T) {
	astSrc := `enum Expr {
  Lit Int
  Let{name: String, value: Expr, body: Expr}
}`
	astTokens := lexer.Lex(astSrc)
	astNodes, _ := parser.ParseWithRecovery(astTokens)

	loader := func(root string, modPath []string) ([]ast.Node, error) {
		if len(modPath) == 1 && modPath[0] == "ast" {
			return astNodes, nil
		}
		return nil, fmt.Errorf("not found: %s", strings.Join(modPath, "."))
	}

	src := `import ast.{Expr}
fn make(): Expr { Expr.Let{name: "x", value: Expr.Lit(1), body: Expr.Lit(2)} }`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	var letRef, litRef *Symbol
	for _, sym := range file.References {
		if sym == nil || sym.Kind != SymbolEnumVariant {
			continue
		}
		switch sym.Name {
		case "Let":
			letRef = sym
		case "Lit":
			litRef = sym
		}
	}
	if letRef == nil {
		t.Fatal("expected reference to enum variant 'Let' in the consumer file")
	}
	if litRef == nil {
		t.Fatal("expected reference to enum variant 'Lit' in the consumer file")
	}
}

// `embeds Circle` must NOT shadow the underlying Circle struct's
// scope entry. Bare `Circle{...}` should resolve to the struct, and
// `Shape.Circle{...}` to the variant. Same applies for distinct-type
// embeds — `embeds UserId` doesn't override the `UserId` constructor.
func TestEmbeds_VariantDoesNotShadowUnderlyingStruct(t *testing.T) {
	src := `struct Circle { radius: Float }
enum Shape { embeds Circle; Point }
fn make_circle(r: Float): Circle { Circle{radius: r} }`
	_, errs := checkSource(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "return type mismatch") ||
			strings.Contains(e.Message, "(Circle) -> Shape") {
			t.Errorf("Circle{...} should resolve to the Circle struct, not the Shape.Circle variant: %v", e)
		}
	}
}

// `embeds UserId` distinct-type embed must not shadow the distinct
// type's constructor. `UserId("alice")` inside the enum's module
// should resolve to the distinct type's constructor.
func TestEmbeds_VariantDoesNotShadowDistinctType(t *testing.T) {
	src := `type UserId String
enum Identifier { embeds UserId; Anonymous }
fn make(id: String): UserId { UserId(id) }`
	_, errs := checkSource(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "must be qualified through its enum") ||
			strings.Contains(e.Message, "return type mismatch") {
			t.Errorf("UserId(\"alice\") should resolve to the distinct type, not the variant: %v", e)
		}
	}
}

// Inline `pub` on a once binding must produce a public symbol.
// Regression guard alongside TestBuilderInlinePubFn.
func TestBuilderInlinePubOnce(t *testing.T) {
	input := `pub once max_retries: Int = 3`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	sym := file.ModuleScope.Lookup("max_retries")
	if sym == nil {
		t.Fatal("expected max_retries to be defined in module scope")
	}
	if sym.Kind != SymbolOnce {
		t.Errorf("expected SymbolOnce, got %v", sym.Kind)
	}
	if !sym.Public {
		t.Error("pub once should produce a public symbol")
	}
}

// Inline `pub` on a fn must produce a public symbol. defineFunc
// already reads n.Public; this is a regression guard alongside
// TestBuilderInlinePubOnce.
func TestBuilderInlinePubFn(t *testing.T) {
	input := `pub fn foo(): Int { 0 }`
	tokens := lexer.Lex(input)
	nodes, _ := parser.ParseWithRecovery(tokens)
	file := BuildFile(nodes)

	sym := file.ModuleScope.Lookup("foo")
	if sym == nil {
		t.Fatal("expected foo to be defined in module scope")
	}
	if sym.Kind != SymbolFunction {
		t.Errorf("expected SymbolFunction, got %v", sym.Kind)
	}
	if !sym.Public {
		t.Error("pub fn should produce a public symbol")
	}
}

// reExportLoader returns a loader for cross-module re-export tests where
// the source for one external module ("other.nomi" or whatever) is provided
// as a string. The loader resolves any single-segment module path against
// the supplied map; anything else is reported missing.
func reExportLoader(t *testing.T, modSources map[string]string) func(string, []string) ([]ast.Node, error) {
	t.Helper()
	parsed := make(map[string][]ast.Node, len(modSources))
	for name, src := range modSources {
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
		parsed[name] = nodes
	}
	return func(_ string, modPath []string) ([]ast.Node, error) {
		if len(modPath) != 1 {
			return nil, fmt.Errorf("unsupported path: %s", strings.Join(modPath, "."))
		}
		nodes, ok := parsed[modPath[0]]
		if !ok {
			return nil, fmt.Errorf("not found: %s", modPath[0])
		}
		return nodes, nil
	}
}

// Per-item re-export under the imported name (no rename) should mark the
// local binding public. Consumers look up `thing` in main's ModuleScope and
// see Public=true.
func TestBuilder_ReExport_PerItemUnderImportedName(t *testing.T) {
	loader := reExportLoader(t, map[string]string{
		"other": `pub fn thing(): Int { 0 }`,
	})

	src := `import other.{thing export}`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	for _, e := range file.TypeErrors {
		t.Errorf("unexpected TypeError: %v", e)
	}
	sym := file.ModuleScope.Lookup("thing")
	if sym == nil {
		t.Fatal("expected `thing` in main's ModuleScope")
	}
	if !sym.Public {
		t.Error("re-exported `thing` should be Public=true")
	}
	if sym.Resolved == nil || sym.Resolved.Kind != SymbolFunction {
		t.Errorf("expected sym.Resolved to point at the upstream fn, got %v", sym.Resolved)
	}
}

// Per-item re-export with rename: `import other.{thing export as t}`.
// The local binding `thing` stays private; a separate public symbol `t`
// is registered, with Resolved pointing at the upstream fn.
func TestBuilder_ReExport_PerItemWithRename(t *testing.T) {
	loader := reExportLoader(t, map[string]string{
		"other": `pub fn thing(): Int { 0 }`,
	})

	src := `import other.{thing export as t}`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	for _, e := range file.TypeErrors {
		t.Errorf("unexpected TypeError: %v", e)
	}

	// Local `thing` exists but is NOT public — the importing module sees it
	// only by its original name.
	thingSym := file.ModuleScope.Lookup("thing")
	if thingSym == nil {
		t.Fatal("expected local binding `thing` in main's ModuleScope")
	}
	if thingSym.Public {
		t.Error("local `thing` should be private when the export uses a different name")
	}

	// Public `t` exists and resolves to the upstream fn.
	tSym := file.ModuleScope.Lookup("t")
	if tSym == nil {
		t.Fatal("expected public re-export symbol `t` in main's ModuleScope")
	}
	if !tSym.Public {
		t.Error("re-exported `t` should be Public=true")
	}
	if tSym.Resolved == nil || tSym.Resolved.Kind != SymbolFunction {
		t.Errorf("expected `t`.Resolved to point at upstream fn, got %v", tSym.Resolved)
	}
}

// Line-level shorthand: `import other.{a, b} export` re-exports every
// selected item under its imported name.
func TestBuilder_ReExport_LineLevelShorthand(t *testing.T) {
	loader := reExportLoader(t, map[string]string{
		"other": `pub fn a(): Int { 1 }
pub fn b(): Int { 2 }`,
	})

	src := `import other.{a, b} export`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	for _, e := range file.TypeErrors {
		t.Errorf("unexpected TypeError: %v", e)
	}
	for _, name := range []string{"a", "b"} {
		sym := file.ModuleScope.Lookup(name)
		if sym == nil {
			t.Fatalf("expected `%s` in main's ModuleScope", name)
		}
		if !sym.Public {
			t.Errorf("line-level export should mark `%s` Public=true", name)
		}
	}
}

// Without an explicit `export` modifier, an imported name must remain
// private to the importing module.
func TestBuilder_ReExport_NotExportedByDefault(t *testing.T) {
	loader := reExportLoader(t, map[string]string{
		"other": `pub fn thing(): Int { 0 }`,
	})

	src := `import other.{thing}`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	sym := file.ModuleScope.Lookup("thing")
	if sym == nil {
		t.Fatal("expected `thing` in main's ModuleScope")
	}
	if sym.Public {
		t.Error("`thing` should be private to main when no `export` modifier is present")
	}
}

// Multi-hop re-export chain: leaf → middle → top → consumer. Each
// intermediate module re-exports the name it imported, so a downstream
// consumer of `top` should be able to call the leaf's function. The
// builder leaves intermediate proxy symbols with `.Type == nil`; only
// the leaf's original symbol carries the FuncType. callFnTypeFromResolved
// must walk the full Resolved chain (not just one hop) for the call
// site to see the leaf's signature.
//
// To prove the type actually surfaces (rather than silently being nil
// at the call site, which would still appear "no errors"), the call
// passes the wrong number of arguments — the arity diagnostic only
// fires when the FuncType is reachable. With the single-hop walk the
// arity error is silently suppressed because fnTy is nil; with the
// chain walk it correctly fires.
func TestBuilder_ReExport_MultiHopChain(t *testing.T) {
	loader := reExportLoader(t, map[string]string{
		"leaf":   `pub fn deep(): Int { 42 }`,
		"middle": `import leaf.{deep export}`,
		"top":    `import middle.{deep export}`,
	})

	// Positive case: a 0-arg call resolves cleanly. Confirm the chain
	// length is >= 2 hops so the test genuinely exercises the
	// multi-hop walker rather than the trivial single-hop case the
	// older code already handled.
	srcOK := `import top.{deep}

fn use_it(): Int { deep() }`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(srcOK))
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)
	typeErrs := BuildTypes(file, nodes)
	for _, e := range file.TypeErrors {
		t.Errorf("unexpected builder TypeError on multi-hop consumer: %v", e)
	}
	for _, e := range typeErrs {
		t.Errorf("unexpected BuildTypes error on multi-hop consumer: %v", e)
	}
	checkErrs := CheckTypes(file, nodes)
	for _, e := range checkErrs {
		t.Errorf("unexpected checker error on multi-hop consumer: %v", e)
	}
	sym := file.ModuleScope.Lookup("deep")
	if sym == nil {
		t.Fatal("downstream `deep` should resolve via top→middle→leaf chain")
	}
	hops := 0
	cur := sym
	for cur.Resolved != nil {
		cur = cur.Resolved
		hops++
	}
	if hops < 2 {
		t.Fatalf("expected the test to exercise a multi-hop chain (>=2 hops), got %d", hops)
	}
	if cur.Type == nil {
		t.Errorf("expected the chain end (leaf's `deep`) to carry a non-nil Type")
	}

	// Negative case: wrong arity. With the single-hop walker, fnTy at
	// the call site is nil (intermediate proxies have Type=nil), so
	// the arity check is silently skipped and no error fires — masking
	// the type mismatch. With the chain walker, fnTy resolves to
	// leaf's `() -> Int` and the arity error fires.
	srcBad := `import top.{deep}

fn use_it(): Int { deep(1) }`
	badNodes, _ := parser.ParseWithRecovery(lexer.Lex(srcBad))
	badFile := BuildFileWithStdlib(badNodes, nil, nil, "/project", loader)
	BuildTypes(badFile, badNodes)
	badCheckErrs := CheckTypes(badFile, badNodes)
	found := false
	for _, e := range badCheckErrs {
		if strings.Contains(e.Message, "expected") && strings.Contains(e.Message, "argument") {
			found = true
			break
		}
	}
	if !found {
		msgs := make([]string, len(badCheckErrs))
		for i, e := range badCheckErrs {
			msgs[i] = e.Error()
		}
		t.Errorf("expected an arity error proving the chain resolved to FuncType; got: %v", msgs)
	}
}

// Cross-module re-export reachability: when main re-exports a name from
// other, a downstream consumer importing that name from main resolves
// without the visibility-consistency check rejecting it.
func TestBuilder_ReExport_DownstreamConsumerCanImportReExport(t *testing.T) {
	loader := reExportLoader(t, map[string]string{
		"other": `pub fn thing(): Int { 0 }`,
		"main": `import other.{thing export}

pub fn use_it(): Int { thing() }`,
	})

	src := `import main.{thing}

pub fn use_it(): Int { thing() }`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	file := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)

	// The downstream consumer's import should succeed — no "private" /
	// "no exported name" diagnostic. Run CheckTypes too so the
	// visibility-pass-through path (checkImportVisibility walking
	// Resolved to test upstream Public) is genuinely exercised, not just
	// the builder-level scope registration.
	for _, e := range file.TypeErrors {
		t.Errorf("unexpected builder TypeError on downstream consumer: %v", e)
	}
	checkErrs := CheckTypes(file, nodes)
	for _, e := range checkErrs {
		t.Errorf("unexpected checker error on downstream consumer of re-export: %v", e)
	}
	if sym := file.ModuleScope.Lookup("thing"); sym == nil {
		t.Fatal("downstream `thing` should resolve via main's re-export")
	}
}
