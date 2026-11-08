package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// defaultArgConfig is the type the defaults below name, declared in a file
// of its own so the declaring file names it through an import.
const defaultArgConfig = `pub struct Config {
    name: String
}
`

// defaultArgDecls declares structs (one with a parameter before the
// defaulted one), an enum, a distinct type, an opaque type and an interface
// whose type parameter defaults to Config, which this file imports and the
// files using the bare forms may not.
const defaultArgDecls = `import config.Config

pub struct Box<T = Config> {
    item: T
}

pub struct Pair<A, B = Config> {
    a: A
    b: B
}

pub enum Slot<T = Config> {
    Full T
    Empty
}

pub type Tag<T = Config> T

pub opaque type Handle<T = Config> T

pub interface Source<T = Config> {
    fn get(s: self) -> T
}

pub struct Fixed {
    n: Int
}

impl Source for Fixed {
    fn get(s: Fixed) -> Config {
        Config{name: "fixed ${s.n}"}
    }
}

pub fn handle(c: Config) -> Handle {
    Handle(c)
}

pub fn opened(h: Handle) -> String {
    Handle(c) = h
    c.name
}

pub fn make(name: String) -> Box {
    Box{item: Config{name: name}}
}

pub fn full(name: String) -> Slot {
    .Full(Config{name: name})
}

pub fn tag(name: String) -> Tag {
    Tag(Config{name: name})
}

pub fn pair(n: Int, name: String) -> Pair<Int> {
    Pair{a: n, b: Config{name: name}}
}

pub fn fixed(n: Int) -> Fixed {
    Fixed{n: n}
}
`

// defaultArgUses names each declaration bare, so each annotation means the
// declaration at Config, whatever this file binds the name Config to.
const defaultArgUses = `fn show(b: Box) -> String {
    b.item.name
}

fn paired(p: Pair<Int>) -> String {
    "${p.a} ${p.b.name}"
}

fn slot(s: Slot) -> String {
    case s {
        .Full(c) -> c.name
        .Empty -> "empty"
    }
}

fn tagged(t: Tag) -> String {
    Tag(c) = t
    c.name
}

fn held(h: Handle) -> String {
    opened(h)
}

fn read(s: Source) -> String {
    Source.get(s).name
}

fn main() {
    io.print(show(make("box")))
    io.print(paired(pair(2, "pair")))
    io.print(slot(full("slot")))
    io.print(slot(.Empty))
    io.print(tagged(tag("tag")))
    io.print(held(defaults.handle(defaults.make("handle").item)))
    io.print(read(fixed(7)))
    sources: List<Source> = [fixed(1), fixed(2)]
    io.inspect(sources |> Iter.map(read) |> Iter.to_list())
}
`

const defaultArgWant = "box\n2 pair\nslot\nempty\ntag\nhandle\nfixed 7\n" + `["fixed 1", "fixed 2"]` + "\n"

const defaultArgImports = "import {\n    std::io\n    defaults.{self, Box, Pair, Slot, Tag, Handle, Source, opened, make, pair, full, tag, fixed}\n"

// A default type argument names what it names where the declaration wrote
// it: `Box` in main.nomi is `Box<Config>` for defaults.nomi's Config, which
// main.nomi need not import, and which a Config of main.nomi's own does not
// replace. The builder used to write the default's syntax into the using
// file and resolve it there, so `fn show(b: Box)` declined at run time
// with "not supported yet" while `nomi check` accepted it.
func TestDefaultTypeArgs_ResolveInTheDeclaringFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	files := map[string]string{
		"config.nomi":   defaultArgConfig,
		"defaults.nomi": defaultArgDecls,
	}
	cases := []struct {
		name, main string
	}{
		{"the using file does not import the default", defaultArgImports + "}\n\n" + defaultArgUses},
		{"the using file imports its module only", defaultArgImports + "    config\n}\n\n" +
			strings.Replace(defaultArgUses, `defaults.make("handle").item`, `config.Config{name: "handle"}`, 1)},
		{"the using file declares its own Config", defaultArgImports + "}\n\nstruct Config {\n    id: Int\n}\n\n" + defaultArgUses},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			requireFrontEndAccepts(t, c.main, files)
			verifyLambdaProgram(t, c.main, defaultArgWant, files)
		})
	}
}

// requireFrontEndAccepts fails the test when the front end rejects the
// program, so a test about what the builder lowers cannot pass on a program
// the checker stopped admitting.
func requireFrontEndAccepts(t *testing.T, src string, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(path); err != nil {
		t.Fatalf("the front end rejects this, so the test no longer covers the builder: %v", err)
	}
}
