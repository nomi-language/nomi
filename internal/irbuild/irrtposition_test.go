package irbuild

import (
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A fault's source position comes from its instruction's position in the IR.
// A few rt operations also take the Nomi position as an explicit `line int`
// argument, and rt.Frame carries no position at all. This file checks that
// account against rt's source.

// irOpWantExplicitPos is how many rt operations take the Nomi position as an
// argument. The VM checks Int arithmetic itself and builds the fault from rt's
// error constructors, so those are not among them.
const irOpWantExplicitPos = 3

// irOpParse parses every non-test Go source in dir.
func irOpParse(dir string) ([]*goast.File, *token.FileSet, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(paths)
	fset := token.NewFileSet()
	var files []*goast.File
	for _, p := range paths {
		if strings.HasSuffix(filepath.Base(p), "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if perr != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", filepath.Base(p), perr)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("no non-test sources under %s", dir)
	}
	return files, fset, nil
}

// irOpExplicitPosition are the rt operations that take the Nomi position as an
// ARGUMENT. Every other fault's line comes from the instruction's own position
// in the IR, and the VM builds the fault from rt's error constructors.
//
// rt.Frame carries dynamic context (a Context, the `once` forcing stack, the
// concurrent scope, two phase flags) and no position, so there is no third
// mechanism.
var irOpExplicitPosition = []string{
	"DivDecimal", "ModDecimal", "DeferBootAt",
}

// TestIRRtOperationsThatTakeAPosition checks that the rt functions taking a
// `line int` are exactly irOpExplicitPosition (error and text constructors
// aside), and that rt.Frame has no positional field.
func TestIRRtOperationsThatTakeAPosition(t *testing.T) {
	sigs, err := irOpRTSignatures()
	if err != nil {
		t.Skipf("rt sources are not readable from here: %v", err)
	}
	withLine := map[string]bool{}
	for name, params := range sigs {
		if strings.Contains(params, "line int") {
			withLine[name] = true
		}
	}
	if len(withLine) == 0 {
		t.Fatal("no rt function takes a `line int`, which cannot be true while rt.DivDecimal exists; " +
			"the signature scanner is broken and every count below would be a false zero")
	}

	// A known line-carrier must be found and a known non-carrier must not.
	if !withLine["DivDecimal"] {
		t.Errorf("rt.DivDecimal(a, b Decimal, line int) was not recognized as carrying a position")
	}
	if withLine["AddOverflows"] {
		t.Errorf("rt.AddOverflows(a, b int64) was recognized as carrying a position; the scanner " +
			"is matching something other than the parameter list")
	}

	var missing, extra []string
	declared := map[string]bool{}
	for _, n := range irOpExplicitPosition {
		declared[n] = true
		if !withLine[n] {
			missing = append(missing, n)
		}
	}
	for n := range withLine {
		if declared[n] {
			continue
		}
		// The error/text CONSTRUCTORS build a boxed error for the same
		// operation. They carry a line for the message, not as an
		// operation's position.
		if strings.HasSuffix(n, "Error") || strings.HasSuffix(n, "Text") {
			continue
		}
		extra = append(extra, n)
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("declared as carrying an explicit position but rt takes no `line int` for them: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("rt takes a `line int` for %v, which the list does not name. Either it is "+
			"a new position-carrying operation or it is only an error constructor; classify "+
			"it before the pin moves", extra)
	}
	if len(irOpExplicitPosition) != irOpWantExplicitPos {
		t.Errorf("%d operations carry an explicit position, pinned at %d",
			len(irOpExplicitPosition), irOpWantExplicitPos)
	}
	t.Logf("%d operations carry the Nomi position as an rt argument: %v",
		len(irOpExplicitPosition), irOpExplicitPosition)

	// And there is no third mechanism. rt.Frame is threaded through every
	// call, so a position field on it would be a position available
	// everywhere and the two mechanisms above would not be the whole story.
	fields, ferr := irOpFrameFields()
	if ferr != nil {
		t.Fatalf("reading rt.Frame's fields: %v", ferr)
	}
	if len(fields) == 0 {
		t.Fatal("rt.Frame has no fields, which cannot be true; the scan below would report a " +
			"clean absence from a broken read")
	}
	for _, f := range fields {
		lower := strings.ToLower(f)
		// A wall-clock deadline is not a source position, but any name for
		// one contains "line" (dead-LINE), so it is excluded by name.
		if strings.Contains(lower, "deadline") {
			continue
		}
		if strings.Contains(lower, "line") || strings.Contains(lower, "pos") ||
			strings.Contains(lower, "loc") || strings.Contains(lower, "site") {
			t.Errorf("rt.Frame has a field %q, which looks positional. If the frame carries "+
				"a position there is a THIRD position mechanism and this file's account "+
				"of two is incomplete", f)
		}
	}
	t.Logf("rt.Frame carries %d fields and none is positional: %v", len(fields), fields)
}

// irOpFrameFields returns rt.Frame's field names.
func irOpFrameFields() ([]string, error) {
	files, _, err := irOpParse(filepath.Join("..", "..", "rt"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*goast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, isType := spec.(*goast.TypeSpec)
				if !isType || ts.Name.Name != "Frame" {
					continue
				}
				st, isStruct := ts.Type.(*goast.StructType)
				if !isStruct || st.Fields == nil {
					continue
				}
				for _, fld := range st.Fields.List {
					for _, n := range fld.Names {
						out = append(out, n.Name)
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// irOpRTSignatures maps every exported top-level rt function to its parameter
// list text.
func irOpRTSignatures() (map[string]string, error) {
	files, _, err := irOpParse(filepath.Join("..", "..", "rt"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*goast.FuncDecl)
			if !ok || fd.Recv != nil || !fd.Name.IsExported() {
				continue
			}
			var parts []string
			if fd.Type.Params != nil {
				for _, p := range fd.Type.Params.List {
					var names []string
					for _, n := range p.Names {
						names = append(names, n.Name)
					}
					parts = append(parts, strings.Join(names, ", ")+" "+irOpTypeText(p.Type))
				}
			}
			out[fd.Name.Name] = strings.Join(parts, ", ")
		}
	}
	return out, nil
}

func irOpTypeText(e goast.Expr) string {
	switch t := e.(type) {
	case *goast.Ident:
		return t.Name
	case *goast.StarExpr:
		return "*" + irOpTypeText(t.X)
	case *goast.SelectorExpr:
		return irOpTypeText(t.X) + "." + t.Sel.Name
	case *goast.ArrayType:
		return "[]" + irOpTypeText(t.Elt)
	case *goast.IndexExpr:
		return irOpTypeText(t.X) + "[" + irOpTypeText(t.Index) + "]"
	default:
		return "?"
	}
}
