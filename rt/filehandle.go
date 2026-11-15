package rt

import (
	"errors"
	"io"
	"runtime"
	"sync"
)

// std/io's `File`: one file open for reads and writes at a position.
//
// A Reader and a Writer are streams: buffered, read or written front to back.
// A File is not. Every call is one operation on the file at a position, with
// no buffer between the program and the file, so a write is in the file when
// the call returns and a read sees what the last write left. The handle keeps
// the position `read` and `write` start at and move on; `read_at` and
// `write_at` name their own and leave it where it was.
//
// What the handle reads and writes is a FileAccess its maker hands in
// (internal/stdio opens the file), so rt makes no file system call itself,
// as with the streams. The handle's value never changes; each call holds the
// handle's lock, so two tasks sharing one never interleave inside a call. A
// handle nobody closes is closed when the program no longer holds it.

// File is std/io's `File`. The value names the open file and never changes.
type File struct{ h *fileHandle }

// FileAccess is what a File reads and writes: positional reads and writes,
// the size, and truncation. *os.File is one.
type FileAccess interface {
	io.ReaderAt
	io.WriterAt
	// Truncate changes the size, dropping what is past it or adding zeros.
	Truncate(size int64) error
}

// FileSource is what a File is made over.
type FileSource struct {
	// Name is the path an error about the file names.
	Name string
	F    FileAccess
	// Size answers the file's size now.
	Size func() (int64, error)
	// Writable says the file was opened to write as well as read. A write
	// to a handle opened only to read is PermissionDenied.
	Writable bool
	// Close releases the file, or is nil.
	Close func() error
	// Fail is the io.Error for err, a failure of operation op, when err is
	// not an IOFailure. Nil makes such a failure Other.
	Fail func(op string, err error) IOError
}

type fileHandle struct{ st *fileState }

type fileState struct {
	mu     sync.Mutex
	src    FileSource
	pos    int64
	closed bool
}

// NewFile is a File over src, at position 0.
func NewFile(src FileSource) File {
	st := &fileState{src: src}
	h := &fileHandle{st: st}
	runtime.AddCleanup(h, func(st *fileState) { st.close() }, st)
	return File{h: h}
}

func (st *fileState) fail(op string, err error) IOError {
	return fail(st.src.Name, op, err, st.src.Fail)
}

func (st *fileState) close() Result[Unit, IOError] {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return Err[Unit](IOError{Tag: TagIOClosed})
	}
	st.closed = true
	if st.src.Close != nil {
		if err := st.src.Close(); err != nil {
			return Err[Unit](st.fail("close", err))
		}
	}
	return Ok[Unit, IOError](Unit{})
}

// fileDo runs body with the handle locked, or answers Closed.
func fileDo[T any](f File, body func(st *fileState) Result[T, IOError]) Result[T, IOError] {
	if f.h == nil {
		return Err[T](IOError{Tag: TagIOClosed})
	}
	st := f.h.st
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return Err[T](IOError{Tag: TagIOClosed})
	}
	return body(st)
}

// readAt reads up to max bytes at offset: Ok(None) at or past the end.
// Both are Ints of any size. An offset past int64 is past the end of every
// file, and a read past what the file holds reads what it holds, so the
// buffer is never larger than the file.
func (st *fileState) readAt(offsetArg, maxArg Int) Result[Maybe[Bytes], IOError] {
	offset, ok := offsetArg.Int64()
	if !ok {
		return Ok[Maybe[Bytes], IOError](None[Bytes]())
	}
	max := maxArg.Saturated()
	if max > readChunk {
		if size, err := st.src.Size(); err == nil {
			max = min(max, size-offset)
		}
		if max <= 0 {
			return Ok[Maybe[Bytes], IOError](None[Bytes]())
		}
	}
	buf := make([]byte, max)
	n, err := st.src.F.ReadAt(buf, offset)
	if n > 0 {
		return Ok[Maybe[Bytes], IOError](Some(Bytes(buf[:n])))
	}
	if err == nil || errors.Is(err, io.EOF) {
		return Ok[Maybe[Bytes], IOError](None[Bytes]())
	}
	return Err[Maybe[Bytes]](st.fail("read", err))
}

// readChunk is the read size past which readAt holds a read to the
// file's size before it allocates.
const readChunk = 1 << 20

