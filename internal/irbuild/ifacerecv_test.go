package irbuild

import (
	goast "go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// THE EXPIRY CONDITION for ifacerecv.go's central citation.
//
// That header relies on `ast.Call.InferredDispatchTarget` having no writer.
// The field's own declaration comment says "analyzer-filled", so the tree
// actively invites the opposite belief.
//
// This names the symbol whose change voids the claim, and asserts it: if the
// analyzer starts filling the field, a receiver-derived dispatch target
// exists and ifacerecv.go's header has to be re-read.
//
// Parsed from source rather than exercised, because the claim IS about the
// source: "nothing assigns this field" is a property of the program text, and
// a behavioural test could only ever show that one input did not reach it.
func TestIfaceRecv_TheDeadFieldStaysDead(t *testing.T) {
	writers, err := assignmentsTo("InferredDispatchTarget",
		"../ast", "../analysis", "../frontend", ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(writers) != 0 {
		t.Errorf("ast.Call.InferredDispatchTarget is now ASSIGNED at:\n  %s\n\n"+
			"ifacerecv.go's header states it is dead and dispatches on the receiver's "+
			"runtime type for that reason. If the analyzer now fills it, a "+
			"receiver-derived dispatch target exists and may disagree with the "+
			"runtime-type answer the builder lowers. Re-read that header before "+
			"deleting this test.", strings.Join(writers, "\n  "))
	}
}

// assignmentsTo lists every position that writes the named struct field, as
// `file:line`, over the given directories.
//
// A field NAME rather than a resolved selector, which over-approximates: any
// `X.InferredDispatchTarget = …` or `InferredDispatchTarget:` in a composite
// literal counts, whatever X is. Over-approximation is the safe direction for
// this guard — a false hit costs a read, a false miss lets the claim rot.
//
// Deliberately not a text scan. A regex over these directories matches this
// test's own error message and ifacerecv.go's header prose, so it reports
// several hits and can never reach zero; the field-name token in an assignment
// position is the thing being counted, so the parser is what counts it.
func assignmentsTo(field string, dirs ...string) ([]string, error) {
	fset := token.NewFileSet()
	var out []string
	for _, dir := range dirs {
		names, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
			if err != nil {
				return nil, err
			}
			goast.Inspect(file, func(n goast.Node) bool {
				switch node := n.(type) {
				case *goast.AssignStmt:
					for _, lhs := range node.Lhs {
						if sel, ok := lhs.(*goast.SelectorExpr); ok && sel.Sel.Name == field {
							out = append(out, fset.Position(sel.Sel.Pos()).String())
						}
					}
				case *goast.KeyValueExpr:
					if id, ok := node.Key.(*goast.Ident); ok && id.Name == field {
						out = append(out, fset.Position(id.Pos()).String())
					}
				}
				return true
			})
		}
	}
	return out, nil
}
