package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// iterSensitiveLib is a file whose `pub` functions use `continue` and
// `break`.
const iterSensitiveLib = `// FILE: lib.nomi
pub fn keep(n: Int) -> Int {
  if n < 0 { continue }
  if n > 100 { break n }
  n * 2
}

pub fn add(acc: Int, n: Int) -> Int {
  if n < 0 { continue }
  acc + n
}
`

// loadIterSensitiveProject loads lib.nomi, an entry and, when other is not
// empty, other.nomi, which the entry imports.
func loadIterSensitiveProject(t *testing.T, entry, other string) (*vmhost.Program, error) {
	t.Helper()
	src := iterSensitiveLib
	if other != "" {
		src += "\n// FILE: other.nomi\n" + other
	}
	src += "\n// FILE: main.nomi\n" + entry
	entrySrc, entryName, files, manifest, err := vmhost.SplitMultiFile(src)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	var out bytes.Buffer
	return vmhost.LoadSource(entryName, entrySrc,
		vmhost.WithVirtualFiles(files),
		vmhost.WithVirtualManifest(manifest),
		vmhost.WithOutput(&out),
	)
}

// Another file's iter-sensitive function may be `pub`, and is used from the
// entry as the callback itself, a fold's with its starting value as `from:`.
func TestIterSensitive_AnotherFilesFunctionAsACallback(t *testing.T) {
	p, err := loadIterSensitiveProject(t, `import {
  std/io
  lib
}

fn main() {
  io.inspect(Iter.map([3, -1, 4, 500, 6], lib.keep) |> Iter.to_list())
  io.inspect(Iter.reduce([1, -5, 2], lib.add, from: 0))
}
`, "")
	if err != nil {
		t.Fatalf("the project does not load: %v", err)
	}
	if u := p.Unsupported(); u != nil {
		t.Fatalf("the project does not lower: %v", u)
	}
}

// A direct call to another file's iter-sensitive function is rejected at
// the call, in the entry and in a file the entry imports, as a call to one
// of the file's own is.
func TestIterSensitive_AnotherFilesFunctionCalledDirectlyIsRejected(t *testing.T) {
	const want = "`lib.keep` uses `break` and `continue`, so it cannot be called: pass it as an iteration callback itself"
	for _, c := range []struct{ name, entry, other, file string }{
		{"as the whole body of a callback lambda", `import {
  std/io
  lib
}

fn main() {
  io.inspect(Iter.map([1, 2], fn x -> lib.keep(x)) |> Iter.to_list())
}
`, "", "main.nomi:7:"},
		{"from the entry", `import {
  std/io
  lib
}

fn main() {
  io.inspect(Iter.map([1, 2], fn x -> lib.keep(x) + 1) |> Iter.to_list())
}
`, "", "main.nomi:7:"},
		{"from a file the entry imports", `import {
  std/io
  other
}

fn main() {
  io.inspect(other.twice(1))
}
`, `import lib

pub fn twice(n: Int) -> Int {
  lib.keep(n) * 2
}
`, "other.nomi:4:"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadIterSensitiveProject(t, c.entry, c.other)
			if err == nil {
				t.Fatal("the checker admits a direct call to another file's iter-sensitive function")
			}
			if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), c.file) {
				t.Fatalf("rejected for some other reason; want %q at %s, got: %v", want, c.file, err)
			}
		})
	}
}
