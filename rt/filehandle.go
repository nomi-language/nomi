package rt

import (
	"errors"
	"io"
	"math"
	"runtime"
	"sync"
	"time"
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
//
// Its maker also says how the file is made durable (Sync) and how the
// handle locks a range of the file's bytes (Lock). A lock belongs to the
// handle, never to the process, and closing the handle releases it. Lock
// never waits: FileLock waits by asking again, with the handle's lock let
// go between attempts, so another task can unlock through the same handle,
// and a wait ends when its task is cancelled as a sleep does.

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
	// Fail is as ReadSource's.
	Fail func(op string, err error) IOError
	// Sync makes what was written to the file durable: its data and its
	// metadata, or with dataOnly the data and what reading it back needs.
	Sync func(dataOnly bool) error
	// Lock sets the handle's lock on length bytes from start (0: from start
	// to past any end the file can have) to mode, and reports whether it
	// could: false, with nothing changed, when another handle holds a lock
	// there that conflicts. It never waits. Close releases the handle's
	// locks.
	Lock func(mode LockMode, start, length int64) (bool, error)
}

// LockMode is what FileLock does with a range: std/io's mode codes.
type LockMode int64

const (
	// LockNone unlocks the range.
	LockNone LockMode = iota
	// LockShared is a lock any number of handles may hold on the same
	// bytes.
	LockShared
	// LockExclusive is a lock no other handle holds any lock beside.
	LockExclusive
)

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
func (st *fileState) readAt(offsetArg, maxArg Int) Result[Option[Bytes], IOError] {
	offset, ok := offsetArg.Int64()
	if !ok {
		return Ok[Option[Bytes], IOError](None[Bytes]())
	}
	max := maxArg.Saturated()
	if max > readChunk {
		if size, err := st.src.Size(); err == nil {
			max = min(max, size-offset)
		}
		if max <= 0 {
			return Ok[Option[Bytes], IOError](None[Bytes]())
		}
	}
	buf := make([]byte, max)
	n, err := st.src.F.ReadAt(buf, offset)
	if n > 0 {
		return Ok[Option[Bytes], IOError](Some(Bytes(buf[:n])))
	}
	if err == nil || errors.Is(err, io.EOF) {
		return Ok[Option[Bytes], IOError](None[Bytes]())
	}
	return Err[Option[Bytes]](st.fail("read", err))
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
func FileRead(f File, max Int) Result[Option[Bytes], IOError] {
	return fileDo(f, func(st *fileState) Result[Option[Bytes], IOError] {
		got := st.readAt(SmallInt(st.pos), max)
		if got.Tag == TagOk && got.Ok.Tag == TagSome {
			st.pos += int64(len(got.Ok.Some))
		}
		return got
	})
}

// FileReadAt is `File.read_at`: up to max bytes at offset. The position
// stays where it was.
func FileReadAt(f File, offset, max Int) Result[Option[Bytes], IOError] {
	return fileDo(f, func(st *fileState) Result[Option[Bytes], IOError] { return st.readAt(offset, max) })
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

// FileSync is `File.sync`: what was written is durable, the metadata too.
func FileSync(f File) Result[Unit, IOError] { return fileSync(f, false) }

// FileSyncData is `File.sync_data`: the data and, of the metadata, what
// reading it back needs reach the device (on macOS its cache).
func FileSyncData(f File) Result[Unit, IOError] { return fileSync(f, true) }

func fileSync(f File, dataOnly bool) Result[Unit, IOError] {
	return fileDo(f, func(st *fileState) Result[Unit, IOError] {
		if err := st.src.Sync(dataOnly); err != nil {
			return Err[Unit](st.fail("sync", err))
		}
		return Ok[Unit, IOError](Unit{})
	})
}

// FileLock is std/fs/host's `lock`, which `fs.lock`, `fs.try_lock` and
// `fs.unlock` call: the handle's lock on length bytes from start (0: to
// past any end) becomes mode. It answers false when
// another handle holds a lock there that conflicts and wait has passed;
// a wait of 0 or less asks once. An exclusive lock on a handle opened only
// to read is PermissionDenied, on every system. A start past int64 is past
// every file, as for a write, and a length that reaches past int64 is the
// same as one to past any end.
func FileLock(fr *Frame, f File, mode int64, startArg, lengthArg Int, wait Duration) Result[bool, IOError] {
	deadline := time.Now().Add(time.Duration(wait))
	pause := time.Millisecond
	for {
		got := fileDo(f, func(st *fileState) Result[bool, IOError] { return st.lock(LockMode(mode), startArg, lengthArg) })
		if got.Tag != TagOk || got.Ok {
			return got
		}
		left := time.Until(deadline)
		if left <= 0 {
			return got
		}
		if !SleepCtx(fr.Context(), Duration(min(pause, left))) {
			CancelIfDone(fr)
			return got
		}
		pause = min(2*pause, 50*time.Millisecond)
	}
}

func (st *fileState) lock(mode LockMode, startArg, lengthArg Int) Result[bool, IOError] {
	if mode == LockExclusive && !st.src.Writable {
		return Err[bool](IOError{Tag: TagIOPermissionDenied, Path: st.src.Name})
	}
	start, ok := startArg.Int64()
	if !ok {
		return Err[bool](st.pastFile("lock", "EOVERFLOW"))
	}
	length := lengthArg.Saturated()
	if length > math.MaxInt64-start {
		length = 0
	}
	got, err := st.src.Lock(mode, start, length)
	if err != nil {
		return Err[bool](st.fail("lock", err))
	}
	return Ok[bool, IOError](got)
}

// FileClose is `File.close`. A handle closed twice answers Closed.
func FileClose(f File) Result[Unit, IOError] {
	if f.h == nil {
		return Err[Unit](IOError{Tag: TagIOClosed})
	}
	return f.h.st.close()
}
