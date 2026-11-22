package irbuild

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The builder lowers what the checker checked and recorded. Each AST node
// it constructs (a literal of a node type, or a copy of a checked node
// with a field changed) is code the checker never saw, which reaches what
// the checker recorded only through a carrier: carryCall and carryType
// give it the record of the source it stands for, carryPattern a pattern's,
// and markUnchecked marks a body the checker never checks (package doc,
// "Calls"). These counts only go down.
var (
	// maxASTConstructionSites is how many places in this package's non-test
	// files construct a node of each internal/ast type: a composite literal
	// of it (`&ast.Call{...}`, `ast.Span{...}`, an element of a
	// `[]*ast.Ident{...}`) and a copy of a node through a pointer
	// (`call := *t`). A type with no entry is held at 0.
	maxASTConstructionSites = map[string]int{
		"Binary":             8,
		"Block":              5,
		"Call":               46,
		"CaseBranch":         2,
		"EnumPattern":        2,
		"ExprStmt":           3,
		"FieldAccess":        18,
		"FuncDef":            9,
		"Ident":              25,
		"If":                 2,
		"IntLit":             1,
		"Lambda":             5,
		"ListLit":            6,
		"ListPattern":        2,
		"MapLit":             4,
		"MapPattern":         2,
		"NamedArg":           4,
		"Param":              9,
		"PatternDestructure": 1,
		"Placeholder":        1,
		"Span":               3,
		"StringText":         3,
		"StructFieldVal":     2,
		"StructLit":          4,
		"StructPattern":      1,
		"TupleLit":           2,
		"TypeIdent":          23,
		"Unary":              1,
	}
	// maxUncarriedCallLiterals is how many of the `ast.Call` literals sit in
	// a function that gives the call no record: no carrier (astCarriers)
	// names it, or names a node it is part of.
	maxUncarriedCallLiterals = 15
)

// astCarriers are the functions that give a node the builder made what
// the checker recorded for the source it stands for, or mark it as code
// the checker never checked.
var astCarriers = map[string]bool{
	"carryCall":          true,
	"carryType":          true,
	"carryRefResult":     true,
	"carryPattern":       true,
	"carryPatternAnswer": true,
	"markUnchecked":      true,
}

// TestASTConstructionSitesOnlyGoDown counts the AST nodes this package's
// non-test files construct, per node type, and the `ast.Call` literals
// that carry no record, and fails when a count rises, or falls without its
// maximum following it.
func TestASTConstructionSitesOnlyGoDown(t *testing.T) {
	sites, err := astConstructionSites(".")
	if err != nil {
		t.Fatal(err)
	}
	byType := map[string][]string{}
	copies := map[string]int{}
	var uncarried, unresolved []string
	for _, s := range sites {
		switch {
		case s.unresolved:
			unresolved = append(unresolved, s.at+": "+s.what)
		default:
			byType[s.typ] = append(byType[s.typ], s.at+": "+s.what)
			if s.copy {
				copies[s.typ]++
			}
			if s.typ == "Call" && !s.copy && !s.carried {
				uncarried = append(uncarried, s.at+" ("+s.fn+")")
			}
		}
	}
	if len(unresolved) > 0 {
		sort.Strings(unresolved)
		t.Errorf("%d copies through a pointer copy a value this test cannot type: give the copied variable a type it reads "+
			"(a parameter, a type assertion, a composite literal, a function's result):\n  %s", len(unresolved), strings.Join(unresolved, "\n  "))
	}
	for typ := range maxASTConstructionSites {
		if len(byType[typ]) == 0 && maxASTConstructionSites[typ] != 0 {
			t.Errorf("no site constructs an ast.%s: remove its maximum", typ)
		}
	}
	var lines []string
	for typ, list := range byType {
		lines = append(lines, fmt.Sprintf("%s: %d (%d copies)", typ, len(list), copies[typ]))
		switch max := maxASTConstructionSites[typ]; {
		case len(list) > max:
			sort.Strings(list)
			t.Errorf("%d sites construct an ast.%s, and at most %d may: lower the source the checker checked, or give the node "+
				"the record of the source it stands for (carryCall, carryType, carryPattern):\n  %s",
				len(list), typ, max, strings.Join(list, "\n  "))
		case len(list) < max:
			t.Errorf("%d sites construct an ast.%s: lower its maximum in maxASTConstructionSites to %d", len(list), typ, len(list))
		}
	}
	switch {
	case len(uncarried) > maxUncarriedCallLiterals:
		sort.Strings(uncarried)
		t.Errorf("%d ast.Call literals carry no record, and at most %d may: give each the record of the call it stands for (carryCall):\n  %s",
			len(uncarried), maxUncarriedCallLiterals, strings.Join(uncarried, "\n  "))
	case len(uncarried) < maxUncarriedCallLiterals:
		t.Errorf("%d ast.Call literals carry no record: lower maxUncarriedCallLiterals to %d", len(uncarried), len(uncarried))
	}
	sort.Strings(lines)
	t.Logf("AST construction sites by node type: %s; ast.Call literals with no record: %d", strings.Join(lines, ", "), len(uncarried))
}

