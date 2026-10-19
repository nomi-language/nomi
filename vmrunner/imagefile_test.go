package vmrunner_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/buildversion"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/vmrunner"
)

const echoProgram = `import std/io

struct App {
  context: Context
}

fn boot(startup: Startup) -> App {
  io.print("args: ${startup.args}")
  #App{context: Context.root()}
}

fn main() {
  case io.read_line() {
    Ok(line) -> io.print("read: ${line}")
    Err(e) -> io.print("no line: ${e}")
  }
}
`

func writeImageFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.nomic")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImageFile_HeaderLayout(t *testing.T) {
	img := image(t, "fn main() {\n  Unit\n}\n")
	data := vmrunner.EncodeImageFile(img, vmrunner.FlagUsesCompiler, "v1.2.3")
	want := []byte("NOMIC\x00\x00\x00")
	want = binary.LittleEndian.AppendUint32(want, ir.FormatVersion)
	want = binary.LittleEndian.AppendUint32(want, 1)
	want = binary.LittleEndian.AppendUint16(want, 6)
	want = append(want, "v1.2.3"...)
	want = binary.LittleEndian.AppendUint64(want, uint64(len(img)))
	if !bytes.Equal(data[:len(want)], want) || !bytes.Equal(data[len(want):], img) {
		t.Fatalf("header % x, want % x", data[:len(want)], want)
	}
	f, err := vmrunner.ReadImageFile(bytes.NewReader(data), "app.nomic")
	if err != nil || f.Format != ir.FormatVersion || f.Flags != 1 || f.Version != "v1.2.3" || !bytes.Equal(f.Image, img) {
		t.Fatalf("read %+v, %v", f, err)
	}
}

func TestRunBare_RunsAnImageFileByPath(t *testing.T) {
	path := writeImageFile(t, vmrunner.EncodeImageFile(image(t, echoProgram), 0, "test"))
	var out, errOut bytes.Buffer
	code := vmrunner.RunBare([]string{path, "a", "b c"}, &out, &errOut, strings.NewReader("typed\n"), false, vmrunner.ErrNoImage)
	if code != 0 || out.String() != "args: [a, b c]\nread: typed\n" || errOut.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

// A framed image on stdin is read to its last byte and no further: the rest
// of stdin is the program's input, whether or not the runner was given -.
func TestRunBare_ReadsAFramedImageFromStdinAndLeavesTheRest(t *testing.T) {
	data := vmrunner.EncodeImageFile(image(t, echoProgram), 0, "test")
	for _, args := range [][]string{{"-", "x"}, nil} {
		stdin := io.MultiReader(bytes.NewReader(data), strings.NewReader("after the image\n"))
		var out, errOut bytes.Buffer
		code := vmrunner.RunBare(args, &out, &errOut, stdin, false, vmrunner.ErrNoImage)
		wantArgs := "[]"
		if len(args) > 0 {
			wantArgs = "[x]"
		}
		want := "args: " + wantArgs + "\nread: after the image\n"
		if code != 0 || out.String() != want || errOut.Len() != 0 {
			t.Fatalf("args %q: exit %d, stdout %q, stderr %q; want %q", args, code, out.String(), errOut.String(), want)
		}
	}
}

func TestReadImageFile_ReadsNothingPastTheImage(t *testing.T) {
	data := vmrunner.EncodeImageFile(image(t, "fn main() {\n  Unit\n}\n"), 0, "test")
	r := bytes.NewReader(append(append([]byte(nil), data...), "rest"...))
	if _, err := vmrunner.ReadImageFile(r, "stdin"); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(r)
	if string(rest) != "rest" {
		t.Fatalf("left %q on stdin, want %q", rest, "rest")
	}
}

// An image in another format is refused before it is decoded, naming the
// version that wrote it and this runner's own.
func TestRunBare_RefusesAnotherImageFormatNamingBothVersions(t *testing.T) {
	data := vmrunner.EncodeImageFile(image(t, "fn main() {\n  Unit\n}\n"), 0, "v9.9.9")
	binary.LittleEndian.PutUint32(data[8:12], ir.FormatVersion+1)
	path := writeImageFile(t, data)
	var errOut bytes.Buffer
	code := vmrunner.RunBare([]string{path}, &bytes.Buffer{}, &errOut, nil, false, vmrunner.ErrNoImage)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr %q", code, errOut.String())
	}
	for _, want := range []string{
		"written by nomi v9.9.9 in program format " + fmt.Sprint(ir.FormatVersion+1),
		"this nomi-vm (nomi " + buildversion.Current() + ") reads format " + fmt.Sprint(ir.FormatVersion),
	} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr %q does not contain %q", errOut.String(), want)
		}
	}
}

func TestRunBare_AnImageUsingStdCompilerNeedsItsRunner(t *testing.T) {
	path := writeImageFile(t, vmrunner.EncodeImageFile(image(t, "fn main() {\n  Unit\n}\n"), vmrunner.FlagUsesCompiler, "test"))
	var errOut bytes.Buffer
	code := vmrunner.RunBare([]string{path}, &bytes.Buffer{}, &errOut, nil, false, vmrunner.ErrNoImage)
	if code != 1 || !strings.Contains(errOut.String(), "imports std/compiler") ||
		!strings.Contains(errOut.String(), "nomi build-vm --compiler") {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
}

func TestRunBare_RefusesWhatIsNotAnImageFile(t *testing.T) {
	data := vmrunner.EncodeImageFile(image(t, "fn main() {\n  Unit\n}\n"), 0, "test")
	cases := []struct {
		name  string
		stdin []byte
		want  string
	}{
		{"empty", nil, "stdin is empty"},
		{"not an image", []byte("hello there, runner\n"), "does not start with"},
		{"inside the header", data[:20], "ends inside its header"},
		{"truncated", data[:len(data)-3], "is truncated"},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		code := vmrunner.RunBare(nil, &out, &errOut, bytes.NewReader(c.stdin), false, vmrunner.ErrNoImage)
		if code != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), c.want) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %q", c.name, code, out.String(), errOut.String(), c.want)
		}
	}
}
