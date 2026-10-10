package rt

import (
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Capture is one `io.capture` or `io.replay` call in force: the input
// `io.read_line` reads and the output `io.print`, `io.write` and `io.inspect`
// append to, for the dynamic extent of the call. `dbg` is not redirected.
//
// It rides the frame, so it is per lineage and not per machine: a task
// spawned inside the call inherits it with the rest of the spawner's frame,
// and two tasks capturing at once each write to their own. A nested capture
// takes over for its extent and points back at the one it replaced.
//
// A task can outlive the call (a supervised one does). Once the call returns
// the capture is closed, and a closed capture passes reads and writes on to
// the one it replaced, ending at the program's own input and output: a late
// write never lands in a `Captured` the caller already holds.
//
// # The transcript
//
// Beside the output a capture keeps a transcript: the output with each line
// the program read written in at the point it was read, behind a four-column
// gutter, Nomi's indent. A line read is InputMarker, `>>` and two spaces,
// followed by exactly the text read. Every output line is OutputGutter, four
// spaces, followed by the line exactly as the program wrote it, and a blank
// output line stays blank. Output is never escaped: what tells the two apart
// is the first four columns.
//
// What the program wrote of a line before a read (a prompt with no newline)
// is output, and the read ends that line, as the newline typed after the
// input does in a terminal:
//
//	io.write("Your age: ") then a read of "36"   ->   "    Your age: " and ">>  36"
//	io.write("> ") then a read of "north"         ->   "    > " and ">>  north"
//	a read of "Ada" at the start of a line         ->   ">>  Ada"
//
// A read at the end of input writes no line and leaves the prompt it
// answered in place, so the prompt and whatever follows it are one output
// line, as in Output.
//
// # Replaying a script
//
// EnterReplay's capture takes its input from a script in the transcript's
// form (ParseReplayScript): the text of each input line answers one read, in
// order, and the rest is the transcript the script expects.
type Capture struct {
	mu sync.Mutex
	in *Input
	// replay is set for EnterReplay's capture, whose input is script.
	replay bool
	script []string
	next   int
	out    strings.Builder
	// tr holds the finished transcript lines; line is the output line in
	// progress, which a read may end.
	tr     strings.Builder
	line   strings.Builder
	closed bool
	outer  *Capture
	// test is set for a test case's capture (EnterTestCapture): it collects
	// the case's output, `dbg` included, and never answers a read.
	test bool
}

// InputMarker starts a transcript line the program read; OutputGutter starts
// every non-blank output line. Both are four columns wide.
const (
	InputMarker  = ">>  "
	OutputGutter = "    "
)

// CaptureResult is what a closed capture holds.
type CaptureResult struct {
	// Output is everything written, byte for byte.
	Output string
	// Transcript is Output with each line read written in (see Capture).
	Transcript string
	// Unread is how many of a replay script's input lines no read reached.
	// Always 0 for a plain capture.
	Unread int
}

// IOReplayed is std/io's `Replayed`, what `io.replay` answers: the output,
// the transcript and the script's expected transcript as ReplayText spells
// them, and CaptureResult's Unread.
type IOReplayed struct {
	Output     string
	Transcript string
	Expected   string
	Unread     int64
}

// EnterCapture returns the frame `run` is called on inside `io.capture`, and
// the capture it writes to. The caller closes the capture on every path.
func EnterCapture(parent *Frame, input string) (*Frame, *Capture) {
	return enter(parent, &Capture{in: NewInput(strings.NewReader(input))})
}

// EnterReplay is EnterCapture for `io.replay`: the input is the script's
// input lines, one per read.
func EnterReplay(parent *Frame, script ReplayScript) (*Frame, *Capture) {
	return enter(parent, &Capture{replay: true, script: script.Input})
}

// EnterTestCapture returns the frame a test case runs on and the capture
// that collects the case's own output: `io.print`, `io.write`, `io.inspect`
// and `dbg`, from the case's boot, setup and body, the tasks it starts and
// the `once` initializers it forces. An `io.capture` inside the case takes
// over the first three for its extent, as it does anywhere; `dbg` still
// reaches the case's capture. `io.read_line` reads the program's input, not
// this capture. The runner closes it when the case ends, and a write after
// that goes to the program's output.
func EnterTestCapture(parent *Frame) (*Frame, *Capture) {
	return enter(parent, &Capture{test: true})
}

func enter(parent *Frame, c *Capture) (*Frame, *Capture) {
	c.outer = parent.capture
	child := *parent
	child.capture = c
	return &child, c
}

// Close ends the capture and answers what it holds. Closing twice answers
// the same thing.
func (c *Capture) Close() CaptureResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	tr := c.tr.String()
	if c.line.Len() > 0 {
		tr += outputLine(c.line.String())
	}
	return CaptureResult{Output: c.out.String(), Transcript: tr, Unread: len(c.script) - c.next}
}

// CaptureWrite appends s to the frame's innermost open capture and reports
// whether one took it. False means the write goes to the program's output.
// With nothing captured it is two nil checks.
func CaptureWrite(fr *Frame, s string) bool {
	if fr == nil {
		return false
	}
	for c := fr.capture; c != nil; c = c.outer {
		c.mu.Lock()
		if !c.closed {
			c.write(s)
			c.mu.Unlock()
			return true
		}
		c.mu.Unlock()
	}
	return false
}

// write appends s to the output and the transcript. c.mu is held.
func (c *Capture) write(s string) {
	c.out.WriteString(s)
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			c.line.WriteString(s)
			return
		}
		c.line.WriteString(s[:i])
		c.endLine()
		s = s[i+1:]
	}
}

