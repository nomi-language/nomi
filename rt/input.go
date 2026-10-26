package rt

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"unicode/utf8"
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

// StdinPath is the path an io.Error about standard input names.
const StdinPath = "<stdin>"

// ReadLine is `io.read_line`: the next line of the frame's input with its line
// ending stripped, Ok(None) when no input remains, or the io.Error that
// stopped the read: InvalidText for a line that is not UTF-8. A final line
// with no newline is still a line. Inside `io.capture` the input is the
// capture's.
func ReadLine(fr *Frame) Result[Maybe[string], IOError] {
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
		return Ok[Maybe[string], IOError](None[string]())
	}
	return in.readLine()
}

// readLine reads the next line of in.
func (in *Input) readLine() Result[Maybe[string], IOError] {
	in.mu.Lock()
	defer in.mu.Unlock()
	line, err := in.r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return Err[Maybe[string]](IOError{Tag: TagIOOther, Operation: "read", Path: StdinPath, Code: ErrnoName(err)})
	}
	if err != nil && line == "" {
		return Ok[Maybe[string], IOError](None[string]())
	}
	return lineRead(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
}

// lineRead is a line read from standard input as `io.read_line` answers it:
// the line, or InvalidText when it is not UTF-8.
func lineRead(line string) Result[Maybe[string], IOError] {
	if !utf8.ValidString(line) {
		return Err[Maybe[string]](IOError{Tag: TagIOInvalidText, Path: StdinPath, Offset: int64(InvalidUTF8Offset(line))})
	}
	return Ok[Maybe[string], IOError](Some(line))
}

// InvalidUTF8Offset is the byte offset of the first sequence in s that is
// not UTF-8, or -1 when s is UTF-8.
func InvalidUTF8Offset(s string) int {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			return i
		}
		i += size
	}
	return -1
}
