package format

import "testing"

func TestConcat_EmptyIsNil(t *testing.T) {
	if _, ok := Concat().(docNil); !ok {
		t.Errorf("empty Concat should be Nil")
	}
}

func TestConcat_SingleIsPassthrough(t *testing.T) {
	tx := Text("a")
	if Concat(tx) != tx {
		t.Errorf("single-element Concat should return its only element unchanged")
	}
}

func TestConcat_TwoArgs(t *testing.T) {
	d := Concat(Text("a"), Text("b"))
	if _, ok := d.(docConcat); !ok {
		t.Errorf("two-arg Concat should be docConcat, got %T", d)
	}
}

func TestConcat_ThreeArgs(t *testing.T) {
	d := Concat(Text("a"), Text("b"), Text("c"))
	// Left-folded: docConcat(docConcat("a","b"), "c")
	outer, ok := d.(docConcat)
	if !ok {
		t.Fatalf("outer type = %T", d)
	}
	if _, ok := outer.a.(docConcat); !ok {
		t.Errorf("expected left-folded Concat, got right-folded")
	}
}

func TestLine_vs_LineOrEmpty(t *testing.T) {
	if Line().(docLine).alt != " " {
		t.Errorf("Line alt = %q, want %q", Line().(docLine).alt, " ")
	}
	if LineOrEmpty().(docLine).alt != "" {
		t.Errorf("LineOrEmpty alt = %q, want empty string", LineOrEmpty().(docLine).alt)
	}
}