// endLine writes the output line in progress to the transcript and starts
// another. c.mu is held.
func (c *Capture) endLine() {
	c.tr.WriteString(outputLine(c.line.String()))
	c.tr.WriteByte('\n')
	c.line.Reset()
}

// outputLine is an output line as the transcript writes it: behind the
// gutter, or blank when it is.
func outputLine(line string) string {
	if line == "" {
		return ""
	}
	return OutputGutter + line
}

// CaptureDbg writes a `dbg` line, text (DbgReportText), to the innermost
// open test capture on the frame (EnterTestCapture) and reports whether one
// took it. An `io.capture` does not take `dbg`.
func CaptureDbg(fr *Frame, text func() string) bool {
	if fr == nil {
		return false
	}
	for c := fr.capture; c != nil; c = c.outer {
		if !c.test {
			continue
		}
		c.mu.Lock()
		if !c.closed {
			c.write(text())
			c.mu.Unlock()
			return true
		}
		c.mu.Unlock()
	}
	return false
}

// DbgReportText is DbgText for a test report: coloured as the report colours
// its own lines, by what stdout is, since the report is where the line
// appears.
func DbgReportText(line int, expr, rendered string) string {
	return DbgText(colourAsStdout{io.Discard}, line, expr, rendered)
}

// captureFor is the frame's innermost open capture that answers reads, or
// nil. A test capture answers none.
func captureFor(fr *Frame) *Capture {
	for c := fr.capture; c != nil; c = c.outer {
		if c.test {
			continue
		}
		c.mu.Lock()
		open := !c.closed
		c.mu.Unlock()
		if open {
			return c
		}
	}
	return nil
}

// readLine is `io.read_line` inside the capture: the next line of its input,
// written into the transcript after the prompt it ends, or Err("eof"), which
// writes nothing and leaves the prompt in place.
func (c *Capture) readLine() Result[string, string] {
	c.mu.Lock()
	defer c.mu.Unlock()
	var text string
	if c.replay {
		if c.next >= len(c.script) {
			return Err[string]("eof")
		}
		text = c.script[c.next]
		c.next++
	} else {
		r := c.in.readLine()
		if r.Tag != TagOk {
			return r
		}
		text = r.Ok
	}
	if c.line.Len() > 0 {
		c.endLine()
	}
	c.tr.WriteString(InputMarker)
	c.tr.WriteString(text)
	c.tr.WriteByte('\n')
	return Ok[string, string](text)
}

// ReplayScript is an `io.replay` script taken apart: the text of each input
// line, in order, and the transcript the script expects, as ReplayText
// spells it.
type ReplayScript struct {
	Input    []string
	Expected string
}

// ParseReplayScript takes an `io.replay` script apart. Trailing whitespace
// and carriage returns on each line do not count (ReplayText).
//
// A script is in the transcript's form, whether or not it has input. A line
// that is InputMarker followed by text that does not start with a space, or
// `>>` alone (empty input, InputMarker with its trailing spaces stripped),
// is input, and its text is what follows InputMarker. A blank line is blank
// output, and a line that starts with OutputGutter is output. Any other line
// is an error naming it (replayLineError): `>>` followed by other than two
// spaces (`>> 500`, `>>500`, `>>   500`), or output behind fewer than four
// columns. Without the gutter, output that starts with `>>` would read as
// input.
func ParseReplayScript(script string) (ReplayScript, error) {
	expected := ReplayText(script)
	var input []string
	for i, line := range strings.Split(strings.TrimSuffix(expected, "\n"), "\n") {
		switch {
		case line == "" || strings.HasPrefix(line, OutputGutter):
		case line == ">>":
			input = append(input, "")
		case strings.HasPrefix(line, InputMarker) && line[len(InputMarker)] != ' ':
			input = append(input, line[len(InputMarker):])
		default:
			return ReplayScript{}, replayLineError(i+1, line)
		}
	}
	return ReplayScript{Input: input, Expected: expected}, nil
}

// replayLineError is the error for line n of a script, which is neither
// input nor output, with a hint that writes the line with the right gutter:
// behind InputMarker when it starts with `>>`; behind OutputGutter
// otherwise, and behind InputMarker as well when it starts with `>`, which
// reads as input written with a short marker. The number is the script's
// line, not a source line, so it is not one of the positioned fault texts.
func replayLineError(n int, line string) error {
	at := "io.replay: line " + strconv.Itoa(n) + " of the script, " + strconv.Quote(line) + ", "
	if rest, ok := strings.CutPrefix(line, ">>"); ok {
		return errors.New(at + "must have two spaces after `>>`\n" +
			"hint: an input line is `" + InputMarker + strings.TrimLeft(rest, " ") + "`")
	}
	text := strings.TrimLeft(line, " ")
	hint := "hint: an output line is `" + OutputGutter + text + "`"
	if rest, ok := strings.CutPrefix(text, ">"); ok && text == line {
		hint = "hint: an input line is `" + InputMarker + strings.TrimLeft(rest, " ") +
			"`, and an output line is `" + OutputGutter + text + "`"
	}
	return errors.New(at + "must start with `" + InputMarker + "` (input) or four spaces (output)\n" + hint)
}

// ReplayText is a transcript or a replay script in the form `io.replay`
// compares: each line without its trailing spaces, tabs and carriage return,
// and every line, the last included, ended by one newline. So a script
// written as a `"""` literal, which drops its last newline, matches a
// program's output that ends in one, and trailing whitespace an editor may
// strip does not decide a replay. Empty text stays empty.
func ReplayText(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}
	if n := len(lines); n > 1 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return strings.Join(lines, "\n") + "\n"
}
