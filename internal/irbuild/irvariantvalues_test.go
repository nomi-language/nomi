package irbuild

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

func TestIRVariantValues_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"constructed and passed payload", `import std/io
enum Wrapper { Only Int }
fn unwrap(Wrapper.Only(n)): Int { n }
fn make(n: Int): Wrapper { Wrapper.Only(n + 1) }
fn identity(v: Wrapper): Wrapper { v }
fn main() {
  wrapped = make(41)
  io.print(unwrap(identity(wrapped)))
}`, "42\n"},
		{"conditional results and closure capture", `import std/io
enum Text { Value String }
fn unwrap(Text.Value(s)): String { s }
fn main() {
  original = Text.Value("outer")
  choose = |flag: Bool| {
    if flag { original } else { Text.Value("other") }
  }
  io.print(unwrap(choose(True)))
  io.print(unwrap(choose(False)))
}`, "outer\nother\n"},
		{"payload effects and multiline arguments", `import std/io
enum Number { Value Int }
fn item(n: Int): Int { io.print(n) n + 10 }
fn unwrap(Number.Value(n)): Int { n }
fn main() {
  value = Number.Value(item(
    2
  ))
  io.print(unwrap(value))
}`, "2\n12\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

func TestIRVariantValues_QualifiedConstructedIdentity(t *testing.T) {
	const src = `enum Signal {
  Idle
  Value Int
}
fn bare(): Signal { Signal.Idle }
fn payload(): Signal { Signal.Value(42) }
fn main() { _ = bare() _ = payload() return }
`
	verifyLambdaProgram(t, src, "")
	path := filepath.Join(t.TempDir(), "signals.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, mod := range res.IR {
		for _, fn := range mod.Funcs() {
			if fn.Name() != "bare" && fn.Name() != "payload" {
				continue
			}
			got, err := vmRunSymV(vm.NewProgram(mod, res.IRModules(), io.Discard), fn.Sym())
			if err != nil {
				t.Fatal(err)
			}
			v, enum, variant, ok := vmRecord(got)
			if !ok || enum != "signals.Signal" {
				t.Fatalf("%s lost the qualified declaration: %#v", fn.Name(), got)
			}
			if fn.Name() == "bare" && (variant != "Idle" || v.NumFields() != 0) {
				t.Fatalf("bare value: %#v", v)
			}
			if fn.Name() == "payload" && (variant != "Value" || v.NumFields() != 1 || v.Field(0) != any(int64(42))) {
				t.Fatalf("payload value: %#v", v)
			}
			checked++
		}
	}
	if checked != 2 {
		t.Fatalf("checked %d retained constructors; want both", checked)
	}
}
