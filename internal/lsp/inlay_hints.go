package lsp

import (
	"sort"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// ---------------------------------------------------------------------------
// LSP 3.17 InlayHint types (not in glsp's protocol_3_16)
// ---------------------------------------------------------------------------

type InlayHintKind int

const (
	InlayHintKindType      InlayHintKind = 1
	InlayHintKindParameter InlayHintKind = 2
)

type InlayHintParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Range struct {
		Start struct {
			Line      uint32 `json:"line"`
			Character uint32 `json:"character"`
		} `json:"start"`
		End struct {
			Line      uint32 `json:"line"`
			Character uint32 `json:"character"`
		} `json:"end"`
	} `json:"range"`
}

type InlayHint struct {
	Position struct {
		Line      uint32 `json:"line"`
		Character uint32 `json:"character"`
	} `json:"position"`
	Label        string         `json:"label"`
	Kind         *InlayHintKind `json:"kind,omitempty"`
	Tooltip      string         `json:"tooltip,omitempty"`
	PaddingLeft  bool           `json:"paddingLeft,omitempty"`
	PaddingRight bool           `json:"paddingRight,omitempty"`
}

func inlayHintPos(line, col int) struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
} {
	l := uint32(0)
	if line > 0 {
		l = uint32(line - 1)
	}
	c := uint32(0)
	if col > 0 {
		c = uint32(col - 1)
	}
	return struct {
		Line      uint32 `json:"line"`
		Character uint32 `json:"character"`
	}{Line: l, Character: c}
}

// ---------------------------------------------------------------------------
// Hint collection
// ---------------------------------------------------------------------------

// collectInlayHints is every hint of the file under the default settings,
// without pipe-stage hints, which need the file's text.
func collectInlayHints(fa *analysis.FileAnalysis, nodes []ast.Node) []InlayHint {
	settings := defaultInlayHintSettings
	settings.PipeTypes = false
	return hintRequest{settings: settings}.collect(fa, nodes)
}

// hintRequest is one textDocument/inlayHint request: the settings, the
// analyzed text and its tokens (read only when a pipeline needs them), and
// the requested lines, 1-based and inclusive. endLine 0 means the whole file.
type hintRequest struct {
	settings  inlayHintSettings
	content   string
	tokens    func() *lexedText
	startLine int
	endLine   int
}

func (r hintRequest) collect(fa *analysis.FileAnalysis, nodes []ast.Node) []InlayHint {
	return r.run(fa, nodes).hints
}

// run walks the file and keeps the hints on the requested lines.
func (r hintRequest) run(fa *analysis.FileAnalysis, nodes []ast.Node) *hintCollector {
	c := &hintCollector{fa: fa, req: r}
	for _, node := range nodes {
		switch n := node.(type) {
		case *ast.FuncDef:
			c.walkFuncBody(n)
		case *ast.ImplBlock:
			for _, item := range n.Items {
				if fn, ok := item.(*ast.FuncDef); ok {
					c.walkFuncBody(fn)
				}
			}
		case *ast.OnceBinding:
			c.noteShownType(n)
			c.walkNode(n.Value)
		case *ast.TestDecl:
			c.walkNode(n)
		}
	}
	var out []InlayHint
	for _, h := range c.hints {
		if c.inRange(int(h.Position.Line)+1, int(h.Position.Line)+1) {
			out = append(out, h)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Position, out[j].Position
		return a.Line < b.Line || (a.Line == b.Line && a.Character < b.Character)
	})
	c.hints = out
	return c
}

type hintCollector struct {
	fa    *analysis.FileAnalysis
	req   hintRequest
	hints []InlayHint
	// text is req.tokens(), read on the first pipeline that needs it.
	text *lexedText
	// generic is set while walking the body of a function whose signature
	// names a type parameter (pipe_hints.go).
	generic bool
	// shownTypes holds, for a value whose type the text or another hint
	// already shows (pipe_hints.go), that type.
	shownTypes map[ast.Node]analysis.Type
	// pipelinesMeasured counts the pipelines whose stage lines were
	// located, for tests that pin the work a ranged request does.
	pipelinesMeasured int
}

// inRange reports whether lines first..last (1-based) meet the request's.
func (c *hintCollector) inRange(first, last int) bool {
	if c.req.endLine == 0 {
		return true
	}
	return last >= c.req.startLine && first <= c.req.endLine
}

func (c *hintCollector) addTypeHint(line, col int, typeName string) {
	if !c.req.settings.BindingTypes {
		return
	}
	kind := InlayHintKindType
	c.hints = append(c.hints, InlayHint{
		Position: inlayHintPos(line, col),
		Label:    ": " + typeName,
		Kind:     &kind,
	})
}

