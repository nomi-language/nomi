package vmhost_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// An `import` at the top of a block binds its names in that block (spec,
// "Imports"). A test-only dependency belongs at the top of the test body,
// where it does not make the file depend on it. The builder used to admit a
// block import only when the module scope already bound each name to the
// same declaration, so an import of anything the file did not import was
// BLOCKED, in a stdlib `//!` case, a user test and a function body alike.

// std/maybe imports neither ranges.Range nor anything binding `Range`, so a
// case naming Range must import it. A stdlib file spells the path bare or
// with `std/`; both run.
func TestBlockImport_StdlibAttachedTest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; lowers and runs std/maybe's cases on the VM; -short")
	}
	pinStdlibEnv(t)
	restoreEnv, err := vmhost.DefaultTestEnv()
	if err != nil {
		t.Fatal(err)
	}
	defer restoreEnv()
	src, ok := std.ReadFile("maybe")
	if !ok {
		t.Fatal("std/maybe is not embedded")
	}
	probe := func(imp string) string {
		return strings.TrimRight(string(src), "\n") + `

/// Probe.
` + imp + `//! assert Range.naturals() |> Iter.take(2) |> Iter.to_list() == [0, 1]
//
pub fn block_import_probe(): Int {
    0
}
`
	}

	// Without the import Range is undefined there, though std/maybe's own
	// `Maybe.collect` case imports it: that import binds only in its body.
	_, err = vmhost.LoadStdlibSource("maybe", probe(""))
	if err == nil || !strings.Contains(err.Error(), "undefined type or variant 'Range'") {
		t.Fatalf("want \"undefined type or variant 'Range'\" without the import, got %v", err)
	}
	for _, path := range []string{"ranges.Range", "std/ranges.Range"} {
		p, err := vmhost.LoadStdlibSource("maybe", probe("//! import "+path+"\n"))
		if err != nil {
			t.Fatalf("import %s: the front end rejects the case: %v", path, err)
		}
		ran := false
		for _, c := range p.Cases(io.Discard, vmhost.TestOptions{}) {
			if c.Err != nil || c.Blocked != nil {
				t.Errorf("import %s: %s does not pass: err %v, blocked %v", path, c.Name, c.Err, c.Blocked)
			}
			if strings.Contains(c.Name, "block_import_probe") {
				ran = true
			}
		}
		if !ran {
			t.Errorf("import %s: the probe case did not run", path)
		}
	}
}

// A user file imports nothing at file level; function bodies, an attached
// test and a test block each import what they use: a type, a variant, a
// function (the spec's `shout`) and a whole file (`io` in main). The module
// scope binds none of those names, so each use resolves through the
// checker's reference to the block's import.
func TestBlockImport_UserBodiesAndTests(t *testing.T) {
	path := writeProgram(t, `fn millis(): Int {
  import std/duration.Duration
  Duration.seconds(2) |> Duration.as_millis()
}

fn less(): Bool {
  import std/comparable.Ordering.{Less}
  Int.compare(1, 2) == Less
}

fn shout(message: String) {
  import std/io.print

  print(String.to_upper(message))
}

/// Three.
//! import std/duration.Duration
//!
//! assert Duration.minutes(1) |> Duration.as_seconds() == three() * 20
fn three(): Int {
  3
}

fn main() {
  import std/io
  io.print(Int.to_string(millis()))
  io.print(Bool.to_string(less()))
  shout("hi")
}

test "a test block imports" {
  import std/duration.Duration as D
  inner = {
    import std/duration.Duration
    Duration.seconds(1) |> Duration.as_millis()
  }
  assert inner == 1000
  assert D.minutes(1) |> D.as_seconds() == 60
}
`)
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatalf("the front end rejects block imports: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if got, want := out.String(), "2000\nTrue\nHI\n"; got != want {
		t.Errorf("output %q, want %q", got, want)
	}
	cases := p.Cases(io.Discard, vmhost.TestOptions{})
	if len(cases) != 2 {
		t.Fatalf("want 2 cases (the attached test and the test block), got %d", len(cases))
	}
	for _, c := range cases {
		if c.Err != nil || c.Blocked != nil {
			t.Errorf("%s does not pass: err %v, blocked %v", c.Name, c.Err, c.Blocked)
		}
	}
}

// A block's import binds nothing outside the block. The checker used to
// resolve a qualified type name through any import in the file, so a body
// with no import of its own type-checked against another body's.
func TestBlockImport_DoesNotReachOtherBodies(t *testing.T) {
	_, err := vmhost.Load(writeProgram(t, `fn millis(): Int {
  import std/duration.Duration
  Duration.seconds(2) |> Duration.as_millis()
}

fn leaked(): Int {
  Duration.seconds(1) |> Duration.as_millis()
}

fn main() {
  _ = millis() + leaked()
}
`))
	if err == nil {
		t.Fatal("a body names Duration, which only another body imports, and the front end admits it")
	}
	if !strings.Contains(err.Error(), "undefined type or variant 'Duration'") {
		t.Errorf("want \"undefined type or variant 'Duration'\", got %v", err)
	}
}
