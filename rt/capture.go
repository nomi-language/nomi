package rt

import (
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
// the program read written in at the point it was read, the way a terminal
// shows a session. A read line is written as its own transcript line, marked
// by TranscriptMarker. When the program had written part of a line before the
// read (a prompt with no newline, `io.write("amount: ")`), that part goes on
// the marked line between the marker and the typed text, as the terminal
// shows the typed text after the prompt:
//
//	io.write("amount: ") then a read of "25"   ->   "> amount: 25"
//
// A read at the end of input adds nothing. An output line that would read as
// a marked line (it starts with `>`), or as an escaped one (it starts with
// `\`), is written with a `\` in front, so every transcript line starting with
// `>` is a read and nothing else.
//
// # Replaying a script
//
// EnterReplay's capture takes its input from a script shaped like a
// transcript instead of plain text: each line starting with `>` answers one
// read, and the rest is the output the script expects (ReplayText compares
// the two). The typed text of a marked line is what follows the marker and
// one space, less the prompt the program wrote on that line before reading:
// for the script line `> amount: 25`, a program that wrote "amount: " reads
// "25". So the input a script feeds is decided at each read, which is why the
// capture and not the caller parses it.
type Capture struct {
	mu sync.Mutex
	in *Input
	// replay is set for EnterReplay's capture, whose input is script.
	replay bool
	script []string
	next   int
	out    strings.Builder
	// tr holds the finished transcript lines; line is the output line in
	// progress, not yet escaped, since a read may still make it a prompt.
	tr     strings.Builder
	line   strings.Builder
	closed bool
	outer  *Capture
}

// TranscriptMarker starts every line of a transcript the program read.
const TranscriptMarker = "> "

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
// the transcript and the script as ReplayText spells them, and CaptureResult's
// Unread.
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

// EnterReplay is EnterCapture for `io.replay`: the input is the lines of
// script that start with `>`, fed one per read as Capture describes.
func EnterReplay(parent *Frame, script string) (*Frame, *Capture) {
	return enter(parent, &Capture{replay: true, script: replayInput(script)})
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
		tr += escapeTranscriptLine(c.line.String())
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
		c.tr.WriteString(escapeTranscriptLine(c.line.String()))
		c.tr.WriteByte('\n')
		c.line.Reset()
		s = s[i+1:]
	}
}

// escapeTranscriptLine is an output line as the transcript writes it: with a
// `\` in front when it starts with `>` or `\`, so it cannot read as a line
// the program read.
func escapeTranscriptLine(line string) string {
	if strings.HasPrefix(line, ">") || strings.HasPrefix(line, `\`) {
		return `\` + line
	}
	return line
}

// captureFor is the frame's innermost open capture, or nil.
func captureFor(fr *Frame) *Capture {
	for c := fr.capture; c != nil; c = c.outer {
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
// written into the transcript, or Err("eof").
func (c *Capture) readLine() Result[string, string] {
	c.mu.Lock()
	defer c.mu.Unlock()
	prompt := c.line.String()
	var text string
	if c.replay {
		if c.next >= len(c.script) {
			return Err[string]("eof")
		}
		text = c.script[c.next]
		c.next++
		switch {
		case strings.HasPrefix(text, prompt):
			text = text[len(prompt):]
		case text == strings.TrimRight(prompt, " \t"):
			// The script's line lost the prompt's trailing space, as
			// ReplayText drops it; nothing was typed.
			text = ""
		}
	} else {
		r := c.in.readLine()
		if r.Tag != TagOk {
			return r
		}
		text = r.Ok
	}
	c.tr.WriteString(TranscriptMarker)
	c.tr.WriteString(prompt)
	c.tr.WriteString(text)
	c.tr.WriteByte('\n')
	c.line.Reset()
	return Ok[string, string](text)
}

// replayInput is a replay script's input: each line that starts with `>`,
// without the `>`, one space after it, and trailing whitespace.
func replayInput(script string) []string {
	var lines []string
	for _, line := range strings.Split(script, "\n") {
		if rest, ok := markedLine(strings.TrimRight(line, " \t\r")); ok {
			lines = append(lines, rest)
		}
	}
	return lines
}

// markedLine answers the text after a transcript line's marker: the `>` and
// at most one space after it.
func markedLine(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, ">")
	if !ok {
		return "", false
	}
	return strings.TrimPrefix(rest, " "), true
}

// ReplayText is a transcript or a replay script in the form `io.replay`
// compares: each line without its trailing spaces, tabs and carriage return,
// a marked line spelled with the marker and one space (`>25` reads `> 25`),
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
		line = strings.TrimRight(line, " \t\r")
		if rest, ok := markedLine(line); ok {
			line = strings.TrimRight(TranscriptMarker+rest, " ")
		}
		lines[i] = line
	}
	if n := len(lines); n > 1 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return strings.Join(lines, "\n") + "\n"
}
