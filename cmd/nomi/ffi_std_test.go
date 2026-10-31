package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stdBindingProgram binds three Go standard library packages with no wrapper
// package of its own: a string function, one returning (T, error), and a
// float function.
const stdBindingProgram = `import std/io

gopkg "strings" as strings
gopkg "strconv" as strconv
gopkg "math" as math

fn upper(s: String) -> String go strings.ToUpper
fn itoa(n: Int) -> String go strconv.Itoa
fn atoi(s: String) -> Result<Int, String> go strconv.Atoi
fn sqrt(x: Float) -> Float go math.Sqrt

fn main() {
    io.print(upper("hi"))
    io.print(itoa(42))
    io.print(atoi("17"))
    io.print(atoi("x"))
    io.print(sqrt(2.0))
}
`

const stdBindingWant = "HI\n42\nOk(17)\nErr(strconv.Atoi: parsing \"x\": invalid syntax)\n1.4142135623730951\n"

// A binding to a Go standard library package runs with no wrapper package and,
// in a directory with no go.mod, with no go.mod at all: `nomi check`, `nomi
// run`, an extensionless `#!` script and `nomi build` all reach it.
func TestFFI_StdLibraryBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds FFI wrappers; -short")
	}
	for _, withGoMod := range []bool{false, true} {
		name := "no go.mod"
		if withGoMod {
			name = "go.mod"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if withGoMod {
				mustWrite(t, filepath.Join(dir, "go.mod"), "module stdbind\n\ngo 1.26.3\n")
			} else if goModAbove(dir) {
				t.Skip("a go.mod above the temp directory")
			}
			env := buildEnv(t.TempDir())
			entry := filepath.Join(dir, "main.nomi")
			mustWrite(t, entry, stdBindingProgram)
			script := filepath.Join(dir, "bin", "hi")
			mustWrite(t, script, "#!/usr/bin/env nomi\n"+stdBindingProgram)

			if r := runProcess(t, env, t.TempDir(), "", nomiBin, "check", entry); r.exit != 0 {
				t.Fatalf("nomi check: exit %d\n%s", r.exit, r.transcript())
			}
			for _, args := range [][]string{{"run", entry}, {"run", script}, {script}} {
				if r := runProcess(t, env, t.TempDir(), "", nomiBin, args...); r.exit != 0 || r.stdout != stdBindingWant {
					t.Errorf("nomi %s: exit %d, stdout %q, want %q\nstderr:\n%s",
						strings.Join(args, " "), r.exit, r.stdout, stdBindingWant, r.stderr)
				}
			}
			built := exe(filepath.Join(t.TempDir(), "hi"))
			if b := runProcess(t, env, t.TempDir(), "", nomiBin, "build", entry, "-o", built); b.exit != 0 {
				t.Fatalf("nomi build: exit %d\n%s", b.exit, b.transcript())
			}
			if r := runProcess(t, env, t.TempDir(), "", built); r.exit != 0 || r.stdout != stdBindingWant {
				t.Errorf("the built program: exit %d, stdout %q, want %q\nstderr:\n%s", r.exit, r.stdout, stdBindingWant, r.stderr)
			}
		})
	}
}

// A `go` binding's defaulted parameter is filled in Nomi before the call
// crosses into Go, as a `fn`'s is: by a call in the declaring file, and by
// one in another file, qualified, imported bare or with a named argument.
// A default may name the declaring file's private functions and an earlier
// parameter.
func TestFFI_DefaultedParameters(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds FFI wrappers; -short")
	}
	dir := t.TempDir()
	if goModAbove(dir) {
		t.Skip("a go.mod above the temp directory")
	}
	mustWrite(t, filepath.Join(dir, "lib.nomi"), `gopkg "strings" as strs

fn twice(s: String) -> Int {
    String.length(s) * 2
}

pub fn rep(s: String, n: Int = twice(s)) -> String go strs.Repeat
`)
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, `import {
    std/io
    lib
    lib.{rep}
}

gopkg "strings" as strs

fn upper(s: String = "abc") -> String go strs.ToUpper

fn main() {
    io.print(upper())
    io.print(upper("x"))
    io.print(lib.rep("ab"))
    io.print(lib.rep("ab", 3))
    io.print(rep("q"))
    io.print(lib.rep(n: 2, s: "z"))
}
`)
	const want = "ABC\nX\nabababab\nababab\nqq\nzz\n"
	env := buildEnv(t.TempDir())
	if r := runProcess(t, env, t.TempDir(), "", nomiBin, "run", entry); r.exit != 0 || r.stdout != want {
		t.Errorf("nomi run: exit %d, stdout %q, want %q\nstderr:\n%s", r.exit, r.stdout, want, r.stderr)
	}
}

