package rt

import (
	"context"
	"strings"
	"testing"
)

func TestReadLine(t *testing.T) {
	in := NewInput(strings.NewReader("one\r\ntwo\nlast"))
	fr := NewFrame(WithInput(context.Background(), in))
	for _, want := range []string{"one", "two", "last"} {
		got := ReadLine(fr)
		if got.Tag != TagOk || got.Ok != want {
			t.Fatalf("ReadLine = %+v, want Ok(%q)", got, want)
		}
	}
	if got := ReadLine(fr); got.Tag != TagErr || got.Err != "eof" {
		t.Fatalf("ReadLine at end = %+v, want Err(eof)", got)
	}
	// A frame over no input reads end of input at once.
	if got := ReadLine(NewFrame(context.Background())); got.Tag != TagErr || got.Err != "eof" {
		t.Fatalf("ReadLine with no input = %+v, want Err(eof)", got)
	}
}
