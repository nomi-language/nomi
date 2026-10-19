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

// loadInEveryFileOrder writes files, whose names and texts call the file
// holding the declarations under test "%s", once as a.nomi and once as
// z.nomi, so the front end types it before the files that read it (user
// files are typed in the order of their module keys) and after them. Each
// time the front end must accept main.nomi, and running main must print
// want and end without an error.
func loadInEveryFileOrder(t *testing.T, files map[string]string, want string) {
	t.Helper()
	for _, name := range []string{"a", "z"} {
		dir := t.TempDir()
		for file, text := range files {
			path := filepath.Join(dir, strings.ReplaceAll(file, "%s", name))
			if err := os.WriteFile(path, []byte(strings.ReplaceAll(text, "%s", name)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		p, err := vmhost.Load(filepath.Join(dir, "main.nomi"))
		if err != nil {
			t.Errorf("declared in %s.nomi: the front end rejects the program: %v", name, err)
			continue
		}
		var out bytes.Buffer
		if err := p.Run(context.Background(), &out, nil, false); err != nil {
			t.Errorf("declared in %s.nomi: run: %v\noutput so far:\n%s", name, err, out.String())
			continue
		}
		if got := out.String(); got != want {
			t.Errorf("declared in %s.nomi: output %q, want %q", name, got, want)
		}
	}
}

// An impl in a file typed before its interface's file read the interface
// before its type parameters were built: `impl Holds<Int> for Box` was
// "Holds expects 0 type argument(s), got 1". internal/analysis's
// TestDeclarationOrder_ImplHeaderOfAnInterfaceFromAnotherFile checks the
// templates it records.
func TestFileOrder_ImplHeaderOfAnInterfaceFromAnotherFile(t *testing.T) {
	loadInEveryFileOrder(t, map[string]string{
		"%s.nomi": `pub interface Holds<T> {
    fn held(value: self) -> T
}
`,
		"impls.nomi": `import %s.Holds

pub struct Box {
    n: Int
}

impl Holds<Int> for Box {
    fn held(value: Box) -> Int {
        value.n
    }
}

pub struct Bag {
    s: String
}

impl Holds for Bag {
    fn held(value: Bag) -> String {
        value.s
    }
}
`,
		"main.nomi": `import {
    std/io
    impls.{Box, Bag}
    %s.Holds
}

fn first<H>(h: H) -> Int where H: Holds<Int> {
    H.held(h)
}

fn second<H>(h: H) -> String where H: Holds<String> {
    H.held(h)
}

fn main() {
    io.print("${first(#Box{n: 2})} ${second(#Bag{s: "x"})}")
}
`,
	}, "2 x\n")
}

// An impl over a generic struct declared in a file typed after the impl's
// read the struct before its type parameters were built, and its
// `fn name(value: Wrap<T>)` was "unknown type "T"".
func TestFileOrder_ImplOverAGenericStructFromAnotherFile(t *testing.T) {
	loadInEveryFileOrder(t, map[string]string{
		"%s.nomi": `pub struct Wrap<T> {
    item: T
    count: Int = 1
}

pub struct Shown<T> where T: Display {
    item: T
}
`,
		"impls.nomi": `import %s.{Wrap, Shown}

pub interface Greets {
    fn name(value: self) -> String
}

impl Greets for Wrap<T> {
    fn name(value: Wrap<T>) -> String {
        "wrap ${value.count}"
    }
}

impl Greets for Shown<T> {
    fn name(value: Shown<T>) -> String {
        "shown ${value.item}"
    }
}
`,
		"main.nomi": `import {
    std/io
    impls.Greets
    %s.{Wrap, Shown}
}

fn main() {
    io.print(Greets.name(#Wrap{item: 1.5}))
    io.print(Greets.name(#Shown{item: 7}))
}
`,
	}, "wrap 1\nshown 7\n")
}

// The same impls above the generic structs they are for, in one file. The
// impls were typed before the structs were built.
func TestFileOrder_ImplAboveTheGenericStructItIsFor(t *testing.T) {
	got := runSourceOutput(t, `import std/io

interface Greets {
    fn name(value: self) -> String
}

impl Greets for Wrap<T> {
    fn name(value: Wrap<T>) -> String {
        "wrap ${value.count}"
    }
}

impl Greets for Shown<T> {
    fn name(value: Shown<T>) -> String {
        "shown ${value.item}"
    }
}

struct Wrap<T> {
    item: T
    count: Int = 1
}

struct Shown<T> where T: Display {
    item: T
}

fn main() {
    io.print(Greets.name(#Wrap{item: 1.5}))
    io.print(Greets.name(#Shown{item: 7}))
}
`)
	if want := "wrap 1\nshown 7\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