// A binding to a function the standard library package does not declare is
// refused before the program runs, naming the binding, the package and the
// function; so is a `gopkg` naming a package internal to the standard library,
// by the checker at the import path.
func TestFFI_StdLibraryBindingErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds FFI wrappers; -short")
	}
	dir := t.TempDir()
	if goModAbove(dir) {
		t.Skip("a go.mod above the temp directory")
	}
	env := buildEnv(t.TempDir())
	missing := filepath.Join(dir, "missing", "main.nomi")
	mustWrite(t, missing, `import std/io

gopkg "strings" as strings

fn shout(s: String) -> String go strings.Shout

fn main() {
    io.print(shout("hi"))
}
`)
	want := `main.nomi:5:42: Go package "strings" has no top-level function "Shout" for Nomi binding "shout"`
	for _, cmd := range []string{"check", "run"} {
		r := runProcess(t, env, t.TempDir(), "", nomiBin, cmd, missing)
		if r.exit == 0 || !strings.Contains(r.stderr, want) {
			t.Errorf("nomi %s: exit %d, want a refusal containing %q\n%s", cmd, r.exit, want, r.transcript())
		}
		if strings.Contains(r.stdout, "HI") {
			t.Errorf("nomi %s ran the program:\n%s", cmd, r.transcript())
		}
	}

	internal := filepath.Join(dir, "internal", "main.nomi")
	mustWrite(t, internal, `gopkg "internal/abi" as abi

fn main() {}
`)
	r := runProcess(t, env, t.TempDir(), "", nomiBin, "check", internal)
	if want := `gopkg "internal/abi": internal to the Go standard library, which no other module may import`; r.exit == 0 || !strings.Contains(r.stderr, want) {
		t.Errorf("nomi check: exit %d, want a refusal containing %q\n%s", r.exit, want, r.transcript())
	}
}

// A binding to math/big, with no go.mod, takes and returns a Go *big.Int as
// an Int of any size.
func TestFFI_StdBigInt(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds FFI wrappers; -short")
	}
	dir := t.TempDir()
	if goModAbove(dir) {
		t.Skip("a go.mod above the temp directory")
	}
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, `import std/io

gopkg "math/big" as big

fn new_int(x: Int) -> Int go big.NewInt
fn jacobi(x: Int, y: Int) -> Int go big.Jacobi

fn main() {
    io.print(new_int(-9223372036854775807) - 2)
    io.print(jacobi(123456789012345678901234567890, 987654321098765432109876543211))
}
`)
	const want = "-9223372036854775809\n-1\n"
	env := buildEnv(t.TempDir())
	if r := runProcess(t, env, t.TempDir(), "", nomiBin, "run", entry); r.exit != 0 || r.stdout != want {
		t.Errorf("nomi run: exit %d, stdout %q, want %q\nstderr:\n%s", r.exit, r.stdout, want, r.stderr)
	}
}

// Go's stream interfaces cross as std/io's Reader and Writer with no
// go.mod: encoding/hex's encoder writes through a Writer.collect writer,
// its decoder reads a Reader, and io.LimitReader cuts one short.
func TestFFI_StdStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds FFI wrappers; -short")
	}
	dir := t.TempDir()
	if goModAbove(dir) {
		t.Skip("a go.mod above the temp directory")
	}
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, `import std/io.{self, Reader, Writer}

gopkg "encoding/hex" as hex
gopkg "io" as goio

fn hex_encoder(w: Writer) -> Writer go hex.NewEncoder
fn hex_decoder(r: Reader) -> Reader go hex.NewDecoder
fn limit(r: Reader, n: Int) -> Reader go goio.LimitReader

fn main() {
    #(_, encoded) = Writer.collect(fn w -> Writer.write_text(hex_encoder(w), "Nomi"))
    io.print(Bytes.to_string(encoded) |> Result.with_default("?"))
    decoded = hex_decoder(Reader.from_bytes(encoded)) |> Reader.read_all_text()
    io.print(decoded |> Result.with_default("?"))
    io.print(limit(Reader.from_text("abcdef"), 3) |> Reader.read_all_text() |> Result.with_default("?"))
}
`)
	const want = "4e6f6d69\nNomi\nabc\n"
	env := buildEnv(t.TempDir())
	if r := runProcess(t, env, t.TempDir(), "", nomiBin, "run", entry); r.exit != 0 || r.stdout != want {
		t.Errorf("nomi run: exit %d, stdout %q, want %q\nstderr:\n%s", r.exit, r.stdout, want, r.stderr)
	}
}

