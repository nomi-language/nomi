package ir

import "testing"

// The `match` and `destructure` classes. What a pattern is and what a
// destructure is: see match.go and destructure.go, which carry the argument.

func matchPos() Pos { return At("t.nomi", 7, 3) }

// TestMatch_ABooleanAnswerIsNotAValue is the class's central shape decision,
// stated as a test so it cannot drift into one.
//
// No kind writes anything unless its answering constructor gave it a
// destination. A test's answer is not a temporary by default, so making Dst
// the Bool would force a name nothing wants.
func TestMatch_ABooleanAnswerIsNotAValue(t *testing.T) {
	pos := matchPos()
	enum := NewSymbol("Shape")
	for _, node := range []*Match{
		NewMatchLit(pos, 9, 8),
		NewMatchVariant(pos, 9, enum, "Circle"),
		NewMatchListLen(pos, 9, 2),
		NewMatchListMin(pos, 9, 2),
	} {
		if got := node.Dst(); got != NoTemp || node.Answers() {
			t.Errorf("%s writes %v", node.Kind(), got)
		}
	}
}

// TestMatch_ReadsItsSubjectAndItsSecondOperand.
//
// An ARITY is part of the question and not an operand: Nomi has no list
// pattern whose length is computed, so the count is on the node. A LITERAL is
// an operand, and that is what makes the class need no predicate — the thing
// that can fail or have an effect at a test is the literal's own evaluation,
// which is somebody else's node.
func TestMatch_ReadsItsSubjectAndItsSecondOperand(t *testing.T) {
	pos := matchPos()
	for _, tc := range []struct {
		node *Match
		want []Temp
	}{
		{NewMatchLit(pos, 9, 8), []Temp{9, 8}},
		{NewMatchVariant(pos, 9, NewSymbol("S"), "C"), []Temp{9}},
		{NewMatchListLen(pos, 9, 0), []Temp{9}},
		{NewMatchListMin(pos, 9, 3), []Temp{9}},
	} {
		got := tc.node.AppendUses(nil)
		if len(got) != len(tc.want) {
			t.Errorf("%s reads %v, want %v", tc.node.Kind(), got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s reads %v, want %v", tc.node.Kind(), got, tc.want)
				break
			}
		}
	}
}

