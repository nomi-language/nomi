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

// The transcript is in two columns: a line read is `> ` and the text, an
// output line is two spaces and the line, and a blank line is blank. A
// prompt written without a newline is output, and the read ends its line. A
// read at end of input writes nothing and leaves its prompt in place, so the
// prompt and what follows it are one output line, as in the output.
func TestCapture_TranscriptWritesReadsIn(t *testing.T) {
	root := NewFrame(context.Background())
	inner, c := EnterCapture(root, "Ada\n36\r\n")
	CaptureWrite(inner, "What is your name?\n")
	ReadLine(inner)
	CaptureWrite(inner, "\nHello, Ada. Your age: ")
	if got := ReadLine(inner); got.Ok != "36" {
		t.Fatalf("ReadLine = %+v, want Ok(\"36\")", got)
	}
	CaptureWrite(inner, "Ada is 36\nmore? ")
	if got := ReadLine(inner); got.Tag != TagErr {
		t.Fatalf("ReadLine past the input = %+v, want Err", got)
	}
	CaptureWrite(inner, "bye")
	got := c.Close()
	if want := "What is your name?\n\nHello, Ada. Your age: Ada is 36\nmore? bye"; got.Output != want {
		t.Fatalf("output = %q, want %q", got.Output, want)
	}
	want := "  What is your name?\n> Ada\n\n  Hello, Ada. Your age: \n> 36\n  Ada is 36\n  more? bye"
	if got.Transcript != want {
		t.Fatalf("transcript = %q, want %q", got.Transcript, want)
	}
	if got.Unread != 0 {
		t.Fatalf("a plain capture reports %d unread lines", got.Unread)
	}
}

// A prompt that starts with `>` is output like any other: it has its own
// line in the gutter, and the line read follows it.
func TestCapture_APromptStartingWithTheMarkerIsOutput(t *testing.T) {
	root := NewFrame(context.Background())
	inner, c := EnterCapture(root, "north\n\n> quoted\n")
	CaptureWrite(inner, "Exits: north.\n")
	for {
		CaptureWrite(inner, "> ")
		if ReadLine(inner).Tag != TagOk {
			break
		}
		CaptureWrite(inner, "ok\n")
	}
	got := c.Close()
	want := "  Exits: north.\n  > \n> north\n  ok\n  > \n> \n  ok\n  > \n> > quoted\n  ok\n  > "
	if got.Transcript != want {
		t.Fatalf("transcript = %q, want %q", got.Transcript, want)
	}
	if want := "Exits: north.\n> ok\n> ok\n> ok\n> "; got.Output != want {
		t.Fatalf("output = %q, want %q", got.Output, want)
	}
}

