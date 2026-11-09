package analysis

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ast/astcodec"
)

// Derives in a partner test file (spec §12 *Derive*). api_test.nomi may
// write `derive FromJson for TodoJson` for the TodoJson api.nomi declares,
// private or `pub`. The derived impl is the test file's own: it is in a
// program exactly when the test file is, which `nomi test` makes the
// entry and an ordinary program reaches only by importing the test file.
// Production code that works only through such an impl fails `nomi check`
// and `nomi run` of the program without the test file.
//
// The impl is derived from the partner's declaration, read from the nodes
// the build reads for the partner (source or interface), so it is the one
// the partner's current fields make. Its body is the body a derive in the
// partner would have, and it names what the partner's fields name: a
// type the partner declares, or one it imports. The test file need not
// import those, and its own names must not change what they mean, so each
// such name in the declaration's component types is written through a
// hidden import of the file that declares it, whose alias holds a `$` no
// source can write (`partner$.Tag`, `partner$1.Uuid`). Text the derived
// code shows (a JSON shape error's `expected`) keeps the field's type as
// the partner writes it.

// partnerAlias is the alias of the hidden import of the partner itself,
// and partnerAlias plus an index that of the hidden import standing for
// the partner's import at that index.
const partnerAlias = "partner$"

// partnerImportCol is the column the hidden partner imports start at on
// hiddenImportLine, after the other hidden imports' columns.
const partnerImportCol = 64

// PartnerKey answers the module key of the file whose partner test is the
// file with key (`api` for `api_test`, `shop/cart` for `shop/cart_test`),
// by the rule IsPartnerTestPath states over paths, and false when key is
// no partner test's.
func PartnerKey(key string) (string, bool) {
	dir, base := path.Split(key)
	stem, ok := strings.CutSuffix(base, "_test")
	if !ok || stem == "" {
		return "", false
	}
	return dir + stem, true
}

// LowerPartnerDerives lowers the derives the file with module key writes
// for its partner's types, when the file is a partner test: each derive
// for a type the file imports by name from its partner and does not
// declare. fileNodes answers a project file's top-level nodes by key, as
// the build reads them. Every other node passes through, a derive for a
// type no partner declares included, which LowerDerives refuses after.
//
// Every path that prepares a file's nodes for a build runs it before
// LowerDerives, with the file's key and the nodes that build reads for
// the partner, so a check from source, one against interfaces and one
// after an edit lower the same impl.
func LowerPartnerDerives(nodes []ast.Node, key string, fileNodes func(key string) ([]ast.Node, bool)) ([]ast.Node, []TypeError) {
	partner, ok := PartnerKey(key)
	if !ok || fileNodes == nil {
		return nodes, nil
	}
	stem := path.Base(partner)
	local := typeDeclsInNodeList(nodes)
	wanted := map[string]bool{}
	var via *ast.ImportStmt
	for _, n := range nodes {
		conf, ok := n.(*ast.ImplConformance)
		if !ok || !conf.Derive || conf.Receiver == nil {
			continue
		}
		name := TypeExprBaseName(conf.Receiver)
		if name == "" || local[name] != nil || typeAliasInNodeList(nodes, name) != nil {
			continue
		}
		if imp := importOfName(nodes, name, stem); imp != nil {
			wanted[name] = true
			if via == nil {
				via = imp
			}
		}
	}
	if len(wanted) == 0 {
		return nodes, nil
	}
	partnerNodes, ok := fileNodes(partner)
	if !ok {
		return nodes, nil
	}
	names := newPartnerNames(partnerNodes, via)
	partnerDecls := typeDeclsInNodeList(partnerNodes)
	decls := typeDeclsInNodeList(nodes)
	foreign := map[string]bool{}
	for name := range wanted {
		if decl := partnerDecls[name]; decl != nil {
			decls[name] = names.declCopy(decl)
			foreign[name] = true
		}
	}
	if len(foreign) == 0 {
		return nodes, nil
	}
	var errs []TypeError
	manualImpls := manualImplsInNodeList(nodes)
	seen := map[string]map[string]bool{}
	index := sourceIndices(nodes)
	partnerImpls := implsWritten(partnerNodes)
	var body []ast.Node
	for i, n := range nodes {
		if conf, ok := n.(*ast.ImplConformance); ok && conf.Derive && conf.Receiver != nil && foreign[TypeExprBaseName(conf.Receiver)] {
			if duplicateIn(conf, partnerImpls) {
				// The partner's impl is in every program the test is, so
				// the derive would be a second one.
				errs = append(errs, partnerDuplicate(conf, stem))
				continue
			}
			blocks := lowerTopLevelDerive(conf, nodes, index[i], decls, manualImpls, seen, &errs)
			names.restoreText(blocks)
			body = append(body, blocks...)
			continue
		}
		body = append(body, n)
	}
	at := 0
	for at < len(body) && isPreludeInjectLine(body[at].LineNum()) {
		at++
	}
	imports := names.imports()
	out := make([]ast.Node, 0, len(body)+len(imports))
	out = append(out, body[:at]...)
	out = append(out, imports...)
	return append(out, body[at:]...), errs
}

