package vmhost_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// writeCycleProject writes files, plus a manifest declaring main.nomi the
// entry, into a new directory, and answers the path of main.nomi.
func writeCycleProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["nomi.toml"] = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "main.nomi")
}

// cycleWrap and cycleTag import each other, and each declares a generic enum
// and a function returning the other's.
const (
	cycleWrap = `import b.Tag

pub enum Wrap<T> {
    Hit(T)
    Miss
}

pub fn tag_of(w: Wrap<Int>): Tag<Int> {
    case w {
        .Hit(n) -> Tag.Big(n)
        .Miss -> Tag.Small
    }
}
`
	cycleTag = `import a.Wrap

pub enum Tag<T> {
    Big(T)
    Small
}

pub fn wrap_of(t: Tag<Int>): Wrap<Int> {
    case t {
        .Big(n) -> Wrap.Hit(n)
        .Small -> Wrap.Miss
    }
}
`
)

// A function of a file in an import cycle runs when its signature names a
// generic type another file of the cycle declares, called bare or
// file-qualified.
func TestImportCycle_GenericSignaturesAcrossTheCycleRun(t *testing.T) {
	for _, tc := range []struct{ name, main string }{
		{"selective import", `import {
    std/io
    a.{Wrap, tag_of}
    b.{Tag, wrap_of}
}

fn main() {
    case tag_of(Wrap.Hit(4)) {
        .Big(n) -> io.print("big ${n}")
        .Small -> io.print("small")
    }
    case wrap_of(Tag.Small) {
        .Hit(n) -> io.print("hit ${n}")
        .Miss -> io.print("miss")
    }
}
`},
		{"file-qualified", `import {
    std/io
    a
    b
}

fn main() {
    case a.tag_of(a.Wrap.Hit(4)) {
        .Big(n) -> io.print("big ${n}")
        .Small -> io.print("small")
    }
    case b.wrap_of(b.Tag.Small) {
        .Hit(n) -> io.print("hit ${n}")
        .Miss -> io.print("miss")
    }
}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeCycleProject(t, map[string]string{"a.nomi": cycleWrap, "b.nomi": cycleTag, "main.nomi": tc.main})
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
			if want := "big 4\nmiss\n"; out.String() != want {
				t.Fatalf("output %q, want %q", out.String(), want)
			}
		})
	}
}

// A test file in an import cycle is one file: the cycle's import of it
// reaches the file being tested, so its tests and its main run.
func TestImportCycle_TestFileInTheCycleRuns(t *testing.T) {
	path := writeCycleProject(t, map[string]string{
		"main.nomi": `import std/io
import ring

pub fn base(n: Int): Int {
    n + 1
}

fn main() {
    io.inspect(ring.twice(2))
}

test "base" {
    assert base(1) == 2
}

test "twice" {
    assert ring.twice(2) == 6
}
`,
		"ring.nomi": `import main

pub fn twice(n: Int): Int {
    main.base(n) * 2
}
`,
	})
	if err := vmhost.Check(path); err != nil {
		t.Fatalf("check: %v", err)
	}
	prog, err := vmhost.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cases := prog.Cases(io.Discard, vmhost.TestOptions{})
	if len(cases) != 2 {
		t.Fatalf("%d cases, want 2", len(cases))
	}
	for _, c := range cases {
		if c.Blocked != nil || c.Err != nil {
			t.Errorf("case %q: blocked %v, err %v", c.Name, c.Blocked, c.Err)
		}
	}
	var out bytes.Buffer
	if err := prog.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "6\n"; out.String() != want {
		t.Fatalf("output %q, want %q", out.String(), want)
	}
}