// astSite is one place that constructs a node of an internal/ast type.
type astSite struct {
	at, fn, what string
	// typ is the node type's name, "" for a copy of a value that is no node.
	typ string
	// copy is a copy through a pointer rather than a literal.
	copy bool
	// carried is, for a literal, whether a carrier in its function names it.
	carried bool
	// unresolved is a copy whose value's type the test cannot tell.
	unresolved bool
}

// astConstructionSites lists the sites in dir's non-test Go files.
func astConstructionSites(dir string) ([]astSite, error) {
	fset := token.NewFileSet()
	pkg, err := parseGoDir(fset, dir)
	if err != nil {
		return nil, err
	}
	astPkg, err := parseGoDir(fset, filepath.Join(dir, "..", "ast"))
	if err != nil {
		return nil, err
	}
	r := &goTypes{local: goTypeDecls(pkg), ast: goTypeDecls(astPkg), funcs: map[string][]*ast.FuncDecl{}}
	for _, f := range pkg {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				r.funcs[fd.Name.Name] = append(r.funcs[fd.Name.Name], fd)
			}
		}
	}
	var sites []astSite
	for _, f := range pkg {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			sites = append(sites, r.functionSites(fset, fd)...)
		}
	}
	return sites, nil
}

// parseGoDir parses dir's non-test Go files.
func parseGoDir(fset *token.FileSet, dir string) ([]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// goTypeDecls maps each struct type the files declare to its fields' types.
func goTypeDecls(files []*ast.File) map[string]map[string]ast.Expr {
	out := map[string]map[string]ast.Expr{}
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, s := range gd.Specs {
				ts := s.(*ast.TypeSpec)
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				fields := map[string]ast.Expr{}
				for _, fl := range st.Fields.List {
					for _, n := range fl.Names {
						fields[n.Name] = fl.Type
					}
				}
				out[ts.Name.Name] = fields
			}
		}
	}
	return out
}

// goTypes types an expression of this package from its declarations alone:
// enough to tell what a copy through a pointer copies.
type goTypes struct {
	local, ast map[string]map[string]ast.Expr
	funcs      map[string][]*ast.FuncDecl
}

// astTypeName is the internal/ast type e names (`ast.Call`, `*ast.Call`),
// or "".
func astTypeName(e ast.Expr) string {
	if s, ok := e.(*ast.StarExpr); ok {
		e = s.X
	}
	if sel, ok := e.(*ast.SelectorExpr); ok {
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == "ast" {
			return sel.Sel.Name
		}
	}
	return ""
}

// functionSites lists the construction sites in fd.
func (r *goTypes) functionSites(fset *token.FileSet, fd *ast.FuncDecl) []astSite {
	var sites []astSite
	carried := r.carriedNames(fd)
	var stack []ast.Node
	ast.Inspect(fd, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		at := fset.Position(n.Pos())
		where := fmt.Sprintf("%s:%d", filepath.Base(at.Filename), at.Line)
		switch n := n.(type) {
		case *ast.CompositeLit:
			typ := astTypeName(n.Type)
			if n.Type == nil {
				typ = elidedAstType(stack)
			}
			if typ == "" {
				return true
			}
			s := astSite{at: where, fn: fd.Name.Name, what: "ast." + typ + " literal", typ: typ}
			if typ == "Call" {
				s.carried = literalCarried(stack, carried)
			}
			sites = append(sites, s)
		case *ast.AssignStmt:
			if len(n.Lhs) != 1 || len(n.Rhs) != 1 {
				return true
			}
			if star, ok := n.Rhs[0].(*ast.StarExpr); ok {
				sites = append(sites, r.copySite(fd, star, n.Pos(), where))
			}
		case *ast.ValueSpec:
			if len(n.Values) == 1 {
				if star, ok := n.Values[0].(*ast.StarExpr); ok {
					sites = append(sites, r.copySite(fd, star, n.Pos(), where))
				}
			}
		}
		return true
	})
	var out []astSite
	for _, s := range sites {
		if s.copy && s.typ == "" && !s.unresolved {
			continue
		}
		out = append(out, s)
	}
	return out
}

