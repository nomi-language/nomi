package irbuild

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// An `or` pattern's alternatives each test the value and copy what they bind
// into one join temporary; the arm's names are bound once, where the
// alternatives join, so its guard and body are lowered once and the debugger
// sees one variable per name, declared at the first alternative's site.
func TestOrPattern_OneBindingWhereTheAlternativesJoin(t *testing.T) {
	src := `import std/io

enum Ev {
  A Int
  B Int
  C Int
}

fn describe(e: Ev): Int {
  case e {
    .A(n) or .B(n) or .C(n) when n > 0 -> n * 2
    _ -> 0
  }
}

fn main() {
  io.print(describe(Ev.B(2)))
}
`
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ir.Func
	for _, m := range res.IR {
		if !strings.HasSuffix(m.Name(), "main.nomi") {
			continue
		}
		for _, f := range m.Funcs() {
			if f.Name() == "describe" {
				fn = f
			}
		}
	}
	if fn == nil {
		t.Fatal("describe was not retained")
	}
	binds, copies := 0, 0
	for _, b := range fn.Blocks() {
		for _, in := range b.Instrs() {
			switch n := in.(type) {
			case *ir.Bind:
				if n.Sym().Name() == "n" {
					binds++
				}
			case *ir.Copy:
				copies++
			}
		}
	}
	if binds != 1 {
		t.Errorf("want one Bind of n, where the alternatives join; got %d", binds)
	}
	if copies < 3 {
		t.Errorf("want each of the three alternatives to copy its payload into the join; got %d copies", copies)
	}
	var vars []string
	for _, v := range fn.Vars() {
		vars = append(vars, fmt.Sprintf("%s: %s %d:%d", v.Name, v.Type, v.Decl.Line(), v.Decl.Col()))
	}
	if strings.Join(vars, ", ") != "e: Ev 9:13, n: Int 11:8" {
		t.Errorf("variables: %v", vars)
	}
}
