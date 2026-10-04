package analysis

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// checkQualifiedTypeVisibility reports every type written as `file.Name`
// whose `Name` is not `pub` in that file. A top-level declaration is private
// to its file (spec §3), so naming another file's private type is the same
// error as calling its private function: "file 'other' has no member
// 'Hidden'".
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
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return
			}
			if v.CanInterface() {
				if qt, ok := v.Interface().(*ast.QualifiedType); ok {
					c.checkQualifiedTypeMember(qt, seen)
				}
			}
			v = v.Elem()
		}
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if field := v.Field(i); field.CanInterface() {
					walk(field)
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	for _, n := range nodes {
		walk(reflect.ValueOf(n))
	}
}

// checkQualifiedTypeMember reports qt when its leading segment is a file API
// object and the next segment names a declaration that file keeps private.
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
	if member.Public || member.SourceFile == c.fa.FilePath || seen[pos] {
		return
	}
	seen[pos] = true
	c.addError(pos.Line, pos.Col, fmt.Sprintf("file '%s' has no member '%s'", segs[0], name))
}
