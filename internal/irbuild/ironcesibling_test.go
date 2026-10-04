package irbuild

import (
	"bytes"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

func TestIROnceSibling_CycleKeepsForcingLineage(t *testing.T) {
	path := fixture("once_cycle_files/main.nomi")

	p, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var entry *ir.Module
	for _, m := range r.IR {
		for _, f := range m.Funcs() {
			if f.Name() == "main" {
				entry = m
			}
		}
	}
	if entry == nil {
		t.Fatal("main was not retained")
	}
	var out bytes.Buffer
	_, err = vm.NewProgram(entry, r.IRModules(), &out).Run("main")
	if err == nil || err.Error() != "cyclic 'once' binding: 'seed' depends on itself" || out.String() != "start\n" {
		t.Fatalf("output %q, error %v", out.String(), err)
	}
}

func TestIROnceSibling_AliasesShareOneLazyCell(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/io
  config as cfg
  config.{tag as label, limit}
  decoy
}
fn main() {
  io.print("created")
  io.print(label)
  io.print(cfg.tag)
  io.print(label)
  io.print(limit)
  io.print(decoy.limit)
  label = "local"
  local_read = || label
  io.print(local_read())
  cfg = {tag: "record"}
  io.print(cfg.tag)
}
`, "created\nforcing\nservice\nservice\nservice\n3\n99\nlocal\nrecord\n", map[string]string{
		"config.nomi": `import std/io
fn initialize(): String { io.print("forcing") "service" }
pub once tag: String = initialize()
pub once limit = 3
`,
		"decoy.nomi": `pub once limit = 99`,
	})
}
