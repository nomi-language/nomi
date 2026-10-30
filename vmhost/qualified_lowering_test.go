package vmhost

import (
	"bytes"
	"context"
	"testing"
)

// A std interface named through its module's qualifier (`sql.Conn`,
// `display.Display`) is an existential over it, as the bare name is, in a
// parameter and a result.
func TestLowering_StdInterfaceThroughItsModulesQualifier(t *testing.T) {
	const program = `// FILE: main.nomi
import {
    std/display
    std/io
    std/sql
    std/sql_testing.Fake
}

fn close(c: sql.Conn) {
    sql.close_quietly(c)
}

fn shown(d: display.Display) -> display.Display {
    d
}

fn main() {
    fake = Fake.new()
    close(fake)
    io.print(Fake.is_closed(fake))
    io.print(shown(3))
}
// FILE: nomi.toml
[module]
name = "app"
entry_points = ["main"]
`
	entry, err := writeInput(t.TempDir(), program)
	if err != nil {
		t.Fatal(err)
	}
	if text := checkText(t, entry); text != "" {
		t.Fatalf("the program does not check:\n%s", text)
	}
	if got := runText(t, entry); got != "True\n3\n" {
		t.Errorf("got %q", got)
	}
}

// The inherent functions of a generic struct another file declares lower:
// an owner function with no receiver (`Box.new`), whose instance is the one
// the checker solved for the call, from a plain caller and a generic one,
// and the receiver's functions on that instance.
func TestLowering_ForeignGenericTypesOwnerFunctions(t *testing.T) {
	const program = `// FILE: main.nomi
import {
    boxes.Box
    std/io
}

fn make<T>(item: T, f: (T) -> Int) -> Box<T> {
    Box.new(item, f)
}

fn main() {
    b = Box.new("abc", String.length)
    io.print(Box.get(b))
    io.print(Box.fold(b, 1, fn acc, n -> acc + n))
    c = make("abcd", String.length)
    io.print(Box.fold(c, 1, fn acc, n -> acc + n))
}
// FILE: boxes.nomi
pub opaque struct Box<T> {
    item: T
    f: (T) -> Int
}

impl Box<T> {
    pub fn new(item: T, f: (T) -> Int) -> Box<T> {
        #Box{item, f}
    }

    pub fn get(b: Box<T>) -> T {
        b.item
    }

    pub fn fold<A>(b: Box<T>, initial: A, g: (A, Int) -> A) -> A {
        g(initial, b.f(b.item))
    }
}
// FILE: nomi.toml
[module]
name = "app"
entry_points = ["main"]
`
	entry, err := writeInput(t.TempDir(), program)
	if err != nil {
		t.Fatal(err)
	}
	if text := checkText(t, entry); text != "" {
		t.Fatalf("the program does not check:\n%s", text)
	}
	if got := runText(t, entry); got != "abc\n4\n5\n" {
		t.Errorf("got %q", got)
	}
}

func checkText(t *testing.T, path string) string {
	t.Helper()
	err := Check(path)
	if err == nil {
		return ""
	}
	return err.Error()
}

func runText(t *testing.T, path string) string {
	t.Helper()
	var out bytes.Buffer
	p, err := Load(path, WithOutput(&out))
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run %s: %v", path, err)
	}
	return out.String()
}
