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

// The transcript has a four-column gutter: a line read is `>>  ` and the
// text, an output line is four spaces and the line, and a blank line is blank. A
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
	want := "    What is your name?\n>>  Ada\n\n    Hello, Ada. Your age: \n>>  36\n    Ada is 36\n    more? bye"
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
	want := "    Exits: north.\n    > \n>>  north\n    ok\n    > \n>>  \n    ok\n    > \n>>  > quoted\n    ok\n    > "
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
	CaptureWrite(inner, "> quoted\n>> double\n\\path\nplain > not first\n>")
	got := c.Close()
	if want := "    > quoted\n    >> double\n    \\path\n    plain > not first\n    >"; got.Transcript != want {
		t.Fatalf("transcript = %q, want %q", got.Transcript, want)
	}
	if want := "> quoted\n>> double\n\\path\nplain > not first\n>"; got.Output != want {
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
	if got := inner.Close().Transcript; got != "    b\n>>  i\n" {
		t.Fatalf("inner transcript = %q", got)
	}
	ReadLine(outerFr)
	if got := outer.Close().Transcript; got != "    a\n>>  o\n" {
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
	script := "    balance?\n>>  500\n    amount:\n>>  25\n>>\n>> 7\n>>  left over\n>>  and this"
	if _, err := ParseReplayScript(script); err == nil || !strings.Contains(err.Error(), "line 6") {
		t.Fatalf("a `>> 7` line parsed: err = %v", err)
	}
	script = strings.Replace(script, ">> 7", ">>  7", 1)
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
	want := "    balance?\n    amount: \n>>  500\n    amount: \n>>  25\n    amount: \n>>  \n    amount: \n>>  7\n"
	if got.Transcript != want {
		t.Fatalf("transcript = %q, want %q", got.Transcript, want)
	}
	if got.Unread != 2 {
		t.Fatalf("unread = %d, want 2", got.Unread)
	}
	inner, c = EnterReplay(root, mustParseReplay(t, ">>  only"))
	ReadLine(inner)
	if r := ReadLine(inner); r.Tag != TagErr || r.Err != "eof" {
		t.Fatalf("a read past the script = %+v, want Err(eof)", r)
	}
	if got := c.Close(); got.Unread != 0 || got.Transcript != ">>  only\n" {
		t.Fatalf("closed replay = %+v", got)
	}
}

// A script with a `>>` line is in the transcript's form: `>>  ` lines are
// input, `>>` alone is empty input, blank lines are blank output, and every
// other line must carry the four-space gutter, whatever it starts with. A
// script with no `>>` line is verbatim output, leading spaces and all. The
// expected transcript is the script as ReplayText spells it.
func TestParseReplayScript(t *testing.T) {
	for _, tc := range []struct {
		script   string
		input    []string
		expected string
		verbatim bool
	}{
		{"    What is your name?\n>>  Ada\n    Hello, Ada. Your age:\n>>  36\n    Ada is 36",
			[]string{"Ada", "36"}, "    What is your name?\n>>  Ada\n    Hello, Ada. Your age:\n>>  36\n    Ada is 36\n", false},
		{"    >\n>>  north\n\n    > quoted \r\n>>\n>>  \n", []string{"north", "", ""},
			"    >\n>>  north\n\n    > quoted\n>>\n>>\n", false},
		{">>  > x", []string{"> x"}, ">>  > x\n", false},
		{">>  >> x\n>>  x  y", []string{">> x", "x  y"}, ">>  >> x\n>>  x  y\n", false},
		{"    hello\n\n    world\n>>", []string{""}, "    hello\n\n    world\n>>\n", false},
		{"      indented\n    >>  output\n    \\>\n>>  in", []string{"in"},
			"      indented\n    >>  output\n    \\>\n>>  in\n", false},
		{"-10.0 C |   14.0 F\n -5.0 C |   23.0 F", nil, "-10.0 C |   14.0 F\n -5.0 C |   23.0 F\n", true},
		{"    hello\n\n    world", nil, "    hello\n\n    world\n", true},
		{"  Ada is 36\n> 500\n  >>  500 \r\n", nil, "  Ada is 36\n> 500\n  >>  500\n", true},
		{"", nil, "", true},
	} {
		got := mustParseReplay(t, tc.script)
		if strings.Join(got.Input, "|") != strings.Join(tc.input, "|") || len(got.Input) != len(tc.input) {
			t.Errorf("ParseReplayScript(%q).Input = %q, want %q", tc.script, got.Input, tc.input)
		}
		if got.Expected != tc.expected || got.Verbatim != tc.verbatim {
			t.Errorf("ParseReplayScript(%q) = %q, verbatim %v, want %q, verbatim %v",
				tc.script, got.Expected, got.Verbatim, tc.expected, tc.verbatim)
		}
	}
}

// A verbatim script compares the transcript with the gutter off each line;
// a script in two columns compares it as it is.
func TestReplayScript_Compared(t *testing.T) {
	transcript := "    -10.0 C\n\n     -5.0 C\n"
	if got := mustParseReplay(t, "-10.0 C").Compared(transcript); got != "-10.0 C\n\n -5.0 C\n" {
		t.Errorf("verbatim Compared = %q", got)
	}
	if got := mustParseReplay(t, ">>  x").Compared(transcript); got != transcript {
		t.Errorf("two-column Compared = %q", got)
	}
}

// A line that is neither input nor output is an error naming the line by
// its number in the script and quoting it, with a hint that writes it with
// the right gutter.
func TestParseReplayScript_RejectsALineWithoutTheGutter(t *testing.T) {
	const output = "must start with `>>  ` (input) or four spaces (output)\n"
	const spacing = "must have two spaces after `>>`\n"
	const columns = "\nhint: a script with a line that starts with `>>` is in two columns; " +
		"output that starts with `>>` is written behind four spaces, as every output line is: `    >> ...`"
	for _, tc := range []struct{ script, want string }{
		{"    Your age:\n>>  36\nAda is 36",
			`io.replay: line 3 of the script, "Ada is 36", ` + output + "hint: an output line is `    Ada is 36`"},
		{"  Ada is 36\n>>  x",
			`io.replay: line 1 of the script, "  Ada is 36", ` + output + "hint: an output line is `    Ada is 36`"},
		{"   Ada is 36\n>>  x",
			`io.replay: line 1 of the script, "   Ada is 36", ` + output + "hint: an output line is `    Ada is 36`"},
		{"    q?\n>> 500",
			`io.replay: line 2 of the script, ">> 500", ` + spacing + "hint: an input line is `>>  500`"},
		{">>500\n    q?",
			`io.replay: line 1 of the script, ">>500", ` + spacing + "hint: an input line is `>>  500`"},
		{">>   500\n>>",
			`io.replay: line 1 of the script, ">>   500", ` + spacing + "hint: an input line is `>>  500`"},
		{">>>\n    q?",
			`io.replay: line 1 of the script, ">>>", ` + spacing + "hint: an input line is `>>  >`"},
		{"> 500\n>>  x",
			`io.replay: line 1 of the script, "> 500", ` + output +
				"hint: an input line is `>>  500`, and an output line is `    > 500`"},
		{"  > 500\n>>  x",
			`io.replay: line 1 of the script, "  > 500", ` + output + "hint: an output line is `    > 500`"},
		{"  >>  500\n>>  x",
			`io.replay: line 1 of the script, "  >>  500", ` + output + "hint: an output line is `    >>  500`"},
		// No line is in two columns: the script may have been meant as
		// written, with output that starts with `>>`.
		{">> done\nbalance 0",
			`io.replay: line 1 of the script, ">> done", ` + spacing + "hint: an input line is `>>  done`" + columns},
		{"total\n>>> 3",
			`io.replay: line 1 of the script, "total", ` + output + "hint: an output line is `    total`" + columns},
		{">>  ok\ntotal",
			`io.replay: line 2 of the script, "total", ` + output + "hint: an output line is `    total`"},
	} {
		_, err := ParseReplayScript(tc.script)
		if err == nil || err.Error() != tc.want {
			t.Errorf("ParseReplayScript(%q):\n err = %v\nwant %s", tc.script, err, tc.want)
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
		{"    a  \r\n>>  x \r\n>>  \r\n", "    a\n>>  x\n>>\n"},
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
	script, err := ParseReplayScript("    Name:\n>>  Ada\n    Hello, Ada\n>>  50\n")
	if err != nil {
		t.Fatal(err)
	}
	inner, c := EnterReplay(NewFrame(context.Background()), script)
	defer c.Close()
	if got := inner.OpenCaptures(); len(got) != 1 || got[0] != c || !inner.Carries(c) || !inner.ReadsCaptured() {
		t.Fatalf("the replay's frame does not carry it: %v", got)
	}
	CaptureWrite(inner, "Name: ")
	ReadLine(inner)
	CaptureWrite(inner, "Hello, A")
	v := c.View()
	if v.Transcript != "    Name:\n>>  Ada\n    Hello, A\n" || v.Output != "Name: Hello, A" ||
		strings.Join(v.Read, ",") != "Ada" || strings.Join(v.Unread, ",") != "50" || v.Diverged != "" {
		t.Fatalf("View = %+v", v)
	}
	CaptureWrite(inner, "l\n")
	if got, want := c.View().Diverged, `line 3 of the script is "    Hello, Ada", and the transcript has "    Hello, Al"`; got != want {
		t.Fatalf("Diverged = %q, want %q", got, want)
	}
}

// A verbatim script's view keeps the transcript in the gutter form, and
// says where it diverges from the script's lines as written.
func TestReplay_ViewOfAVerbatimScript(t *testing.T) {
	inner, c := EnterReplay(NewFrame(context.Background()), mustParseReplay(t, "-10.0 C\n -5.0 C\n"))
	defer c.Close()
	CaptureWrite(inner, "-10.0 C\n -5.")
	if v := c.View(); v.Transcript != "    -10.0 C\n     -5.\n" || v.Diverged != "" {
		t.Fatalf("View = %+v", v)
	}
	CaptureWrite(inner, "1 C\n")
	if got, want := c.View().Diverged, `line 2 of the script is " -5.0 C", and the transcript has " -5.1 C"`; got != want {
		t.Fatalf("Diverged = %q, want %q", got, want)
	}
}

func TestDivergence(t *testing.T) {
	expected := "    one\n>>  x\n"
	for _, tc := range []struct{ finished, partial, want string }{
		{"", "", ""},
		{"    one\n", "", ""},
		{"    one\n>>  x\n", "", ""},
		{"", "    on", ""},
		{"", "    two", `line 1 of the script is "    one", and the transcript has begun "    two"`},
		{"    one  \n", "", ""},
		{"    one\n>>  x\n", "    more", `the script ends after line 2, and the transcript has begun "    more"`},
		{"    one\n>>  y\n", "", `line 2 of the script is ">>  x", and the transcript has ">>  y"`},
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