// copySite is the site of a copy through the pointer star.X.
func (r *goTypes) copySite(fd *ast.FuncDecl, star *ast.StarExpr, pos token.Pos, where string) astSite {
	s := astSite{at: where, fn: fd.Name.Name, what: "a copy of *" + types.ExprString(star.X), copy: true}
	ty := r.typeOf(fd, star.X, pos, 0)
	if ty == nil {
		s.unresolved = true
		return s
	}
	ptr, isPtr := ty.(*ast.StarExpr)
	if !isPtr {
		s.unresolved = true
		return s
	}
	if name := astTypeName(ptr.X); name != "" {
		s.typ = name
		s.what = "a copy of an ast." + name
	}
	return s
}

// elidedAstType is the internal/ast type of a composite literal written
// without its type, as an element or a map value of a literal whose type
// names it, and "" for any other.
func elidedAstType(stack []ast.Node) string {
	for i := len(stack) - 2; i >= 0; i-- {
		switch p := stack[i].(type) {
		case *ast.KeyValueExpr:
			continue
		case *ast.CompositeLit:
			switch t := p.Type.(type) {
			case *ast.ArrayType:
				return astTypeName(t.Elt)
			case *ast.MapType:
				return astTypeName(t.Value)
			}
			return ""
		default:
			return ""
		}
	}
	return ""
}

// carriedNames is the text of each first argument a carrier in fd is
// called with.
func (r *goTypes) carriedNames(fd *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(fd, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok || len(c.Args) == 0 {
			return true
		}
		name := ""
		switch f := c.Fun.(type) {
		case *ast.SelectorExpr:
			name = f.Sel.Name
		case *ast.Ident:
			name = f.Name
		}
		if astCarriers[name] {
			out[types.ExprString(stripAddr(c.Args[0]))] = true
		}
		return true
	})
	return out
}

func stripAddr(e ast.Expr) ast.Expr {
	for {
		switch x := e.(type) {
		case *ast.UnaryExpr:
			if x.Op == token.AND {
				e = x.X
				continue
			}
		case *ast.ParenExpr:
			e = x.X
			continue
		}
		return e
	}
}

// literalCarried reports whether the literal on top of stack, or a literal
// it is part of, is passed to a carrier: directly, or through the name it
// is bound to.
func literalCarried(stack []ast.Node, carried map[string]bool) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		child := stack[i+1]
		switch p := stack[i].(type) {
		case *ast.UnaryExpr, *ast.ParenExpr, *ast.KeyValueExpr, *ast.CompositeLit:
			continue
		case *ast.CallExpr:
			if e, ok := child.(ast.Expr); ok && carried[types.ExprString(stripAddr(e))] {
				return true
			}
			name := ""
			switch f := p.Fun.(type) {
			case *ast.SelectorExpr:
				name = f.Sel.Name
			case *ast.Ident:
				name = f.Name
			}
			if astCarriers[name] {
				return true
			}
			// An argument of another call: a slice built for it, say.
			continue
		case *ast.AssignStmt:
			for j, rhs := range p.Rhs {
				if rhs == child && j < len(p.Lhs) {
					return carried[types.ExprString(p.Lhs[j])]
				}
			}
			return false
		case *ast.ValueSpec:
			for j, v := range p.Values {
				if v == child && j < len(p.Names) {
					return carried[p.Names[j].Name]
				}
			}
			return false
		default:
			return false
		}
	}
	return false
}

// typeOf is the type expression of e in fd at pos, from the declarations
// this package and internal/ast write, or nil.
func (r *goTypes) typeOf(fd *ast.FuncDecl, e ast.Expr, pos token.Pos, depth int) ast.Expr {
	if depth > 8 {
		return nil
	}
	switch e := e.(type) {
	case *ast.ParenExpr:
		return r.typeOf(fd, e.X, pos, depth+1)
	case *ast.Ident:
		return r.identType(fd, e.Name, pos, depth)
	case *ast.SelectorExpr:
		xt := r.typeOf(fd, e.X, pos, depth+1)
		if xt == nil {
			return nil
		}
		if s, ok := xt.(*ast.StarExpr); ok {
			xt = s.X
		}
		var fields map[string]ast.Expr
		if name := astTypeName(xt); name != "" {
			fields = r.ast[name]
		} else if id, ok := xt.(*ast.Ident); ok {
			fields = r.local[id.Name]
		}
		ft := fields[e.Sel.Name]
		if ft == nil {
			return nil
		}
		if name := astTypeName(xt); name != "" {
			return qualifyAst(ft)
		}
		return ft
	case *ast.CallExpr:
		return r.resultType(e)
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			if t := r.typeOf(fd, e.X, pos, depth+1); t != nil {
				return &ast.StarExpr{X: t}
			}
		}
	case *ast.StarExpr:
		if t, ok := r.typeOf(fd, e.X, pos, depth+1).(*ast.StarExpr); ok {
			return t.X
		}
	case *ast.CompositeLit:
		return e.Type
	case *ast.TypeAssertExpr:
		return e.Type
	case *ast.IndexExpr:
		switch t := r.typeOf(fd, e.X, pos, depth+1).(type) {
		case *ast.ArrayType:
			return t.Elt
		case *ast.MapType:
			return t.Value
		}
	}
	return nil
}