// importOfName answers the import of nodes that names the type name,
// unaliased, from a file whose name is stem (`import api.{TodoJson}`, or
// `import shop::api.{TodoJson}`), or nil.
func importOfName(nodes []ast.Node, name, stem string) *ast.ImportStmt {
	for _, imp := range importStmts(nodes) {
		if isPreludeInjectLine(imp.Line) {
			continue
		}
		file := importFilePath(imp)
		if len(file) == 0 || file[len(file)-1] != stem || len(file) != len(imp.ModulePath) {
			continue
		}
		for i, id := range imp.Names {
			if i < len(imp.Aliases) && imp.Aliases[i] != nil {
				continue
			}
			if ti, ok := id.(*ast.TypeIdent); ok && ti.Name == name {
				return imp
			}
		}
	}
	return nil
}

// importStmts are the imports among nodes, each entry of a block import
// (`import { ... }`) one of them.
func importStmts(nodes []ast.Node) []*ast.ImportStmt {
	var out []*ast.ImportStmt
	for _, n := range nodes {
		switch imp := n.(type) {
		case *ast.ImportStmt:
			out = append(out, imp)
		case *ast.ImportBlock:
			out = append(out, imp.Entries...)
		}
	}
	return out
}

// importFilePath is the file path an import names, the owners written
// after it left out.
func importFilePath(imp *ast.ImportStmt) []string {
	segs := imp.ModulePath
	if imp.FileSegments > 0 && imp.FileSegments < len(segs) {
		segs = segs[:imp.FileSegments]
	}
	out := make([]string, 0, len(segs))
	for _, seg := range segs {
		out = append(out, ast.ImportNodeName(seg))
	}
	return out
}

// partnerNames is what the partner's top-level names mean, as hidden
// imports of the test file can say it: each type the partner declares is
// a member of the partner, each name it imports a member of the file it
// imports it from, and each file it binds by name that file.
type partnerNames struct {
	// self is the import of the partner the test file wrote, whose file
	// path the hidden import of the partner repeats.
	self *ast.ImportStmt
	// declared are the type names the partner declares.
	declared map[string]bool
	// imported maps a type name the partner imports to the index of its
	// import and the name the imported file declares it by.
	imported map[string]partnerMember
	// files maps a file name the partner binds (`time` for
	// `import std::time`) to the index of its import.
	files map[string]int
	// sources are the partner's imports, by index.
	sources []*ast.ImportStmt
	// used are the aliases the copies name, each hidden import once.
	used map[string]bool
}

type partnerMember struct {
	at     int
	member string
}

