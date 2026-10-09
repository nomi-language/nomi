package ast

import "testing"

// TestTriviaCarrier_Interface verifies that all Node types satisfy HasTrivia.
func TestTriviaCarrier_Interface(t *testing.T) {
	nodes := []Node{
		&SimpleType{}, &QualifiedType{}, &GenericType{}, &FuncType{}, &SelfType{}, &AnonStructType{},
		&IntLit{}, &FloatLit{}, &StringLit{}, &StringInterp{}, &ListLit{}, &VectorLit{}, &SetLit{},
		&ListSpreadLit{}, &MapLit{}, &TupleLit{}, &Ident{}, &TypeIdent{},
		&Unary{}, &Binary{}, &GroupedExpr{}, &If{}, &Block{}, &Call{}, &FieldAccess{},
		&StructDef{}, &StructLit{}, &EnumDef{}, &TypeDef{}, &TypeAlias{},
		&OnceBinding{}, &Binding{}, &TupleDestructure{}, &StructDestructure{},
		&MapDestructure{}, &PatternDestructure{}, &PatternBinding{}, &BindingElse{}, &DistinctDestructure{}, &ExprStmt{},
		&InterfaceMethod{}, &InterfaceDef{},
		&ImportStmt{}, &FuncDef{}, &ExternFunc{}, &ExternType{}, &Lambda{},
		&Return{}, &Break{}, &Continue{}, &TryOp{}, &Placeholder{},
		&NamedArg{}, &Case{}, &WildcardPattern{}, &IdentPattern{}, &AsPattern{},
		&EnumPattern{}, &StructPattern{}, &TuplePattern{}, &ListPattern{},
		&MapPattern{}, &TestDecl{}, &Assertion{}, &Dbg{},
	}
	for _, n := range nodes {
		h, ok := n.(HasTrivia)
		if !ok {
			t.Errorf("%T does not implement HasTrivia", n)
			continue
		}
		// Round-trip: add a leading, add a trailing, read back.
		h.AddLeading(Trivia{Kind: TriviaComment, Text: "// x", Line: 1, Col: 1})
		h.AddTrailing(Trivia{Kind: TriviaComment, Text: "// y", Line: 1, Col: 2})
		if got := h.GetLeading(); len(got) != 1 || got[0].Text != "// x" {
			t.Errorf("%T leading roundtrip failed: %v", n, got)
		}
		if got := h.GetTrailing(); len(got) != 1 || got[0].Text != "// y" {
			t.Errorf("%T trailing roundtrip failed: %v", n, got)
		}
	}
}

func TestTrivia_BlankLineKind(t *testing.T) {
	c := TriviaCarrier{}
	c.AddLeading(Trivia{Kind: TriviaBlankLine, Line: 2, Col: 1})
	if c.Leading[0].Kind != TriviaBlankLine {
		t.Errorf("kind mismatch")
	}
}
