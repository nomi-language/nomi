package lsp

import (
	"context"
	"encoding/json"
	"os"
	"sort"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Call hierarchy over functions: module functions, impl functions and
// host fns. A call is a call expression whose callee (`f`, `mod.f`,
// `Type.f`, a pipe stage `|> Type.f()`) resolves to the function; a
// function passed as a value is not a call. The callers are functions,
// impl functions, `test` blocks (each test of a `tests` group) and `once`
// initializers, across the project via the workspace index (references'
// file set: every open document and each closed file whose text holds
// the name).

// callItemData is what a CallHierarchyItem carries back to the server:
// the declaration's file and name position (1-based, byte columns).
type callItemData struct {
	URI  string `json:"uri"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Name string `json:"name"`
}

func (s *Server) textDocumentPrepareCallHierarchy(_ *glsp.Context, params *protocol.CallHierarchyPrepareParams) ([]protocol.CallHierarchyItem, error) {
	uri := string(params.TextDocument.URI)
	doc := s.docs.Snapshot(uri)
	if doc == nil || doc.Analysis == nil {
		return nil, nil
	}
	pos := analysis.Pos{
		Line: int(params.Position.Line) + 1,
		Col:  s.lines.get(uri, doc.Content).byteCol(params.Position.Line, params.Position.Character),
	}
	sym := doc.Analysis.SymbolAt(pos)
	if sym == nil {
		return nil, nil
	}
	item, ok := s.functionItem(callFiles{}, uri, sym)
	if !ok {
		return nil, nil
	}
	return []protocol.CallHierarchyItem{item}, nil
}

// functionItem is the call hierarchy item of the function sym names,
// reading the declaring file through files.
func (s *Server) functionItem(files callFiles, uri string, sym *analysis.Symbol) (protocol.CallHierarchyItem, bool) {
	target := sym
	if target.Resolved != nil {
		target = target.Resolved
	}
	if target.Kind != analysis.SymbolFunction || analysis.IsSynthesizedLine(target.Pos.Line) {
		return protocol.CallHierarchyItem{}, false
	}
	loc := s.definitionLocationForSymbol(uri, target, sym)
	if loc == nil {
		return protocol.CallHierarchyItem{}, false
	}
	at := analysis.Pos{Line: int(loc.Range.Start.Line) + 1, Col: int(loc.Range.Start.Character) + 1}
	return s.callItem(files, string(loc.URI), at, target.Name), true
}

// callable is a body calls are made from, or a function calls go to.
type callable struct {
	name    string
	kind    protocol.SymbolKind
	detail  string
	namePos analysis.Pos
	nameLen int
	span    ast.Span
	body    ast.Node
}

// callables lists a file's functions (top-level and in impl blocks), tests
// and `once` initializers.
func callables(nodes []ast.Node) []callable {
	var out []callable
	fn := func(f *ast.FuncDef, kind protocol.SymbolKind) {
		var body ast.Node
		if f.Body != nil {
			body = f.Body
		}
		out = append(out, callable{f.Name, kind, formatFuncDetail(f), analysis.Pos{Line: f.Line, Col: f.Col}, len(f.Name), f.Span, body})
	}
	host := func(f *ast.ExternFunc, kind protocol.SymbolKind) {
		out = append(out, callable{f.Name, kind, formatExternFuncDetail(f), analysis.Pos{Line: f.Line, Col: f.Col}, len(f.Name), f.Span, nil})
	}
	var test func(t *ast.TestDecl)
	test = func(t *ast.TestDecl) {
		if t.Group {
			if t.Body != nil {
				for _, st := range t.Body.Stmts {
					if c, ok := st.(*ast.TestDecl); ok {
						test(c)
					}
				}
			}
			return
		}
		at, n := analysis.Pos{Line: t.Line, Col: t.Col}, len("test")
		if t.NameLine > 0 {
			at, n = analysis.Pos{Line: t.NameLine, Col: t.NameCol}, len(t.Name)+2
		}
		var body ast.Node
		if t.Body != nil {
			body = t.Body
		}
		out = append(out, callable{`test "` + t.Name + `"`, protocol.SymbolKindFunction, "", at, n, t.Span, body})
	}
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.FuncDef:
			fn(v, protocol.SymbolKindFunction)
		case *ast.ExternFunc:
			host(v, protocol.SymbolKindFunction)
		case *ast.ImplBlock:
			for _, item := range v.Items {
				switch it := item.(type) {
				case *ast.FuncDef:
					fn(it, protocol.SymbolKindMethod)
				case *ast.ExternFunc:
					host(it, protocol.SymbolKindMethod)
				}
			}
		case *ast.TestDecl:
			test(v)
		case *ast.OnceBinding:
			out = append(out, callable{v.Name, protocol.SymbolKindConstant, "", analysis.Pos{Line: v.Line, Col: v.Col}, len(v.Name), v.Span, v.Value})
		}
	}
	return out
}

// callSites returns the callee name position of each call in body.
func callSites(body ast.Node) []analysis.Pos {
	if body == nil {
		return nil
	}
	var out []analysis.Pos
	analysis.WalkNodes(body, func(n ast.Node) {
		c, ok := n.(*ast.Call)
		if !ok {
			return
		}
		switch f := c.Func.(type) {
		case *ast.Ident:
			out = append(out, analysis.Pos{Line: f.Line, Col: f.Col})
		case *ast.FieldAccess:
			if f.Field != nil {
				out = append(out, analysis.Pos{Line: f.Field.Line, Col: f.Field.Col})
			}
		}
	})
	return out
}

// fileNodes is a file's parse and text: an open document's, else the
// file on disk's.
func (s *Server) fileNodes(uri string) ([]ast.Node, string, bool) {
	if s.docs.IsOpen(uri) {
		if snap := s.docs.Snapshot(uri); snap != nil {
			return snap.Nodes, snap.Content, true
		}
	}
	data, err := os.ReadFile(uriToPath(uri))
	if err != nil {
		return nil, "", false
	}
	nodes, _, _ := parser.ParseResilient(lexer.Lex(string(data)))
	return nodes, string(data), true
}

// callFile is one file's callables and line index.
type callFile struct {
	callables []callable
	lines     *lineIndex
}

// callFiles keeps, for one request, the files whose callables it reads,
// so call sites into one file read and index it once. The zero value
// keeps nothing.
type callFiles map[string]*callFile

func (s *Server) callFile(files callFiles, uri string) *callFile {
	if f, ok := files[uri]; ok {
		return f
	}
	nodes, content, _ := s.fileNodes(uri)
	f := &callFile{callables: callables(nodes), lines: newLineIndex(content)}
	if files != nil {
		files[uri] = f
	}
	return f
}

// callItem is the item of the callable named name at at in uri's file.
// A declaration the parse does not show gets its name as its range.
func (s *Server) callItem(files callFiles, uri string, at analysis.Pos, name string) protocol.CallHierarchyItem {
	f := s.callFile(files, uri)
	c := callable{name: name, kind: protocol.SymbolKindFunction, namePos: at, nameLen: len(name)}
	for _, cand := range f.callables {
		if cand.namePos == at {
			c = cand
			break
		}
	}
	return itemOf(uri, f.lines, c)
}

func itemOf(uri string, lines *lineIndex, c callable) protocol.CallHierarchyItem {
	sel := nameRange(c.namePos.Line, c.namePos.Col, c.nameLen)
	r := coverRange(spanRange(c.span), sel)
	item := protocol.CallHierarchyItem{
		Name:           c.name,
		Kind:           c.kind,
		URI:            protocol.DocumentUri(uri),
		Range:          lines.utf16Range(r),
		SelectionRange: lines.utf16Range(sel),
		Data:           callItemData{URI: uri, Line: c.namePos.Line, Col: c.namePos.Col, Name: c.name},
	}
	if c.detail != "" {
		d := c.detail
		item.Detail = &d
	}
	return item
}

// itemData reads an item's data, which arrives from the client as JSON.
func itemData(item protocol.CallHierarchyItem) (callItemData, bool) {
	var d callItemData
	raw, err := json.Marshal(item.Data)
	if err != nil || json.Unmarshal(raw, &d) != nil || d.URI == "" || d.Name == "" {
		return d, false
	}
	return d, true
}

func (s *Server) callHierarchyIncomingCalls(gctx *glsp.Context, params *protocol.CallHierarchyIncomingCallsParams) ([]protocol.CallHierarchyIncomingCall, error) {
	d, ok := itemData(params.Item)
	if !ok {
		return nil, nil
	}
	return s.incomingCalls(s.requestContext(gctx), d), nil
}

func (s *Server) incomingCalls(ctx context.Context, d callItemData) []protocol.CallHierarchyIncomingCall {
	id := symIdentity{Name: d.Name, File: uriToPath(d.URI), Pos: analysis.Pos{Line: d.Line, Col: d.Col}}
	root := s.docs.WorkspaceRoot()
	if root == "" {
		root = s.docs.FindProjectRoot(id.File)
	}
	var out []protocol.CallHierarchyIncomingCall
	for _, path := range findNomiFiles(root) {
		if ctx.Err() != nil {
			return nil
		}
		uri := pathToURI(path)
		if !s.docs.IsOpen(uri) {
			data, err := os.ReadFile(path)
			if err != nil || !containsAny(string(data), []string{d.Name}) {
				continue
			}
		}
		snap := s.docs.Analyzed(uri)
		if snap == nil || snap.Analysis == nil {
			continue
		}
		var lines *lineIndex
		for _, c := range callables(snap.Nodes) {
			var ranges []protocol.Range
			for _, at := range callSites(c.body) {
				ref, ok := snap.Analysis.References[at]
				if !ok || !matchesIdentity(ref, id) || !isFunctionRef(ref) {
					continue
				}
				if lines == nil {
					lines = newLineIndex(snap.Content)
				}
				ranges = append(ranges, lines.utf16Range(nameRange(at.Line, at.Col, len(d.Name))))
			}
			if len(ranges) > 0 {
				sortRanges(ranges)
				out = append(out, protocol.CallHierarchyIncomingCall{From: itemOf(uri, lines, c), FromRanges: ranges})
			}
		}
	}
	return out
}

func isFunctionRef(sym *analysis.Symbol) bool {
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	return sym.Kind == analysis.SymbolFunction
}

func (s *Server) callHierarchyOutgoingCalls(_ *glsp.Context, params *protocol.CallHierarchyOutgoingCallsParams) ([]protocol.CallHierarchyOutgoingCall, error) {
	d, ok := itemData(params.Item)
	if !ok {
		return nil, nil
	}
	return s.outgoingCalls(d), nil
}

// outgoingCalls are the functions the item's body calls, read from its
// file's analysis. A stdlib function's body is not analyzed here, so it
// has none.
func (s *Server) outgoingCalls(d callItemData) []protocol.CallHierarchyOutgoingCall {
	snap := s.docs.Analyzed(d.URI)
	if snap == nil || snap.Analysis == nil {
		return nil
	}
	at := analysis.Pos{Line: d.Line, Col: d.Col}
	var body ast.Node
	found := false
	for _, c := range callables(snap.Nodes) {
		if c.namePos == at {
			body, found = c.body, true
			break
		}
	}
	if !found {
		return nil
	}
	type group struct {
		item   protocol.CallHierarchyItem
		ranges []protocol.Range
	}
	type target struct {
		uri protocol.DocumentUri
		sel protocol.Range
	}
	byTarget := map[target]*group{}
	var order []target
	files := callFiles{}
	lines := newLineIndex(snap.Content)
	for _, site := range callSites(body) {
		ref, ok := snap.Analysis.References[site]
		if !ok || !isFunctionRef(ref) {
			continue
		}
		item, ok := s.functionItem(files, d.URI, ref)
		if !ok {
			continue
		}
		key := target{item.URI, item.SelectionRange}
		g := byTarget[key]
		if g == nil {
			g = &group{item: item}
			byTarget[key] = g
			order = append(order, key)
		}
		g.ranges = append(g.ranges, lines.utf16Range(nameRange(site.Line, site.Col, len(ref.Name))))
	}
	for _, g := range byTarget {
		sortRanges(g.ranges)
	}
	sort.SliceStable(order, func(i, j int) bool {
		return posBefore(byTarget[order[i]].ranges[0].Start, byTarget[order[j]].ranges[0].Start)
	})
	out := make([]protocol.CallHierarchyOutgoingCall, 0, len(order))
	for _, k := range order {
		g := byTarget[k]
		out = append(out, protocol.CallHierarchyOutgoingCall{To: g.item, FromRanges: g.ranges})
	}
	return out
}

func sortRanges(rs []protocol.Range) {
	sort.Slice(rs, func(i, j int) bool { return posBefore(rs[i].Start, rs[j].Start) })
}
