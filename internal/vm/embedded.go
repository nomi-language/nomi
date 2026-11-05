package vm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nomi-language/nomi/rt"
)

// The embedded files a program names (ir.ConstEmbedded) are entered in the
// machine's table when its `embed` runs, and read through their source when
// the program reads one (rt.EmbeddedRead). A machine reads them from disk
// unless its host says otherwise (WithEmbeddedSource): a built program
// reads them from its image, and an in-memory program from its data files.

// WithEmbeddedSource makes src where the machine reads its embedded files'
// contents. Without it they are read from disk (DiskEmbedded), as `nomi
// run` reads them.
func (m *Machine) WithEmbeddedSource(src rt.EmbeddedSource) *Machine {
	m.embedded = rt.NewEmbeddedFiles(src)
	return m
}

// DiskEmbedded reads an embedded file from its source on disk, each time
// the program reads it, and answers it only when it is as the program was
// compiled with it: the same size and SHA-256. A file changed, replaced or
// removed since is an error naming what changed, never other contents
// served as the file's.
type DiskEmbedded struct{}

func (DiskEmbedded) ReadEmbedded(f rt.EmbeddedFile) (rt.Bytes, error) {
	file, err := os.Open(f.Source)
	if err != nil {
		return "", fmt.Errorf("%v; it was removed or moved after the program was compiled. Run the program again", err)
	}
	defer file.Close()
	var b strings.Builder
	b.Grow(int(f.Size))
	h := sha256.New()
	// One byte more than the file had, so a file that grew is caught
	// without reading what it grew by.
	n, err := io.Copy(io.MultiWriter(&b, h), io.LimitReader(file, f.Size+1))
	if err != nil {
		return "", err
	}
	if n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.Sum {
		return "", fmt.Errorf("%s changed after the program was compiled (%s). "+
			"An embedded file's contents are the ones the program was compiled with: run the program again",
			f.Source, howChanged(n, f.Size))
	}
	return rt.Bytes(b.String()), nil
}

// howChanged says how a file read up to one byte past its old size, n
// bytes, differs from the was bytes it had.
func howChanged(n, was int64) string {
	switch {
	case n > was:
		return fmt.Sprintf("it had %d bytes and has more now", was)
	case n < was:
		return fmt.Sprintf("it had %d bytes and has %d now", was, n)
	}
	return fmt.Sprintf("its %d bytes are not the ones it had", n)
}
