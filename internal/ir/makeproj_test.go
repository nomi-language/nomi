package ir

import "testing"

// The `make` and `proj` classes. What a construction is and what a
// projection is: see make.go and proj.go, which carry the argument.

func makeProjPos() Pos { return At("t.nomi", 4, 9) }

// TestMake_OperandsAreTheValuesTheNodeReads is the invariant `AppendUses` has
// to hold for this class, and the three places it is not simply "every field".
//
// A range's absent endpoint is NoTemp and names no value. A list's tail is an
// operand and is not in the operand list. A variant's payloads are the ones
// the producer HAS, which is not one per declared payload — a payload with no
// run-time representation contributes nothing to read.
func TestMake_OperandsAreTheValuesTheNodeReads(t *testing.T) {
	pos := makeProjPos()
	enum := NewSymbol("Shape")

	for _, tc := range []struct {
		what string
		node *Make
		want []Temp
	}{
		{"a struct reads every field",
			NewMakeStruct(pos, 1, NewSymbol("Point"), []string{"x", "y"}, []Temp{2, 3}),
			[]Temp{2, 3}},
		{"a variant reads the payloads it was given",
			NewMakeVariant(pos, 1, enum, "Circle", []Temp{2}), []Temp{2}},
		{"a bare-storage variant reads nothing",
			NewMakeVariant(pos, 1, enum, "Dot", nil), nil},
		{"a map reads keys and values alike",
			NewMakeMap(pos, 1, []Temp{2, 3, 4, 5}), []Temp{2, 3, 4, 5}},
		{"a list reads its tail too",
			NewMakeList(pos, 1, []Temp{2, 3}, 4), []Temp{2, 3, 4}},
		{"a list with no spread reads only its elements",
			NewMakeList(pos, 1, []Temp{2, 3}, NoTemp), []Temp{2, 3}},
		{"a range skips an absent endpoint",
			NewMakeRange(pos, 1, 2, NoTemp, false), []Temp{2}},
		{"a range with neither endpoint reads nothing",
			NewMakeRange(pos, 1, NoTemp, NoTemp, false), nil},
	} {
		got := tc.node.AppendUses(nil)
		if len(got) != len(tc.want) {
			t.Errorf("%s: reads %v, want %v", tc.what, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: reads %v, want %v", tc.what, got, tc.want)
				break
			}
		}
	}
}

// TestMake_TheProducerCannotBuildAShapeThatNamesNothing.
//
// Every panic here is a producer bug with no user input that reaches it,
// which is requirePos's standard applied to the rest of the node: a
// construction of a DECLARED type without that declaration's identity is the
// receipt-not-identity failure `table.go` names, and a record whose names do
// not match its values is a type nobody can read back.
func TestMake_TheProducerCannotBuildAShapeThatNamesNothing(t *testing.T) {
	pos := makeProjPos()
	for _, tc := range []struct {
		what string
		call func()
	}{
		{"a struct with no declaration", func() { NewMakeStruct(pos, 1, nil, nil, nil) }},
		{"a struct with more names than values",
			func() { NewMakeStruct(pos, 1, NewSymbol("P"), []string{"a", "b"}, []Temp{2}) }},
		{"a struct with no names for its values",
			func() { NewMakeStruct(pos, 1, NewSymbol("P"), nil, []Temp{2}) }},
		{"a variant with no enum", func() { NewMakeVariant(pos, 1, nil, "C", nil) }},
		{"a variant with no name", func() { NewMakeVariant(pos, 1, NewSymbol("S"), "", nil) }},
		{"a distinct with no declaration", func() { NewMakeDistinct(pos, 1, nil, 2) }},
		{"a record with more names than values",
			func() { NewMakeRecord(pos, 1, []string{"a", "b"}, []Temp{2}) }},
		{"a one-component tuple", func() { NewMakeTuple(pos, 1, []Temp{2}) }},
		{"a map with an odd operand count", func() { NewMakeMap(pos, 1, []Temp{2, 3, 4}) }},
		{"an unbounded INCLUSIVE range", func() { NewMakeRange(pos, 1, 2, NoTemp, true) }},
		{"a construction with no destination",
			func() { NewMakeTuple(pos, NoTemp, []Temp{2, 3}) }},
		{"a construction with no position", func() { NewMakeTuple(Pos{}, 1, []Temp{2, 3}) }},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s was accepted", tc.what)
				}
			}()
			tc.call()
		}()
	}

	// PLANT A POSITIVE: the same constructors accept the well-formed shape, so
	// the panics above are about what they name and not about the harness.
	NewMakeStruct(pos, 1, NewSymbol("Point"), []string{"x"}, []Temp{2})
	NewMakeVariant(pos, 1, NewSymbol("Shape"), "Circle", []Temp{2})
	NewMakeRecord(pos, 1, []string{"a"}, []Temp{2})
	NewMakeTuple(pos, 1, []Temp{2, 3})
	NewMakeMap(pos, 1, []Temp{2, 3})
	NewMakeRange(pos, 1, 2, 3, true)
}