func newPartnerNames(partnerNodes []ast.Node, self *ast.ImportStmt) *partnerNames {
	p := &partnerNames{self: self, declared: map[string]bool{}, imported: map[string]partnerMember{},
		files: map[string]int{}, used: map[string]bool{}}
	for _, n := range partnerNodes {
		switch d := n.(type) {
		case *ast.StructDef, *ast.EnumDef, *ast.TypeDef, *ast.ExternType:
			if name, ok := typeDeclName(d); ok && name != "" {
				p.declared[name] = true
			}
		case *ast.TypeAlias:
			p.declared[d.Name] = true
		case *ast.InterfaceDef:
			p.declared[d.Name] = true
		}
	}
	for _, d := range importStmts(partnerNodes) {
		if isPreludeInjectLine(d.Line) {
			// The prelude's, which the test file has too, and the
			// hidden ones, whose aliases no field type names.
			continue
		}
		file := importFilePath(d)
		if len(file) == 0 {
			continue
		}
		at := len(p.sources)
		p.sources = append(p.sources, d)
		owned := len(file) < len(d.ModulePath)
		switch {
		case d.ModuleAlias != nil:
			p.files[ast.ImportNodeName(d.ModuleAlias)] = at
		case len(d.Names) == 0 || d.IncludeParent:
			if !owned {
				p.files[file[len(file)-1]] = at
			}
		}
		if owned {
			// `import shape.Shape.{Circle}` selects an owner's
			// members, not a file's.
			continue
		}
		for i, id := range d.Names {
			ti, ok := id.(*ast.TypeIdent)
			if !ok {
				continue
			}
			local := ti.Name
			if i < len(d.Aliases) && d.Aliases[i] != nil {
				local = ast.ImportNodeName(d.Aliases[i])
			}
			p.imported[local] = partnerMember{at: at, member: ti.Name}
		}
	}
	return p
}

// alias is the hidden import alias of the partner's import at, or of the
// partner itself when at is -1.
func alias(at int) string {
	if at < 0 {
		return partnerAlias
	}
	return fmt.Sprintf("%s%d", partnerAlias, at)
}

// declCopy is a deep copy of decl, a type the partner declares, whose
// component types name what they name in the partner through the hidden
// imports.
func (p *partnerNames) declCopy(decl ast.Node) ast.Node {
	copied, err := astcodec.Decode(astcodec.Encode([]ast.Node{decl}))
	if err != nil || len(copied) != 1 {
		panic("analysis: a declaration does not survive the syntax codec")
	}
	params := map[string]bool{}
	switch d := copied[0].(type) {
	case *ast.StructDef:
		for _, tp := range d.TypeParams {
			params[tp.Name] = true
		}
		p.fields(d.Fields, params)
	case *ast.EnumDef:
		for _, tp := range d.TypeParams {
			params[tp.Name] = true
		}
		for i := range d.Variants {
			v := &d.Variants[i]
			v.DataTypeExpr = p.typeExpr(v.DataTypeExpr, params)
			v.EmbeddedTypeExpr = p.typeExpr(v.EmbeddedTypeExpr, params)
			p.fields(v.Fields, params)
		}
	case *ast.TypeDef:
		for _, tp := range d.TypeParams {
			params[tp.Name] = true
		}
		d.InnerTypeExpr = p.typeExpr(d.InnerTypeExpr, params)
	}
	return copied[0]
}

func (p *partnerNames) fields(fields []ast.StructField, params map[string]bool) {
	for i := range fields {
		fields[i].TypeAnnotation = p.typeExpr(fields[i].TypeAnnotation, params)
	}
}

// typeExpr answers te with each name the partner gives a meaning written
// through its hidden import. A type parameter of the declaration, and a
// name the partner neither declares nor imports (the prelude's), stay.
func (p *partnerNames) typeExpr(te ast.TypeExpr, params map[string]bool) ast.TypeExpr {
	switch t := te.(type) {
	case *ast.SimpleType:
		return p.qualify(t.Name, t, params, t.Line, t.Col)
	case *ast.GenericType:
		for i, param := range t.Params {
			t.Params[i] = p.typeExpr(param, params)
		}
		return p.qualify(t.Name, t, params, t.Line, t.Col)
	case *ast.QualifiedType:
		if at, ok := p.files[t.Module]; ok {
			t.Module = alias(at)
			p.used[t.Module] = true
		}
		return t
	case *ast.FuncType:
		for i, param := range t.Params {
			t.Params[i] = p.typeExpr(param, params)
		}
		if t.Return != nil {
			t.Return = p.typeExpr(t.Return, params)
		}
		return t
	case *ast.AnonStructType:
		p.fields(t.Fields, params)
		return t
	}
	return te
}