// TestMatch_TheProducerCannotBuildAQuestionNothingAsks.
//
// The interesting row is the LAST one. `MatchListMin(0)` is satisfied by every
// list, so it is not a question, and a consumer handed one would emit a test
// that is always true. That is a vacuous question, which this class refuses
// at construction, as opposed to a vacuous predicate field that is always
// false.
// `MatchListLen(0)` is a real question: it is the `[]` pattern.
func TestMatch_TheProducerCannotBuildAQuestionNothingAsks(t *testing.T) {
	pos := matchPos()
	for _, tc := range []struct {
		what string
		call func()
	}{
		{"a literal test with no literal", func() { NewMatchLit(pos, 9, NoTemp) }},
		{"a variant test with no enum", func() { NewMatchVariant(pos, 9, nil, "C") }},
		{"a variant test with no variant", func() { NewMatchVariant(pos, 9, NewSymbol("S"), "") }},
		{"a negative element count", func() { NewMatchListLen(pos, 9, -1) }},
		{"a minimum of zero elements", func() { NewMatchListMin(pos, 9, 0) }},
		{"a test with no subject", func() { NewMatchListLen(pos, NoTemp, 1) }},
		{"a test with no position", func() { NewMatchListLen(Pos{}, 9, 1) }},
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

	// PLANT A POSITIVE: the well-formed shapes are accepted, including the
	// `[]` pattern, so the panics above are about what they name and not about
	// the harness.
	NewMatchLit(pos, 9, 8)
	NewMatchVariant(pos, 9, NewSymbol("S"), "C")
	NewMatchListLen(pos, 9, 0)
	NewMatchListMin(pos, 9, 1)
}

// TestMatch_NoFieldCarriesAConsumerDECISION, which is the IR/consumer line for
// this class.
//
// The node is read back field by field and each field is one of: which
// question, an operand, a declaration identity, a variant NAME, a count, a
// position. Nothing on it is a tag number, a tag field name, a Go comparison
// operator, a `found` variable, an arm index, an arm count, or a default-arm
// bit — and exhaustiveness in particular is the front end's.
func TestMatch_NoFieldCarriesAConsumerDecision(t *testing.T) {
	enum := NewSymbol("Shape")
	m := NewMatchVariant(At("t.nomi", 7, 3), 9, enum, "Circle")

	if m.Sym() != enum {
		t.Error("a variant test does not name the enum it was given")
	}
	if m.Variant() != "Circle" {
		t.Errorf("variant is %q", m.Variant())
	}
	// The two facts a consumer needs and the node refuses to hold: there is no
	// accessor for either, so this is the whole statement.
	if m.Arity() != 0 || m.Arg() != NoTemp {
		t.Errorf("a variant test carries an arity %d or an operand %v", m.Arity(), m.Arg())
	}
	// A literal test names no declaration: the literal is an operand, and
	// WHICH equality answers it is a function of that operand's type.
	if l := NewMatchLit(At("t.nomi", 7, 3), 9, 8); l.Sym() != nil || l.Variant() != "" {
		t.Errorf("a literal test names %v / %q", l.Sym(), l.Variant())
	}
}

// TestMatchDestructure_EveryNodeCarriesItsOwnPosition holds two more classes
// to the per-node position rule.
func TestMatchDestructure_EveryNodeCarriesItsOwnPosition(t *testing.T) {
	pos := At("t.nomi", 12, 5)
	b := NewRegion(pos, "r").NewBlock(pos, "entry")
	b.Append(NewMatchVariant(pos, 1, NewSymbol("S"), "C"))
	b.Append(NewBind(pos, 2, 1, NewSymbol("x")))
	b.Append(NewProjElem(pos, 3, 1, 0, ValUnknown))
	b.Append(NewProjSuffix(pos, 4, 1, 1, ValUnknown))
	for _, in := range b.Instrs() {
		if got := in.Pos(); got.Line() != 12 || got.Col() != 5 || got.File() != "t.nomi" {
			t.Errorf("%s carries %s", in, got)
		}
	}
}

// TestBind_IsADeclarationAndNotACopy.
//
// Two bindings of ONE name are two declarations, which is why `NewSymbol`
// mints and why `Table.Symbol`'s interning would be wrong here. The test is
// the pointer comparison: same text, two identities.
func TestBind_IsADeclarationAndNotACopy(t *testing.T) {
	pos := matchPos()
	first := NewBind(pos, 1, 9, NewSymbol("x"))
	second := NewBind(pos, 2, 9, NewSymbol("x"))
	if first.Sym() == second.Sym() {
		t.Error("two bindings of `x` share one declaration identity")
	}
	if first.Sym().Name() != "x" || second.Sym().Name() != "x" {
		t.Error("a binding does not print the name it was given")
	}
	if uses := first.AppendUses(nil); len(uses) != 1 || uses[0] != 9 {
		t.Errorf("a binding reads %v, want exactly its source", uses)
	}
	if first.Dst() != 1 || first.Src() != 9 {
		t.Errorf("a binding names %v from %v", first.Dst(), first.Src())
	}

	// A Copy has no name, which is the difference. `tupleDestructure` builds
	// a Bind for a fresh name and a Copy for a rebind, which reuses the
	// binding it finds.
	c := NewCopy(pos, 1, 9)
	if c.Dst() != 1 || c.Src() != 9 {
		t.Errorf("a copy names %v from %v", c.Dst(), c.Src())
	}
}

// TestBind_TheProducerCannotBuildANamelessBinding.
func TestBind_TheProducerCannotBuildANamelessBinding(t *testing.T) {
	pos := matchPos()
	for _, tc := range []struct {
		what string
		call func()
	}{
		{"a binding with no identity", func() { NewBind(pos, 1, 9, nil) }},
		{"a binding with an empty name", func() { NewBind(pos, 1, 9, NewSymbol("")) }},
		{"a binding with no source", func() { NewBind(pos, 1, NoTemp, NewSymbol("x")) }},
		{"a binding with no destination", func() { NewBind(pos, NoTemp, 9, NewSymbol("x")) }},
		{"a binding with no position", func() { NewBind(Pos{}, 1, 9, NewSymbol("x")) }},
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
	NewBind(pos, 1, 9, NewSymbol("x"))
}

// TestProjElemSuffix_AreStructuralReadsAtAConstantPosition.
//
// They are in `proj` and not in `match` because their location is fixed by the
// subject's static type plus a compile-time constant, which is `ProjSlot`'s
// property. An element read at a COMPUTED position is `List.at`, a call.
func TestProjElemSuffix_AreStructuralReadsAtAConstantPosition(t *testing.T) {
	pos := matchPos()
	for _, p := range []*Proj{
		NewProjElem(pos, 1, 9, 2, ValUnknown),
		NewProjSuffix(pos, 1, 9, 2, ValUnknown),
	} {
		if uses := p.AppendUses(nil); len(uses) != 1 || uses[0] != 9 {
			t.Errorf("%s reads %v, want exactly the subject", p.Kind(), uses)
		}
		if p.Sym() != nil || p.Name() != "" {
			t.Errorf("%s names %v / %q; it is structural", p.Kind(), p.Sym(), p.Name())
		}
		if p.Index() != 2 {
			t.Errorf("%s is at %d, want 2", p.Kind(), p.Index())
		}
		if p.Faults() {
			t.Errorf("%s claims it can fault", p.Kind())
		}
	}

	// A SUFFIX AT ZERO IS REAL — `[..rest]` binds the whole list — where
	// `MatchListMin(0)` is not a question. The asymmetry is the point: the
	// navigation is total and only the test is refutable.
	NewProjSuffix(pos, 1, 9, 0, ValUnknown)
	for _, tc := range []struct {
		what string
		call func()
	}{
		{"a negative element position", func() { NewProjElem(pos, 1, 9, -1, ValUnknown) }},
		{"a negative suffix position", func() { NewProjSuffix(pos, 1, 9, -1, ValUnknown) }},
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
}

// TestMatchDestructure_StringNamesTheOperationAndItsOperands, so a diagnostic
// and a test failure say which shape they are about.
func TestMatchDestructure_StringNamesTheOperationAndItsOperands(t *testing.T) {
	pos := matchPos()
	sym := NewSymbol("Shape")
	for _, tc := range []struct {
		got  string
		want string
	}{
		{NewMatchLit(pos, 9, 8).String(), "match lit t9 t8"},
		{NewMatchVariant(pos, 9, sym, "Circle").String(), "match variant t9 Shape.Circle"},
		{NewMatchListLen(pos, 9, 2).String(), "match listlen t9 2"},
		{NewMatchListMin(pos, 9, 2).String(), "match listmin t9 2"},
		{NewBind(pos, 1, 9, NewSymbol("radius")).String(), "t1 = bind radius t9"},
		{NewProjElem(pos, 1, 9, 2, ValUnknown).String(), "t1 = elem t9.2"},
		{NewProjSuffix(pos, 1, 9, 2, ValUnknown).String(), "t1 = suffix t9.2"},
	} {
		if tc.got != tc.want {
			t.Errorf("String() is %q, want %q", tc.got, tc.want)
		}
	}
}
