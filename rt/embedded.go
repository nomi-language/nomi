package rt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"sync"
)

// The files an `embed` declaration holds (spec §18 *Embedded files*), as a
// machine reads them.
//
// A program never holds an embedded file's contents until it reads the
// file. Its `Files` value holds each file's handle and size, and a read
// asks the machine's EmbeddedFiles for the contents by handle
// (EmbeddedRead). The handle is the file's index in the table, made when
// the program's `embed` first runs (EmbeddedFiles.Handle). Where the
// contents are is the host's: a built program's image (internal vmrunner),
// the source file on disk for `nomi run` (internal/vm), or an in-memory
// program's data files (vmhost). rt makes no file system call, so it holds
// only the table and the interface.

// EmbeddedFile is one embedded file as the program was compiled with it:
// where its contents are read from, and their size and SHA-256 (lowercase
// hex), which a source checks what it reads against.
type EmbeddedFile struct {
	Source string
	Size   int64
	Sum    string
}

// EmbeddedSource reads embedded files' contents. Each read answers the
// whole file, and nothing is kept between reads: a source that holds the
// file already (a built image in memory or mapped) answers a view of it,
// and one that reads (the disk) answers what it read this time.
type EmbeddedSource interface {
	ReadEmbedded(f EmbeddedFile) (Bytes, error)
}

// EmbeddedFiles is a machine's table of the embedded files its program
// named, and the source it reads their contents from.
type EmbeddedFiles struct {
	src   EmbeddedSource
	mu    sync.Mutex
	files []EmbeddedFile
	index map[EmbeddedFile]int64
}

// NewEmbeddedFiles is an empty table over src.
func NewEmbeddedFiles(src EmbeddedSource) *EmbeddedFiles {
	return &EmbeddedFiles{src: src, index: map[EmbeddedFile]int64{}}
}

// Handle is f's handle, the same for every call with the same file.
func (t *EmbeddedFiles) Handle(f EmbeddedFile) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if h, ok := t.index[f]; ok {
		return h
	}
	h := int64(len(t.files))
	t.files = append(t.files, f)
	t.index[f] = h
	return h
}

// file is the file handle names.
func (t *EmbeddedFiles) file(handle int64) (EmbeddedFile, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if handle < 0 || handle >= int64(len(t.files)) {
		return EmbeddedFile{}, false
	}
	return t.files[handle], true
}

type embeddedKey struct{}

// WithEmbeddedFiles answers ctx carrying t, for every frame built over it.
func WithEmbeddedFiles(ctx context.Context, t *EmbeddedFiles) context.Context {
	return context.WithValue(ctx, embeddedKey{}, t)
}

// EmbeddedRead is std/embed's `read_stored`: the contents of the embedded
// file handle names, whose path in its tree is path. A file the source
// cannot answer as it was compiled (one changed on disk since, for `nomi
// run`) is a fault naming it: the program stops rather than read other
// contents than it was compiled with.
func EmbeddedRead(fr *Frame, handle int64, path string) Bytes {
	t, f := embeddedFile(fr, handle, path)
	data, err := t.src.ReadEmbedded(f)
	if err != nil {
		Trap("embedded file " + strconv.Quote(path) + ": " + err.Error())
	}
	return data
}

// EmbeddedSum is std/embed/host's `sum`: the SHA-256 of the embedded file
// handle names, in lowercase hex, as the compiler took it when it read the
// file. A file compiled without one (by a front end that read no
// contents) is read and hashed now.
func EmbeddedSum(fr *Frame, handle int64, path string) string {
	_, f := embeddedFile(fr, handle, path)
	if f.Sum != "" {
		return f.Sum
	}
	h := sha256.Sum256([]byte(EmbeddedRead(fr, handle, path)))
	return hex.EncodeToString(h[:])
}

// embeddedFile is the machine's table and the file handle names in it, or
// a fault naming path.
func embeddedFile(fr *Frame, handle int64, path string) (*EmbeddedFiles, EmbeddedFile) {
	var t *EmbeddedFiles
	if fr != nil && fr.ctx != nil {
		t, _ = fr.ctx.Value(embeddedKey{}).(*EmbeddedFiles)
	}
	if t == nil {
		Trap("embedded file " + strconv.Quote(path) + ": this machine has no embedded files")
	}
	f, ok := t.file(handle)
	if !ok {
		Trap("embedded file " + strconv.Quote(path) + ": no such handle " + strconv.FormatInt(handle, 10))
	}
	return t, f
}
