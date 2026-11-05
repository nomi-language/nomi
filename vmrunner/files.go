package vmrunner

import (
	"errors"
	"fmt"
	"io"
	"os"
	"unsafe"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// An image is its code, then its files region: the contents of the
// program's embedded files (ir.Image.Files). A runner decodes the code when
// the program starts and reads a file's contents when the program reads the
// file, so starting reads the same bytes however much is embedded, and no
// embedded file is held in memory unless its holder already was.
//
// Where the region is decides how a file is read:
//
//   - in a file (a native executable, a .nomic file named by path): the
//     runner keeps the file open and maps the region read-only where the
//     platform can (mmap_*.go). A read answers a view of the mapping, which
//     costs no copy and which the operating system pages in from the file
//     and drops again under pressure; Bytes are immutable, and the mapping
//     lasts as long as the process, so a view is safe while nobody rewrites
//     the file in place (`nomi build` replaces an output by renaming a new
//     file over it, which leaves a running program's file as it was). Where
//     it cannot map, each read is a ReadAt of the file's range.
//   - in memory already (a wasip1 module's own memory, a .nomic on stdin or
//     handed to a reactor): a read answers a view of those bytes, or a copy
//     when the caller still owns them (Run, Start, Boot).
//   - in a page's JavaScript memory (a js/wasm module's custom section): the
//     runner copies the code into Go once, and each read copies that file's
//     range (image_js.go), so the region is never in Go's heap whole.
//
// Nothing read is kept: the program's value holds the Bytes as long as it
// uses them.

// Image is a built program as a runner holds it: its code, and where its
// embedded files' contents are.
type Image struct {
	code  []byte
	files region
	close func() error
}

// Close releases what the image holds open: the file its files region is
// read from, and the mapping of it. Bytes the program read from a mapped
// region must not be used after.
func (im *Image) Close() error {
	if im == nil || im.close == nil {
		return nil
	}
	c := im.close
	im.close = nil
	return c()
}

// region is an image's files region.
type region interface {
	// read answers n bytes at off, from the region's start.
	read(off, n int64) (rt.Bytes, error)
	size() int64
}

// memRegion is a files region held in memory. A view answers its bytes
// without copying them, which only an owner that never changes them may
// ask for.
type memRegion struct {
	b    []byte
	view bool
}

func (r memRegion) size() int64 { return int64(len(r.b)) }

func (r memRegion) read(off, n int64) (rt.Bytes, error) {
	if off < 0 || n < 0 || off+n > int64(len(r.b)) {
		return "", fmt.Errorf("bytes %d to %d are outside the program's %d bytes of files", off, off+n, len(r.b))
	}
	if n == 0 {
		return "", nil
	}
	if r.view {
		return rt.Bytes(unsafe.String(&r.b[off], int(n))), nil
	}
	return rt.Bytes(r.b[off : off+n]), nil
}

// readerRegion is a files region read through ReadAt: n bytes at base.
type readerRegion struct {
	r    io.ReaderAt
	base int64
	n    int64
}

func (r readerRegion) size() int64 { return r.n }

func (r readerRegion) read(off, n int64) (rt.Bytes, error) {
	if off < 0 || n < 0 || off+n > r.n {
		return "", fmt.Errorf("bytes %d to %d are outside the program's %d bytes of files", off, off+n, r.n)
	}
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, n)
	if _, err := r.r.ReadAt(buf, r.base+off); err != nil {
		return "", fmt.Errorf("reading the program's files: %w", err)
	}
	// buf is this read's alone, so the Bytes share it.
	return rt.Bytes(unsafe.String(&buf[0], len(buf))), nil
}

// imageFromBytes is an image held whole in memory. view says the caller
// will never change b, so a read may answer a view of it.
func imageFromBytes(b []byte, view bool) (*Image, error) {
	code, files, err := ir.SplitImage(b)
	if err != nil {
		return nil, err
	}
	return &Image{code: code, files: memRegion{b: files, view: view}}, nil
}

// OpenImageAt reads the code of the image of n bytes at off in r, and
// answers the image, whose files region is read through r.ReadAt when the
// program reads a file. It reads the image's header and code and nothing
// past them.
func OpenImageAt(r io.ReaderAt, off, n int64) (*Image, error) {
	head := make([]byte, min(n, int64(ir.ImageHeaderMax)))
	if _, err := r.ReadAt(head, off); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	codeLen, ok := ir.ImageCodeLen(head)
	if !ok {
		return nil, errors.New("the compiled program cannot be read: its header is damaged")
	}
	if codeLen > n {
		return nil, fmt.Errorf("the compiled program is truncated: its code is %d bytes and %d are there", codeLen, n)
	}
	code := make([]byte, codeLen)
	k := copy(code, head)
	if k < len(code) {
		if _, err := r.ReadAt(code[k:], off+int64(k)); err != nil {
			return nil, err
		}
	}
	return &Image{code: code, files: readerRegion{r: r, base: off + codeLen, n: n - codeLen}}, nil
}

// openImageFile is OpenImageAt over f, with the files region mapped where
// the platform can. The image keeps f open until Close.
func openImageFile(f *os.File, off, n int64) (*Image, error) {
	im, err := OpenImageAt(f, off, n)
	if err != nil {
		return nil, err
	}
	im.close = f.Close
	files := im.files.(readerRegion)
	if files.n == 0 {
		return im, nil
	}
	data, unmap, err := mapRange(f, files.base, files.n)
	if err != nil {
		// A region the platform cannot map is read a range at a time.
		return im, nil
	}
	im.files = memRegion{b: data, view: true}
	im.close = func() error {
		err := unmap()
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	}
	return im, nil
}

// imageEmbedded reads a machine's embedded files from an image's files
// region, by each file's sum.
type imageEmbedded struct {
	at    map[string]ir.FileEntry
	files region
}

func newImageEmbedded(entries []ir.FileEntry, files region) imageEmbedded {
	at := make(map[string]ir.FileEntry, len(entries))
	for _, e := range entries {
		at[e.Sum] = e
	}
	return imageEmbedded{at: at, files: files}
}

func (s imageEmbedded) ReadEmbedded(f rt.EmbeddedFile) (rt.Bytes, error) {
	e, ok := s.at[f.Sum]
	if !ok || e.Size != f.Size {
		return "", errors.New("the compiled program does not hold it")
	}
	if s.files == nil || s.files.size() < e.Offset+e.Size {
		return "", errors.New("the compiled program's files are missing or truncated")
	}
	return s.files.read(e.Offset, e.Size)
}
