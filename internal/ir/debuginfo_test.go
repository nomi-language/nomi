package ir

import (
	"bytes"
	"testing"
)

// debugFixture is wellFormed with a variable table and statement marks: the
// parameter `a`, and `sum` bound to the addition.
func debugFixture() *Func {
	at := func(line int) Pos { return At(lintFile(), line, 1) }
	f := NewFunc(at(1), "total")
	symA := NewSymbol("a")
	pa := typedParam(f, symA, IntType)
	f.AddVar("a", pa, "Int", Spanning(lintFile(), 1, 1, 3, 1), At(lintFile(), 1, 1))
	b := f.NewBlock(at(1), "entry")
	b.MarkStmt(at(2), StmtStatement)
	one := NewInt(at(2), f.NewTemp(), 1)
	b.Append(one)
	sum := NewArith(at(2), f.NewTemp(), OpAdd, IntArith(OverflowFaults), pa, one.Dst())
	b.Append(sum)
	f.AddVar("sum", sum.Dst(), "Int", Spanning(lintFile(), 2, 1, 3, 1), Pos{})
	b.MarkStmt(at(3), StmtStatement)
	b.SetTerm(NewReturn(at(3), sum.Dst()))
	return f
}

func TestDebugInfo_TheFixtureIsClean(t *testing.T) {
	f := debugFixture()
	if err := Lint(f); err != nil {
		t.Fatalf("the debug fixture is well formed and Lint rejected it:\n%v", err)
	}
	if len(f.Vars()) != 2 || len(f.Blocks()[0].Stmts()) != 2 {
		t.Fatalf("the fixture holds %d vars and %d marks, not 2 and 2",
			len(f.Vars()), len(f.Blocks()[0].Stmts()))
	}
}

func TestLint_VarTempNamesSomethingTheFunctionHas(t *testing.T) {
	f := debugFixture()
	stray := f.NewTemp()
	f.AddVar("ghost", stray, "Int", At(lintFile(), 2, 1), Pos{})
	got := violationsFor(t, f, RuleVarTemp)
	if len(got) != 1 {
		t.Fatalf("a variable over a temporary nothing writes: want one %s violation, got %v",
			RuleVarTemp, got)
	}
}

func TestLint_StmtMarksNameInstructionsInOrder(t *testing.T) {
	f := debugFixture()
	b := f.Blocks()[0]
	b.stmts = append(b.stmts, StmtMark{Index: 9, Pos: At(lintFile(), 3, 1)})
	got := violationsFor(t, f, RuleStmtMark)
	if len(got) != 1 {
		t.Fatalf("a mark past the block's end: want one %s violation, got %v", RuleStmtMark, got)
	}
}

func TestMarkStmt_SynthesizedPositionsAreNeverStops(t *testing.T) {
	f := debugFixture()
	b := f.NewBlock(At(lintFile(), 4, 1), "derived")
	b.MarkStmt(AtSynthesized(lintFile(), 4, 1), StmtStatement)
	b.MarkStmt(At(lintFile(), 5, 1), StmtStatement)
	b.MarkStmt(At(lintFile(), 5, 3), StmtStage)
	if got := b.Stmts(); len(got) != 1 || got[0].Pos.Col() != 1 || got[0].Kind != StmtStatement {
		t.Fatalf("want one statement mark at 5:1, got %v", got)
	}
}

func TestInsertSlot_MovesTheMarksAfterIt(t *testing.T) {
	f := debugFixture()
	b := f.Blocks()[0]
	b.InsertSlot(1, NewSlot(At(lintFile(), 2, 1), f.NewTemp(), v3Int()))
	marks := b.Stmts()
	if marks[0].Index != 0 || marks[1].Index != 3 {
		t.Fatalf("after a slot inserted at 1, want marks at 0 and 3, got %v", marks)
	}
}

func TestDefined_AnswersTheMustAnalysisAtAPoint(t *testing.T) {
	f := debugFixture()
	d := DefinedIn(f)
	sum := f.Vars()[1].Temp
	if d.Before(0, 1, sum) {
		t.Fatalf("sum is written by instruction 1, so it is not defined before it")
	}
	if !d.Before(0, 2, sum) || !d.Before(0, 0, f.Vars()[0].Temp) {
		t.Fatalf("sum is defined after instruction 1, and a parameter at entry")
	}
}

func TestImage_CarriesTheDebugTables(t *testing.T) {
	m := NewModule("debug")
	f := debugFixture()
	m.AddFunc(f)
	data, err := EncodeImage(Image{Modules: []*Module{m}})
	if err != nil {
		t.Fatal(err)
	}
	img, err := DecodeImage(data)
	if err != nil {
		t.Fatal(err)
	}
	g := img.Modules[0].Funcs()[0]
	if len(g.Vars()) != 2 || g.Vars()[1] != f.Vars()[1] {
		t.Fatalf("vars after a round trip: %v, want %v", g.Vars(), f.Vars())
	}
	if got, want := g.Blocks()[0].Stmts(), f.Blocks()[0].Stmts(); len(got) != len(want) || got[1] != want[1] {
		t.Fatalf("marks after a round trip: %v, want %v", got, want)
	}
	again, err := EncodeImage(img)
	if err != nil || !bytes.Equal(again, data) {
		t.Fatalf("re-encoding the decoded image changed it (err %v)", err)
	}
}
