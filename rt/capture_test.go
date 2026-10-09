package rt

import (
	"context"
	"strings"
	"testing"
)

// A capture's input reads as the program's own input does: `\r\n` and `\n`
// end a line, a final line with no newline is still a line, and then
// Err("eof"). The program's input is untouched while a capture is in force.
func TestCapture_ReadsItsOwnInput(t *testing.T) {
	root := NewFrame(WithInput(context.Background(), NewInput(strings.NewReader("real\n"))))
	inner, c := EnterCapture(root, "one\r\ntwo\nlast")
	defer c.Close()
	for _, want := range []string{"one", "two", "last"} {
		if got := ReadLine(inner); got.Tag != TagOk || got.Ok != want {
			t.Fatalf("ReadLine = %+v, want Ok(%q)", got, want)
		}
	}
	if got := ReadLine(inner); got.Tag != TagErr || got.Err != "eof" {
		t.Fatalf("ReadLine past the capture's input = %+v, want Err(eof)", got)
	}
	if got := ReadLine(root); got.Tag != TagOk || got.Ok != "real" {
		t.Fatalf("ReadLine on the program's frame = %+v, want Ok(\"real\")", got)
	}
}

// A nested capture takes over for its extent; once closed, a frame that still
// names it (a task that outlived the call) writes to the capture it replaced,
// and past every closed one, to the program's output.
func TestCapture_NestsAndFallsBackWhenClosed(t *testing.T) {
	root := NewFrame(context.Background())
	if CaptureWrite(root, "x") {
		t.Fatal("a frame with no capture took a write")
	}
	outerFr, outer := EnterCapture(root, "")
	innerFr, inner := EnterCapture(outerFr, "")
	CaptureWrite(outerFr, "a")
	CaptureWrite(innerFr, "b")
	if got := inner.Close().Output; got != "b" {
		t.Fatalf("inner output = %q, want \"b\"", got)
	}
	CaptureWrite(innerFr, "c")
	if got := outer.Close().Output; got != "ac" {
		t.Fatalf("outer output = %q, want \"ac\"", got)
	}
	if CaptureWrite(innerFr, "d") {
		t.Fatal("a write past two closed captures was taken by one of them")
	}
	if got := ReadLine(innerFr); got.Tag != TagErr {
		t.Fatalf("ReadLine past closed captures over no input = %+v, want Err", got)
	}
}

// The print path's lookup with nothing captured allocates nothing.
func TestCapture_UncapturedWriteAllocatesNothing(t *testing.T) {
	fr := NewFrame(context.Background())
	if n := testing.AllocsPerRun(1000, func() { CaptureWrite(fr, "line\n") }); n != 0 {
		t.Fatalf("CaptureWrite with no capture allocates %v times per call, want 0", n)
	}
}

// Every frame rt builds for a task carries the spawner's capture.
func TestCapture_TaskFramesInheritIt(t *testing.T) {
	root := NewFrame(context.Background())
	inner, c := EnterCapture(root, "")
	scope := EnterScope(inner)
	h := TaskSpawn(scope, func(fr *Frame) Unit {
		CaptureWrite(fr, "task")
		return Unit{}
	})
	TaskAwait(scope, h)
	ScopeExit(scope)
	if got := c.Close().Output; got != "task" {
		t.Fatalf("captured %q, want \"task\"", got)
	}
}

// The transcript writes each line read in at the point it was read, marked
// `> `. A prompt written without a newline goes on the marked line before the
// typed text, as a terminal shows it; a read at end of input adds nothing.
func TestCapture_TranscriptWritesReadsIn(t *testing.T) {
	root := NewFrame(context.Background())
	inner, c := EnterCapture(root, "Ada\n25\r\n")
	CaptureWrite(inner, "name?\n")
	ReadLine(inner)
	CaptureWrite(inner, "hi Ada\namount: ")
	if got := ReadLine(inner); got.Ok != "25" {
		t.Fatalf("ReadLine = %+v, want Ok(\"25\")", got)
	}
	CaptureWrite(inner, "ok\nmore? ")
	if got := ReadLine(inner); got.Tag != TagErr {
		t.Fatalf("ReadLine past the input = %+v, want Err", got)
	}
	CaptureWrite(inner, "bye")
	got := c.Close()
	if want := "name?\nhi Ada\namount: ok\nmore? bye"; got.Output != want {
		t.Fatalf("output = %q, want %q", got.Output, want)
	}
	if want := "name?\n> Ada\nhi Ada\n> amount: 25\nok\nmore? bye"; got.Transcript != want {
		t.Fatalf("transcript = %q, want %q", got.Transcript, want)
	}
	if got.Unread != 0 {
		t.Fatalf("a plain capture reports %d unread lines", got.Unread)
	}
}

