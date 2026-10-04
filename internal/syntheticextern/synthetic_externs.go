package syntheticextern

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Decl is a hidden host declaration supplied by the wrapper path. ffirun uses
// it to expose source-level Go bindings to analysis when a loaded file
// does not already contain the matching declaration.
type Decl struct {
	Key        string
	Source     string
	SourceFile string
	SourceLine int
	SourceCol  int
	SourceSpan int
	OwnerFile  string
	OwnerLine  int
	OwnerCol   int
	OwnerSpan  int
}

// Inject appends hidden host declarations to the AST slice. Only bare keys
// are injected, and only for the entry file. Owner-qualified keys such as
// `Sqlite.open_raw` are not injected: their declarations already sit in the
// owning type's body in source, which is the lookup path they resolve on.
func Inject(nodes []ast.Node, decls []Decl, includeBare bool) ([]ast.Node, error) {
	if len(decls) == 0 {
		return nodes, nil
	}
	parsed := make(map[string]ast.Node, len(decls))
	for i, decl := range decls {
		if decl.Key == "" || strings.TrimSpace(decl.Source) == "" {
			continue
		}
		n, err := parseDecl(decl, syntheticExternLineBase+i*syntheticExternLineStride)
		if err != nil {
			return nil, err
		}
		parsed[decl.Key] = n
	}
	if includeBare {
		nodes = appendForOwner(nodes, "", decls, parsed)
	}
	return nodes, nil
}

func appendForOwner(nodes []ast.Node, owner string, decls []Decl, parsed map[string]ast.Node) []ast.Node {
	existing := existingExternNames(nodes)
	for _, decl := range decls {
		declOwner, name := splitKey(decl.Key)
		if declOwner != owner || name == "" || existing[name] {
			continue
		}
		n := parsed[decl.Key]
		if n == nil {
			continue
		}
		nodes = append(nodes, n)
		existing[name] = true
	}
	return nodes
}

const (
	// analysis.IsSynthesizedLine treats every line >= 1<<30 as compiler-owned.
	// Keep hidden extern parser positions in that band so editor surfaces that
	// walk Definitions/References never paint or navigate invisible text.
	syntheticExternLineBase   = 1 << 30
	syntheticExternLineStride = 1024
)

func parseDecl(decl Decl, lineBase int) (ast.Node, error) {
	tokens := lexer.Lex(strings.TrimSpace(decl.Source) + "\n")
	nodes, err := parser.Parse(tokens)
	if err != nil {
		return nil, fmt.Errorf("synthetic extern %q: %w", decl.Key, err)
	}
	if len(nodes) != 1 {
		return nil, fmt.Errorf("synthetic extern %q: expected one declaration, got %d", decl.Key, len(nodes))
	}
	switch n := nodes[0].(type) {
	case *ast.ExternFunc:
		stampSyntheticExternFuncPositions(n, lineBase)
		n.DefinitionFile = decl.SourceFile
		n.DefinitionLine = decl.SourceLine
		n.DefinitionCol = decl.SourceCol
		n.DefinitionSpan = decl.SourceSpan
		return n, nil
	case *ast.ExternType:
		stampSyntheticExternTypePositions(n, lineBase)
		n.DefinitionFile = decl.SourceFile
		n.DefinitionLine = decl.SourceLine
		n.DefinitionCol = decl.SourceCol
		n.DefinitionSpan = decl.SourceSpan
		return nodes[0], nil
	default:
		return nil, fmt.Errorf("synthetic extern %q: expected extern declaration, got %s", decl.Key, nodes[0].NodeType())
	}
}

type syntheticPosState struct {
	line int
	col  int
}

func (s *syntheticPosState) bump() (int, int) {
	s.col++
	if s.col > 1023 {
		s.line++
		s.col = 1
	}
	return s.line, s.col
}

func stampSyntheticExternFuncPositions(n *ast.ExternFunc, lineBase int) {
	state := syntheticPosState{line: lineBase}
	n.Line, n.Col = state.bump()
	for i := range n.TypeParams {
		stampSyntheticTypeParam(&state, &n.TypeParams[i])
	}
	for i := range n.Params {
		n.Params[i].Line, n.Params[i].Col = state.bump()
		stampSyntheticTypeExpr(&state, n.Params[i].TypeAnnotation)
	}
	stampSyntheticTypeExpr(&state, n.ReturnTypeExpr)
	for i := range n.WhereClauses {
		stampSyntheticWhere(&state, &n.WhereClauses[i])
	}
}

func stampSyntheticExternTypePositions(n *ast.ExternType, lineBase int) {
	state := syntheticPosState{line: lineBase}
	n.Line, n.Col = state.bump()
	for i := range n.TypeParams {
		stampSyntheticTypeParam(&state, &n.TypeParams[i])
	}
	for i := range n.WhereClauses {
		stampSyntheticWhere(&state, &n.WhereClauses[i])
	}
}

func stampSyntheticTypeParam(state *syntheticPosState, tp *ast.TypeParam) {
	tp.Line, tp.Col = state.bump()
	for _, bound := range tp.Bounds {
		stampSyntheticTypeExpr(state, bound)
	}
}

func stampSyntheticWhere(state *syntheticPosState, wc *ast.WhereConstraint) {
	wc.Line, wc.Col = state.bump()
	for _, bound := range wc.Bounds {
		stampSyntheticTypeExpr(state, bound)
	}
}

func stampSyntheticTypeExpr(state *syntheticPosState, expr ast.TypeExpr) {
	switch t := expr.(type) {
	case *ast.SimpleType:
		t.Line, t.Col = state.bump()
	case *ast.QualifiedType:
		t.ModuleLine, t.ModuleCol = state.bump()
		stampSyntheticTypeExpr(state, t.Member)
	case *ast.GenericType:
		t.Line, t.Col = state.bump()
		for _, param := range t.Params {
			stampSyntheticTypeExpr(state, param)
		}
	case *ast.FuncType:
		t.Line, t.Col = state.bump()
		for _, param := range t.Params {
			stampSyntheticTypeExpr(state, param)
		}
		stampSyntheticTypeExpr(state, t.Return)
	}
}

func existingExternNames(nodes []ast.Node) map[string]bool {
	out := make(map[string]bool)
	for _, n := range nodes {
		switch d := n.(type) {
		case *ast.ExternFunc:
			out[d.Name] = true
		case *ast.ExternType:
			out[d.Name] = true
		}
	}
	return out
}

func splitKey(key string) (owner, name string) {
	if dot := strings.LastIndexByte(key, '.'); dot >= 0 {
		return key[:dot], key[dot+1:]
	}
	return "", key
}
