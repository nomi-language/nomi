package ir

// RuleTempTyped's and RuleTempType's plants, each beside a control that
// differs in the one stated type.

import (
	"strings"
	"testing"
)

func typePos() Pos { return At("types.nomi", 3, 1) }

// typeViolations is every violation of rule Lint reports for f.
func typeViolations(t *testing.T, f *Func, rule LintRule) []Violation {
	t.Helper()
	return violationsFor(t, f, rule)
}

// typedAdd is `fn inc(n: Int): Int { n + 1 }` with every temporary typed.
func typedAdd() (*Func, *Block, Temp) {
	p := typePos()
	f := NewFunc(p, "inc")
	n := typedParam(f, NewSymbol("n"), IntType)
	b := f.NewBlock(p, "entry")
	one := NewInt(p, f.NewTemp(), 1)
	b.Append(one)
	sum := NewArith(p, f.NewTemp(), OpAdd, IntArith(OverflowFaults), n, one.Dst())
	b.Append(sum)
	return f, b, sum.Dst()
}

func TestTypeLint_TheControlIsClean(t *testing.T) {
	f, b, sum := typedAdd()
	b.SetTerm(NewReturn(typePos(), sum))
	if err := Lint(f); err != nil {
		t.Fatalf("a fully typed function is well formed and Lint rejected it:\n%v", err)
	}
	if got := f.TempType(sum); got != IntType {
		t.Fatalf("an Int addition's destination is typed %v at append, want Int", got)
	}
}

func TestTypeLint_AnUntypedTemporaryIsReported(t *testing.T) {
	p := typePos()
	build := func(typed bool) *Func {
		f, b, sum := typedAdd()
		call := NewCall(p, f.NewTemp(), OrdinaryCall, NewSymbol("show"), sum)
		if typed {
			f.SetType(call.Dst(), StringType)
		}
		b.Append(call)
		b.SetTerm(NewReturn(p, call.Dst()))
		return f
	}
	if vs := typeViolations(t, build(true), RuleTempTyped); len(vs) != 0 {
		t.Fatalf("the typed control reported %v", vs)
	}
	vs := typeViolations(t, build(false), RuleTempTyped)
	if len(vs) != 1 || !strings.Contains(vs[0].Why, "has no stored type") {
		t.Fatalf("an untyped call result: want one violation, got %v", vs)
	}

	// Allocated and named by nothing is not part of the graph: a lambda
	// whose arms all return allocates a result it never writes.
	f, b, sum := typedAdd()
	f.NewTemp()
	b.SetTerm(NewReturn(p, sum))
	if vs := typeViolations(t, f, RuleTempTyped); len(vs) != 0 {
		t.Fatalf("an allocated temporary nothing names was reported: %v", vs)
	}
}