// An output line starting with `>` or `\` gets a `\` in front, so only a
// read starts a transcript line with `>`; the output itself is untouched.
func TestCapture_TranscriptEscapesOutputThatReadsAsInput(t *testing.T) {
	root := NewFrame(context.Background())
	inner, c := EnterCapture(root, "")
	CaptureWrite(inner, "> quoted\n\\path\nplain > not first\n>")
	got := c.Close()
	if want := "\\> quoted\n\\\\path\nplain > not first\n\\>"; got.Transcript != want {
		t.Fatalf("transcript = %q, want %q", got.Transcript, want)
	}
	if want := "> quoted\n\\path\nplain > not first\n>"; got.Output != want {
		t.Fatalf("output = %q, want %q", got.Output, want)
	}
}

// An inner capture's reads and writes are in its own transcript only.
func TestCapture_NestedTranscriptsStayApart(t *testing.T) {
	root := NewFrame(context.Background())
	outerFr, outer := EnterCapture(root, "o\n")
	CaptureWrite(outerFr, "a\n")
	innerFr, inner := EnterCapture(outerFr, "i\n")
	CaptureWrite(innerFr, "b\n")
	ReadLine(innerFr)
	if got := inner.Close().Transcript; got != "b\n> i\n" {
		t.Fatalf("inner transcript = %q", got)
	}
	ReadLine(outerFr)
	if got := outer.Close().Transcript; got != "a\n> o\n" {
		t.Fatalf("outer transcript = %q", got)
	}
}

// A replay script's `>` lines are its input, less the prompt the program
// wrote on that line; the transcript writes each read back with the prompt.
// Lines no read reached are counted, and a read past them is end of input.
func TestReplay_FeedsTheScriptsMarkedLines(t *testing.T) {
	root := NewFrame(context.Background())
	script := "balance?\n> 500\n> amount: 25\n> amount:\n>7\n> left over\n> and this"
	inner, c := EnterReplay(root, script)
	CaptureWrite(inner, "balance?\n")
	var read []string
	for range 4 {
		CaptureWrite(inner, "amount: ")
		read = append(read, ReadLine(inner).Ok)
	}
	if got, want := strings.Join(read, "|"), "500|25||7"; got != want {
		t.Fatalf("reads = %q, want %q", got, want)
	}
	got := c.Close()
	if want := "balance?\n> amount: 500\n> amount: 25\n> amount: \n> amount: 7\n"; got.Transcript != want {
		t.Fatalf("transcript = %q, want %q", got.Transcript, want)
	}
	if got.Unread != 2 {
		t.Fatalf("unread = %d, want 2", got.Unread)
	}
	inner, c = EnterReplay(root, "> only")
	ReadLine(inner)
	if r := ReadLine(inner); r.Tag != TagErr || r.Err != "eof" {
		t.Fatalf("a read past the script = %+v, want Err(eof)", r)
	}
	if got := c.Close(); got.Unread != 0 || got.Transcript != "> only\n" {
		t.Fatalf("closed replay = %+v", got)
	}
}

// ReplayText drops what a `"""` script and an editor lose, trailing
// whitespace and carriage returns, ends every line in one newline, and spells
// a marked line with one space after `>`.
func TestReplayText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"a\nb\n", "a\nb\n"},
		{"a\nb", "a\nb\n"},
		{"a\n\n", "a\n\n"},
		{"a  \r\n> x \r\n", "a\n> x\n"},
		{">x\n>\n> ", "> x\n>\n>\n"},
		{"\\> out", "\\> out\n"},
		{"\n", "\n"},
		{"", ""},
	} {
		if got := ReplayText(tc.in); got != tc.want {
			t.Errorf("ReplayText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
