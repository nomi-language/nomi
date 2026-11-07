// Package lineinput is the input std/csv's and std/xml's readers read: a
// Nomi Reader as an io.Reader, with a leading UTF-8 byte order mark
// dropped, and a log of the lines read so far, so that a parser's
// position in bytes becomes a column in code points.
//
// Go's encoding/csv and encoding/xml report a column as a byte offset in
// the line. std reports columns in code points, as std/json does, so the
// input keeps the bytes of each line from the oldest one a caller may
// still ask about (Forget moves that line on).
package lineinput

import (
	"bytes"
	"io"
	"unicode/utf8"

	"github.com/nomi-language/nomi/rt"
)

// Input reads a Reader for one parser. Frame is the frame of the host call
// that is reading, set by each call before it reads, since a Reader is
// read on the frame of the crossing that reads it.
type Input struct {
	Frame *rt.Frame
	r     rt.Reader
	// started is set once the first bytes were read and any byte order
	// mark dropped.
	started bool
	pending []byte
	// Failure is the io.Error the Reader failed with, if it did.
	Failure *rt.IOError
	log     lineLog
}

// New is an Input over r.
func New(r rt.Reader) *Input {
	return &Input{r: r, log: lineLog{first: 1, lines: [][]byte{nil}}}
}

// Read reads the Reader, as io.Reader says.
func (in *Input) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(in.pending) == 0 {
		chunk := rt.ReaderRead(in.Frame, in.r, int64(max(len(p), 4096)))
		if chunk.Tag != rt.TagOk {
			e := chunk.Err
			in.Failure = &e
			return 0, rt.IOFailure{E: e}
		}
		if chunk.Ok.Tag != rt.TagSome {
			if !in.started {
				in.started = true
			}
			return 0, io.EOF
		}
		in.pending = []byte(chunk.Ok.Some)
		if !in.started {
			// A byte order mark may arrive split over reads; wait for
			// three bytes or the end before deciding.
			if len(in.pending) < 3 && bytes.HasPrefix([]byte("\xef\xbb\xbf"), in.pending) {
				more := rt.ReaderRead(in.Frame, in.r, 3)
				if more.Tag == rt.TagOk && more.Ok.Tag == rt.TagSome {
					in.pending = append(in.pending, more.Ok.Some...)
				}
				if more.Tag != rt.TagOk {
					e := more.Err
					in.Failure = &e
					return 0, rt.IOFailure{E: e}
				}
			}
			in.pending = bytes.TrimPrefix(in.pending, []byte("\xef\xbb\xbf"))
			in.started = true
		}
	}
	n := copy(p, in.pending)
	in.log.add(in.pending[:n])
	in.pending = in.pending[n:]
	return n, nil
}

// Column is the 1-based column, in code points, of the byte at the
// 1-based byte column col of the 1-based line, or col itself when the
// line is no longer kept.
func (in *Input) Column(line, col int) int {
	return in.log.column(line, col)
}

// Forget drops every line before line: no caller asks about them again.
func (in *Input) Forget(line int) {
	in.log.forget(line)
}

// lineLog is the lines read since the line first, each without its "\n".
// The last is the line being read.
type lineLog struct {
	first int
	lines [][]byte
}

func (l *lineLog) add(p []byte) {
	for {
		i := bytes.IndexByte(p, '\n')
		last := len(l.lines) - 1
		if i < 0 {
			l.lines[last] = append(l.lines[last], p...)
			return
		}
		l.lines[last] = append(l.lines[last], p[:i]...)
		l.lines = append(l.lines, nil)
		p = p[i+1:]
	}
}

func (l *lineLog) column(line, col int) int {
	i := line - l.first
	if i < 0 || i >= len(l.lines) || col < 1 {
		return col
	}
	text := l.lines[i]
	if col-1 < len(text) {
		text = text[:col-1]
	}
	return utf8.RuneCount(text) + 1
}

func (l *lineLog) forget(line int) {
	drop := line - l.first
	if drop <= 0 {
		return
	}
	if drop >= len(l.lines) {
		drop = len(l.lines) - 1
	}
	l.lines = append([][]byte(nil), l.lines[drop:]...)
	l.first += drop
}
