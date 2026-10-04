package vmrunner_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
	"github.com/nomi-language/nomi/vmrunner"
)

// image lowers src as `nomi build` does and answers its image.
func image(t *testing.T, src string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := p.BuildImage()
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// executable writes runner bytes, image and trailer as `nomi build` does.
func executable(t *testing.T, runner, img []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app")
	data := append(append(append([]byte(nil), runner...), img...), vmrunner.Trailer(int64(len(runner)), int64(len(img)))...)
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRun_RunsTheImageAsNomiRunDoes(t *testing.T) {
	img := image(t, "import std/io\n\nfn main() {\n  io.print(\"hi\")\n  case io.read_line() {\n    Ok(l) -> io.print(l)\n    Err(e) -> io.print(e)\n  }\n}\n")
	var out, errOut bytes.Buffer
	code := vmrunner.Run(img, &out, &errOut, strings.NewReader("typed\n"), nil, false)
	if code != 0 || out.String() != "hi\ntyped\n" || errOut.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

// promptReader is stdin that records what stdout held when the program first
// read it: an `io.write` prompt must already be there.
type promptReader struct {
	out      *bytes.Buffer
	r        io.Reader
	seen     string
	readOnce bool
}

func (p *promptReader) Read(b []byte) (int, error) {
	if !p.readOnce {
		p.readOnce = true
		p.seen = p.out.String()
	}
	return p.r.Read(b)
}

func TestRun_WriteAddsNoNewlineAndPrecedesTheRead(t *testing.T) {
	img := image(t, `import std/io

fn main() {
  io.write("> ")
  case io.read_line() {
    Ok(l) -> io.print("got ${l}")
    Err(e) -> io.print(e)
  }
  io.write(1)
  io.write([2, 3])
  io.print("|")
  "piped" |> io.write()
  w: (Int) -> Unit = io.write
  w(4)
  io.print("")
}
`)
	var out, errOut bytes.Buffer
	in := &promptReader{out: &out, r: strings.NewReader("typed\n")}
	code := vmrunner.Run(img, &out, &errOut, in, nil, false)
	want := "> got typed\n1[2, 3]|\npiped4\n"
	if code != 0 || out.String() != want || errOut.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q; want stdout %q", code, out.String(), errOut.String(), want)
	}
	if in.seen != "> " {
		t.Fatalf("stdout held %q when read_line first read stdin; want the prompt %q", in.seen, "> ")
	}
}

func TestRun_WriteBySelectiveImport(t *testing.T) {
	img := image(t, "import std/io.write\nimport std/io.print\n\nfn main() {\n  write(\"a\")\n  write(2)\n  print(\"b\")\n}\n")
	var out, errOut bytes.Buffer
	code := vmrunner.Run(img, &out, &errOut, nil, nil, false)
	if code != 0 || out.String() != "a2b\n" || errOut.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

func TestRun_AFaultExitsOneWithItsText(t *testing.T) {
	img := image(t, "import std/io\n\nfn d(a: Int, b: Int): Int {\n  a / b\n}\n\nfn main() {\n  io.print(d(1, 0))\n}\n")
	var out, errOut bytes.Buffer
	if code := vmrunner.Run(img, &out, &errOut, nil, nil, false); code != 1 || !strings.Contains(errOut.String(), "division by zero") {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
}

func TestRun_ADamagedImageIsAnErrorNotAPanic(t *testing.T) {
	img := image(t, "fn main() {\n  Unit\n}\n")
	img[len(img)-1] ^= 0xff
	var errOut bytes.Buffer
	if code := vmrunner.Run(img, &bytes.Buffer{}, &errOut, nil, nil, false); code != 1 ||
		!strings.Contains(errOut.String(), "the appended program cannot be read") {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
}

func TestReadImage_ReadsWhatWasAppended(t *testing.T) {
	img := image(t, "fn main() {\n  Unit\n}\n")
	path := executable(t, []byte("not really a runner"), img)
	got, err := vmrunner.ReadImage(path)
	if err != nil || !bytes.Equal(got, img) {
		t.Fatalf("read %d bytes of %d, err %v", len(got), len(img), err)
	}
}

func TestReadImage_TheBareRunnerHasNoImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 100), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := vmrunner.ReadImage(path); !errors.Is(err, vmrunner.ErrNoImage) {
		t.Fatalf("got %v, want ErrNoImage", err)
	}
}

func TestReadImage_ADamagedTrailerIsAnError(t *testing.T) {
	img := image(t, "fn main() {\n  Unit\n}\n")
	// A trailer whose length does not reach the image's start.
	data := append(append([]byte("runner"), img...), vmrunner.Trailer(0, int64(len(img)))...)
	path := filepath.Join(t.TempDir(), "app")
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := vmrunner.ReadImage(path); err == nil || !strings.Contains(err.Error(), "trailer is damaged") {
		t.Fatalf("got %v", err)
	}
}
