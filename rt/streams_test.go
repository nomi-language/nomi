package rt

import (
	"context"
	"strings"
	"testing"
)

// A write to stderr reaches the innermost open capture's errors, apart from
// its output and its transcript; a test case's capture takes it into the
// case's output; with no capture it goes to the frame's error writer.
func TestCapture_ErrorsAreKeptApart(t *testing.T) {
	var out, errs strings.Builder
	root := NewFrame(WithStreams(context.Background(), &out, &errs))
	caseFr, caseCap := EnterTestCapture(root)
	inner, c := EnterCapture(caseFr, "")
	WriteError(inner, "warn\n")
	CaptureWrite(inner, "said\n")
	got := c.Close()
	if got.Output != "said\n" || got.Errors != "warn\n" || got.Transcript != "said\n" {
		t.Fatalf("capture = %+v", got)
	}
	WriteError(caseFr, "case stderr\n")
	if r := caseCap.Close(); r.Output != "case stderr\n" {
		t.Fatalf("the test case's capture holds %q", r.Output)
	}
	WriteError(root, "program stderr\n")
	if errs.String() != "program stderr\n" || out.String() != "" {
		t.Fatalf("program streams: stdout %q, stderr %q", out.String(), errs.String())
	}
}

// The handles write where their stream goes: stdout through a capture or the
// program's output, stderr likewise, and a reader on stdin reads its input.
func TestStreams_HandlesReachTheirStreams(t *testing.T) {
	var out, errs strings.Builder
	ctx := WithStreams(WithInput(context.Background(), NewInput(strings.NewReader("a\n"))), &out, &errs)
	fr := NewFrame(ctx)
	if r := WriterWriteText(fr, IOStdout(), "o"); r.Tag != TagOk {
		t.Fatalf("stdout write = %+v", r)
	}
	if r := WriterWrite(fr, IOStderr(), Bytes("e")); r.Tag != TagOk {
		t.Fatalf("stderr write = %+v", r)
	}
	if got, ok := lineOf(ReaderReadLine(fr, IOStdin())); !ok || got != "a" {
		t.Fatalf("stdin read = %q", got)
	}
	if out.String() != "o" || errs.String() != "e" {
		t.Fatalf("stdout %q, stderr %q", out.String(), errs.String())
	}
}