// qualify answers the type te names by name, written through the hidden
// import that gives name its meaning in the partner, or te itself.
func (p *partnerNames) qualify(name string, te ast.TypeExpr, params map[string]bool, line, col int) ast.TypeExpr {
	if params[name] {
		return te
	}
	at, member := -1, name
	switch {
	case p.declared[name]:
	case p.imported[name] != partnerMember{}:
		m := p.imported[name]
		at, member = m.at, m.member
	default:
		return te
	}
	switch t := te.(type) {
	case *ast.SimpleType:
		t.Name = member
	case *ast.GenericType:
		t.Name = member
	}
	a := alias(at)
	p.used[a] = true
	return &ast.QualifiedType{Module: a, ModuleLine: line, ModuleCol: col, Member: te}
}

// imports are the hidden imports the copies name, in a fixed order: the
// partner's, then each of its imports' by index.
func (p *partnerNames) imports() []ast.Node {
	l := hiddenImportLine
	var out []ast.Node
	add := func(imp *ast.ImportStmt, a string, n int) {
		col := partnerImportCol + 4*n
		var segs []ast.Node
		for i, name := range importFilePath(imp) {
			segs = append(segs, &ast.Ident{Name: name, Line: l, Col: col + 1 + i})
		}
		out = append(out, &ast.ImportStmt{
			ModulePath:   segs,
			FileSegments: len(segs),
			ModuleAlias:  &ast.Ident{Name: a, Line: l, Col: col + 1 + len(segs)},
			Line:         l,
			Col:          col,
		})
	}
	if p.used[partnerAlias] && p.self != nil {
		add(p.self, partnerAlias, 0)
	}
	for at, imp := range p.sources {
		if a := alias(at); p.used[a] {
			add(imp, a, at+1)
		}
	}
	return out
}

// restoreText puts back, in the text the derived blocks show (a JSON
// shape error's `expected`), the names the copies wrote through hidden
// imports as the partner writes them.
func (p *partnerNames) restoreText(blocks []ast.Node) {
	if len(p.used) == 0 {
		return
	}
	for _, b := range blocks {
		ast.Inspect(b, func(n ast.Node) bool {
			if s, ok := n.(*ast.StringLit); ok && strings.Contains(s.Value, partnerAlias) {
				s.Value = p.unqualified(s.Value)
			}
			return true
		})
	}
}

// unqualified is text with each hidden alias's qualifier taken out, and a
// file the partner binds by name put back.
func (p *partnerNames) unqualified(text string) string {
	for _, name := range slices.Sorted(maps.Keys(p.imported)) {
		m := p.imported[name]
		text = strings.ReplaceAll(text, alias(m.at)+"."+m.member, name)
	}
	for _, name := range slices.Sorted(maps.Keys(p.files)) {
		text = strings.ReplaceAll(text, alias(p.files[name])+".", name+".")
	}
	return strings.ReplaceAll(text, partnerAlias+".", "")
}

// CheckForeignDeriveBounds is CheckDeriveBounds for the impls fa's file
// derives for a type another file declares (a partner test's derive, or a
// REPL input's for an earlier input's type): each component of the
// declaration must implement the interface, which the declaring file's
// analysis answers by fileOf, its path's analysis, since the component
// types are named there. A component without the impl is reported as one
// of a derive in the declaring file is, naming the derive.
func CheckForeignDeriveBounds(fa *FileAnalysis, nodes []ast.Node, impls *ProjectImplIndex, fileOf func(path string) *FileAnalysis) []TypeError {
	if fa == nil || impls == nil || fa.ModuleScope == nil {
		return nil
	}
	var errs []TypeError
	seen := map[Pos]bool{}
	for _, n := range nodes {
		blk, ok := n.(*ast.ImplBlock)
		if !ok {
			continue
		}
		iface := foreignDeriveInterface(blk)
		if iface == nil {
			continue
		}
		derivePos := Pos{File: fa.FilePath, Line: iface.Line, Col: iface.Col}
		if seen[derivePos] {
			continue
		}
		seen[derivePos] = true
		sym := fa.ModuleScope.Lookup(TypeExprBaseName(blk.Receiver))
		if sym != nil && sym.Resolved != nil {
			sym = sym.Resolved
		}
		if sym == nil || sym.Node == nil || sym.SourceFile == "" || sym.SourceFile == fa.FilePath {
			continue
		}
		declFA := fileOf(sym.SourceFile)
		if declFA == nil {
			continue
		}
		var params []ast.TypeParam
		switch d := sym.Node.(type) {
		case *ast.StructDef:
			params = d.TypeParams
		case *ast.EnumDef:
			params = d.TypeParams
		case *ast.TypeDef:
			params = d.TypeParams
		default:
			continue
		}
		set := make(map[string]bool, len(params))
		for _, tp := range params {
			set[tp.Name] = true
		}
		name, _ := typeDeclName(sym.Node)
		errs = append(errs, checkDeriveComponentTypes(declFA, fa, impls, sym.Node, name, iface.Name, derivePos, set)...)
	}
	return errs
}

