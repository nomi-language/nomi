package vmrunner

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/nomi-language/nomi/internal/buildversion"
	"github.com/nomi-language/nomi/internal/ir"
)

// A compiled program (a .nomic file) is what `nomi build --no-vm` writes for a
// nomi-vm that does not embed it: a header, then the linked IR image
// (ir.EncodeImage). Go code calls it an image file.
// The same bytes are the stdin framing a runner reads when it is given no
// path, because the header carries the image's length:
//
//	offset  size  field
//	0       8     magic "NOMIC\x00\x00\x00"
//	8       4     IR image format version (ir.FormatVersion), little-endian u32
//	12      4     flags, little-endian u32; bit 0: the program crosses into
//	              std/compiler's hosts. Every other bit is zero.
//	16      2     n, the length of the writer's version, little-endian u16
//	18      n     the Nomi version that wrote it, UTF-8 (`nomi --version`'s token)
//	18+n    8     L, the image's length, little-endian u64
//	26+n    L     the image, which opens with "NOMIIR\x00": its code, then
//	              its files region (ir.ImageCodeLen)
//
// Nothing follows the image in a file. On stdin, what follows is the
// program's own input. A runner handed the file by path reads the code and
// leaves the files region in the file (OpenImageFile); one handed it on
// stdin reads the whole image, since stdin cannot be read again.

// ImageFileMagic opens an image file and a framed image on stdin.
const ImageFileMagic = "NOMIC\x00\x00\x00"

// FlagUsesCompiler marks an image whose program crosses into std/compiler's
// hosts, which only a runner built with them can answer.
const FlagUsesCompiler uint32 = 1 << 0

// maxImageLen bounds the length a header may claim, so a damaged header is
// reported rather than allocated.
const maxImageLen = 1 << 32

// ImageFile is an image file's header and its image.
type ImageFile struct {
	// Format is the IR image format version the image is encoded in.
	Format uint32
	// Flags are FlagUsesCompiler or zero.
	Flags uint32
	// Version is the Nomi version that wrote the file.
	Version string
	// Image is the linked IR image.
	Image []byte
}

// EncodeImageFile writes image, which ir.EncodeImage produced in this
// build's format, as an image file written by version.
func EncodeImageFile(image []byte, flags uint32, version string) []byte {
	return append(ImageFileHead(int64(len(image)), flags, version), image...)
}

// ImageFileHead is the header of an image file whose image is n bytes:
// written before the image, its code and then its files region, it makes
// the file EncodeImageFile makes, so a build can write the region from its
// sources without holding it.
func ImageFileHead(n int64, flags uint32, version string) []byte {
	if len(version) > 0xffff {
		version = version[:0xffff]
	}
	out := make([]byte, 0, 26+len(version))
	out = append(out, ImageFileMagic...)
	out = binary.LittleEndian.AppendUint32(out, ir.FormatVersion)
	out = binary.LittleEndian.AppendUint32(out, flags)
	out = binary.LittleEndian.AppendUint16(out, uint16(len(version)))
	out = append(out, version...)
	return binary.LittleEndian.AppendUint64(out, uint64(n))
}

// IsImageFile reports whether head, a file's first bytes, opens an image
// file.
func IsImageFile(head []byte) bool {
	return bytes.HasPrefix(head, []byte(ImageFileMagic))
}

// errNotImageFile is input that does not open with ImageFileMagic.
var errNotImageFile = errors.New("not a compiled Nomi program (.nomic)")

// ReadImageFile reads one image file from r, header and image, and nothing
// past the image's last byte: what follows on stdin is the program's input.
// name says where it came from in errors.
func ReadImageFile(r io.Reader, name string) (ImageFile, error) {
	f, n, err := readImageFileHead(r, name)
	if err != nil {
		return f, err
	}
	// Read through a limit rather than allocating n up front, so a damaged
	// length fails at the input's end, not in the allocator.
	image, err := io.ReadAll(io.LimitReader(r, int64(n)))
	if err != nil {
		return f, fmt.Errorf("reading %s: %w", name, err)
	}
	if uint64(len(image)) != n {
		return f, fmt.Errorf("%s is truncated: its header says %d program bytes and %d follow", name, n, len(image))
	}
	f.Image = image
	return f, nil
}