// An Int a Go binding's integer parameter cannot hold is a trap at the
// call, naming the Go function and its parameter: `nomi test` fails the
// case, `nomi run` exits 1 with the trap, and neither reports a machine
// limit or a program the VM cannot run.
func TestFFI_IntPastAGoSlotTraps(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds FFI wrappers; -short")
	}
	dir := t.TempDir()
	if goModAbove(dir) {
		t.Skip("a go.mod above the temp directory")
	}
	env := buildEnv(t.TempDir())
	tests := filepath.Join(dir, "widths_test.nomi")
	mustWrite(t, tests, `gopkg "strconv" as strconv
gopkg "math/bits" as bits

fn format(i: Int, base: Int) -> String go strconv.FormatInt
fn format_unsigned(i: Int, base: Int) -> String go strconv.FormatUint
fn len32(x: Int) -> Int go bits.Len32

test "an Int past int64" {
    assert format(123456789012345678901234567890, 10) == ""
}

test "a negative Int for uint64" {
    assert format_unsigned(-1, 10) == ""
}

test "an Int past uint32" {
    assert len32(4294967296) == 0
}

test "in range" {
    assert format(-9223372036854775808, 10) == "-9223372036854775808"
    assert format_unsigned(18446744073709551615, 16) == "ffffffffffffffff"
    assert len32(4294967295) == 32
}
`)
	r := runProcess(t, env, t.TempDir(), "", nomiBin, "test", tests)
	if r.exit == 0 {
		t.Fatalf("nomi test passed:\n%s", r.transcript())
	}
	for _, want := range []string{
		"line 9: `strconv.FormatInt`'s `i` takes an Int that fits in 64 bits; got 123456789012345678901234567890",
		"line 13: `strconv.FormatUint`'s `i` takes an Int from 0 to 18446744073709551615; got -1",
		"line 17: `bits.Len32`'s `x` takes an Int from 0 to 4294967295; got 4294967296",
		"1 passed, 3 failed",
	} {
		if !strings.Contains(r.stdout+r.stderr, want) {
			t.Errorf("nomi test: missing %q\n%s", want, r.transcript())
		}
	}
	for _, refused := range []string{"BLOCKED", "machine limit", "cannot run"} {
		if strings.Contains(r.stdout+r.stderr, refused) {
			t.Errorf("nomi test reports %q:\n%s", refused, r.transcript())
		}
	}

	entry := filepath.Join(dir, "run", "main.nomi")
	mustWrite(t, entry, `import std/io

gopkg "strconv" as strconv

fn format(i: Int, base: Int) -> String go strconv.FormatInt

fn main() {
    io.print(format(12, 10))
    io.print(format(123456789012345678901234567890, 10))
    io.print("not reached")
}
`)
	r = runProcess(t, env, t.TempDir(), "", nomiBin, "run", entry)
	if want := "line 9: `strconv.FormatInt`'s `i` takes an Int that fits in 64 bits; got 123456789012345678901234567890"; r.exit != 1 || r.stdout != "12\n" || !strings.Contains(r.stderr, want) {
		t.Errorf("nomi run: exit %d, stdout %q, want exit 1, stdout \"12\\n\" and %q on stderr\n%s", r.exit, r.stdout, want, r.transcript())
	}
	for _, refused := range []string{"BLOCKED", "machine limit", "cannot run"} {
		if strings.Contains(r.stderr, refused) {
			t.Errorf("nomi run reports %q:\n%s", refused, r.transcript())
		}
	}
}

// goModAbove reports whether a go.mod sits in dir or a directory above it.
func goModAbove(dir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
