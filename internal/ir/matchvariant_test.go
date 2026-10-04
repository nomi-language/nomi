package ir

import "testing"

func TestMatchVariant_OptionalAnswer(t *testing.T) {
	at := At("variant.nomi", 1, 1)
	sym := NewSymbol("signals.Signal")
	plain := NewMatchVariant(at, Temp(1), sym, "Value")
	answered := NewMatchVariantInto(at, Temp(2), Temp(1), sym, "Value")
	if plain.Answers() || plain.Dst() != NoTemp || !answered.Answers() || answered.Dst() != Temp(2) {
		t.Fatal("variant test lost its optional destination")
	}
	if answered.Sym() != sym || answered.Variant() != "Value" {
		t.Fatal("variant test lost declaration identity")
	}
	uses := answered.AppendUses(nil)
	if len(uses) != 1 || uses[0] != Temp(1) {
		t.Fatalf("variant operands = %v", uses)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("answering variant test accepted NoTemp destination")
		}
	}()
	NewMatchVariantInto(at, NoTemp, Temp(1), sym, "Value")
}