func (c *hintCollector) addParamHint(line, col int, paramName string) {
	if !c.req.settings.ParameterNames {
		return
	}
	kind := InlayHintKindParameter
	c.hints = append(c.hints, InlayHint{
		Position:     inlayHintPos(line, col),
		Label:        paramName + ":",
		Kind:         &kind,
		PaddingRight: true,
	})
}

func (c *hintCollector) walkFuncBody(fn *ast.FuncDef) {
	if fn.Body == nil {
		return
	}
	c.generic = len(fn.TypeParams) > 0
	if sym, ok := c.fa.Definitions[analysis.Pos{Line: fn.Line, Col: fn.Col}]; !ok || sym.Type == nil || analysis.ContainsTypeParam(sym.Type) {
		c.generic = true
	}
	defer func() { c.generic = false }()
	c.noteShownType(fn)
	for _, stmt := range fn.Body.Stmts {
		c.walkNode(stmt)
	}
}

func (c *hintCollector) walkNode(node ast.Node) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.Binding:
		c.hintBinding(n)
		c.noteShownType(n)
		c.walkNode(n.Value)

	case *ast.PatternBinding:
		c.walkNode(n.Value)
		for _, e := range n.ElseNodes() {
			c.walkNode(e)
		}

	case *ast.Call:
		c.hintCall(n)

	case *ast.Lambda:
		c.hintLambda(n)
		if n.Body != nil {
			for _, stmt := range n.Body.Stmts {
				c.walkNode(stmt)
			}
		}

	case *ast.Block:
		for _, stmt := range n.Stmts {
			c.walkNode(stmt)
		}

	case *ast.If:
		c.walkNode(n.Cond)
		c.walkNode(n.CondPattern)
		if n.Then != nil {
			for _, stmt := range n.Then.Stmts {
				c.walkNode(stmt)
			}
		}
		c.walkNode(n.Else)

	case *ast.Case:
		c.walkNode(n.Value)
		for _, branch := range n.Branches {
			c.walkNode(branch.Guard)
			c.walkNode(branch.Body)
		}

	case *ast.With:
		c.walkNode(n.Value)

	case *ast.Binary:
		if n.Op == "|>" {
			// The whole chain at once: its inner pipes are stages of
			// this pipeline, not pipelines of their own.
			head, stages := flattenPipe(n)
			c.hintPipeline(head, stages)
			c.walkNode(head)
			for _, st := range stages {
				c.walkNode(st.Right)
			}
			return
		}
		c.walkNode(n.Left)
		c.walkNode(n.Right)

	case *ast.GroupedExpr:
		c.walkNode(n.Expr)

	case *ast.NamedArg:
		c.walkNode(n.Value)

	case *ast.Assertion:
		c.walkNode(n.Expr)

	case *ast.Dbg:
		c.walkNode(n.Expr)

	case *ast.TestDecl:
		c.walkNode(n.Setup)
		if n.Body != nil {
			for _, stmt := range n.Body.Stmts {
				c.walkNode(stmt)
			}
		}

	case *ast.Unary:
		c.walkNode(n.Right)

	case *ast.Return:
		c.walkNode(n.Value)

	case *ast.ExprStmt:
		c.walkNode(n.Expr)

	case *ast.FieldAccess:
		c.walkNode(n.Object)

	case *ast.ListLit:
		for _, item := range n.Items {
			c.walkNode(item)
		}

	case *ast.VectorLit:
		for _, item := range n.Items {
			c.walkNode(item)
		}

	case *ast.SetLit:
		for _, item := range n.Items {
			c.walkNode(item)
		}

	case *ast.TupleLit:
		for _, item := range n.Items {
			c.walkNode(item)
		}

	case *ast.MapLit:
		for _, entry := range n.Entries {
			c.walkNode(entry.Key)
			c.walkNode(entry.Value)
		}

	case *ast.StructLit:
		for _, f := range n.Fields {
			c.walkNode(f.Value)
		}

	case *ast.TryOp:
		if n.Expr != nil {
			c.walkNode(n.Expr)
		}

	case *ast.StringInterp:
		// Walk Dynamic slots so inlay hints fire on identifiers /
		// nested calls inside `${...}`. Static parts have no
		// expressions to hint.
		for _, part := range n.Parts {
			if se, ok := part.(ast.StringExpr); ok {
				c.walkNode(se.Expr)
			}
		}

	case *ast.TaggedString:
		// Typed literals have the same Static/Dynamic interleave;
		// walk slot expressions for the same reason as StringInterp.
		// Raw-typed literals are a single Static fragment with no
		// expression nodes — the loop is a no-op for those.
		for _, part := range n.Parts {
			if se, ok := part.(ast.StringExpr); ok {
				c.walkNode(se.Expr)
			}
		}
	}
}