// Output that starts with `>` or `\` is written as it is: the gutter, not an
// escape, keeps it from reading as input.
func TestCapture_TranscriptNeverEscapesOutput(t *testing.T) {
	root := NewFrame(context.Background())
	inner, c := EnterCapture(root, "")
	CaptureWrite(inner, "> quoted\n\\path\nplain > not first\n>")
	got := c.Close()
	if want := "  > quoted\n  \\path\n  plain > not first\n  >"; got.Transcript != want {
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
	if got := inner.Close().Transcript; got != "  b\n> i\n" {
		t.Fatalf("inner transcript = %q", got)
	}
	ReadLine(outerFr)
	if got := outer.Close().Transcript; got != "  a\n> o\n" {
		t.Fatalf("outer transcript = %q", got)
	}
}

func mustParseReplay(t *testing.T, script string) ReplayScript {
	t.Helper()
	parsed, err := ParseReplayScript(script)
	if err != nil {
		t.Fatalf("ParseReplayScript(%q): %v", script, err)
	}
	return parsed
}

// A replay script's input lines feed the reads in order, whatever prompt the
// program wrote; the transcript writes each back in the gutter form. Lines
// no read reached are counted, and a read past them is end of input.
func TestReplay_FeedsTheScriptsInputLines(t *testing.T) {
	root := NewFrame(context.Background())
	script := "  balance?\n> 500\n  amount:\n> 25\n>\n>7\n> left over\n> and this"
	if _, err := ParseReplayScript(script); err == nil || !strings.Contains(err.Error(), "line 6") {
		t.Fatalf("a `>7` line parsed: err = %v", err)
	}
	script = strings.Replace(script, ">7", "> 7", 1)
	inner, c := EnterReplay(root, mustParseReplay(t, script))
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
	want := "  balance?\n  amount: \n> 500\n  amount: \n> 25\n  amount: \n> \n  amount: \n> 7\n"
	if got.Transcript != want {
		t.Fatalf("transcript = %q, want %q", got.Transcript, want)
	}
	if got.Unread != 2 {
		t.Fatalf("unread = %d, want 2", got.Unread)
	}
	inner, c = EnterReplay(root, mustParseReplay(t, "> only"))
	ReadLine(inner)
	if r := ReadLine(inner); r.Tag != TagErr || r.Err != "eof" {
		t.Fatalf("a read past the script = %+v, want Err(eof)", r)
	}
	if got := c.Close(); got.Unread != 0 || got.Transcript != "> only\n" {
		t.Fatalf("closed replay = %+v", got)
	}
}

// A script is in two columns: `> ` lines are input, blank lines are blank
// output, and every other line must carry the gutter, input or not. The
// expected transcript is the script as ReplayText spells it.
func TestParseReplayScript(t *testing.T) {
	for _, tc := range []struct {
		script   string
		input    []string
		expected string
	}{
		{"  What is your name?\n> Ada\n  Hello, Ada. Your age:\n> 36\n  Ada is 36",
			[]string{"Ada", "36"}, "  What is your name?\n> Ada\n  Hello, Ada. Your age:\n> 36\n  Ada is 36\n"},
		{"  >\n> north\n\n  > quoted \r\n>\n", []string{"north", ""}, "  >\n> north\n\n  > quoted\n>\n"},
		{"> > x", []string{"> x"}, "> > x\n"},
		{"  hello\n\n  world", nil, "  hello\n\n  world\n"},
		{"    indented\n  \\>", nil, "    indented\n  \\>\n"},
		{"", nil, ""},
	} {
		got := mustParseReplay(t, tc.script)
		if strings.Join(got.Input, "|") != strings.Join(tc.input, "|") || len(got.Input) != len(tc.input) {
			t.Errorf("ParseReplayScript(%q).Input = %q, want %q", tc.script, got.Input, tc.input)
		}
		if got.Expected != tc.expected {
			t.Errorf("ParseReplayScript(%q).Expected = %q, want %q", tc.script, got.Expected, tc.expected)
		}
	}
	_, err := ParseReplayScript("  Your age:\n> 36\nAda is 36")
	if err == nil || err.Error() != "io.replay: line 3 of the script, \"Ada is 36\", must start with "+
		"`> ` (input) or two spaces (output)" {
		t.Fatalf("an output line without the gutter: err = %v", err)
	}
	for _, script := range []string{"hello", " one space", ">x"} {
		if _, err := ParseReplayScript(script); err == nil || !strings.Contains(err.Error(), "line 1 of") {
			t.Errorf("ParseReplayScript(%q): err = %v, want the gutter error", script, err)
		}
	}
}

// ReplayText drops what a `"""` script and an editor lose, trailing
// whitespace and carriage returns, and ends every line in one newline. It
// leaves the rest of each line alone.
func TestReplayText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"a\nb\n", "a\nb\n"},
		{"a\nb", "a\nb\n"},
		{"a\n\n", "a\n\n"},
		{"  a  \r\n> x \r\n", "  a\n> x\n"},
		{">x\n>\n> ", ">x\n>\n>\n"},
		{"  >>> 1 \n", "  >>> 1\n"},
		{"\n", "\n"},
		{"", ""},
	} {
		if got := ReplayText(tc.in); got != tc.want {
			t.Errorf("ReplayText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
