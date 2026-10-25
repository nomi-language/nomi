package vmhost

import (
	"context"
	"strings"
	"testing"
)

// A std module with one error type calls it `Error`, and a file that uses two
// such modules writes each through its file's name: `uuid.Error` and
// `random.Error` are two types in one file, in signatures, patterns and
// variant literals.
func TestModuleErrors_FileQualifiedErrorsLiveSideBySide(t *testing.T) {
	const src = `import {
    std/io
    std/random.{self, Generator}
    std/uuid.{self, Uuid}
}

fn read_id(text: String) -> Result<Uuid, uuid.Error> {
    Uuid.parse(text)
}

fn pick() -> Result<Generator<String>, random.Error> {
    Generator.weighted(#(0.0, "never"), [])
}

fn main() {
    case read_id("6ba7b810") {
        Err(#uuid.Error.Length{length}) -> io.print("length ${length}")
        _ -> io.print("other")
    }
    case pick() {
        Err(random.Error.ZeroWeightTotal) -> io.print("zero total")
        _ -> io.print("other")
    }
    io.print(read_id("6ba7b810") == Err(#uuid.Error.Length{length: 8}))
}
`
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects uuid.Error beside random.Error: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "length 8\nzero total\nTrue\n"; out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// Importing the bare `Error` of two modules binds the name twice. The later
// import wins, so the earlier one is unused and the file is rejected there:
// the file-qualified spelling above is the one that works.
func TestModuleErrors_TwoBareErrorImportsAreRejected(t *testing.T) {
	const src = `import {
    std/io
    std/random.Error
    std/uuid.Error
}

fn show(e: Error) -> String {
    "${e}"
}

fn main() {
    io.print("x")
}
`
	_, err := LoadSource("main", src)
	if err == nil {
		t.Fatal("the front end accepts two bare `Error` imports")
	}
	if want := "imported name 'Error' is unused"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want one containing %q", err, want)
	}
}