// hintBinding emits a type hint for an unannotated binding whose type was inferred.
func (c *hintCollector) hintBinding(n *ast.Binding) {
	pos := analysis.Pos{Line: n.Line, Col: n.Col}
	sym, ok := c.fa.Definitions[pos]
	if !ok || sym.Type == nil {
		return
	}
	// Position after the binding name.
	c.addTypeHint(n.Line, n.Col+len(n.Name), sym.Type.String())
}

// hintCall emits parameter name hints for positional arguments and recurses into args.
func (c *hintCollector) hintCall(n *ast.Call) {
	// Always walk args for nested hints.
	for _, arg := range n.Args {
		c.walkNode(arg)
	}

	// Resolve the called function to get param names.
	params := c.resolveCallParams(n)
	if params == nil {
		return
	}

	for i, arg := range n.Args {
		// Skip named args — they already show the param name.
		if _, ok := arg.(*ast.NamedArg); ok {
			continue
		}
		if i >= len(params) {
			break
		}
		line, col := nodePosition(arg)
		if col == 0 {
			continue
		}
		c.addParamHint(line, col, params[i].Name)
	}
}

// resolveCallParams finds the parameter list for a Call's target function.
// Works for both FuncDef and ExternFunc.
func (c *hintCollector) resolveCallParams(n *ast.Call) []ast.Param {
	var sym *analysis.Symbol

	switch fn := n.Func.(type) {
	case *ast.Ident:
		pos := analysis.Pos{Line: fn.Line, Col: fn.Col}
		sym = c.fa.References[pos]
	case *ast.TypeIdent:
		pos := analysis.Pos{Line: fn.Line, Col: fn.Col}
		sym = c.fa.References[pos]
	case *ast.FieldAccess:
		if fn.Field != nil {
			pos := analysis.Pos{Line: fn.Field.Line, Col: fn.Field.Col}
			sym = c.fa.References[pos]
		}
	}

	if sym == nil {
		return nil
	}
	// Follow resolved symbol (e.g. imports).
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	switch node := sym.Node.(type) {
	case *ast.FuncDef:
		return node.Params
	case *ast.ExternFunc:
		return node.Params
	}
	return nil
}

// hintLambda emits type hints for lambda params whose types were inferred.
func (c *hintCollector) hintLambda(n *ast.Lambda) {
	for _, p := range n.Params {
		if p.TypeAnnotation != nil {
			continue // already annotated
		}
		// Skip implicit 'it' params — they're synthetic and not written in source.
		// Detected by: param named "it" at the same position as the lambda's '{'.
		if p.Name == "it" && p.Line == n.Line && p.Col == n.Col {
			continue
		}
		pos := analysis.Pos{Line: p.Line, Col: p.Col}
		sym, ok := c.fa.Definitions[pos]
		if !ok || sym.Type == nil {
			continue
		}
		c.addTypeHint(p.Line, p.Col+len(p.Name), sym.Type.String())
	}
}

// nodePosition extracts Line and Col from any AST node.
func nodePosition(node ast.Node) (int, int) {
	switch n := node.(type) {
	case *ast.Ident:
		return n.Line, n.Col
	case *ast.TypeIdent:
		return n.Line, n.Col
	case *ast.IntLit:
		return n.Line, n.Col
	case *ast.FloatLit:
		return n.Line, n.Col
	case *ast.DecimalLit:
		return n.Line, n.Col
	case *ast.CodepointLit:
		return n.Line, n.Col
	case *ast.StringLit:
		return n.Line, n.Col
	case *ast.StringInterp:
		return n.Line, n.Col
	case *ast.Lambda:
		return n.Line, n.Col
	case *ast.ListLit:
		return n.Line, n.Col
	case *ast.VectorLit:
		return n.Line, n.Col
	case *ast.SetLit:
		return n.Line, n.Col
	case *ast.TupleLit:
		return n.Line, n.Col
	case *ast.MapLit:
		return n.Line, n.Col
	case *ast.Call:
		// Call.Line/Col is at the '(', recurse to get start of callee.
		return nodePosition(n.Func)
	case *ast.Unary:
		return n.Line, n.Col
	case *ast.Binary:
		return n.Line, n.Col
	case *ast.Block:
		return n.Line, n.Col
	case *ast.If:
		return n.Line, n.Col
	case *ast.With:
		return n.Line, n.Col
	case *ast.Case:
		return n.Line, n.Col
	case *ast.StructLit:
		return n.Line, n.Col
	case *ast.FieldAccess:
		// FieldAccess.Line/Col is at the '.', recurse to get start of object.
		return nodePosition(n.Object)
	case *ast.Placeholder:
		return n.Line, n.Col
	case *ast.NamedArg:
		return n.Line, n.Col
	default:
		return node.LineNum(), 0
	}
}
