package analysis

import (
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"strings"
	"testing"
)

// Tests for impl-block analyzer integration. `impl Iface for Type { fn m(...) }`
// registers dispatch structures (Impls / DispatchNames), while ordinary helper
// functions live at module scope.

// A host-backed default (`host fn` in an interface body) is provided by
// the runtime, so an implementor that supplies only the required methods
// must satisfy the interface — no "missing function" error for the extern.
func TestInterfaceHostBackedDefault_NotRequired(t *testing.T) {
	src := `pub interface Seq {
  fn next(value: self): Int
  host fn reduce(value: self): Int
}

pub struct counter {
  n: Int
}

impl Seq for counter {

  fn next(c: counter): Int {
    c.n
  }
}

`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = LowerDerives(nodes)
	fa := BuildFile(nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, BuildTypes(fa, nodes)...)
	all = append(all, CheckTypes(fa, nodes)...)
	if containsErr(all, "missing function 'reduce'", "missing function \"reduce\"", "reduce") {
		t.Errorf("host-backed default 'reduce' should not be required of implementors; got:\n  %s", joinErrs(all))
	}
}

// An interface default method may carry explicit function-local type params
// (the `U` in `fn mapped<U>(b: self, f: (T) -> U): U`), not bound by the
// interface header. The signature must resolve without "unknown type" errors.
func TestInterfaceDefaultExplicitTypeParam_SigResolves(t *testing.T) {
	src := `pub interface Box<T> {
  fn get(b: self): T

  fn mapped<U>(b: self, f: (T) -> U): U {
    f(Box.get(b))
  }
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = LowerDerives(nodes)
	fa := BuildFile(nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, BuildTypes(fa, nodes)...)
	all = append(all, CheckTypes(fa, nodes)...)
	if containsErr(all, "unknown type \"U\"", "unknown type 'U'") {
		t.Errorf("explicit type param U in a default sig should resolve; got:\n  %s", joinErrs(all))
	}
}

func TestInterfaceDefaultBareSiblingCallInInterpolationResolves(t *testing.T) {
	src := `pub interface Formatted {
  fn label(value: self): String

  fn shout(value: self): String {
    "${label(value)}!"
  }

  open fn brief(value: self): String {
    label(value)
  }
}

pub struct User {
  name: String
}

impl Formatted for User {
  fn label(user: User): String {
    user.name
  }
}
`
	_, all := buildAndCheckFromSource(src)
	if len(all) != 0 {
		t.Errorf("bare sibling calls in interface defaults should resolve, got:\n  %s", joinErrs(all))
	}
}

func TestImplBlock_ImplBlockAcceptsPlainFunction(t *testing.T) {
	src := `pub interface Greeting {
  fn greet(value: self): String
}

pub struct Guest {
  name: String
}

impl Greeting for Guest {

  fn greet(g: Guest): String {
    "hi ${g.name}"
  }
}
`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = LowerDerives(nodes)
	fa := BuildFile(nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, BuildTypes(fa, nodes)...)
	all = append(all, CheckTypes(fa, nodes)...)
	if len(all) != 0 {
		t.Errorf("expected plain `fn greet` inside `impl Greeting for Guest` to satisfy the interface, got:\n  %s", joinErrs(all))
	}
}

func TestImplBlock_BareOperatorInterfaceCallSelectsMatchingImplArgs(t *testing.T) {
	src := `pub interface Divide<Rhs, Out> {
  fn divide(lhs: self, rhs: Rhs): Out
}

pub opaque type NonZeroInt Int

impl Divide<Int, Int> for Int {
  fn divide(lhs: Int, rhs: Int): Int {
    _ = rhs
    lhs
  }
}

impl Divide<NonZeroInt, Int> for Int {
  fn divide(lhs: Int, rhs: NonZeroInt): Int {
    divide(lhs, rhs)
  }
}

fn use_non_zero(nz: NonZeroInt): Int {
  divide(10, nz)
}
`
	_, all := buildAndCheckFromSource(src)
	if len(all) != 0 {
		t.Errorf("bare operator interface call should select impl by all arguments, got:\n  %s", joinErrs(all))
	}
}

func TestImplBlock_PlainFunctionDoesNotSatisfyImplEntry(t *testing.T) {
	src := `pub interface Greeting {
  fn greet(value: self): String
}

pub struct Guest {
  name: String
}

pub fn greet(g: Guest): String {
  "hi ${g.name}"
}


impl Greeting for Guest
`
	_, all := buildAndCheckFromSource(src)
	if !containsErr(all, "missing function 'greet'", "missing function \"greet\"") {
		t.Errorf("expected plain module function not to satisfy impl entry; got:\n  %s", joinErrs(all))
	}
}

func TestImplBlock_ImplementsBlockMissingRequiredMethod(t *testing.T) {
	src := `pub interface Greeting {
  fn greet(value: self): String
}

pub struct Guest {
  name: String
}

impl Greeting for Guest
`
	_, all := buildAndCheckFromSource(src)
	if !containsErr(all, "missing function 'greet'", "missing function \"greet\"") {
		t.Errorf("expected missing required method from impl entry; got:\n  %s", joinErrs(all))
	}
}

func TestImplBlock_ModuleNestedImplementsMissingRequiredMethod(t *testing.T) {
	src := `pub interface Display {
  fn to_string(value: self): String
}

host type RawConn

pub opaque struct Conn {
  raw: RawConn
}

impl Display for Conn
`
	_, all := buildAndCheckFromSource(src)
	if !containsErr(all, "missing function 'to_string'", "missing function \"to_string\"") {
		t.Errorf("expected missing required method from module-scoped impl block; got:\n  %s", joinErrs(all))
	}
}

func TestImplBlock_DuplicateInterfaceMethodNamesAreScopedByBlock(t *testing.T) {
	src := `pub interface Speaker {
  fn label(value: self): String
}

pub interface Performer {
  fn label(value: self): String
}

pub struct Guest {
  name: String
}

impl Speaker for Guest {

  fn label(g: Guest): String {
    g.name
  }
}

impl Performer for Guest {

  fn label(g: Guest): String {
    g.name
  }
}
`
	_, all := buildAndCheckFromSource(src)
	if len(all) != 0 {
		t.Errorf("expected duplicate interface method names in separate blocks to be unambiguous, got:\n  %s", joinErrs(all))
	}
}

func TestImplBlock_DuplicateInterfaceMethodBlocksCanUseDifferentBodies(t *testing.T) {
	src := `pub interface Speaker {
  fn label(value: self): String
}

pub interface Performer {
  fn label(value: self): String
}

pub struct Guest {
  name: String
}

impl Speaker for Guest {

  fn label(g: Guest): String {
    g.name
  }
}

impl Performer for Guest {

  fn label(g: Guest): String {
    "guest ${g.name}"
  }
}
`
	_, all := buildAndCheckFromSource(src)
	if len(all) != 0 {
		t.Errorf("expected qualified names to disambiguate duplicate function names; got:\n  %s", joinErrs(all))
	}
}

func TestImplBlock_PlainImplFunctionAllowedWhenUnambiguous(t *testing.T) {
	src := `pub interface Greeting {
  fn greet(value: self): String
}

pub struct Guest {
  name: String
}

impl Greeting for Guest {

  fn greet(g: Guest): String {
    "hi ${g.name}"
  }
}
`
	_, all := buildAndCheckFromSource(src)
	if len(all) != 0 {
		t.Errorf("expected implementation function to be accepted, got:\n  %s", joinErrs(all))
	}
}

func TestImplBlock_ConcreteReceiverType_GenericResolves(t *testing.T) {
	src := `pub struct Box<T> {
  item: T
}

pub fn echo<T>(b: Box<T>): Box<T> {
  copy: Box<T> = Box{item: b.item}
  copy
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = LowerDerives(nodes)
	fa := BuildFile(nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, BuildTypes(fa, nodes)...)
	all = append(all, CheckTypes(fa, nodes)...)
	if len(all) > 0 {
		t.Errorf("concrete receiver type should resolve with no errors; got:\n  %s", joinErrs(all))
	}
}

func TestImplBlock_SelfAsBodyTypeRejected(t *testing.T) {
	src := `pub struct Box<T> {
  item: T
}

pub fn echo(b: self): self {
  copy: self = Box{item: b.item}
  copy
}`
	_, errs := buildTypesFromSource(src)
	if !containsErr(errs, "self is only valid in interface definitions") {
		t.Fatalf("expected concrete body `self` to be rejected, got:\n  %s", joinErrs(errs))
	}
}

func TestImplBlock_BareSameOwnerCallResolves(t *testing.T) {
	src := `pub struct Box {
  item: Int
}

fn get(b: Box): Int {
  b.item
}

fn twice(b: Box): Int {
  get(b) + get(b)
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = LowerDerives(nodes)
	fa := BuildFile(nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, BuildTypes(fa, nodes)...)
	all = append(all, CheckTypes(fa, nodes)...)
	if len(all) > 0 {
		t.Errorf("bare same-owner call should resolve with no errors; got:\n  %s", joinErrs(all))
	}
}

func TestImplBlock_SelfQualifierRejected(t *testing.T) {
	src := `pub struct Box {
  item: Int
}

fn get(b: Box): Int {
  b.item
}

fn twice(b: Box): Int {
  self.get(b)
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = LowerDerives(nodes)
	fa := BuildFile(nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, BuildTypes(fa, nodes)...)
	all = append(all, CheckTypes(fa, nodes)...)
	if !containsErr(all, "`self` is an interface type placeholder, not an expression qualifier") {
		t.Errorf("expected self qualifier rejection; got:\n  %s", joinErrs(all))
	}
}

func TestImplBlock_BareSameOwnerAmbiguousInterfaceMethodRejected(t *testing.T) {
	src := `pub interface A {
  fn label(value: self): String
}

pub interface B {
  fn label(value: self): String
}

pub struct Item {
  name: String
}

fn show(value: Item): String {
  label(value)
}

impl A for Item {
  fn label(value: Item): String {
    value.name
  }
}

impl B for Item {
  fn label(value: Item): String {
    value.name
  }
}
`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = LowerDerives(nodes)
	fa := BuildFile(nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, BuildTypes(fa, nodes)...)
	all = append(all, CheckTypes(fa, nodes)...)
	if !containsErr(all, "bare-name dispatch is not supported", "label") {
		t.Errorf("expected ambiguous bare same-owner call rejection; got:\n  %s", joinErrs(all))
	}
}

// `self` is an interface placeholder only; even nested body annotations inside
// a concrete type function must spell the concrete receiver type.
func TestImplBlock_SelfAsNestedBodyTypeRejected(t *testing.T) {
	src := `pub struct Box<T> {
  item: T

  pub fn bad(b: Box<T>): Box<T> {
    wrong: self = "not a box"
    b
  }
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = LowerDerives(nodes)
	fa := BuildFile(nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, BuildTypes(fa, nodes)...)
	all = append(all, CheckTypes(fa, nodes)...)
	if !containsErr(all, "self is only valid in interface definitions") {
		t.Errorf("expected `self`-as-a-type in a concrete body to be rejected; got:\n  %s", joinErrs(all))
	}
}

// `self` as a type outside an interface definition errors.
func TestImplBlock_SelfAsType_FreeFunctionRejected(t *testing.T) {
	src := `pub struct Point { x: Int }

fn make(): Int {
  p: self = Point{x: 1}
  p.x
}`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	nodes, _ = LowerDerives(nodes)
	fa := BuildFile(nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, BuildTypes(fa, nodes)...)
	all = append(all, CheckTypes(fa, nodes)...)
	if !containsErr(all, "self is only valid in interface definitions") {
		t.Errorf("expected `self`-as-a-type in a free function to be rejected; got:\n  %s", joinErrs(all))
	}
}

// --- 1. Block-form interface impl registration ---------------------------

// TestImplBlock_InterfaceImpl_RegistersImpls verifies the block form feeds
// the Impls / DispatchNames structures.
func TestImplBlock_InterfaceImpl_RegistersImpls(t *testing.T) {
	src := `pub interface Formatted {
    fn format(value: self, style: String): String
}

pub struct User { name: String }

impl Formatted for User {
    fn format(user: User, _style: String): String {
        user.name
    }
}`

	fa, errs := buildTypesFromSource(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if fa.Impls == nil || !fa.Impls["User"]["Formatted"] {
		t.Fatalf("expected Impls[User][Formatted], got %v", fa.Impls)
	}
	if !fa.DispatchNames["format"] {
		t.Fatalf("expected DispatchNames[format], got %v", fa.DispatchNames)
	}
}

// TestImplBlock_VsDecorator_SameDispatch asserts that the block-form impl
// populates the dispatch structures (Impls / DispatchNames).
func TestImplBlock_VsDecorator_SameDispatch(t *testing.T) {
	blockSrc := `pub interface Speech {
    fn speak(value: self): String
}

pub struct Dog { name: String }

impl Speech for Dog {
    fn speak(d: Dog): String { d.name }
}`

	fa, errs := buildTypesFromSource(blockSrc)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !fa.Impls["Dog"]["Speech"] {
		t.Errorf("expected Impls[Dog][Speech], got %v", fa.Impls)
	}
	if !fa.DispatchNames["speak"] {
		t.Errorf("expected DispatchNames[speak], got %v", fa.DispatchNames)
	}
}

// TestImplBlock_VisibilityInheritance: a `pub interface` + bare `fn` inside
// the block still yields a public method symbol (spec §3: impl functions
// inherit visibility from the interface).
func TestImplBlock_VisibilityInheritance(t *testing.T) {
	src := `pub interface Formatted {
    fn format(value: self): String
}

pub struct User { name: String }

impl Formatted for User {
    fn format(user: User): String { user.name }
}`
	fa, errs := buildTypesFromSource(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// The block method symbol is keyed under its OwningType; find it via the
	// type-method table.
	sym := fa.LookupTypeMethod("User", "format")
	if sym == nil {
		t.Fatal("expected User.format method symbol")
	}
	if !sym.Public {
		t.Error("impl-block method of pub interface should be public, got private")
	}
}

// TestImplBlock_UndefinedInterfaceErrors: a block naming an undeclared
// interface produces an analyzer error.
func TestImplBlock_UndefinedInterfaceErrors(t *testing.T) {
	src := `pub struct FakeClock { at: Int }

impl Clock for FakeClock {
    fn now(value: FakeClock): Int { value.at }
}`
	fa, errs := buildTypesFromSource(src)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, errs...)
	if !containsErr(all, "undefined interface", "Clock") {
		t.Errorf("expected undefined-interface error, got: %s", joinErrs(all))
	}
}

// TestImplBlock_NotAnInterfaceErrors: a block whose interface position names a
// struct (not an interface) errors.
func TestImplBlock_NotAnInterfaceErrors(t *testing.T) {
	src := `pub struct Foo { x: Int }
pub struct Bar { y: Int }

impl Foo for Bar {
    fn handle(b: Bar): Int { b.y }
}`
	fa, errs := buildTypesFromSource(src)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, errs...)
	if !containsErr(all, "not an interface") {
		t.Errorf("expected not-an-interface error, got: %s", joinErrs(all))
	}
}

// --- 2/3. Module helpers -------------------------------------------------

func TestImplBlock_ModuleFunctionIndexed(t *testing.T) {
	src := `pub struct Point {
  x: Int
  y: Int
}

pub fn origin(): Point { Point{x: 0, y: 0} }
fn helper(p: Point): Int { p.x }`
	fa, errs := buildTypesFromSource(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	origin := fa.ModuleScope.Lookup("origin")
	if origin == nil {
		t.Fatal("expected origin in module scope")
	}
	if !origin.Public {
		t.Error("origin declared `pub fn` should be public")
	}
	if origin.OwningType != "" {
		t.Errorf("origin.OwningType = %q, want empty", origin.OwningType)
	}
	helper := fa.ModuleScope.Lookup("helper")
	if helper == nil {
		t.Fatal("expected helper in module scope")
	}
	if helper.Public {
		t.Error("helper declared bare `fn` should be private")
	}
}

func TestImplBlock_ModuleFunctionCall_CrossModule(t *testing.T) {
	loader := reExportLoader(t, map[string]string{
		"geo": `pub struct Point {
  x: Int
  y: Int
}

pub fn origin(): Point { Point{x: 0, y: 0} }`,
	})
	src := `import geo.{self, Point}

fn make(): Point {
    geo.origin()
}`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	fa := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)
	errs := BuildTypes(fa, nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, errs...)
	all = append(all, CheckTypes(fa, nodes)...)
	if len(all) > 0 {
		t.Fatalf("unexpected errors calling geo.origin() cross-module: %s", joinErrs(all))
	}
}

func TestImplBlock_ModuleFunctionCall_CrossModuleResolvesType(t *testing.T) {
	loader := reExportLoader(t, map[string]string{
		"geo": `pub struct Point {
  x: Int
  y: Int
}

pub fn origin(): Point { Point{x: 0, y: 0} }`,
	})
	src := `import geo

fn bad(): Int {
    geo.origin()
}`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	fa := BuildFileWithStdlib(nodes, nil, nil, "/project", loader)
	errs := BuildTypes(fa, nodes)
	all := append([]TypeError{}, fa.TypeErrors...)
	all = append(all, errs...)
	all = append(all, CheckTypes(fa, nodes)...)
	if len(all) == 0 {
		t.Fatal("expected a return-type mismatch (Point vs Int): cross-module geo.origin() return type was not resolved in single-file analysis")
	}
}

// --- 4. self resolution ---------------------------------------------------

// TestImplBlock_SelfResolves_Concrete: concrete type annotations inside a
// module helper resolve against the declared type.
func TestImplBlock_SelfResolves_Concrete(t *testing.T) {
	src := `pub struct Point {
  x: Int
  y: Int
}

pub fn shift(p: Point): Point { p }`
	fa, errs := buildTypesFromSource(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	shift := fa.ModuleScope.Lookup("shift")
	if shift == nil {
		t.Fatal("expected shift")
	}
	ft, ok := shift.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType for shift, got %T", shift.Type)
	}
	if len(ft.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(ft.Params))
	}
	st, ok := ft.Params[0].(*StructType)
	if !ok || st.Name != "Point" {
		t.Errorf("param should resolve to struct Point, got %v", ft.Params[0])
	}
	rst, ok := ft.Return.(*StructType)
	if !ok || rst.Name != "Point" {
		t.Errorf("return should resolve to struct Point, got %v", ft.Return)
	}
}

func TestImplBlock_ConcreteReceiverTypeResolves_Generic(t *testing.T) {
	src := `pub struct Box<T> {
  value: T
}

pub fn unwrap<T>(b: Box<T>): T { b.value }`
	fa, errs := buildTypesFromSource(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	unwrap := fa.ModuleScope.Lookup("unwrap")
	if unwrap == nil {
		t.Fatal("expected unwrap")
	}
	ft, ok := unwrap.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType for unwrap, got %T", unwrap.Type)
	}
	st, ok := ft.Params[0].(*StructType)
	if !ok || st.Name != "Box" {
		t.Errorf("self param should resolve to Box<T>, got %v", ft.Params[0])
	}
	if len(st.TypeParamDefs) == 0 && len(st.TypeArgs) == 0 {
		t.Errorf("self should carry the generic binding Box<T>, got bare %v", st)
	}
}

// --- 6. Bodyless conformance and the absence of interface fields ---------

// TestImplBlock_BodylessConformance: an interface with nothing to implement (a
// marker, or one whose functions all have defaults) is satisfied by a bodyless
// `impl Iface for Type`, which registers the conformance.
func TestImplBlock_BodylessConformance(t *testing.T) {
	src := `pub interface Marker {
}

pub interface Greets {
    fn greet(_value: self): String {
        "hi"
    }
}

pub struct Robot { model: String }

impl Marker for Robot

impl Greets for Robot`
	fa, all := buildAndCheckFromSource(src)
	if len(all) > 0 {
		t.Fatalf("a bodyless conformance should type-check, got: %s", joinErrs(all))
	}
	if !fa.Impls["Robot"]["Marker"] || !fa.Impls["Robot"]["Greets"] {
		t.Errorf("expected Impls[Robot][Marker] and Impls[Robot][Greets], got %v", fa.Impls)
	}
}

// TestFieldAccess_InterfaceValuesAndTypeParametersHaveNoFields: an interface
// declares functions only, so `x.name` on a value of interface type or of a
// bounded type parameter is the ordinary "no field" error, and so is the
// accessor `.name` over a type parameter. Each source has a positive twin that
// reads the same value through the function requirement and must type-check
// clean, so a checker that refused everything would fail.
func TestFieldAccess_InterfaceValuesAndTypeParametersHaveNoFields(t *testing.T) {
	const decls = `pub interface HasName {
    fn name(value: self): String
}

pub struct Person { name: String }

impl HasName for Person {
    fn name(person: Person): String { person.name }
}

`
	for _, tc := range []struct {
		name, bad, want, good string
	}{
		{"an interface value",
			"fn read(n: HasName): String { n.label }",
			"interface 'HasName' has no field 'label'",
			"fn read(n: HasName): String { HasName.name(n) }"},
		{"a bounded type parameter",
			"fn read<T>(x: T): String where T: HasName { x.name }",
			"type parameter `T` has no field 'name'",
			"fn read<T>(x: T): String where T: HasName { T.name(x) }"},
		{"a field accessor over a bounded type parameter",
			"fn apply<T>(x: T, f: (T) -> String): String { f(x) }\nfn read<T>(x: T): String where T: HasName { apply(x, .name) }",
			"`.name` reads a field of a struct, a record or a tuple, and T is none of those",
			"fn apply<T>(x: T, f: (T) -> String): String { f(x) }\nfn read<T>(x: T): String where T: HasName { apply(x, |y| T.name(y)) }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, all := buildAndCheckFromSource(decls + tc.bad)
			if len(all) == 0 {
				t.Fatalf("a field read through %s was accepted", tc.name)
			}
			if !containsErr(all, tc.want) {
				t.Errorf("rejected, but not with %q: %s", tc.want, joinErrs(all))
			}
			if _, all := buildAndCheckFromSource(decls + tc.good); len(all) > 0 {
				t.Errorf("the function-requirement spelling was refused: %s", joinErrs(all))
			}
		})
	}
}

// TestFieldAccess_AFunctionIsNotAFieldOfAValue: `x.name` on a VALUE never
// reaches a function named `name`, whatever declares it: the interface of an
// interface-typed value, the bound of a type parameter, or an impl on the
// struct's type. Each read, bare, as a function value or called, is the one
// "no field" error whose hint spells the qualified call. Only a NAME
// qualifies a function, so each source's twin, the qualified spelling, must
// check clean.
func TestFieldAccess_AFunctionIsNotAFieldOfAValue(t *testing.T) {
	const decls = `pub interface HasName {
    fn name(value: self): String
}

pub interface Labeled {
    fn name(value: self): String
}

pub struct Person { first: String }

impl HasName for Person {
    fn name(person: Person): String { person.first }
}

impl Person {
    fn initial(person: Person): String { person.first }
}

pub struct Robot { serial: String }

impl HasName for Robot {
    fn name(robot: Robot): String { robot.serial }
}

impl Labeled for Robot {
    fn name(robot: Robot): String { robot.serial }
}

pub struct Holder { inner: HasName }

`
	for _, tc := range []struct {
		name, bad, msg, hint, good string
	}{
		{"an interface-typed parameter",
			"fn read(h: HasName): String { h.name }",
			"interface 'HasName' has no field 'name'",
			"`HasName` declares `fn name`; call it as `HasName.name(h)`",
			"fn read(h: HasName): String { HasName.name(h) }"},
		{"an interface-typed parameter as a function value",
			"fn read(h: HasName): String {\n    f = h.name\n    f(h)\n}",
			"interface 'HasName' has no field 'name'",
			"`HasName` declares `fn name`; call it as `HasName.name(h)`",
			"fn read(h: HasName): String {\n    f: (HasName) -> String = HasName.name\n    f(h)\n}"},
		{"an interface-typed parameter called through the read",
			"fn read(h: HasName): String { h.name(h) }",
			"interface 'HasName' has no field 'name'",
			"`HasName` declares `fn name`; call it as `HasName.name(h)`",
			"fn read(h: HasName): String { HasName.name(h) }"},
		{"an interface-typed binding",
			"fn read(p: Person): String {\n    h: HasName = p\n    h.name\n}",
			"interface 'HasName' has no field 'name'",
			"`HasName` declares `fn name`; call it as `HasName.name(h)`",
			"fn read(p: Person): String {\n    h: HasName = p\n    HasName.name(h)\n}"},
		{"an interface-typed field",
			"fn read(o: Holder): String { o.inner.name }",
			"interface 'HasName' has no field 'name'",
			"`HasName` declares `fn name`; call it as `HasName.name(o.inner)`",
			"fn read(o: Holder): String { HasName.name(o.inner) }"},
		{"an interface-typed lambda parameter",
			"fn read(h: HasName): String {\n    f = |v: HasName| v.name\n    f(h)\n}",
			"interface 'HasName' has no field 'name'",
			"`HasName` declares `fn name`; call it as `HasName.name(v)`",
			"fn read(h: HasName): String {\n    f = |v: HasName| HasName.name(v)\n    f(h)\n}"},
		{"a value of a bounded type parameter",
			"fn read<T>(x: T): String where T: HasName { x.name }",
			"type parameter `T` has no field 'name'",
			"`HasName` declares `fn name`; call it as `T.name(x)`",
			"fn read<T>(x: T): String where T: HasName { T.name(x) }"},
		{"a value of a type parameter whose two bounds declare the function",
			"fn read<T>(x: T): String where T: HasName and Labeled { x.name }",
			"type parameter `T` has no field 'name'",
			"bounds `HasName` and `Labeled` each declare `fn name`; call it as `HasName.name(x)` or `Labeled.name(x)`",
			"fn read<T>(x: T): String where T: HasName and Labeled { HasName.name(x) }"},
		{"a struct value whose impl provides the function",
			"fn read(p: Person): String { p.name }",
			"struct 'Person' has no field 'name'",
			"`name` is a function of `Person`, not a field; call it as `Person.name(p)` or `HasName.name(p)`",
			"fn read(p: Person): String { Person.name(p) }"},
		{"a struct value as a function value",
			"fn read(p: Person): String {\n    f = p.name\n    f(p)\n}",
			"struct 'Person' has no field 'name'",
			"`name` is a function of `Person`, not a field; call it as `Person.name(p)` or `HasName.name(p)`",
			"fn read(p: Person): String {\n    f = Person.name\n    f(p)\n}"},
		{"a struct value whose inherent impl provides the function",
			"fn read(p: Person): String { p.initial }",
			"struct 'Person' has no field 'initial'",
			"`initial` is a function of `Person`, not a field; call it as `Person.initial(p)`",
			"fn read(p: Person): String { Person.initial(p) }"},
		{"a struct value whose two interfaces provide the function",
			"fn read(r: Robot): String { r.name }",
			"struct 'Robot' has no field 'name'",
			"`name` is a function of `Robot`, not a field; call it as `HasName.name(r)` or `Labeled.name(r)`",
			"fn read(r: Robot): String { Labeled.name(r) }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, all := buildAndCheckFromSource(decls + tc.bad)
			if len(all) == 0 {
				t.Fatalf("a function read as a field of %s was accepted", tc.name)
			}
			if len(all) != 1 || all[0].Message != tc.msg || len(all[0].Hints) != 1 || all[0].Hints[0] != tc.hint {
				t.Fatalf("want the one error %q with the hint %q, got: %s", tc.msg, tc.hint, joinErrs(all))
			}
			if _, all := buildAndCheckFromSource(decls + tc.good); len(all) > 0 {
				t.Errorf("the qualified spelling was refused: %s", joinErrs(all))
			}
		})
	}
}

// --- helpers --------------------------------------------------------------

// buildAndCheckFromSource runs the single-file pipeline including CheckTypes
// (which buildTypesFromSource stops short of), returning the FA and the union
// of build-time + check-time errors.
func buildAndCheckFromSource(src string) (*FileAnalysis, []TypeError) {
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	// Lower derive declarations into top-level *ast.ImplBlock nodes before
	// BuildTypes/CheckTypes, so the per-block field-conformance checker
	// (checkImplBlock) sees them. BuildFile lowers its own internal copy;
	// BuildTypes/CheckTypes take nodes directly.
	nodes, lowerErrs := LowerDerives(nodes)
	fa := BuildFile(nodes)
	all := append([]TypeError{}, lowerErrs...)
	all = append(all, fa.TypeErrors...)
	all = append(all, BuildTypes(fa, nodes)...)
	all = append(all, CheckTypes(fa, nodes)...)
	return fa, all
}

func containsErr(errs []TypeError, anyOf ...string) bool {
	for _, e := range errs {
		for _, s := range anyOf {
			if strings.Contains(diagText(e), s) {
				return true
			}
		}
	}
	return false
}

func joinErrs(errs []TypeError) string {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n  ")
}
