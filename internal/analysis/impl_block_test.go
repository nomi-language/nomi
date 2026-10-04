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
// the block still yields a public method symbol (spec §38.7 / block-as-module).
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

// --- 6. Field-only interface conformance via bodyless impl ----------------

// TestImplBlock_FieldOnlyConformance_Empty: a field-only interface satisfied
// by `impl Tagged for Robot` registers the conformance and
// passes the field check.
func TestImplBlock_FieldOnlyConformance_Empty(t *testing.T) {
	src := `pub interface Tagged {
    field tag: String
}

pub struct Robot { tag: String; model: String }

impl Tagged for Robot`
	fa, all := buildAndCheckFromSource(src)
	if len(all) > 0 {
		t.Fatalf("bodyless field-only conformance should type-check, got: %s", joinErrs(all))
	}
	if !fa.Impls["Robot"]["Tagged"] {
		t.Errorf("expected Impls[Robot][Tagged], got %v", fa.Impls)
	}
}

// TestImplBlock_FieldOnlyConformance_Missing: the same empty block on a type
// MISSING the required field is rejected.
func TestImplBlock_FieldOnlyConformance_Missing(t *testing.T) {
	src := `pub interface Tagged {
    field tag: String
}

pub struct Robot { model: String }

impl Tagged for Robot {
}`
	_, all := buildAndCheckFromSource(src)
	if !containsErr(all, "tag", "field") {
		t.Errorf("expected missing-field conformance error, got: %s", joinErrs(all))
	}
}

// --- 7. A requirement the receiver's KIND cannot carry --------------------

// A `field` requirement is the one interface obligation discharged by the
// receiver's DECLARATION rather than by an item in the impl block, and its
// validator used to RETURN when the receiver was the wrong kind instead of
// rejecting. The requirement was then vacuous: the program below type-checked
// clean at 3a933661 and died at run time with "cannot access field 'name' on
// variant 'Red'", which is exactly the failure a static field requirement
// exists to make impossible.
//
// Every test in this section carries its own POSITIVE, because a fix that
// rejected every impl of a requirement-bearing interface would satisfy the
// negative half alone.

// TestImplBlock_FieldRequirementRejectsAnEnumReceiver is the measured defect.
// `impl Named for Color` must be refused, and `impl Named for Person` in the
// same source must not be.
func TestImplBlock_FieldRequirementRejectsAnEnumReceiver(t *testing.T) {
	src := `pub interface Named {
    field name: String
}

pub struct Person { name: String }

pub enum Color { Red
Blue }

impl Named for Person

impl Named for Color

fn greet(n: Named): String { n.name }`
	_, all := buildAndCheckFromSource(src)
	if !containsErr(all, "'Color' is an enum") {
		t.Fatalf("an enum claiming a field-bearing interface was accepted, so `n.name` "+
			"inside greet can still fault at run time. Errors: %s", joinErrs(all))
	}
	// The reason, not merely the refusal: the diagnostic has to say why a
	// receiver of this kind cannot satisfy the requirement at all.
	if !containsErr(all, "declares no fields") || !containsErr(all, "storage obligation") {
		t.Errorf("the rejection does not name WHY the receiver cannot satisfy the "+
			"requirement: %s", joinErrs(all))
	}
	// THE PLANTED POSITIVE. `Person` declares the field, so nothing about it
	// may be reported — a fix that refused every impl would pass the assertion
	// above and fail here.
	for _, e := range all {
		if strings.Contains(e.Message, "Person") {
			t.Errorf("`impl Named for Person` was reported: %s", e.Message)
		}
	}
}

// TestImplBlock_FieldRequirementStructReceiverStillConforms is the positive in
// isolation: the same interface and struct, with the enum impl deleted, must
// type-check clean AND register the conformance. Without this, a checker that
// had started erroring on every field-bearing impl would look correct from the
// negative tests alone.
func TestImplBlock_FieldRequirementStructReceiverStillConforms(t *testing.T) {
	src := `pub interface Named {
    field name: String
}

pub struct Person { name: String }

pub enum Color { Red
Blue }

impl Named for Person

fn greet(n: Named): String { n.name }`
	fa, all := buildAndCheckFromSource(src)
	if len(all) > 0 {
		t.Fatalf("a struct that declares the required field must conform: %s", joinErrs(all))
	}
	if !fa.Impls["Person"]["Named"] {
		t.Errorf("expected Impls[Person][Named], got %v", fa.Impls)
	}
}

// TestImplBlock_FieldRequirementRejectsADistinctReceiver shows the rule is
// about STORAGE and not about enums specifically. A distinct type supports no
// field access at all (`fieldTypeFromObject` has no DistinctType arm), so it
// cannot satisfy a field requirement either.
func TestImplBlock_FieldRequirementRejectsADistinctReceiver(t *testing.T) {
	src := `pub interface Named {
    field name: String
}

pub type Email String

impl Named for Email`
	_, all := buildAndCheckFromSource(src)
	if !containsErr(all, "'Email' is a distinct type") {
		t.Errorf("a distinct type claiming a field-bearing interface was accepted: %s",
			joinErrs(all))
	}
}

// TestImplBlock_RequirementOnAnUnresolvedReceiverIsQuiet pins the fail-OPEN
// arm. A nil receiver type means the checker does not know the kind, which is a
// different fact from knowing it is wrong — single-file analysis of an impl
// whose receiver lives in a sibling file lands here, and a rejection would be a
// false diagnostic in the editor. The receiver's own "undefined" error is
// somebody else's and is not asserted; what is asserted is that no
// requirement-kind rejection is invented on top of it.
func TestImplBlock_RequirementOnAnUnresolvedReceiverIsQuiet(t *testing.T) {
	src := `pub interface Named {
    field name: String
}

impl Named for Ghost`
	_, all := buildAndCheckFromSource(src)
	if containsErr(all, "declares no fields") {
		t.Errorf("an UNRESOLVED receiver was rejected as the wrong kind, which would fire "+
			"on every single-file analysis of a sibling-declared receiver: %s", joinErrs(all))
	}
}