func TestTypeLint_EachConsistencyConditionCatchesItsPlant(t *testing.T) {
	p := typePos()
	cases := []struct {
		name string
		why  string
		// build answers the function; plant says whether to deform it.
		build func(plant bool) *Func
	}{
		{"an intrinsic destination stated otherwise", "writes Int into", func(plant bool) *Func {
			f := NewFunc(p, "f")
			b := f.NewBlock(p, "entry")
			d := f.NewTemp()
			if plant {
				f.SetType(d, StringType)
			}
			b.Append(NewInt(p, d, 1))
			b.SetTerm(NewReturn(p, d))
			return f
		}},
		{"a derived shape the stored type contradicts", "writes a tuple into", func(plant bool) *Func {
			f := NewFunc(p, "f")
			b := f.NewBlock(p, "entry")
			one := NewInt(p, f.NewTemp(), 1)
			b.Append(one)
			tup := NewMakeTuple(p, f.NewTemp(), []Temp{one.Dst(), one.Dst()})
			f.SetType(tup.Dst(), NewTupleType(IntType, IntType))
			if plant {
				f.SetType(tup.Dst(), IntType)
			}
			b.Append(tup)
			b.SetTerm(NewReturn(p, tup.Dst()))
			return f
		}},
		{"a copy into storage of another type", "copied value", func(plant bool) *Func {
			f := NewFunc(p, "f")
			b := f.NewBlock(p, "entry")
			slot := f.NewTemp()
			ty, _ := NewTable().Concrete("Int", "Int")
			b.Append(NewSlot(p, slot, valued(ty, IntType)))
			var src *Const
			if plant {
				src = NewString(p, f.NewTemp(), "x")
			} else {
				src = NewInt(p, f.NewTemp(), 1)
			}
			b.Append(src)
			b.Append(NewCopy(p, slot, src.Dst()))
			b.SetTerm(NewReturn(p, slot))
			return f
		}},
		{"a slot whose storage is stored as another type", "is declared Int", func(plant bool) *Func {
			f := NewFunc(p, "f")
			b := f.NewBlock(p, "entry")
			slot := f.NewTemp()
			if plant {
				f.SetType(slot, StringType)
			}
			ty, _ := NewTable().Concrete("Int", "Int")
			b.Append(NewSlot(p, slot, valued(ty, IntType)))
			one := NewInt(p, f.NewTemp(), 1)
			b.Append(one)
			b.Append(NewCopy(p, slot, one.Dst()))
			b.SetTerm(NewReturn(p, slot))
			return f
		}},
		{"an Int operation over a String", "arithmetic operand", func(plant bool) *Func {
			f := NewFunc(p, "f")
			ty := IntType
			if plant {
				ty = StringType
			}
			n := f.AddParam(NewSymbol("n"), ValUnknown)
			f.SetType(n, ty)
			b := f.NewBlock(p, "entry")
			sum := NewArith(p, f.NewTemp(), OpAdd, IntArith(OverflowFaults), n, n)
			b.Append(sum)
			b.SetTerm(NewReturn(p, sum.Dst()))
			return f
		}},
		{"a negation of an Int", "negation operand", func(plant bool) *Func {
			f := NewFunc(p, "f")
			ty := BoolType
			if plant {
				ty = IntType
			}
			x := f.AddParam(NewSymbol("x"), ValUnknown)
			f.SetType(x, ty)
			b := f.NewBlock(p, "entry")
			n := NewNot(p, f.NewTemp(), x)
			b.Append(n)
			b.SetTerm(NewReturn(p, n.Dst()))
			return f
		}},
		{"a concatenation of an Int", "concat part", func(plant bool) *Func {
			f := NewFunc(p, "f")
			ty := StringType
			if plant {
				ty = IntType
			}
			x := f.AddParam(NewSymbol("x"), ValUnknown)
			f.SetType(x, ty)
			b := f.NewBlock(p, "entry")
			c := NewConcat(p, f.NewTemp(), x, x)
			b.Append(c)
			b.SetTerm(NewReturn(p, c.Dst()))
			return f
		}},
		{"a branch on an Int", "branched condition", func(plant bool) *Func {
			f := NewFunc(p, "f")
			ty := BoolType
			if plant {
				ty = IntType
			}
			x := f.AddParam(NewSymbol("x"), ValUnknown)
			f.SetType(x, ty)
			entry := f.NewBlock(p, "entry")
			then := f.NewBlock(p, "then")
			entry.SetTerm(NewBranch(p, x, then.ID(), then.ID()))
			then.SetTerm(NewReturnUnit(p))
			return f
		}},
		{"a parameter whose shape and type disagree", "declared shape Int", func(plant bool) *Func {
			f := NewFunc(p, "f")
			x := f.AddParam(NewSymbol("x"), ValInt)
			if plant {
				f.SetType(x, StringType)
			} else {
				f.SetType(x, IntType)
			}
			f.NewBlock(p, "entry").SetTerm(NewReturn(p, x))
			return f
		}},
		{"a parameter read into a temporary of another type", "read parameter", func(plant bool) *Func {
			f := NewFunc(p, "f")
			sym := NewSymbol("x")
			typedParam(f, sym, IntType)
			b := f.NewBlock(p, "entry")
			r := NewRefLocal(p, f.NewTemp(), sym)
			if plant {
				f.SetType(r.Dst(), StringType)
			}
			b.Append(r)
			b.SetTerm(NewReturn(p, r.Dst()))
			return f
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Lint(c.build(false)); err != nil {
				t.Fatalf("the control is well formed and Lint rejected it:\n%v", err)
			}
			vs := typeViolations(t, c.build(true), RuleTempType)
			found := false
			for _, v := range vs {
				found = found || strings.Contains(v.Why, c.why)
			}
			if !found {
				t.Fatalf("want a %s violation mentioning %q, got %v", RuleTempType, c.why, vs)
			}
		})
	}
}

