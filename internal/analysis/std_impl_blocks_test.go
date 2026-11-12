package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// Two `impl T` blocks for one type in one std file are an error
// std.Load records, as a program's build reports them in its own files. A
// build over the stdlib's base folds only the program's files, so before
// StdImplBlockErrors no build saw two std blocks together: a program
// reaching the module was refused at lowering instead, with no error.
func TestStdImplBlockErrors_TwoBlocksForOneTypeInOneFile(t *testing.T) {
	const twice = "pub struct P\n    x: Int\nend\n\nimpl P\n    pub fn a(p: P) -> Int\n        p.x\n    end\nend\n\nimpl P\n    pub fn b(p: P) -> Int\n        p.x\n    end\nend\n"
	fa, nodes := checkNodes(t, twice)
	fa.FilePath = "std/thing.nomi"
	errs := StdImplBlockErrors(map[string]*FileAnalysis{"thing": fa}, map[string][]ast.Node{"thing": nodes})
	if len(errs["thing"]) != 1 || !strings.Contains(errs["thing"][0].Message, "duplicate type-owned impl: `impl P` appears 2 times") {
		t.Fatalf("errors = %v, want one duplicate type-owned impl error for std/thing", errs)
	}

	const once = "pub struct P\n    x: Int\nend\n\nimpl P\n    pub fn a(p: P) -> Int\n        p.x\n    end\nend\n"
	fa, nodes = checkNodes(t, once)
	fa.FilePath = "std/thing.nomi"
	if errs := StdImplBlockErrors(map[string]*FileAnalysis{"thing": fa}, map[string][]ast.Node{"thing": nodes}); len(errs) != 0 {
		t.Fatalf("one block reported %v", errs)
	}
}
