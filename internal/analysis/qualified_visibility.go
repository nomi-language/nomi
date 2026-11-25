package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// checkQualifiedTypeVisibility reports every type written as `file.Name`
// whose `Name` this file does not see (FileAnalysis.Sees, spec §14
// *Visibility*), with the error calling such a function gets:
// "`Hidden` is private to `other.nomi`".
//
// A file-qualified type is spelled as an *ast.QualifiedType wherever it
// appears: an annotation, a struct literal's head (`other.Hidden{v: 1}`), a
// pattern's head, an interface bound, an `impl other.Iface for ...` header,
// and a type argument (`List<other.Hidden>`). The walk visits every one of
// them in the file's AST rather than relying on each resolution path to
// remember the rule. The expression spellings (`other.helper()`,
// `other.Color.Red` as a value) are FieldAccess nodes, which
// checkModuleMemberVisibility covers, and selective imports are
// checkImportVisibility's.
func (c *checker) checkQualifiedTypeVisibility(nodes []ast.Node) {
	if c == nil || c.fa == nil || c.fa.ModuleScope == nil {
		return
	}
	seen := make(map[Pos]bool)
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			if qt, ok := n.(*ast.QualifiedType); ok {
				c.checkQualifiedTypeMember(qt, seen)
			}
			return true
		})
	}
}

// checkQualifiedTypeMember reports qt when its leading segment is a file API
// object and the next segment names a declaration this file does not see.
// The private name sits inside qt.Module when the module path is dotted
// (`other.Color.Red` holds Module "other.Color"), and is the member
// otherwise (`other.Hidden`).
func (c *checker) checkQualifiedTypeMember(qt *ast.QualifiedType, seen map[Pos]bool) {
	if qt == nil || IsSynthesizedLine(qt.ModuleLine) {
		return
	}
	segs := strings.Split(qt.Module, ".")
	fileSym := c.fa.ModuleScope.Lookup(segs[0])
	if fileSym == nil {
		return
	}
	if fileSym.Resolved != nil {
		fileSym = fileSym.Resolved
	}
	if fileSym.Kind != SymbolModule || fileSym.ModuleScope == nil {
		return
	}
	var name string
	var pos Pos
	if len(segs) > 1 {
		name = segs[1]
		pos = Pos{Line: qt.ModuleLine, Col: qt.ModuleCol + len(segs[0]) + 1}
	} else {
		switch m := qt.Member.(type) {
		case *ast.SimpleType:
			name, pos = m.Name, Pos{Line: m.Line, Col: m.Col}
		case *ast.GenericType:
			name, pos = m.Name, Pos{Line: m.Line, Col: m.Col}
		default:
			return
		}
	}
	member := fileSym.ModuleScope.LookupLocal(name)
	if member == nil {
		return
	}
	if member.Resolved != nil {
		member = member.Resolved
	}
	if seen[pos] || c.fa.Sees(member) || isHiddenImportAlias(segs[0]) {
		return
	}
	seen[pos] = true
	c.report(c.fa.unseen(c.fa.Program().Packages, member, name, pos.Line, pos.Col))
}
