package analysis

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/ast"
	"strings"
)

// CheckDuplicateTestNames reports duplicate runnable test names within a
// module. The full name includes enclosing modules and tests groups, matching
// the runtime test runner's reporting path.
func CheckDuplicateTestNames(nodes []ast.Node) []TypeError {
	first := map[string]testNameSite{}
	var errs []TypeError
	checkDuplicateTestNames(nodes, nil, first, &errs)
	return errs
}

type testNameSite struct {
	line int
}

func checkDuplicateTestNames(nodes []ast.Node, prefix []string, first map[string]testNameSite, errs *[]TypeError) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.TestDecl:
			checkDuplicateTestDecl(v, prefix, first, errs)
		}
	}
}

func checkDuplicateTestDecl(decl *ast.TestDecl, prefix []string, first map[string]testNameSite, errs *[]TypeError) {
	names := appendTestName(prefix, decl.Name)
	if !decl.Group {
		fullName := strings.Join(names, " / ")
		if site, ok := first[fullName]; ok {
			line, col := decl.NameLine, decl.NameCol
			if line == 0 {
				line = decl.Line
			}
			if col == 0 {
				col = decl.Col
			}
			*errs = append(*errs, TypeError{
				Line:    line,
				Col:     col,
				Message: fmt.Sprintf("duplicate test name %q; first declared at line %d", fullName, site.line),
			})
			return
		}
		first[fullName] = testNameSite{line: decl.Line}
		return
	}
	for _, stmt := range decl.Body.Stmts {
		if child, ok := stmt.(*ast.TestDecl); ok {
			checkDuplicateTestDecl(child, names, first, errs)
		}
	}
}

func appendTestName(prefix []string, name string) []string {
	out := append([]string(nil), prefix...)
	if name != "" {
		out = append(out, name)
	}
	return out
}