// TestProj_ReadsExactlyItsSubject.
//
// An INDEX is part of the operation and not an operand: Nomi has no projection
// at a computed position, so a tuple slot and a payload position are on the
// node and nothing about them is a temporary.
func TestProj_ReadsExactlyItsSubject(t *testing.T) {
	pos := makeProjPos()
	sym := NewSymbol("T")
	for _, p := range []*Proj{
		NewProjField(pos, 1, 9, sym, "x", ValUnknown),
		NewProjRecordField(pos, 1, 9, "x", ValUnknown),
		NewProjSlot(pos, 1, 9, 2, ValUnknown),
		NewProjPayload(pos, 1, 9, sym, "Circle", 0, ValUnknown),
		NewProjEnumField(pos, 1, 9, sym, "radius", true, ValUnknown),
		NewProjInner(pos, 1, 9, sym, ValUnknown),
	} {
		uses := p.AppendUses(nil)
		if len(uses) != 1 || uses[0] != 9 {
			t.Errorf("%s reads %v, want exactly the subject", p.Kind(), uses)
		}
		if p.Subject() != 9 || p.Dst() != 1 {
			t.Errorf("%s names subject %v into %v", p.Kind(), p.Subject(), p.Dst())
		}
	}
}

// TestProj_TheProducerCannotBuildAReadThatNamesNothing.
func TestProj_TheProducerCannotBuildAReadThatNamesNothing(t *testing.T) {
	pos := makeProjPos()
	sym := NewSymbol("T")
	for _, tc := range []struct {
		what string
		call func()
	}{
		{"a field with no declaration", func() { NewProjField(pos, 1, 2, nil, "x", ValUnknown) }},
		{"a record field with no name", func() { NewProjRecordField(pos, 1, 2, "", ValUnknown) }},
		{"a negative slot", func() { NewProjSlot(pos, 1, 2, -1, ValUnknown) }},
		{"a payload with no variant", func() { NewProjPayload(pos, 1, 2, sym, "", 0, ValUnknown) }},
		{"an enum field with no name", func() { NewProjEnumField(pos, 1, 2, sym, "", false, ValUnknown) }},
		{"an unwrap with no declaration", func() { NewProjInner(pos, 1, 2, nil, ValUnknown) }},
		{"a read with no subject", func() { NewProjSlot(pos, 1, NoTemp, 0, ValUnknown) }},
		{"a read with no destination", func() { NewProjSlot(pos, NoTemp, 2, 0, ValUnknown) }},
		{"a read with no position", func() { NewProjSlot(Pos{}, 1, 2, 0, ValUnknown) }},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s was accepted", tc.what)
				}
			}()
			tc.call()
		}()
	}

	NewProjField(pos, 1, 2, sym, "x", ValUnknown)
	NewProjRecordField(pos, 1, 2, "x", ValUnknown)
	NewProjSlot(pos, 1, 2, 0, ValUnknown)
	NewProjPayload(pos, 1, 2, sym, "Circle", 0, ValUnknown)
	NewProjEnumField(pos, 1, 2, sym, "radius", false, ValUnknown)
	NewProjInner(pos, 1, 2, sym, ValUnknown)
}

// TestMakeProj_EveryNodeCarriesItsOwnPosition holds two more classes to the
// per-node position rule.
func TestMakeProj_EveryNodeCarriesItsOwnPosition(t *testing.T) {
	pos := At("t.nomi", 12, 5)
	b := NewRegion(pos, "r").NewBlock(pos, "entry")
	b.Append(NewMakeTuple(pos, 1, []Temp{2, 3}))
	b.Append(NewProjSlot(pos, 4, 1, 0, ValUnknown))
	for _, in := range b.Instrs() {
		if got := in.Pos(); got.Line() != 12 || got.Col() != 5 || got.File() != "t.nomi" {
			t.Errorf("%s carries %s", in, got)
		}
	}
}

// TestMakeProj_StringNamesTheOperationAndItsOperands, so a diagnostic and a
// test failure say which shape they are about.
func TestMakeProj_StringNamesTheOperationAndItsOperands(t *testing.T) {
	pos := makeProjPos()
	sym := NewSymbol("Shape")
	for _, tc := range []struct {
		got  string
		want string
	}{
		{NewMakeVariant(pos, 1, sym, "Circle", []Temp{2}).String(),
			"t1 = make variant Shape.Circle t2"},
		{NewMakeRecord(pos, 1, []string{"x", "y"}, []Temp{2, 3}).String(),
			"t1 = make record x: t2, y: t3"},
		{NewMakeList(pos, 1, []Temp{2}, 3).String(), "t1 = make list t2 ..t3"},
		{NewMakeRange(pos, 1, 2, 3, true).String(), "t1 = make range t2, t3 inclusive"},
		{NewProjSlot(pos, 1, 2, 3, ValUnknown).String(), "t1 = slot t2.3"},
		{NewProjPayload(pos, 1, 2, sym, "Circle", 0, ValUnknown).String(),
			"t1 = payload t2 Shape.Circle[0]"},
		{NewProjEnumField(pos, 1, 2, sym, "radius", true, ValUnknown).String(),
			"t1 = enumfield t2.radius faults"},
	} {
		if tc.got != tc.want {
			t.Errorf("String() is %q, want %q", tc.got, tc.want)
		}
	}
}