// TestTypeLint_AnyAgreesWithAnyType is the one relaxation the rule makes: an
// untyped literal's missing type argument, or a type parameter's position, is
// Any, and it matches what it is stored beside.
func TestTypeLint_AnyAgreesWithAnyType(t *testing.T) {
	p := typePos()
	f := NewFunc(p, "f")
	b := f.NewBlock(p, "entry")
	slot := f.NewTemp()
	ty, _ := NewTable().Concrete("List<Int>", "List<Int>")
	b.Append(NewSlot(p, slot, valued(ty, NewListType(IntType))))
	empty := NewEmptyList(p, f.NewTemp(), nil)
	f.SetType(empty.Dst(), NewListType(AnyType))
	b.Append(empty)
	b.Append(NewCopy(p, slot, empty.Dst()))
	b.SetTerm(NewReturn(p, slot))
	if err := Lint(f); err != nil {
		t.Fatalf("an untyped empty list stored as a List<Int> is well formed:\n%v", err)
	}
	if NewListType(IntType).Accepts(NewListType(StringType)) {
		t.Fatal("List<Int> accepted List<String>")
	}
}

// TestTypeLint_ADeclaredTypeWidensToAnExistential: a struct or enum stored
// where an interface is stated is the same value in the same register, as a
// function returning `Speech` from a `Dog{...}` tail does. A scalar or a
// scalar distinct is not: it lives in another register bank, and an erased
// scalar distinct would lose its identity.
func TestTypeLint_ADeclaredTypeWidensToAnExistential(t *testing.T) {
	speech := NewIfaceType(NewSymbol("Speech"))
	if !speech.Accepts(NewStructType(NewSymbol("Dog"))) {
		t.Fatal("dyn Speech refused a struct")
	}
	if speech.Accepts(IntType) {
		t.Fatal("dyn Speech accepted an Int")
	}
	if speech.Accepts(NewDistinctType(NewSymbol("Meters"), IntType)) {
		t.Fatal("dyn Speech accepted a distinct over Int")
	}
	if NewStructType(NewSymbol("Dog")).Accepts(speech) {
		t.Fatal("a struct accepted an existential")
	}
}

func TestTypeLint_ACellMustStateItsValueType(t *testing.T) {
	tbl := NewTable()
	build := func(state bool) *Module {
		ty, _ := tbl.Concrete(new(int), "Clock")
		if state {
			ty.SetVal(NewHandleType(NewSymbol("Clock")))
		}
		m := NewModule("cells.nomi")
		m.DeclareCell(NewSymbol("appCell_Clock"), ty)
		return m
	}
	if err := LintModule(build(true)); err != nil {
		t.Fatalf("a cell whose type states a value type is well formed:\n%v", err)
	}
	err := LintModule(build(false))
	if err == nil || !strings.Contains(err.Error(), "states no value type") {
		t.Fatalf("a cell whose type states no value type: want a %s violation, got %v",
			RuleTempTyped, err)
	}
}