// readImageFileHead reads an image file's header from r, and answers it
// and the length of the image that follows.
func readImageFileHead(r io.Reader, name string) (ImageFile, uint64, error) {
	var f ImageFile
	head := make([]byte, 18)
	if n, err := io.ReadFull(r, head); err != nil {
		if n == 0 {
			return f, 0, fmt.Errorf("%s is empty: %w", name, errNotImageFile)
		}
		if !IsImageFile(head[:n]) {
			return f, 0, fmt.Errorf("%s: %w", name, errNotImageFile)
		}
		return f, 0, fmt.Errorf("%s ends inside its header", name)
	}
	if !IsImageFile(head) {
		return f, 0, fmt.Errorf("%s does not start with %q: %w (`nomi build --no-vm` writes one)", name, ImageFileMagic, errNotImageFile)
	}
	f.Format = binary.LittleEndian.Uint32(head[8:12])
	f.Flags = binary.LittleEndian.Uint32(head[12:16])
	rest := make([]byte, int(binary.LittleEndian.Uint16(head[16:18]))+8)
	if _, err := io.ReadFull(r, rest); err != nil {
		return f, 0, fmt.Errorf("%s ends inside its header", name)
	}
	f.Version = string(rest[:len(rest)-8])
	n := binary.LittleEndian.Uint64(rest[len(rest)-8:])
	if n > maxImageLen {
		return f, 0, fmt.Errorf("%s claims a program of %d bytes; its header is damaged", name, n)
	}
	return f, n, nil
}

// headLen is the length of the header readImageFileHead read for f.
func (f ImageFile) headLen() int64 { return 26 + int64(len(f.Version)) }

// OpenImageFile opens the image file at path as a runner holds its own
// image (OpenImage): it reads the header and the code, and keeps the file
// open for the files region. It answers why a runner with opts cannot run
// it (ImageFile's check). Close releases it.
func OpenImageFile(path string, opts ...Option) (*Image, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	f, n, err := readImageFileHead(file, path)
	if err == nil {
		err = f.check(path, cfg.compiler != nil)
	}
	var info os.FileInfo
	if err == nil {
		if info, err = file.Stat(); err == nil && info.Size() != f.headLen()+int64(n) {
			err = fmt.Errorf("%s is truncated: its header says %d program bytes and %d follow", path, n, info.Size()-f.headLen())
		}
	}
	var im *Image
	if err == nil {
		im, err = openImageFile(file, f.headLen(), int64(n))
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return im, nil
}

// check reports why this runner cannot run f: an image format other than the
// one it reads, flags it does not know, or a crossing into std/compiler when
// it does not link those hosts. name says where f came from.
func (f ImageFile) check(name string, haveCompiler bool) error {
	self := buildversion.Current()
	if f.Format != ir.FormatVersion {
		return fmt.Errorf("%s was written by nomi %s in program format %d, and this nomi-vm (nomi %s) reads "+
			"format %d. Rebuild it with `nomi build --no-vm` from this nomi-vm's nomi, or run it with nomi %s's nomi-vm",
			name, f.Version, f.Format, self, ir.FormatVersion, f.Version)
	}
	if f.Flags&^FlagUsesCompiler != 0 {
		return fmt.Errorf("%s was written by nomi %s with flags %#x, which this nomi-vm (nomi %s) does not know",
			name, f.Version, f.Flags, self)
	}
	if f.Flags&FlagUsesCompiler != 0 && !haveCompiler {
		return fmt.Errorf("%s imports std/compiler, whose hosts this nomi-vm does not link. Run it with the "+
			"nomi-vm `nomi build-vm --compiler` writes", name)
	}
	return nil
}
