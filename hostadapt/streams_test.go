package hostadapt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

type closeCounter struct {
	io.Reader
	closed int
}

func (c *closeCounter) Close() error { c.closed++; return nil }

// A Go reader becomes a Reader that reads it and closes it through its own
// Close; a Go writer a Writer that hands it each write as it is made.
func TestStreams_GoValuesBecomeHandles(t *testing.T) {
	fr := rt.NewFrame(context.Background())
	src := &closeCounter{Reader: strings.NewReader("one\ntwo\n")}
	r, err := ReaderFromGo(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := rt.ReaderReadLine(fr, r); got.Tag != rt.TagOk || got.Ok.Some != "one" {
		t.Fatalf("first line: %+v", got)
	}
	rt.ReaderClose(r)
	if src.closed != 1 {
		t.Fatalf("closing the Reader closed the Go reader %d times", src.closed)
	}

	var buf bytes.Buffer
	w, err := WriterFromGo(&buf)
	if err != nil {
		t.Fatal(err)
	}
	rt.WriterWriteText(fr, w, "sent")
	if buf.String() != "sent" {
		t.Fatalf("after a write the Go writer holds %q", buf.String())
	}

	if _, err := ReaderFromGo(nil); err == nil {
		t.Error("a nil io.Reader made a Reader")
	}
	if _, err := WriterFromGo(nil); err == nil {
		t.Error("a nil io.Writer made a Writer")
	}
}

// A Reader handed to Go is read through its own buffer, so Go reads what
// the program would have read next, and a Writer is written through its
// own, in order with the program's writes. A failure is the io.Error.
func TestStreams_HandlesBecomeGoValues(t *testing.T) {
	fr := rt.NewFrame(context.Background())
	r := rt.ReaderFromText("first\nrest of it")
	rt.ReaderReadLine(fr, r)
	data, err := io.ReadAll(GoReader(fr, r))
	if err != nil || string(data) != "rest of it" {
		t.Fatalf("Go read %q, %v", data, err)
	}

	w := rt.WriterToMemory()
	rt.WriterWriteText(fr, w, "a")
	gw := GoWriter(fr, w)
	if _, err := io.WriteString(gw, "b"); err != nil {
		t.Fatal(err)
	}
	rt.WriterWriteText(fr, w, "c")
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if got := string(rt.WriterCollected(w)); got != "abc" {
		t.Fatalf("collected %q", got)
	}
	_, err = io.WriteString(gw, "late")
	var f rt.IOFailure
	if !errors.As(err, &f) || f.E.Tag != rt.TagIOClosed {
		t.Fatalf("a write after close: %v, want Closed", err)
	}
}
