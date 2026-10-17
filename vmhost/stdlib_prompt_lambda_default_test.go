package vmhost_test

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// A stdlib module's `//!` prompt calls the module's own Nomi-bodied function
// with a defaulted argument omitted, inside a lambda. The call reaches the
// function's arity wrapper only where the builder knows it is building one
// of the module's own test bodies, and a lambda's builder is a child of the
// test body's, not a test body itself, so the call was BLOCKED with
// "qualified call, Type.method: String.pad_start" while the same call outside
// the lambda ran.
func TestStdlibPrompt_LambdaOmitsADefaultedArgument(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; lowers and runs std/strings' cases on the VM; -short")
	}
	pinStdlibEnv(t)
	restoreEnv, err := vmhost.DefaultTestEnv()
	if err != nil {
		t.Fatal(err)
	}
	defer restoreEnv()
	src, ok := std.ReadFile("strings")
	if !ok {
		t.Fatal("std/strings is not embedded")
	}
	probe := strings.TrimRight(string(src), "\n") + `

/// Probe.
//! assert ["7", "42"] |> Iter.map(fn s -> String.pad_start(s, 3)) |> Iter.to_list() == ["  7", " 42"]
//! rows = [["a", "bc"]] |> Iter.map(fn row -> row |> Iter.map(fn c -> String.pad_end(c, 2)) |> String.join("|"))
//! assert Iter.to_list(rows) == ["a |bc"]
//
pub fn lambda_default_probe() -> Int {
    0
}
`
	p, err := vmhost.LoadStdlibSource("strings", probe)
	if err != nil {
		t.Fatalf("the front end rejects the probe: %v", err)
	}
	ran := false
	for _, c := range p.Cases(io.Discard, vmhost.TestOptions{}) {
		if !strings.Contains(c.Name, "lambda_default_probe") {
			continue
		}
		ran = true
		if c.Err != nil || c.Blocked != nil {
			t.Errorf("%s does not pass: err %v, blocked %v", c.Name, c.Err, c.Blocked)
		}
	}
	if !ran {
		t.Error("the probe case did not run")
	}
}
