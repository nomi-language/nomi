package vmhost_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// implImportTypes declares a type and an interface; implImportImpls holds the
// one impl block for them, and names nothing a file importing it could use.
const (
	implImportTypes = "pub struct Thing {\n    n: Int\n}\n\npub interface Alpha {\n    fn go(x: self): Int\n}\n\npub fn thing(n: Int): Thing {\n    Thing{n}\n}\n"
	implImportImpls = "import types.{Alpha, Thing}\n\nimpl Alpha for Thing {\n    fn go(x: Thing): Int {\n        x.n\n    }\n}\n\nimpl Display for Thing {\n    fn to_string(x: Thing): String {\n        \"thing ${Int.to_string(x.n)}\"\n    }\n}\n"
)

func writeImplImportProgram(t *testing.T, main string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range map[string]string{
		"nomi.toml":  "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"types.nomi": implImportTypes,
		"impls.nomi": implImportImpls,
		"main.nomi":  main,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "main.nomi")
}

// An import that names nothing is used when the program uses an impl block the
// imported file declares, however the program reaches it: a type-qualified
// call, a call through a bound, or a demand an interpolation makes.
func TestImplImport_UsedForAnImplIsNotReported(t *testing.T) {
	for _, tc := range []struct {
		name, main, want string
	}{
		{
			name: "type-qualified call",
			main: "import {\n    std/io\n    impls\n    types.{self, Thing}\n}\n\n" +
				"fn main() {\n    io.print(Int.to_string(Thing.go(types.thing(3))))\n}\n",
			want: "3\n",
		},
		{
			name: "bound",
			main: "import {\n    std/io\n    impls\n    types.{self, Alpha}\n}\n\n" +
				"fn run<T>(x: T): Int where T: Alpha {\n    T.go(x)\n}\n\n" +
				"fn main() {\n    io.print(Int.to_string(run(types.thing(4))))\n}\n",
			want: "4\n",
		},
		{
			name: "interpolation",
			main: "import {\n    std/io\n    impls\n    types\n}\n\n" +
				"fn main() {\n    t = types.thing(5)\n    io.print(\"${t}\")\n}\n",
			want: "thing 5\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeImplImportProgram(t, tc.main)
			if err := vmhost.Check(path); err != nil {
				t.Fatalf("check: %v", err)
			}
			prog, err := vmhost.Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			var out bytes.Buffer
			if err := prog.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatalf("run: %v", err)
			}
			if out.String() != tc.want {
				t.Fatalf("output %q, want %q", out.String(), tc.want)
			}
		})
	}
}

// An import of a file whose impl blocks the program never uses is unused like
// any other.
func TestImplImport_UnusedImplsAreReported(t *testing.T) {
	main := "import {\n    std/io\n    impls\n    types\n}\n\nfn main() {\n    io.print(Int.to_string(types.thing(1).n))\n}\n"
	path := writeImplImportProgram(t, main)
	err := vmhost.Check(path)
	if err == nil {
		t.Fatal("an import whose impls nothing uses passed the check")
	}
	want := path + ":3:5: imported module 'impls' is unused — remove the import"
	if got := strings.TrimRight(err.Error(), "\n"); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
