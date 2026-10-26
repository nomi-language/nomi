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
		got, ok := lineOf(ReadLine(fr))
		if !ok || got != want {
			t.Fatalf("ReadLine = %q, want Ok(Some(%q))", got, want)
		}
	}
	if got := ReadLine(fr); !atEnd(got) {
		t.Fatalf("ReadLine at end = %+v, want Ok(None)", got)
	}
	// A frame over no input reads end of input at once.
	if got := ReadLine(NewFrame(context.Background())); !atEnd(got) {
		t.Fatalf("ReadLine with no input = %+v, want Ok(None)", got)
	}
}

// A line that is not UTF-8 is InvalidText at its first bad byte, and the
// lines around it read as usual.
func TestReadLine_InvalidText(t *testing.T) {
	fr := NewFrame(WithInput(context.Background(), NewInput(strings.NewReader("ok\nab\xffc\nnext\n"))))
	if got, _ := lineOf(ReadLine(fr)); got != "ok" {
		t.Fatalf("first line = %q", got)
	}
	got := ReadLine(fr)
	want := IOError{Tag: TagIOInvalidText, Path: StdinPath, Offset: 2}
	if got.Tag != TagErr || got.Err != want {
		t.Fatalf("ReadLine = %+v, want Err(%+v)", got, want)
	}
	if got, _ := lineOf(ReadLine(fr)); got != "next" {
		t.Fatalf("line after the bad one = %q", got)
	}
}

// lineOf is the line a read answered and whether it answered one.
func lineOf(r Result[Maybe[string], IOError]) (string, bool) {
	if r.Tag != TagOk || r.Ok.Tag != TagSome {
		return "", false
	}
	return r.Ok.Some, true
}

// atEnd reports whether a read answered the end of input, Ok(None).
func atEnd(r Result[Maybe[string], IOError]) bool {
	return r.Tag == TagOk && r.Ok.Tag == TagNone
}
