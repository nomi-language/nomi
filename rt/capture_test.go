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
		if got, ok := lineOf(ReadLine(inner)); !ok || got != want {
			t.Fatalf("ReadLine = %q, want Ok(Some(%q))", got, want)
		}
	}
	if got := ReadLine(inner); !atEnd(got) {
		t.Fatalf("ReadLine past the capture's input = %+v, want Ok(None)", got)
	}
	if got := ReadLine(root); got.Tag != TagOk || got.Ok.Some != "real" {
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
	if got := ReadLine(innerFr); !atEnd(got) {
		t.Fatalf("ReadLine past closed captures over no input = %+v, want Ok(None)", got)
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

// The transcript is the output as written, with each line read written in
// as four spaces, `> ` and the text, and a blank line blank. A prompt
// written without a newline is output, and the read ends its line. A read
// at end of input writes nothing and leaves its prompt in place, so the
// prompt and what follows it are one output line, as in the output.
func TestCapture_TranscriptWritesReadsIn(t *testing.T) {
	root := NewFrame(context.Background())
	inner, c := EnterCapture(root, "Ada\n36\r\n")
	CaptureWrite(inner, "What is your name?\n")
	ReadLine(inner)
	CaptureWrite(inner, "\nHello, Ada. Your age: ")
	if got := ReadLine(inner); got.Ok.Some != "36" {
		t.Fatalf("ReadLine = %+v, want Ok(\"36\")", got)
	}
	CaptureWrite(inner, "Ada is 36\nmore? ")
	if got := ReadLine(inner); !atEnd(got) {
		t.Fatalf("ReadLine past the input = %+v, want Ok(None)", got)
	}
	CaptureWrite(inner, "bye")
	got := c.Close()
	if want := "What is your name?\n\nHello, Ada. Your age: Ada is 36\nmore? bye"; got.Output != want {
		t.Fatalf("output = %q, want %q", got.Output, want)
	}
	want := "What is your name?\n    > Ada\n\nHello, Ada. Your age: \n    > 36\nAda is 36\nmore? bye"
	if got.Transcript != want {
		t.Fatalf("transcript = %q, want %q", got.Transcript, want)
	}
	if got.Unread != 0 {
		t.Fatalf("a plain capture reports %d unread lines", got.Unread)
	}
}

// A prompt that starts with `>` at the base column is output like any
// other: it has its own line, and the line read follows it.
func TestCapture_APromptStartingWithTheMarkerIsOutput(t *testing.T) {
	root := NewFrame(context.Background())
	inner, c := EnterCapture(root, "north\n\n> quoted\n")
	CaptureWrite(inner, "Exits: north.\n")
	for {
		CaptureWrite(inner, "> ")
		if _, ok := lineOf(ReadLine(inner)); !ok {
			break
		}
		CaptureWrite(inner, "ok\n")
	}
	got := c.Close()
	want := "Exits: north.\n> \n    > north\nok\n> \n    > \nok\n> \n    > > quoted\nok\n> "
	if got.Transcript != want {
		t.Fatalf("transcript = %q, want %q", got.Transcript, want)
	}
	if want := "Exits: north.\n> ok\n> ok\n> ok\n> "; got.Output != want {
		t.Fatalf("output = %q, want %q", got.Output, want)
	}
}

// Output is written as it is, except an indented line that would read as
// input: it gets a backslash before its `>`, and one that already has
// backslashes there gets one more. A backslash anywhere else, `>` not after
// indentation, and `>` not followed by a space stay as they are.
func TestCapture_TranscriptEscapesOutputThatReadsAsInput(t *testing.T) {
	root := NewFrame(context.Background())
	inner, c := EnterCapture(root, "")
	out := "> quoted\n>> double\n\\path\nplain > not first\n" +
		"    > padded\n  >\n\t> tab\n    >> double\n    >x\n    \\> slashed\n    \\x\n    > "
	CaptureWrite(inner, out)
	got := c.Close()
	want := "> quoted\n>> double\n\\path\nplain > not first\n" +
		"    \\> padded\n  \\>\n\t\\> tab\n    >> double\n    >x\n    \\\\> slashed\n    \\x\n    \\> "
	if got.Transcript != want {
		t.Fatalf("transcript = %q, want %q", got.Transcript, want)
	}
	if got.Output != out {
		t.Fatalf("output = %q, want %q", got.Output, out)
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
	if got := inner.Close().Transcript; got != "b\n    > i\n" {
		t.Fatalf("inner transcript = %q", got)
	}
	ReadLine(outerFr)
	if got := outer.Close().Transcript; got != "a\n    > o\n" {
		t.Fatalf("outer transcript = %q", got)
	}
}

// A replay script's input lines feed the reads in order, whatever prompt the
// program wrote; the transcript writes each back at the indentation the
// script wrote it at. Lines no read reached are counted, and a read past
// them is end of input.
func TestReplay_FeedsTheScriptsInputLines(t *testing.T) {
	root := NewFrame(context.Background())
	script := "balance?\n    > 500\namount:\n  > 25\n\t>\n    >  7\n    > left over\n    > and this"
	inner, c := EnterReplay(root, ParseReplayScript(script))
	CaptureWrite(inner, "balance?\n")
	var read []string
	for range 4 {
		CaptureWrite(inner, "amount: ")
		read = append(read, ReadLine(inner).Ok.Some)
	}
	if got, want := strings.Join(read, "|"), "500|25|| 7"; got != want {
		t.Fatalf("reads = %q, want %q", got, want)
	}
	got := c.Close()
	want := "balance?\namount: \n    > 500\namount: \n  > 25\namount: \n\t> \namount: \n    >  7\n"
	if got.Transcript != want {
		t.Fatalf("transcript = %q, want %q", got.Transcript, want)
	}
	if got.Unread != 2 {
		t.Fatalf("unread = %d, want 2", got.Unread)
	}
	inner, c = EnterReplay(root, ParseReplayScript("    > only"))
	ReadLine(inner)
	if r := ReadLine(inner); !atEnd(r) {
		t.Fatalf("a read past the script = %+v, want Ok(None)", r)
	}
	if got := c.Close(); got.Unread != 0 || got.Transcript != "    > only\n" {
		t.Fatalf("closed replay = %+v", got)
	}
}

// A line indented past the base column whose text then starts with `> ` is
// input, and `>` alone there (`> ` with its trailing space stripped) is an
// empty line typed. Every other line is output as written: at the base
// column, padded, `>>`, `>` with no space after it, and a `\>` escape,
// which stays in the expected transcript as the transcript writes it. A
// script with no input is the output as written. The expected transcript
// is the script as ReplayText spells it.
func TestParseReplayScript(t *testing.T) {
	for _, tc := range []struct {
		script   string
		input    []string
		expected string
	}{
		{"What is your name?\n    > Ada\nHello, Ada. Your age:\n    > 36\nAda is 36",
			[]string{"Ada", "36"}, "What is your name?\n    > Ada\nHello, Ada. Your age:\n    > 36\nAda is 36\n"},
		{">\n    > north\n\n> quoted \r\n    >\n    > \n", []string{"north", "", ""},
			">\n    > north\n\n> quoted\n    >\n    >\n"},
		{"  > x\n\t> y\n >  z", []string{"x", "y", " z"}, "  > x\n\t> y\n >  z\n"},
		{"    > > x\n    > >> x  y", []string{"> x", ">> x  y"}, "    > > x\n    > >> x  y\n"},
		{"      padded\n    >> output\n    >x\n    \\> escaped\n    \\>\n    \\\\> twice\n\\> base\n    > in",
			[]string{"in"},
			"      padded\n    >> output\n    >x\n    \\> escaped\n    \\>\n    \\\\> twice\n\\> base\n    > in\n"},
		{">> what was input\n>>  is output", nil, ">> what was input\n>>  is output\n"},
		{"-10.0 C |   14.0 F\n -5.0 C |   23.0 F", nil, "-10.0 C |   14.0 F\n -5.0 C |   23.0 F\n"},
		{"    hello\n\n    world", nil, "    hello\n\n    world\n"},
		{"", nil, ""},
	} {
		got := ParseReplayScript(tc.script)
		if strings.Join(got.Input, "|") != strings.Join(tc.input, "|") || len(got.Input) != len(tc.input) {
			t.Errorf("ParseReplayScript(%q).Input = %q, want %q", tc.script, got.Input, tc.input)
		}
		if got.Expected != tc.expected {
			t.Errorf("ParseReplayScript(%q).Expected = %q, want %q", tc.script, got.Expected, tc.expected)
		}
	}
}

// EscapeReplayOutput and its inverse round-trip every output line, and an
// escaped line never reads as input.
func TestEscapeReplayOutput(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{"plain", "plain"},
		{"> at the base column", "> at the base column"},
		{"    > padded", "    \\> padded"},
		{"    >", "    \\>"},
		{"    > ", "    \\> "},
		{"    \\> x", "    \\\\> x"},
		{"    \\x", "    \\x"},
		{"    >x", "    >x"},
		{"    >> x", "    >> x"},
		{"\\> x", "\\> x"},
	} {
		got := EscapeReplayOutput(tc.line)
		if got != tc.want {
			t.Errorf("EscapeReplayOutput(%q) = %q, want %q", tc.line, got, tc.want)
		}
		if back := unescapeReplayOutput(got); back != tc.line {
			t.Errorf("unescapeReplayOutput(%q) = %q, want %q", got, back, tc.line)
		}
		if _, _, input := ReplayInput(strings.TrimRight(got, " ")); input {
			t.Errorf("EscapeReplayOutput(%q) = %q reads as input", tc.line, got)
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
		{"a  \r\n    > x \r\n    > \r\n", "a\n    > x\n    >\n"},
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

// A replay's view while it runs: the transcript as the replay compares it,
// the input read and still to come, and no divergence while the transcript
// is a prefix of the script, an output line in progress included.
func TestReplay_ViewWhileItRuns(t *testing.T) {
	script := ParseReplayScript("Name:\n    > Ada\nHello, Ada\n    > 50\n")
	inner, c := EnterReplay(NewFrame(context.Background()), script)
	defer c.Close()
	if got := inner.OpenCaptures(); len(got) != 1 || got[0] != c || !inner.Carries(c) || !inner.ReadsCaptured() {
		t.Fatalf("the replay's frame does not carry it: %v", got)
	}
	CaptureWrite(inner, "Name: ")
	ReadLine(inner)
	CaptureWrite(inner, "Hello, A")
	v := c.View()
	if v.Transcript != "Name:\n    > Ada\nHello, A\n" || v.Output != "Name: Hello, A" ||
		strings.Join(v.Read, ",") != "Ada" || strings.Join(v.Unread, ",") != "50" || v.Diverged != "" {
		t.Fatalf("View = %+v", v)
	}
	CaptureWrite(inner, "l\n")
	if got, want := c.View().Diverged, `line 3 of the script is "Hello, Ada", and the transcript has "Hello, Al"`; got != want {
		t.Fatalf("Diverged = %q, want %q", got, want)
	}
}

// A script with no input: the view's transcript is the output, and it says
// where it diverges from the script's lines as written.
func TestReplay_ViewOfAScriptWithNoInput(t *testing.T) {
	inner, c := EnterReplay(NewFrame(context.Background()), ParseReplayScript("-10.0 C\n -5.0 C\n"))
	defer c.Close()
	CaptureWrite(inner, "-10.0 C\n -5.")
	if v := c.View(); v.Transcript != "-10.0 C\n -5.\n" || v.Diverged != "" {
		t.Fatalf("View = %+v", v)
	}
	CaptureWrite(inner, "1 C\n")
	if got, want := c.View().Diverged, `line 2 of the script is " -5.0 C", and the transcript has " -5.1 C"`; got != want {
		t.Fatalf("Diverged = %q, want %q", got, want)
	}
}

func TestDivergence(t *testing.T) {
	expected := "one\n    > x\n    \\> two\n"
	for _, tc := range []struct{ finished, partial, want string }{
		{"", "", ""},
		{"one\n", "", ""},
		{"one\n    > x\n", "", ""},
		{"", "on", ""},
		{"", "two", `line 1 of the script is "one", and the transcript has begun "two"`},
		{"one  \n", "", ""},
		{"one\n", "    >", `line 2 of the script is "    > x", and the transcript has begun "    >"`},
		{"one\n    > x\n", "    > t", ""},
		{"one\n    > x\n    \\> two\n", "more", `the script ends after line 3, and the transcript has begun "more"`},
		{"one\n    > y\n", "", `line 2 of the script is "    > x", and the transcript has "    > y"`},
	} {
		if got := Divergence(expected, tc.finished, tc.partial); got != tc.want {
			t.Errorf("Divergence(%q, %q) = %q, want %q", tc.finished, tc.partial, got, tc.want)
		}
	}
}

// A streamed test capture writes as it goes; a capture inside it keeps its
// own output.
func TestCapture_StreamsATestCapture(t *testing.T) {
	var live strings.Builder
	fr, c := EnterTestCapture(NewFrame(context.Background()))
	c.Stream(&live)
	CaptureWrite(fr, "a\n")
	inner, ic := EnterCapture(fr, "")
	CaptureWrite(inner, "kept\n")
	ic.Close()
	CaptureWrite(fr, "b")
	if got := c.Close().Output; got != "a\nb" || live.String() != "a\nb" {
		t.Fatalf("output %q, streamed %q", got, live.String())
	}
	if len(fr.OpenCaptures()) != 0 {
		t.Fatal("a test capture counts as an io.capture")
	}
}
