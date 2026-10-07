package ir

import "testing"

// Deriving the shape of every temporary in a function reads its
// instructions once. Each lookup scanned the whole function before, which
// made lintOperandShapes quadratic in the function's size: a 20000-term
// string concatenation spent most of half a minute linting.
func TestShape_EveryTempReadsTheFunctionOnce(t *testing.T) {
	const n = 1000
	p := shapePos()
	f := NewFunc(p, "concat")
	b := f.NewBlock(p, "entry")
	acc := f.NewTemp()
	b.Append(NewString(p, acc, "a"))
	temps := []Temp{acc}
	for i := 0; i < n; i++ {
		part, copied, next := f.NewTemp(), f.NewTemp(), f.NewTemp()
		b.Append(NewString(p, part, "a"))
		b.Append(NewCopy(p, copied, part))
		b.Append(NewConcat(p, next, acc, copied))
		temps = append(temps, part, copied, next)
		acc = next
	}
	b.SetTerm(NewReturn(p, acc))

	sh := newShapes(f)
	for _, tmp := range temps {
		if got := sh.ofTemp(tmp, 0); got != ValString {
			t.Fatalf("%s derived %s, not String", tmp, got)
		}
	}
	if instrs := len(b.Instrs()); sh.visited != instrs {
		t.Errorf("%d instruction reads for %d instructions", sh.visited, instrs)
	}
	if vs := shapeViolations(t, f); len(vs) != 0 {
		t.Fatalf("a String concatenation was reported: %v", vs)
	}
}