// --- 8. The interface's own parameters ---------------------------------------

// TestImplBlock_AGenericInterfaceRequirementIsSubstituted is the SECOND defect
// the receiver-kind survey turned up, and it runs the other way: a FALSE
// REJECTION. `validateImplBlockMethodSignatures` substitutes the interface's
// type arguments before comparing and the declaration-discharged field
// validator did not, so a generic interface could not carry a field
// requirement mentioning its own parameter at all — every CORRECT impl was
// refused. Pre-existing at `3a933661`; see `interfaceReqSubs`.
//
// Each half is a matched pair, because the fix is a substitution and a
// substitution that produced `Any` would satisfy the positive alone.
func TestImplBlock_AGenericInterfaceRequirementIsSubstituted(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr string // "" = must type-check clean
	}{
		{"field, matching type argument", `pub interface Container<T> {
    field items: List<T>
}

pub struct Box { items: List<Int> }

impl Container<Int> for Box`, ""},
		{"field, MISMATCHED type argument", `pub interface Container<T> {
    field items: List<T>
}

pub struct Bad { items: List<String> }

impl Container<Int> for Bad`, "has type List<String>, but interface declares List<Int>"},
		{"self in a field requirement, non-generic receiver", `pub interface H {
    field next: self
}

pub struct S { next: S }

impl H for S`, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, all := buildAndCheckFromSource(tc.src)
			if tc.wantErr == "" {
				if len(all) > 0 {
					t.Fatalf("a CORRECT impl of a generic interface was refused: %s", joinErrs(all))
				}
				return
			}
			if len(all) == 0 {
				t.Fatalf("a MISMATCHED requirement was accepted, so the substitution is vacuous "+
					"rather than correct — it would pass by comparing nothing. Wanted %q", tc.wantErr)
			}
			if !containsErr(all, tc.wantErr) {
				t.Errorf("rejected, but not with the substituted type: %s", joinErrs(all))
			}
		})
	}
}

// TestImplBlock_SelfInARequirementAgainstAGenericReceiver pins the RESIDUE
// `interfaceReqSubsWithSelf` names. `recvTy` is `reg.Lookup(recvName)` — the
// UNAPPLIED generic `Box`, not `Box<T>` — and there is no self-position
// parameter to read the applied receiver off the way the method validator
// does.
//
// THE OBSERVABLE IS LENIENCE, NOT A FALSE REJECTION, and the version of this
// test that read `if len(all) == 0 { t.Skip("...now checks correctly...") }`
// could not tell those apart. It skipped at `4772329c`, instructing the next
// reader to delete it and the residue paragraph. Measured against the real
// binary instead: `impl H for Box<T>` accepts `next: Box<T>` (correct) AND
// `next: Box<Int>` (wrong), because `TypesEqual` treats an argument-less
// generic as equal to every instantiation of it — `interface C { field items:
// Box }` against `struct S { items: Box<Int> }` passes for the same reason.
// What it still rejects is a field whose NOMINAL HEAD is not the receiver.
//
// So all three arms are asserted, and the wrong-argument arm is the one that
// makes the pin mean something: if somebody resolves the applied receiver
// here, that arm fails and says so.
func TestImplBlock_SelfInARequirementAgainstAGenericReceiver(t *testing.T) {
	const iface = "pub interface H {\n    field next: self\n}\n\n"
	for _, c := range []struct{ name, src, wantErr string }{
		{"the receiver's own instantiation — accepted, and correct",
			iface + "pub struct Box<T> { next: Box<T>\nv: T }\n\nimpl H for Box<T>", ""},
		{"a DIFFERENT instantiation — accepted, and that is the residue",
			iface + "pub struct Box<T> { next: Box<Int>\nv: T }\n\nimpl H for Box<T>", ""},
		{"a field that is not the receiver at all — still rejected",
			iface + "pub struct Bad<T> { next: Int\nv: T }\n\nimpl H for Bad<T>", "field 'next' has type Int"},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			_, all := buildAndCheckFromSource(c.src)
			if c.wantErr == "" {
				if len(all) > 0 {
					t.Fatalf("accepted at `4772329c`, rejected now — the residue changed shape: %s", joinErrs(all))
				}
				return
			}
			if len(all) == 0 {
				t.Fatalf("a `self` field requirement against a generic receiver is now vacuous even on "+
					"the nominal head, so the check compares nothing. Wanted %q", c.wantErr)
			}
			if !containsErr(all, c.wantErr) {
				t.Errorf("rejected for some other reason: %s", joinErrs(all))
			}
		})
	}
}

// TestImplBlock_AFieldRequirementWithAndWithoutFunctionsIsStillFine is the
// planted positive for the kind check. A rejection keyed on "declares a
// requirement" rather than on "the receiver's kind cannot carry it" would pass
// every negative assertion above and break `std/app.nomi` outright.
func TestImplBlock_AFieldRequirementWithAndWithoutFunctionsIsStillFine(t *testing.T) {
	for name, src := range map[string]string{
		"field only": `pub interface Named {
    field name: String
}

pub struct Person { name: String }

impl Named for Person`,
		"field plus a function": `pub interface Named {
    field name: String
    fn greet(value: self): String
}

pub struct Person { name: String }

impl Named for Person {
  fn greet(value: Person): String { value.name }
}`,
	} {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			_, all := buildAndCheckFromSource(src)
			if len(all) > 0 {
				t.Errorf("a field requirement, with or without functions, must stay legal: %s", joinErrs(all))
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
