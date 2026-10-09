package rt

import (
	"bufio"
	"context"
	"io"
	"strings"
	"sync"
)

// Input is a program's standard input: the reader `io.read_line` reads a line
// at a time. One Input is shared by every frame a machine builds, so its
// buffer is too: a line read in one task is not read again in another.
type Input struct {
	mu sync.Mutex
	r  *bufio.Reader
}

// NewInput wraps r as a program's input.
func NewInput(r io.Reader) *Input {
	return &Input{r: bufio.NewReader(r)}
}

type inputKey struct{}

// WithInput answers ctx carrying in, for every frame built over it. A frame
// over a context with no Input reads end of input at once.
func WithInput(ctx context.Context, in *Input) context.Context {
	return context.WithValue(ctx, inputKey{}, in)
}

// ReadLine is `io.read_line`: the next line of the frame's input with its line
// ending stripped, or Err("eof") when no input remains. A final line with no
// newline is still a line. Inside `io.capture` the input is the capture's.
func ReadLine(fr *Frame) Result[string, string] {
	var in *Input
	if fr != nil {
		if c := captureFor(fr); c != nil {
			return c.readLine()
		}
		if fr.ctx != nil {
			in, _ = fr.ctx.Value(inputKey{}).(*Input)
		}
	}
	if in == nil {
		return Err[string]("eof")
	}
	return in.readLine()
}

// readLine reads the next line of in.
func (in *Input) readLine() Result[string, string] {
	in.mu.Lock()
	defer in.mu.Unlock()
	line, err := in.r.ReadString('\n')
	if err != nil && line == "" {
		return Err[string]("eof")
	}
	return Ok[string, string](strings.TrimRight(line, "\r\n"))
}
