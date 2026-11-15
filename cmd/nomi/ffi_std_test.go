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
const stdBindingProgram = `import
    std::int.Int64
    std::io
end

gopkg "strings" as strings
gopkg "strconv" as strconv
gopkg "math" as math

fn upper(s: String) -> String go strings.ToUpper
fn itoa(n: Int64) -> String go strconv.Itoa
fn atoi(s: String) -> Result<Int, String> go strconv.Atoi
fn sqrt(x: Float) -> Float go math.Sqrt

fn main()
    io.print(upper("hi"))
    io.print(itoa(42))
    io.print(atoi("17"))
    io.print(atoi("x"))
    io.print(sqrt(2.0))
end
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
	mustWrite(t, filepath.Join(dir, "lib.nomi"), `import std::int.Int64

gopkg "strings" as strs

pub subtype Count NonNegativeInt and Int64

fn twice(s: String) -> Count
    Count.try_from(String.length(s) * 2)!
end

pub fn rep(s: String, n: Count = twice(s)) -> String go strs.Repeat
`)
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, `import
    std::io
    lib
    lib.{rep}
end

gopkg "strings" as strs

fn upper(s: String = "abc") -> String go strs.ToUpper

fn main()
    io.print(upper())
    io.print(upper("x"))
    io.print(lib.rep("ab"))
    io.print(lib.rep("ab", 3))
    io.print(rep("q"))
    io.print(lib.rep(n: 2, s: "z"))
end
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
	mustWrite(t, missing, `import std::io

gopkg "strings" as strings

fn shout(s: String) -> String go strings.Shout

fn main()
    io.print(shout("hi"))
end
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

fn main() end
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
	mustWrite(t, entry, `import
    std::int.Int64
    std::io
end

gopkg "math/big" as big

fn new_int(x: Int64) -> Int go big.NewInt
fn jacobi(x: Int, y: Int) -> Int go big.Jacobi

fn main()
    io.print(new_int(-9223372036854775807) - 2)
    io.print(jacobi(123456789012345678901234567890, 987654321098765432109876543211))
end
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
	mustWrite(t, entry, `import
    std::int.Int64
    std::io.{self, Reader, Writer}
end

gopkg "encoding/hex" as hex
gopkg "io" as goio

fn hex_encoder(w: Writer) -> Writer go hex.NewEncoder
fn hex_decoder(r: Reader) -> Reader go hex.NewDecoder
fn limit(r: Reader, n: Int64) -> Reader go goio.LimitReader

fn main()
    (_, encoded) = Writer.collect(fn w -> Writer.write_text(hex_encoder(w), "Nomi"))
    io.print(Bytes.to_string(encoded) |> Result.with_default("?"))
    decoded = hex_decoder(Reader.from_bytes(encoded)) |> Reader.read_all_text()
    io.print(decoded |> Result.with_default("?"))
    io.print(limit(Reader.from_text("abcdef"), 3) |> Reader.read_all_text() |> Result.with_default("?"))
end
`)
	const want = "4e6f6d69\nNomi\nabc\n"
	env := buildEnv(t.TempDir())
	if r := runProcess(t, env, t.TempDir(), "", nomiBin, "run", entry); r.exit != 0 || r.stdout != want {
		t.Errorf("nomi run: exit %d, stdout %q, want %q\nstderr:\n%s", r.exit, r.stdout, want, r.stderr)
	}
}

// A Go binding's integer parameter takes a type whose every value fits:
// one declared `Int` is an error at the binding for `nomi check` and `nomi
// test`, before anything runs, naming the type to declare.
// Declared so, values reach Go across the whole of each width, and a
// computed Int is checked with `Int64.try_from` before the call.
func TestFFI_IntWiderThanAGoSlotIsRefusedAtTheBinding(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds FFI wrappers; -short")
	}
	dir := t.TempDir()
	if goModAbove(dir) {
		t.Skip("a go.mod above the temp directory")
	}
	env := buildEnv(t.TempDir())
	tests := filepath.Join(dir, "refused", "widths_test.nomi")
	mustWrite(t, tests, `gopkg "strconv" as strconv
gopkg "math/bits" as bits

fn format(i: Int, base: Int) -> String go strconv.FormatInt
fn format_unsigned(i: Int, base: Int) -> String go strconv.FormatUint
fn len32(x: Int) -> Int go bits.Len32

test "in range"
    assert format(-1, 10) == "-1"
end
`)
	wants := []string{
		`widths_test.nomi:4:51: Go function "FormatInt" in package "strconv": parameter i (Int) to Go int64: ` +
			"Go int64 holds an Int in -9223372036854775808..9223372036854775807, and Int admits Ints past that: declare it `Int64` (std::int)",
		`Go function "FormatUint" in package "strconv": parameter i (Int) to Go uint64: Go uint64 holds an Int in 0..18446744073709551615, ` +
			"and Int admits Ints past that: declare it `NonNegativeInt and Int64` (std::int)",
		`Go function "Len32" in package "math/bits": parameter x (Int) to Go uint32: Go uint32 holds an Int in 0..4294967295, ` +
			"and Int admits Ints past that: declare it a subtype whose `when` bounds it within 0..4294967295",
	}
	for _, cmd := range []string{"check", "test"} {
		r := runProcess(t, env, t.TempDir(), "", nomiBin, cmd, tests)
		if r.exit == 0 {
			t.Fatalf("nomi %s accepted the bindings:\n%s", cmd, r.transcript())
		}
		for _, want := range wants {
			if !strings.Contains(r.stdout+r.stderr, want) {
				t.Errorf("nomi %s: missing %q\n%s", cmd, want, r.transcript())
			}
		}
		if strings.Contains(r.stdout, "in range") {
			t.Errorf("nomi %s ran a test past a refused binding:\n%s", cmd, r.transcript())
		}
	}

	entry := filepath.Join(dir, "typed", "main.nomi")
	mustWrite(t, entry, `import
    std::int.Int64
    std::io
end

gopkg "strconv" as strconv
gopkg "math/bits" as bits

subtype Base Int when fn n -> n >= 2 and n <= 36
subtype Uint64 Int when fn n -> n >= 0 and n <= 18446744073709551615
subtype Uint32 Int when fn n -> n >= 0 and n <= 4294967295

fn format(i: Int64, base: Base) -> String go strconv.FormatInt
fn format_unsigned(i: Uint64, base: Base) -> String go strconv.FormatUint
fn len32(x: Uint32) -> Int go bits.Len32

fn main()
    io.print(format(-9223372036854775808, 10))
    io.print(format_unsigned(18446744073709551615, 16))
    io.print(len32(4294967295))
    io.print(Int64.try_from(Int.pow(10, 30)))
end
`)
	const want = "-9223372036854775808\nffffffffffffffff\n32\nNone\n"
	if r := runProcess(t, env, t.TempDir(), "", nomiBin, "run", entry); r.exit != 0 || r.stdout != want {
		t.Errorf("nomi run: exit %d, stdout %q, want %q\nstderr:\n%s", r.exit, r.stdout, want, r.stderr)
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