// qualifyAst is an internal/ast field's type as this package names it.
func qualifyAst(t ast.Expr) ast.Expr {
	switch x := t.(type) {
	case *ast.Ident:
		if x.IsExported() {
			return &ast.SelectorExpr{X: ast.NewIdent("ast"), Sel: x}
		}
	case *ast.StarExpr:
		return &ast.StarExpr{X: qualifyAst(x.X)}
	case *ast.ArrayType:
		return &ast.ArrayType{Elt: qualifyAst(x.Elt)}
	}
	return t
}

// resultType is the first result's type of the function c calls, when
// every function of this package of its name declares the same one.
func (r *goTypes) resultType(c *ast.CallExpr) ast.Expr {
	name := ""
	switch f := c.Fun.(type) {
	case *ast.SelectorExpr:
		name = f.Sel.Name
	case *ast.Ident:
		name = f.Name
	}
	var out ast.Expr
	for _, fd := range r.funcs[name] {
		if fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
			return nil
		}
		t := fd.Type.Results.List[0].Type
		if out != nil && types.ExprString(out) != types.ExprString(t) {
			return nil
		}
		out = t
	}
	return out
}

// identType is the type of the variable name declared nearest before pos
// in fd: a receiver, a parameter, a result, an assignment's, a range's or a
// type switch's.
func (r *goTypes) identType(fd *ast.FuncDecl, name string, pos token.Pos, depth int) ast.Expr {
	var best ast.Expr
	bestPos := token.NoPos
	consider := func(at token.Pos, t func() ast.Expr) {
		if at < pos && at > bestPos {
			if ty := t(); ty != nil {
				best, bestPos = ty, at
			}
		}
	}
	fields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				if n.Name == name {
					ft := f.Type
					consider(n.Pos(), func() ast.Expr { return ft })
				}
			}
		}
	}
	fields(fd.Recv)
	fields(fd.Type.Params)
	fields(fd.Type.Results)
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			fields(n.Type.Params)
		case *ast.AssignStmt:
			if n.Tok != token.DEFINE {
				return true
			}
			for i, l := range n.Lhs {
				id, ok := l.(*ast.Ident)
				if !ok || id.Name != name {
					continue
				}
				switch {
				case len(n.Rhs) == len(n.Lhs):
					rhs := n.Rhs[i]
					consider(id.Pos(), func() ast.Expr { return r.typeOf(fd, rhs, id.Pos(), depth+1) })
				case len(n.Rhs) == 1 && i == 0:
					rhs := n.Rhs[0]
					consider(id.Pos(), func() ast.Expr {
						if ta, ok := rhs.(*ast.TypeAssertExpr); ok {
							return ta.Type
						}
						return r.typeOf(fd, rhs, id.Pos(), depth+1)
					})
				}
			}
		case *ast.ValueSpec:
			for i, id := range n.Names {
				if id.Name != name {
					continue
				}
				if n.Type != nil {
					t := n.Type
					consider(id.Pos(), func() ast.Expr { return t })
				} else if i < len(n.Values) {
					v := n.Values[i]
					consider(id.Pos(), func() ast.Expr { return r.typeOf(fd, v, id.Pos(), depth+1) })
				}
			}
		case *ast.RangeStmt:
			if id, ok := n.Value.(*ast.Ident); ok && id.Name == name {
				x := n.X
				consider(id.Pos(), func() ast.Expr {
					switch t := r.typeOf(fd, x, id.Pos(), depth+1).(type) {
					case *ast.ArrayType:
						return t.Elt
					case *ast.MapType:
						return t.Value
					}
					return nil
				})
			}
		case *ast.TypeSwitchStmt:
			as, ok := n.Assign.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != 1 || as.Lhs[0].(*ast.Ident).Name != name {
				return true
			}
			for _, s := range n.Body.List {
				cc := s.(*ast.CaseClause)
				if len(cc.List) == 1 && cc.Pos() < pos && pos < cc.End() {
					t := cc.List[0]
					consider(cc.Colon, func() ast.Expr { return t })
				}
			}
		}
		return true
	})
	return best
}
