package analysis

import "testing"

// --- accept: irrefutable patterns ---

func TestParamDestructure_Distinct_SelfTyped(t *testing.T) {
	src := `type Dur Int
fn unwrap(Dur(x)): Int { x }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestParamDestructure_Distinct_RedundantAnnotation(t *testing.T) {
	src := `type Dur Int
fn unwrap(Dur(x): Dur): Int { x }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestParamDestructure_Tuple_Annotated(t *testing.T) {
	src := `fn add((a, b): (Int, Int)): Int { a + b }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestParamDestructure_TypedStruct_SelfTyped(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
fn sum(Point{x, y}): Int { x + y }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestParamDestructure_AnonStruct_Annotated(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
fn sum({x, y}: Point): Int { x + y }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestParamDestructure_SingleVariantEnum_Qualified(t *testing.T) {
	src := `enum Wrapper { Only(Int) }
fn open(Wrapper.Only(n)): Int { n }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestParamDestructure_NestedTupleOfDistinct(t *testing.T) {
	src := `type Dur Int
fn pair((Dur(a), n): (Dur, Int)): Int { a + n }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestParamDestructure_SelfTyped_BindsInnerType(t *testing.T) {
	// The inner binding x has the distinct's Inner type (Int); using it as an
	// Int must type-check.
	src := `type Dur Int
fn dbl(Dur(x)): Int { x + 1 }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Self-typing must populate the function's signature so call-site argument
// types are checked against the derived type, not skipped.
func TestParamDestructure_SelfTyped_CallSiteTyped(t *testing.T) {
	src := `type Dur Int
fn unwrap(Dur(x)): Int { x }
fn caller(): Int { unwrap("oops") }`
	_, errs := checkSource(src)
	expectError(t, errs, "expected Dur")
}

func TestParamDestructure_SelfTyped_CallSiteAccepts(t *testing.T) {
	src := `type Dur Int
fn unwrap(Dur(x)): Int { x }
fn caller(): Int { unwrap(Dur(5)) }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// --- reject: refutable patterns ---

func TestParamDestructure_Reject_MultiVariantEnum(t *testing.T) {
	// .Some on a multi-variant enum is refutable.
	src := `enum Opt { Some(Int); Nothing }
fn f(.Some(x): Opt): Int { x }`
	_, errs := checkSource(src)
	expectError(t, errs, "refutable pattern in parameter")
}

func TestParamDestructure_Reject_MapPattern(t *testing.T) {
	src := `type Kvs Map<String, Int>
fn f({"k" => v}: Kvs): Int { v }`
	_, errs := checkSource(src)
	expectError(t, errs, "refutable pattern in parameter")
}

func TestParamDestructure_Reject_ListPattern(t *testing.T) {
	src := `fn f([a, b]: List<Int>): Int { a + b }`
	_, errs := checkSource(src)
	expectError(t, errs, "refutable pattern in parameter")
}

func TestParamDestructure_Reject_NestedRefutable(t *testing.T) {
	// A tuple whose element is a refutable multi-variant enum variant.
	src := `enum Opt { Some(Int); Nothing }
fn f((.Some(x), n): (Opt, Int)): Int { x + n }`
	_, errs := checkSource(src)
	expectError(t, errs, "refutable pattern in parameter")
}

// --- missing annotation on type-less shapes ---

// The no-annotation derivation error must be reported EXACTLY ONCE. It used to
// fire twice — buildFuncType and checkDestructureParams both derived the param
// type and both appended the error (fix I-1). expectErrorCount pins the count
// so that regression can't slip back in.

func TestParamDestructure_Reject_TupleNoAnnotation(t *testing.T) {
	src := `fn f((a, b)): Int { a + b }`
	_, errs := checkSource(src)
	expectErrorCount(t, errs, "needs a type annotation", 1)
}

func TestParamDestructure_Reject_AnonStructNoAnnotation(t *testing.T) {
	src := `fn f({x, y}): Int { x + y }`
	_, errs := checkSource(src)
	expectErrorCount(t, errs, "needs a type annotation", 1)
}

func TestParamDestructure_Reject_DotEnumNoAnnotation(t *testing.T) {
	src := `enum Opt { Some(Int); Nothing }
fn f(.Some(x)): Int { x }`
	_, errs := checkSource(src)
	expectErrorCount(t, errs, "needs a type annotation", 1)
}

// --- embeds-distinct variant is out of scope for parameter destructuring ---

// `enum Identifier { embeds UserId }` (UserId a distinct) self-types to the
// enum, so the param shape is irrefutable, but destructuring it in a parameter
// silently mis-binds (the runtime would unwrap to the distinct's inner while a
// body `case` binds the embedded value). The checker guards it with a clear
// diagnostic (fix I-2) rather than accepting the mis-typed binding.
// A single-variant enum embedding a distinct (`enum Identifier { embeds UserId }`)
// can be destructured in a parameter: `fn open(Identifier.UserId(n))` binds n to
// UserId's inner type, consistent with the `case` form and the runtime
// applyDestructure path. (Previously guarded; the guard was over-conservative —
// the divergence it feared lives only in matchPattern's case path, not params.)
func TestParamDestructure_EmbeddedDistinctVariant_Accepted(t *testing.T) {
	src := `type UserId Int
enum Identifier { embeds UserId }
fn open(Identifier.UserId(n)): Int { n }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// The struct-embeds analogue: `enum S { embeds Circle }` (Circle a struct) with
// `fn area(S.Circle{r})`. Exercises the QualifiedType struct-pattern head
// derivation (resolve the qualifier S, not the unknown type `S.Circle`).
func TestParamDestructure_EmbeddedStructVariant_Accepted(t *testing.T) {
	src := `struct Circle { r: Int }
enum S { embeds Circle }
fn area(S.Circle{r}): Int { r * 2 }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// --- lambda gets the refutability rule too ---

func TestParamDestructure_Lambda_Distinct_SelfTyped(t *testing.T) {
	src := `type Dur Int
fn run(): Int {
  f = |Dur(x)| x + 1
  f(Dur(5))
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestParamDestructure_Lambda_Reject_MapPattern(t *testing.T) {
	// The intended map-pattern break: a map pattern in a lambda param is now
	// refutable and rejected.
	src := `type Kvs Map<String, Int>
fn run(): Int {
  f = |{"k" => v}: Kvs| v
  f(g())
}
fn g(): Kvs { g() }`
	_, errs := checkSource(src)
	expectError(t, errs, "refutable pattern in parameter")
}
