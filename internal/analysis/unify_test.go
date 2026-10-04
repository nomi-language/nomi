package analysis

import "testing"

func TestUnifyPrimitiveMatch(t *testing.T) {
	if err := Unify(TypeInt, TypeInt); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestUnifyPrimitiveMismatch(t *testing.T) {
	if err := Unify(TypeInt, TypeString); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUnifyTypeVarToConcrete(t *testing.T) {
	tv := &TypeVar{ID: 1}
	if err := Unify(tv, TypeInt); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	resolved := resolveTV(tv)
	if resolved != TypeInt {
		t.Fatalf("expected TypeInt, got %s", resolved)
	}
}

func TestUnifyTwoTypeVarsChained(t *testing.T) {
	tv1 := &TypeVar{ID: 1}
	tv2 := &TypeVar{ID: 2}

	if err := Unify(tv1, TypeInt); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if err := Unify(tv2, tv1); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	resolved := resolveTV(tv2)
	if resolved != TypeInt {
		t.Fatalf("expected TypeInt, got %s", resolved)
	}
}

func TestUnifyListType(t *testing.T) {
	tv := &TypeVar{ID: 1}
	a := &ListType{Elem: tv}
	b := &ListType{Elem: TypeInt}

	if err := Unify(a, b); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	resolved := resolveTV(tv)
	if resolved != TypeInt {
		t.Fatalf("expected TypeInt, got %s", resolved)
	}
}

func TestUnifyFuncType(t *testing.T) {
	tvParam := &TypeVar{ID: 1}
	tvRet := &TypeVar{ID: 2}

	a := &FuncType{Params: []Type{tvParam}, Return: tvRet}
	b := &FuncType{Params: []Type{TypeString}, Return: TypeBool}

	if err := Unify(a, b); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if resolveTV(tvParam) != TypeString {
		t.Fatalf("expected TypeString for param, got %s", resolveTV(tvParam))
	}
	if resolveTV(tvRet) != TypeBool {
		t.Fatalf("expected TypeBool for return, got %s", resolveTV(tvRet))
	}
}

func TestUnifyConflict(t *testing.T) {
	tv := &TypeVar{ID: 1}
	if err := Unify(tv, TypeInt); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if err := Unify(tv, TypeString); err == nil {
		t.Fatal("expected error for conflicting unification, got nil")
	}
}

func TestUnifySameTypeVar(t *testing.T) {
	tv := &TypeVar{ID: 1}
	if err := Unify(tv, tv); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestUnifyMapType(t *testing.T) {
	tvKey := &TypeVar{ID: 1}
	tvVal := &TypeVar{ID: 2}

	a := &MapType{Key: tvKey, Val: tvVal}
	b := &MapType{Key: TypeString, Val: TypeInt}

	if err := Unify(a, b); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if resolveTV(tvKey) != TypeString {
		t.Fatalf("expected TypeString for key, got %s", resolveTV(tvKey))
	}
	if resolveTV(tvVal) != TypeInt {
		t.Fatalf("expected TypeInt for val, got %s", resolveTV(tvVal))
	}
}

func TestUnifyTupleType(t *testing.T) {
	tv := &TypeVar{ID: 1}
	a := &TupleType{Elems: []Type{tv, TypeBool}}
	b := &TupleType{Elems: []Type{TypeFloat, TypeBool}}

	if err := Unify(a, b); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if resolveTV(tv) != TypeFloat {
		t.Fatalf("expected TypeFloat, got %s", resolveTV(tv))
	}
}

func TestUnifyTupleLengthMismatch(t *testing.T) {
	a := &TupleType{Elems: []Type{TypeInt}}
	b := &TupleType{Elems: []Type{TypeInt, TypeBool}}

	if err := Unify(a, b); err == nil {
		t.Fatal("expected error for tuple length mismatch, got nil")
	}
}

func TestUnifyFuncArityMismatch(t *testing.T) {
	a := &FuncType{Params: []Type{TypeInt}, Return: TypeBool}
	b := &FuncType{Params: []Type{TypeInt, TypeString}, Return: TypeBool}

	if err := Unify(a, b); err == nil {
		t.Fatal("expected error for arity mismatch, got nil")
	}
}

func TestUnifyStructNameEquality(t *testing.T) {
	a := &StructType{Name: "Foo"}
	b := &StructType{Name: "Foo"}
	if err := Unify(a, b); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	c := &StructType{Name: "Bar"}
	if err := Unify(a, c); err == nil {
		t.Fatal("expected error for different struct names, got nil")
	}
}

func TestUnifyEnumNameEquality(t *testing.T) {
	a := &EnumType{Name: "Color"}
	b := &EnumType{Name: "Color"}
	if err := Unify(a, b); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	c := &EnumType{Name: "Shape"}
	if err := Unify(a, c); err == nil {
		t.Fatal("expected error for different enum names, got nil")
	}
}

func TestUnifyDistinctNameEquality(t *testing.T) {
	a := &DistinctType{Name: "UserID"}
	b := &DistinctType{Name: "UserID"}
	if err := Unify(a, b); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	c := &DistinctType{Name: "PostID"}
	if err := Unify(a, c); err == nil {
		t.Fatal("expected error for different distinct names, got nil")
	}
}

// --- TypeParam_ unification tests ---

func TestUnifyTypeParamBinding(t *testing.T) {
	subs := map[*TypeParam_]Type{}
	tp := &TypeParam_{Name_: "T"}
	if err := UnifyWith(tp, TypeInt, subs); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if subs[tp] != TypeInt {
		t.Fatalf("expected T=Int, got T=%s", subs[tp])
	}
}

func TestUnifyTypeParamConsistent(t *testing.T) {
	subs := map[*TypeParam_]Type{}
	tp := &TypeParam_{Name_: "T"}
	if err := UnifyWith(tp, TypeInt, subs); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	// Same param, same type — should succeed
	if err := UnifyWith(tp, TypeInt, subs); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestUnifyTypeParamConflict(t *testing.T) {
	subs := map[*TypeParam_]Type{}
	tp := &TypeParam_{Name_: "T"}
	if err := UnifyWith(tp, TypeInt, subs); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	// Same param, different type — should fail
	if err := UnifyWith(tp, TypeString, subs); err == nil {
		t.Fatal("expected error for conflicting TypeParam binding, got nil")
	}
}

func TestUnifyTypeParamInList(t *testing.T) {
	subs := map[*TypeParam_]Type{}
	tp := &TypeParam_{Name_: "T"}
	a := &ListType{Elem: tp}
	b := &ListType{Elem: TypeInt}
	if err := UnifyWith(a, b, subs); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if subs[tp] != TypeInt {
		t.Fatalf("expected T=Int, got T=%s", subs[tp])
	}
}

func TestUnifyTypeParamInFunc(t *testing.T) {
	subs := map[*TypeParam_]Type{}
	tpT := &TypeParam_{Name_: "T"}
	tpU := &TypeParam_{Name_: "U"}
	a := &FuncType{Params: []Type{tpT}, Return: tpU}
	b := &FuncType{Params: []Type{TypeString}, Return: TypeBool}
	if err := UnifyWith(a, b, subs); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if subs[tpT] != TypeString {
		t.Fatalf("expected T=String, got T=%s", subs[tpT])
	}
	if subs[tpU] != TypeBool {
		t.Fatalf("expected U=Bool, got U=%s", subs[tpU])
	}
}

func TestUnifyTypeParamOnRightSide(t *testing.T) {
	subs := map[*TypeParam_]Type{}
	tp := &TypeParam_{Name_: "T"}
	if err := UnifyWith(TypeInt, tp, subs); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if subs[tp] != TypeInt {
		t.Fatalf("expected T=Int, got T=%s", subs[tp])
	}
}

// --- Substitute tests ---

func TestSubstituteSimple(t *testing.T) {
	tp := &TypeParam_{Name_: "T"}
	subs := map[*TypeParam_]Type{tp: TypeInt}
	result := Substitute(tp, subs)
	if result != TypeInt {
		t.Fatalf("expected Int, got %s", result)
	}
}

func TestSubstituteUnbound(t *testing.T) {
	tpT := &TypeParam_{Name_: "T"}
	subs := map[*TypeParam_]Type{tpT: TypeInt}
	tpU := &TypeParam_{Name_: "U"}
	result := Substitute(tpU, subs)
	if result != tpU {
		t.Fatalf("expected unbound U, got %s", result)
	}
}

func TestSubstituteList(t *testing.T) {
	tp := &TypeParam_{Name_: "T"}
	subs := map[*TypeParam_]Type{tp: TypeInt}
	result := Substitute(&ListType{Elem: tp}, subs)
	lt, ok := result.(*ListType)
	if !ok {
		t.Fatalf("expected ListType, got %T", result)
	}
	if lt.Elem != TypeInt {
		t.Fatalf("expected List<Int>, got List<%s>", lt.Elem)
	}
}

func TestSubstituteFunc(t *testing.T) {
	tpT := &TypeParam_{Name_: "T"}
	tpU := &TypeParam_{Name_: "U"}
	subs := map[*TypeParam_]Type{tpT: TypeInt, tpU: TypeString}
	result := Substitute(&FuncType{
		Params: []Type{tpT},
		Return: tpU,
	}, subs)
	ft, ok := result.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", result)
	}
	if ft.Params[0] != TypeInt {
		t.Fatalf("expected param Int, got %s", ft.Params[0])
	}
	if ft.Return != TypeString {
		t.Fatalf("expected return String, got %s", ft.Return)
	}
}

func TestSubstituteEnumTypeArgs(t *testing.T) {
	tpV := &TypeParam_{Name_: "V"}
	subs := map[*TypeParam_]Type{tpV: TypeInt}
	input := &EnumType{
		Name:     "Maybe",
		TypeArgs: []Type{tpV},
		Variants: []VariantDef{
			{Name: "Some", DataType: tpV},
			{Name: "None"},
		},
	}
	result := Substitute(input, subs)
	et, ok := result.(*EnumType)
	if !ok {
		t.Fatalf("expected EnumType, got %T", result)
	}
	if et.String() != "Maybe<Int>" {
		t.Fatalf("expected Maybe<Int>, got %s", et.String())
	}
	if et.Variants[0].DataType != TypeInt {
		t.Fatalf("expected Some(Int), got Some(%s)", et.Variants[0].DataType)
	}
}

func TestSubstituteResultTypeArgs(t *testing.T) {
	tpT := &TypeParam_{Name_: "T"}
	tpE := &TypeParam_{Name_: "E"}
	subs := map[*TypeParam_]Type{tpT: TypeInt, tpE: TypeString}
	input := &EnumType{
		Name:     "Result",
		TypeArgs: []Type{tpT, tpE},
		Variants: []VariantDef{
			{Name: "Ok", DataType: tpT},
			{Name: "Err", DataType: tpE},
		},
	}
	result := Substitute(input, subs)
	et, ok := result.(*EnumType)
	if !ok {
		t.Fatalf("expected EnumType, got %T", result)
	}
	if et.String() != "Result<Int, String>" {
		t.Fatalf("expected Result<Int, String>, got %s", et.String())
	}
}

// TestSubstitute_RecursiveEnumTerminates pins the recursive-enum
// termination guarantee of substituteWithNominals's `nominals` visited-set.
// Without it the `Node List<Tree<T>>` carrier cycles back into Tree until the
// Go stack overflows; with it, the cycle-break at the recursive back-pointer
// returns the same EnumType pointer unchanged and the walk terminates.
//
// Also pins the "TypeArgs still get substituted at the outer entry"
// claim — the result's TypeArgs[0] should be Int (the substitution),
// not the original TypeParam_. Stale TypeArgs on the *inner* recursive
// reference (inside `Node`'s `List<Tree<T>>`) are an acknowledged
// limitation; see substituteWithNominals's doc-comment.
func TestSubstitute_RecursiveEnumTerminates(t *testing.T) {
	// enum Tree<T> { Leaf T; Node List<Tree<T>> }
	tp := &TypeParam_{Name_: "T"}
	tree := &EnumType{
		Name:          "Tree",
		TypeArgs:      []Type{tp},
		TypeParamDefs: []*TypeParam_{tp},
	}
	tree.Variants = []VariantDef{
		{Name: "Leaf", Kind: VariantPositional, DataType: tp},
		{Name: "Node", Kind: VariantPositional, DataType: &ListType{Elem: tree}},
	}
	subs := map[*TypeParam_]Type{tp: TypeInt}
	// Termination is the primary assertion: a substitution that recursed into
	// the self-reference would never return.
	got, ok := Substitute(tree, subs).(*EnumType)
	if !ok {
		t.Fatalf("expected *EnumType, got %T", got)
	}
	if got.TypeArgs[0] != TypeInt {
		t.Errorf("expected TypeArgs[0]=Int, got %v", got.TypeArgs[0])
	}
}

func TestSubstituteMapGetScenario(t *testing.T) {
	// Simulates: host fn Get(m: Map<K, V>, key: K): Maybe<V>
	// Called as: map.Get(ages, "alice") where ages: Map<String, Int>
	subs := map[*TypeParam_]Type{}

	tpK := &TypeParam_{Name_: "K"}
	tpV := &TypeParam_{Name_: "V"}
	paramType := &MapType{Key: tpK, Val: tpV}
	argType := &MapType{Key: TypeString, Val: TypeInt}

	if err := UnifyWith(paramType, argType, subs); err != nil {
		t.Fatalf("unify failed: %v", err)
	}

	returnType := &EnumType{
		Name:     "Maybe",
		TypeArgs: []Type{tpV},
		Variants: []VariantDef{
			{Name: "Some", DataType: tpV},
			{Name: "None"},
		},
	}

	result := Substitute(returnType, subs)
	if result.String() != "Maybe<Int>" {
		t.Fatalf("expected Maybe<Int>, got %s", result.String())
	}

	et := result.(*EnumType)
	if et.Variants[0].DataType != TypeInt {
		t.Fatalf("expected Some(Int), got Some(%s)", et.Variants[0].DataType)
	}
}

func TestSubstituteNested(t *testing.T) {
	// List<(T) -> U> with T=Int, U=Bool → List<(Int) -> Bool>
	tpT := &TypeParam_{Name_: "T"}
	tpU := &TypeParam_{Name_: "U"}
	subs := map[*TypeParam_]Type{tpT: TypeInt, tpU: TypeBool}
	result := Substitute(&ListType{
		Elem: &FuncType{
			Params: []Type{tpT},
			Return: tpU,
		},
	}, subs)
	lt, ok := result.(*ListType)
	if !ok {
		t.Fatalf("expected ListType, got %T", result)
	}
	ft, ok := lt.Elem.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType elem, got %T", lt.Elem)
	}
	if ft.Params[0] != TypeInt || ft.Return != TypeBool {
		t.Fatalf("expected (Int) -> Bool, got %s", ft)
	}
}

func TestUnifyEnumTypeArgs(t *testing.T) {
	subs := map[*TypeParam_]Type{}
	tpV := &TypeParam_{Name_: "V"}
	a := &EnumType{
		Name:     "Maybe",
		TypeArgs: []Type{tpV},
	}
	b := &EnumType{
		Name:     "Maybe",
		TypeArgs: []Type{TypeInt},
	}
	if err := UnifyWith(a, b, subs); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if subs[tpV] != TypeInt {
		t.Fatalf("expected V=Int, got V=%s", subs[tpV])
	}
}

func TestUnifyEnumTypeMismatch(t *testing.T) {
	a := &EnumType{Name: "Maybe", TypeArgs: []Type{TypeInt}}
	b := &EnumType{Name: "Result", TypeArgs: []Type{TypeInt}}
	if err := Unify(a, b); err == nil {
		t.Fatal("expected error for Maybe vs Result")
	}
}

func TestSubstitute_AnonStruct_AppliesToFields(t *testing.T) {
	tParam := &TypeParam_{Name_: "T"}
	in := &AnonStructType{Fields: []FieldDef{
		{Name: "val", Type: tParam},
		{Name: "tag", Type: TypeString},
	}}
	subs := map[*TypeParam_]Type{tParam: TypeInt}
	out, ok := Substitute(in, subs).(*AnonStructType)
	if !ok {
		t.Fatalf("Substitute returned %T, want *AnonStructType", out)
	}
	if len(out.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(out.Fields))
	}
	if out.Fields[0].Name != "val" || out.Fields[0].Type != TypeInt {
		t.Errorf("fields[0] = {%s, %v}; want {val, Int}", out.Fields[0].Name, out.Fields[0].Type)
	}
	if out.Fields[1].Name != "tag" || out.Fields[1].Type != TypeString {
		t.Errorf("fields[1] = {%s, %v}; want {tag, String}", out.Fields[1].Name, out.Fields[1].Type)
	}
}

func TestSubstitute_AnonStruct_NoChangeReturnsSame(t *testing.T) {
	tParam := &TypeParam_{Name_: "T"}
	in := &AnonStructType{Fields: []FieldDef{
		{Name: "tag", Type: TypeString},
	}}
	subs := map[*TypeParam_]Type{tParam: TypeInt}
	out := Substitute(in, subs)
	if out != Type(in) {
		t.Error("Substitute on an anon struct with no relevant type params should return the same instance")
	}
}

func TestUnify_AnonStruct_BindsNestedTypeParam(t *testing.T) {
	tParam := &TypeParam_{Name_: "T"}
	a := &AnonStructType{Fields: []FieldDef{{Name: "val", Type: tParam}}}
	b := &AnonStructType{Fields: []FieldDef{{Name: "val", Type: TypeInt}}}
	subs := map[*TypeParam_]Type{}
	if err := UnifyWith(a, b, subs); err != nil {
		t.Fatalf("unify failed: %v", err)
	}
	if subs[tParam] != TypeInt {
		t.Errorf("expected T -> Int, got %v", subs[tParam])
	}
}

func TestUnify_AnonStruct_FieldOrderIgnored(t *testing.T) {
	a := &AnonStructType{Fields: []FieldDef{
		{Name: "x", Type: TypeInt},
		{Name: "y", Type: TypeString},
	}}
	b := &AnonStructType{Fields: []FieldDef{
		{Name: "y", Type: TypeString},
		{Name: "x", Type: TypeInt},
	}}
	if err := Unify(a, b); err != nil {
		t.Errorf("expected unify to succeed regardless of field order, got %v", err)
	}
}

func TestUnify_AnonStruct_FieldNameMismatch(t *testing.T) {
	a := &AnonStructType{Fields: []FieldDef{{Name: "x", Type: TypeInt}}}
	b := &AnonStructType{Fields: []FieldDef{{Name: "y", Type: TypeInt}}}
	if err := Unify(a, b); err == nil {
		t.Error("expected unify to fail when field names differ")
	}
}

// --- Unit ≡ () normalization ---
//
// The checker spells "returns nothing" two ways: an empty TupleType{} (printed
// `()`, e.g. a FuncType return inferred from a Unit-typed body) and the TypeUnit
// singleton (printed `Unit`). They denote the same type and must unify, so that a
// function returning Unit satisfies a parameter declared to return nothing (the
// sql_fragments case).

func TestUnifyEmptyTupleEqualsUnit(t *testing.T) {
	empty := &TupleType{}
	if err := Unify(empty, TypeUnit); err != nil {
		t.Fatalf("empty tuple vs Unit: expected nil, got %v", err)
	}
	if err := Unify(TypeUnit, empty); err != nil {
		t.Fatalf("Unit vs empty tuple: expected nil, got %v", err)
	}
}

func TestUnifyFuncReturningEmptyTupleVsUnit(t *testing.T) {
	a := &FuncType{Params: []Type{TypeString}, Return: &TupleType{}}
	b := &FuncType{Params: []Type{TypeString}, Return: TypeUnit}
	if err := Unify(a, b); err != nil {
		t.Fatalf("(String) -> () vs (String) -> Unit: expected nil, got %v", err)
	}
}

// A non-empty tuple must NOT collapse to Unit.
func TestUnifyNonEmptyTupleNotUnit(t *testing.T) {
	if err := Unify(&TupleType{Elems: []Type{TypeInt}}, TypeUnit); err == nil {
		t.Fatal("expected (Int,) vs Unit to fail, got nil")
	}
}

func TestNormalizeReturnEmptyTupleToUnit(t *testing.T) {
	if got := normalizeReturn(&TupleType{}); got != TypeUnit {
		t.Fatalf("normalizeReturn(()) = %s, want Unit", got)
	}
}

// TestSubstitute_BoundParamsAreNotCaptured pins the capture rule in
// maskBoundParams. Substituting a type param whose binding is a nominal
// re-walks that whole nominal with the same subs, so a nested already-
// instantiated generic (`Chan<Int>` here) is reached with a subs map keyed on
// a *TypeParam_ that means something else there: its own bound parameter.
// Without the mask the walk would rewrite that struct's fields and leave its
// TypeArgs alone, producing a `Chan<Int>` whose `sender` claims to be
// `Channel<Request>`.
func TestSubstitute_BoundParamsAreNotCaptured(t *testing.T) {
	// struct Chan<T> { sender: Channel<T> }  -- Channel is a generic extern
	chanParam := &TypeParam_{Name_: "T"}
	channelParam := &TypeParam_{Name_: "T"}
	channelOf := func(arg Type) Type {
		return &DistinctType{Name: "Channel", TypeParams: []string{"T"},
			TypeParamDefs: []*TypeParam_{channelParam}, TypeArgs: []Type{arg}}
	}
	chanOf := func(arg Type) *StructType {
		return &StructType{
			Name:          "Chan",
			TypeParams:    []string{"T"},
			TypeParamDefs: []*TypeParam_{chanParam},
			TypeArgs:      []Type{arg},
			Fields:        []FieldDef{{Name: "sender", Type: channelOf(chanParam)}},
		}
	}

	// enum Request { Get {reply: Chan<Int>} }
	request := &EnumType{Name: "Request", Variants: []VariantDef{{
		Name:   "Get",
		Kind:   VariantStruct,
		Fields: []FieldDef{{Name: "reply", Type: chanOf(TypeInt)}},
	}}}

	// The subs a `Chan<Request>` field read builds: the struct's own param
	// bound to Request. Applying it to `Channel<T>` must not follow the
	// binding into Request and rewrite the unrelated `Chan<Int>` inside it.
	got := Substitute(channelOf(chanParam), map[*TypeParam_]Type{chanParam: request})

	dt, ok := got.(*DistinctType)
	if !ok || len(dt.TypeArgs) != 1 {
		t.Fatalf("expected Channel<...>, got %T (%s)", got, got)
	}
	inner, ok := dt.TypeArgs[0].(*EnumType)
	if !ok || inner.Name != "Request" {
		t.Fatalf("expected Channel<Request>, got %s", got)
	}
	reply := inner.Variants[0].Fields[0].Type.(*StructType)
	if reply.TypeArgs[0] != TypeInt {
		t.Fatalf("reply.TypeArgs = %s, want [Int]", reply.TypeArgs[0])
	}
	sender := reply.Fields[0].Type.(*DistinctType)
	if sender.TypeArgs[0] != chanParam {
		t.Fatalf("reply's field was captured: sender = Channel<%s>, want Channel<T> "+
			"(fields stay written in terms of the struct's own bound param)", sender.TypeArgs[0])
	}
}

// A type param that still appears in the nominal's own TypeArgs is not bound
// by them — the partially-applied form a generic recursive type takes when it
// references itself — so it stays substitutable.
func TestSubstitute_SelfReferentialArgsStillSubstitute(t *testing.T) {
	tp := &TypeParam_{Name_: "T"}
	box := &StructType{
		Name:          "Box",
		TypeParams:    []string{"T"},
		TypeParamDefs: []*TypeParam_{tp},
		TypeArgs:      []Type{tp},
		Fields:        []FieldDef{{Name: "item", Type: tp}},
	}
	got := Substitute(box, map[*TypeParam_]Type{tp: TypeInt}).(*StructType)
	if got.TypeArgs[0] != TypeInt {
		t.Fatalf("TypeArgs = %s, want Int", got.TypeArgs[0])
	}
	if got.Fields[0].Type != TypeInt {
		t.Fatalf("Fields[0] = %s, want Int", got.Fields[0].Type)
	}
}
