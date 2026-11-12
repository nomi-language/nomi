package vmrunner_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/vmhost"
	"github.com/nomi-language/nomi/vmrunner"
)

// embedsProgram embeds a small text file and a big binary one. It prints
// the big file's size, which is metadata and reads none of it, then the
// small file.
const embedsProgram = `import
  std::embed.Files
  std::io
end

embed site ` + "`small.txt big.bin`" + `

fn main()
  io.print(Files.metadata(site, "big.bin")!.size)
  io.print(Files.read_text(site, "small.txt")!)
end
`

const smallText = "hello from a small file\n"

// builtProgram builds src, which embeds small.txt and big.bin, its big file bigSize bytes, as `nomi
// build` writes an executable: runner bytes, the image's code, its files
// region from the files on disk, and the trailer. It answers the
// executable's path, where the image starts and how long it is, and how
// long its code is.
func builtProgram(t *testing.T, src string, bigSize int) (path string, off, n, codeLen int64, p *vmhost.Program) {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("nomi.toml", []byte("[module]\nname = \"app\"\nentry_points = [\"main\"]\n"))
	write("main.nomi", []byte(src))
	write("small.txt", []byte(smallText))
	big := make([]byte, bigSize)
	for i := range big {
		big[i] = byte(i*7 + i/251)
	}
	write("big.bin", big)
	p, err := vmhost.Load(filepath.Join(dir, "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := p.BuildImage()
	if err != nil {
		t.Fatal(err)
	}
	sources, err := p.EmbeddedSources(img)
	if err != nil {
		t.Fatal(err)
	}
	runner := []byte("a runner's bytes, of an odd length")
	var data bytes.Buffer
	data.Write(runner)
	data.Write(img)
	if err := vmhost.WriteFilesRegion(&data, img, sources); err != nil {
		t.Fatal(err)
	}
	n = int64(data.Len() - len(runner))
	data.Write(vmrunner.Trailer(int64(len(runner)), n))
	path = filepath.Join(t.TempDir(), "app")
	if err := os.WriteFile(path, data.Bytes(), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, int64(len(runner)), n, int64(len(img)), p
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.ReaderAt
	n atomic.Int64
}

func (c *countingReader) ReadAt(p []byte, off int64) (int, error) {
	k, err := c.r.ReadAt(p, off)
	c.n.Add(int64(k))
	return k, err
}

// openCounting opens the image of the executable at path through a
// counting reader, as a runner opens its own (without the mapping, so
// every byte read is counted), and answers how many bytes opening read.
func openCounting(t *testing.T, path string, off, n int64) (*vmrunner.Image, *countingReader) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	c := &countingReader{r: f}
	im, err := vmrunner.OpenImageAt(c, off, n)
	if err != nil {
		t.Fatal(err)
	}
	return im, c
}

// A built program reads its code when it starts and an embedded file when
// it reads that file: starting reads the same bytes whether the program
// embeds 1 KiB or 64 MiB, and a run that reads the small file reads its
// bytes and none of the big one's. Counted through the reader the image is
// opened over, not timed.
func TestOpenImageAt_StartingReadsTheCodeAndAFileOnlyWhenRead(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 64 MiB file; -short")
	}
	const big = 64 << 20
	smallPath, smallOff, smallN, smallCode, _ := builtProgram(t, embedsProgram, 1<<10)
	bigPath, bigOff, bigN, bigCode, _ := builtProgram(t, embedsProgram, big)
	if bigN-bigCode < big {
		t.Fatalf("the image's files region is %d bytes, less than the %d embedded", bigN-bigCode, big)
	}

	_, cs := openCounting(t, smallPath, smallOff, smallN)
	im, cb := openCounting(t, bigPath, bigOff, bigN)
	startSmall, startBig := cs.n.Load(), cb.n.Load()
	t.Logf("starting reads %d bytes embedding 1 KiB and %d embedding 64 MiB; the whole image is %d bytes",
		startSmall, startBig, bigN)
	if startBig != bigCode || startSmall != smallCode {
		t.Fatalf("starting read %d and %d bytes, want the code's %d and %d", startSmall, startBig, smallCode, bigCode)
	}
	// The two programs' code differs only in the big file's size and sum
	// as the code writes them.
	if d := startBig - startSmall; d < 0 || d > 8 {
		t.Fatalf("starting read %d bytes more for the big embed", d)
	}

	var out, errOut bytes.Buffer
	if code := vmrunner.RunImage(im, &out, &errOut, nil, nil, false); code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut.String())
	}
	if want := "67108864\n" + smallText + "\n"; out.String() != want {
		t.Fatalf("stdout %q, want %q", out.String(), want)
	}
	if read := cb.n.Load() - startBig; read != int64(len(smallText)) {
		t.Fatalf("the run read %d bytes of files, want the small file's %d", read, len(smallText))
	}
}

// readsBigProgram reads the big file whole and prints its length and the
// bytes at its ends and around the first page boundaries.
const readsBigProgram = `import
  std::embed.Files
  std::io
end

embed site ` + "`small.txt big.bin`" + `

fn main()
  data = Files.read(site, "big.bin")!
  io.print(Bytes.length(data))
  io.print([0, 4095, 4096, 16383, 16384, 65536, Bytes.length(data) - 1] |> Iter.map(fn i -> Bytes.get(data, i)) |> Iter.to_list())
  io.print(Files.read_text(site, "small.txt")!)
end
`

// A runner holding its own image maps the files region where it can, and
// a read answers the file's bytes from it: the big file, which starts at
// an offset no page boundary falls on, reads back as `nomi run` reads it
// from disk.
func TestOpenImage_ReadsAFileFromTheMappedRegion(t *testing.T) {
	path, _, _, _, p := builtProgram(t, readsBigProgram, 3<<20+17)
	im, err := vmrunner.OpenImage(path)
	if err != nil {
		t.Fatal(err)
	}
	defer im.Close()
	var out, errOut bytes.Buffer
	if code := vmrunner.RunImage(im, &out, &errOut, nil, nil, false); code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut.String())
	}
	var want bytes.Buffer
	if err := p.Run(context.Background(), &want, nil, false); err != nil {
		t.Fatal(err)
	}
	if out.String() != want.String() || !strings.HasPrefix(out.String(), "3145745\n") {
		t.Fatalf("the built program printed\n%s\nand nomi run printed\n%s", out.String(), want.String())
	}
	// The whole image, read as a test reads it, holds the code and the
	// region the mapping read.
	img, err := vmrunner.ReadImage(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, files, err := ir.SplitImage(img); err != nil || len(files) != 3<<20+17+len(smallText) {
		t.Fatalf("files region of %d bytes, %v", len(files), err)
	}
}
