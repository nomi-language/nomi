package cldrdata

import (
	"context"
	"testing"
)

// A lazy store asks for its data once, when a frame first reads it, so a
// program that formats nothing never decodes the locale data its machine
// carries.
func TestLazyStoreLoadsOnFirstRead(t *testing.T) {
	loads := 0
	d := Empty()
	s := NewLazyStore(func() *Data { loads++; return d })
	ctx := WithStore(context.Background(), s)
	if loads != 0 {
		t.Fatalf("making the store loaded its data %d times", loads)
	}
	if got := From(ctx); got != d {
		t.Fatalf("From = %p, want the loaded data %p", got, d)
	}
	From(ctx)
	if !s.Replace(d, Empty()) {
		t.Fatal("Replace did not replace the loaded data")
	}
	if loads != 1 {
		t.Fatalf("the store loaded its data %d times, want once", loads)
	}
}