func TestValType_ClassesAndSpelling(t *testing.T) {
	point := NewSymbol("shapes.Point")
	cp := NewSymbol("codepoints.Codepoint")
	cases := []struct {
		ty    *ValType
		class RegClass
		text  string
	}{
		{UnitType, ClassNone, "Unit"},
		{IntType, ClassWord, "Int"},
		{FloatType, ClassWord, "Float"},
		{BoolType, ClassWord, "Bool"},
		{ByteType, ClassWord, "Byte"},
		{StringType, ClassStr, "String"},
		{BytesType, ClassStr, "Bytes"},
		{DecimalType, ClassRef, "Decimal"},
		{NewDistinctType(cp, IntType), ClassWord, "codepoints.Codepoint(Int)"},
		{NewDistinctType(NewSymbol("Expired"), nil), ClassNone, "Expired"},
		{NewStructType(point), ClassRef, "shapes.Point"},
		{NewEnumType(NewSymbol("maybe.Maybe"), IntType), ClassRef, "maybe.Maybe<Int>"},
		{NewTupleType(IntType, StringType), ClassRef, "(Int, String)"},
		{NewRecordType([]string{"x", "y"}, []*ValType{IntType, IntType}), ClassRef, "{x: Int, y: Int}"},
		{NewMapType(StringType, NewListType(IntType)), ClassRef, "Map<String, List<Int>>"},
		{NewFuncType([]*ValType{IntType}, BoolType), ClassRef, "(Int) -> Bool"},
		{NewIfaceType(NewSymbol("Speaker")), ClassRef, "dyn Speaker"},
		{AnyType, ClassRef, "Any"},
	}
	for _, c := range cases {
		if got := c.ty.Class(); got != c.class {
			t.Errorf("%s: class %v, want %v", c.text, got, c.class)
		}
		if got := c.ty.String(); got != c.text {
			t.Errorf("spelled %q, want %q", got, c.text)
		}
	}
	if NewStructType(point).Identical(NewStructType(NewSymbol("shapes.Point"))) {
		t.Error("two declarations sharing a printed name compared identical")
	}
	if !NewStructType(point).Identical(NewStructType(point)) {
		t.Error("one declaration compared as two")
	}
}

// TestValType_LayoutIsStatedOnceAndMayReachItself: a declared type's layout is
// stated after the type exists, so `Node { next: Maybe<Node> }` can name
// itself, and a second statement is a producer bug.
func TestValType_LayoutIsStatedOnceAndMayReachItself(t *testing.T) {
	node := NewStructType(NewSymbol("list.Node"))
	maybe := NewEnumType(NewSymbol("maybe.Maybe"), node)
	maybe.SetVariants([]Variant{
		{Name: "Some", Form: VariantPositional, Fields: []Field{{Type: node}}},
		{Name: "None", Form: VariantBare},
	})
	node.SetFields([]Field{{Name: "value", Type: IntType}, {Name: "next", Type: maybe}})
	if l := node.Layout(); l == nil || len(l.Fields) != 2 || l.Fields[1].Type != maybe {
		t.Fatalf("node layout: %+v", node.Layout())
	}
	if got := maybe.Layout().Variants[0].Fields[0].Type; got != node {
		t.Fatalf("Some's payload is %v, want the node itself", got)
	}
	if node.String() != "list.Node" || maybe.String() != "maybe.Maybe<list.Node>" {
		t.Fatalf("a recursive type must still spell finitely: %s, %s", node, maybe)
	}
	mustPanic := func(what string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s did not panic", what)
			}
		}()
		f()
	}
	mustPanic("a second layout", func() { node.SetFields(nil) })
	mustPanic("fields on an enum", func() { NewEnumType(NewSymbol("E")).SetFields(nil) })
	mustPanic("a positional variant with two payloads", func() {
		NewEnumType(NewSymbol("E")).SetVariants([]Variant{{Name: "V", Form: VariantPositional,
			Fields: []Field{{Type: IntType}, {Type: IntType}}}})
	})
	mustPanic("a bare variant with a payload", func() {
		NewEnumType(NewSymbol("E")).SetVariants([]Variant{{Name: "V", Form: VariantBare,
			Fields: []Field{{Type: IntType}}}})
	})
}