// pastFile is the error for an offset past int64, which no operating
// system's file reaches: what one reports for a write or a size past its
// largest file.
func (st *fileState) pastFile(op, code string) IOError {
	return IOError{Tag: TagIOOther, Path: st.src.Name, Operation: op, Code: code}
}

func (st *fileState) writeAt(offsetArg Int, data Bytes) Result[Unit, IOError] {
	if !st.src.Writable {
		return Err[Unit](IOError{Tag: TagIOPermissionDenied, Path: st.src.Name})
	}
	offset, ok := offsetArg.Int64()
	if !ok {
		return Err[Unit](st.pastFile("write", "EFBIG"))
	}
	if _, err := st.src.F.WriteAt([]byte(data), offset); err != nil {
		return Err[Unit](st.fail("write", err))
	}
	return Ok[Unit, IOError](Unit{})
}

// FileRead is std/io's private `file_read`, which `File.read` calls: up to
// max bytes at the position, which moves past them. std declares max a
// PositiveInt, and every position here a NonNegativeInt, so none is checked.
func FileRead(f File, max Int) Result[Maybe[Bytes], IOError] {
	return fileDo(f, func(st *fileState) Result[Maybe[Bytes], IOError] {
		got := st.readAt(SmallInt(st.pos), max)
		if got.Tag == TagOk && got.Ok.Tag == TagSome {
			st.pos += int64(len(got.Ok.Some))
		}
		return got
	})
}

// FileReadAt is `File.read_at`: up to max bytes at offset. The position
// stays where it was.
func FileReadAt(f File, offset, max Int) Result[Maybe[Bytes], IOError] {
	return fileDo(f, func(st *fileState) Result[Maybe[Bytes], IOError] { return st.readAt(offset, max) })
}

// FileWrite is `File.write`: data at the position, which moves past it.
func FileWrite(f File, data Bytes) Result[Unit, IOError] {
	return fileDo(f, func(st *fileState) Result[Unit, IOError] {
		res := st.writeAt(SmallInt(st.pos), data)
		if res.Tag == TagOk {
			st.pos += int64(len(data))
		}
		return res
	})
}

// FileWriteAt is `File.write_at`: data at offset. A write past the end
// fills the gap with zeros. The position stays where it was.
func FileWriteAt(f File, offset Int, data Bytes) Result[Unit, IOError] {
	return fileDo(f, func(st *fileState) Result[Unit, IOError] { return st.writeAt(offset, data) })
}

// FileSeek is `File.seek`: the position becomes to, counted from the start.
// A position past the end is allowed; a write there fills the gap. One past
// int64 is past what a file's position holds, as an operating system's
// seek reports.
func FileSeek(f File, toArg Int) Result[Unit, IOError] {
	return fileDo(f, func(st *fileState) Result[Unit, IOError] {
		to, ok := toArg.Int64()
		if !ok {
			return Err[Unit](st.pastFile("seek", "EOVERFLOW"))
		}
		st.pos = to
		return Ok[Unit, IOError](Unit{})
	})
}

// FilePosition is `File.position`.
func FilePosition(f File) Result[int64, IOError] {
	return fileDo(f, func(st *fileState) Result[int64, IOError] { return Ok[int64, IOError](st.pos) })
}

// FileSize is `File.size`: the size of the file now.
func FileSize(f File) Result[int64, IOError] {
	return fileDo(f, func(st *fileState) Result[int64, IOError] {
		n, err := st.src.Size()
		if err != nil {
			return Err[int64](st.fail("stat", err))
		}
		return Ok[int64, IOError](n)
	})
}

// FileTruncate is `File.truncate`: the file's size becomes size. The
// position stays where it was.
func FileTruncate(f File, sizeArg Int) Result[Unit, IOError] {
	return fileDo(f, func(st *fileState) Result[Unit, IOError] {
		if !st.src.Writable {
			return Err[Unit](IOError{Tag: TagIOPermissionDenied, Path: st.src.Name})
		}
		size, ok := sizeArg.Int64()
		if !ok {
			return Err[Unit](st.pastFile("truncate", "EFBIG"))
		}
		if err := st.src.F.Truncate(size); err != nil {
			return Err[Unit](st.fail("truncate", err))
		}
		return Ok[Unit, IOError](Unit{})
	})
}

// FileClose is `File.close`. A handle closed twice answers Closed.
func FileClose(f File) Result[Unit, IOError] {
	if f.h == nil {
		return Err[Unit](IOError{Tag: TagIOClosed})
	}
	return f.h.st.close()
}