// foreignDeriveInterface answers the interface as written of blk when blk
// is an impl derived for a type another file declares, and nil for any
// other block. Such a block's header names the interface at the derive's
// own position (lowerTopLevelDerive), where a derive for a type of the
// file names it in the synthesized band.
func foreignDeriveInterface(blk *ast.ImplBlock) *ast.SimpleType {
	if blk == nil || blk.SynthOriginLine <= 0 || !IsSynthesizedLine(blk.Line) {
		return nil
	}
	iface, ok := blk.Interface.(*ast.SimpleType)
	if !ok || IsSynthesizedLine(iface.Line) || !deriveSupported[iface.Name] {
		return nil
	}
	return iface
}

// implsWritten answers, by receiver and then interface base name, the
// interface impls nodes declare: a derive, lowered or not, or an impl
// block.
func implsWritten(nodes []ast.Node) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	add := func(recv, iface string) {
		if recv == "" || iface == "" {
			return
		}
		if out[recv] == nil {
			out[recv] = map[string]bool{}
		}
		out[recv][iface] = true
	}
	for _, n := range nodes {
		switch d := n.(type) {
		case *ast.ImplConformance:
			if d.Derive && d.Receiver != nil {
				for _, iface := range conformanceInterfaces(d) {
					add(TypeExprBaseName(d.Receiver), TypeExprBaseName(iface))
				}
			}
		case *ast.ImplBlock:
			// A block the compiler synthesized is a lowered derive, which
			// the declaration's decorator names, or a universal Debug.
			if d.Interface != nil && d.Receiver != nil && !IsSynthesizedLine(d.Line) {
				add(TypeExprBaseName(d.Receiver), TypeExprBaseName(d.Interface))
			}
		default:
			name, ok := typeDeclName(n)
			if !ok {
				continue
			}
			// Every type has Debug in its own file: a derive or impl
			// there, or the one synthesized when it has neither.
			add(name, "Debug")
			for _, dec := range declDecorators(n) {
				if dec.Name != "derive" {
					continue
				}
				for _, arg := range dec.Args {
					if te, ok := arg.(ast.TypeExpr); ok {
						add(name, TypeExprBaseName(te))
					}
				}
			}
		}
	}
	return out
}

// declDecorators are the decorators of n, a type declaration.
func declDecorators(n ast.Node) []ast.Decorator {
	switch d := n.(type) {
	case *ast.StructDef:
		return d.Decorators
	case *ast.EnumDef:
		return d.Decorators
	case *ast.TypeDef:
		return d.Decorators
	case *ast.ExternType:
		return d.Decorators
	}
	return nil
}

// partnerDuplicate is the error at conf, a partner test's derive of an
// interface its partner, the file stem, implements for the type.
func partnerDuplicate(conf *ast.ImplConformance, stem string) TypeError {
	iface := conf.Interface
	if iface == nil && len(conf.Interfaces) > 0 {
		iface = conf.Interfaces[0]
	}
	line, col := typeExprPos(iface, conf.Line, conf.Col)
	name, recv := TypeExprBaseName(iface), TypeExprBaseName(conf.Receiver)
	return TypeError{Line: line, Col: col, Message: fmt.Sprintf(
		"duplicate impl: `%s` for `%s` is derived here and implemented in `%s`; drop this derive, since the test already has the impl `%s` declares",
		name, recv, stem, stem)}
}

// duplicateIn reports whether conf derives an interface the partner
// implements for its receiver.
func duplicateIn(conf *ast.ImplConformance, partnerImpls map[string]map[string]bool) bool {
	recv := TypeExprBaseName(conf.Receiver)
	for _, iface := range conformanceInterfaces(conf) {
		if partnerImpls[recv][TypeExprBaseName(iface)] {
			return true
		}
	}
	return false
}
