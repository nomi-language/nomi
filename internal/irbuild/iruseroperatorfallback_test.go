package irbuild

import (
	"testing"
)

func TestIRUserOperator_CoercedOperandPreservesFallback(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", `type Score Int
type Points Int
interface Numeric {
 fn number(value: self): Int
}
impl Numeric for Points {
 fn number(value: Points): Int { Points(n) = value; n }
}
impl Add<Numeric, Score> for Score {
 fn add(lhs: Score, rhs: Numeric): Score { _ = lhs; Score(Numeric.number(rhs)) }
}
fn calculate(): Score { Score(2) + Points(3) }
fn main() { _ = calculate() }
`)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range got.IR {
		for _, f := range mod.Funcs() {
			if f.Name() == "calculate" {
				t.Fatal("coercion-dependent operator retained")
			}
		}
	}
}
