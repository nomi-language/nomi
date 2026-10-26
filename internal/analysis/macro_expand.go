package analysis

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Macros: a declaration keyword a library adds (ast.MacroDecl), and
// the Nomi code that expands it (std/macro). A declaration written with
// an imported macro keyword expands as a `message` does, through the same
// mechanisms (expansion.go): the declaration's inputs are read through the
// site's InputReader and recorded with the declaring file, what it expands
// to is checked as part of the file, and the span map moves an error in the
// generated code back to what the user wrote.
//
// What is particular to a macro is that its expansion is Nomi source
// text the macro writes, not nodes the compiler builds. The text is
// parsed here, and its declarations follow the declaration in the file's
// nodes, positioned in the synthesized band at the declaration's slot with
// the text's own lines and columns (generated line L is band line base+L),
// so a position in the generated code names the byte of the text it is at,
// which the span map translates (RelocateExpansionErrors). The declaration
// stays in the nodes, carrying the expansion (KeywordDecl.Expansion).
//
// The macro runs on the VM, which this package cannot import: the
// site's MacroHost runs it (vmhost provides one).

// MacroHost runs macros on behalf of the front end.
type MacroHost interface {
	// Expand runs the macro call names on call.Decl and answers what
	// it expanded to, or why it could not.
	Expand(call *MacroCall) *MacroResult
	// CheckAll runs the macro's ProgramCheck over every declaration
	// of its keyword in a module, when it implements one: what is wrong,
	// or why it could not run.
	CheckAll(call *MacroCheckCall) ([]MacroDiagnostic, string)
	// Navigate asks the macro's Navigation impl, when it implements
	// one, about a position in call.Decl's body.
	Navigate(call *MacroNavigation) *MacroAnswer
}

// MacroCheckCall is one ProgramCheck: the macro (as in
// MacroCall), every declaration of its keyword in the module, in file
// and line order, the configuration, and the files their requests read.
type MacroCheckCall struct {
	File, Root, Module, Name string
	Decls                    []MacroDeclaration
	Config                   string
	Inputs                   []ExpansionInput
}

// MacroNavigation is one question for a macro's Navigation impl:
// Op is "hover", "definition", "complete" or "actions"; Line and Col the
// position in the declaring file, and Diagnostic the one an action fixes.
type MacroNavigation struct {
	File, Root, Module, Name string
	Decl                     MacroDeclaration
	// Config is the macro module's table, as MacroCall's.
	Config     string
	Inputs     []ExpansionInput
	Op         string
	Line, Col  int
	Diagnostic MacroDiagnostic
}

// MacroAnswer is what a Navigation impl answered.
type MacroAnswer struct {
	Hover       string
	Definition  *MacroSpan
	Completions []MacroCompletion
	Actions     []MacroAction
}

// MacroCompletion is std/macro's Completion.
type MacroCompletion struct{ Label, Detail, Insert string }

// MacroEdit is std/macro's Edit.
type MacroEdit struct {
	Span MacroSpan
	Text string
}

// MacroAction is std/macro's Action. Tool is nil when the action
// runs no tool.
type MacroAction struct {
	Title string
	Edits []MacroEdit
	Tool  *MacroTool
}

// MacroTool is std/macro's Tool: a tool of the macro's own
// module, by name, and its arguments.
type MacroTool struct {
	Name string
	Args []string
}

// MacroSpan is a span of text in the declaring module: File is a path
// from the module root, lines and columns start at 1, columns count bytes
// and the end is one past the span's last byte. std/macro's Span.
type MacroSpan struct {
	File                       string
	Line, Col, EndLine, EndCol int
}

// MacroTypeText is a type as a declaration writes it.
type MacroTypeText struct {
	Text string
	Span MacroSpan
}

// MacroParam is a declaration's parameter.
type MacroParam struct {
	Name string
	Type MacroTypeText
	Span MacroSpan
}

// MacroBody is a declaration's raw body: its text, without the
// indentation a multi-line body's lines share, the span of its literal,
// where its first line starts and the column every later line starts at.
type MacroBody struct {
	Text                           string
	Span                           MacroSpan
	StartLine, StartCol, IndentCol int
	// Tag is the typed-literal tag written before the body (`Sql` in
	// Sql`...`), empty for none, and TagSpan where it is.
	Tag     string
	TagSpan MacroSpan
}

// MacroDeclaration is std/macro's Declaration.
type MacroDeclaration struct {
	Keyword, Name string
	Public        bool
	Params        []MacroParam
	Result        *MacroTypeText
	Body          *MacroBody
	Doc           string
	// File is the declaring file's path from the module root, Span the
	// declaration's keyword and NameSpan its name.
	File     string
	Span     MacroSpan
	NameSpan MacroSpan
}

// MacroSite is a declaration of a keyword a request named.
type MacroSite struct {
	Keyword, Name, File string
	Span                MacroSpan
}

// MacroSpanMap maps bytes Start to End of an expansion's source to
// the span they were made from.
type MacroSpanMap struct {
	Start, End int
	Span       MacroSpan
	Hint       string
}

// MacroDiagnostic is a problem a macro reports.
type MacroDiagnostic struct {
	Span    MacroSpan
	Message string
	Hints   []string
}

// MacroCall is one expansion the front end asks of its host.
type MacroCall struct {
	// File is the absolute path of the file that declares the macro,
	// Root its module's root and Module the module's [module].name; Name
	// is the macro's name in that file.
	File, Root, Module, Name string
	// Decl is the declaration to expand, and Config the macro module's
	// table in the declaring module's nomi.toml as JSON ("null" for none).
	Decl   MacroDeclaration
	Config string
	// DeclRoot is the declaring module's root: requested files are read
	// under it, through Read, which records each with the declaring file.
	DeclRoot string
	Read     InputReader
	// Sites answers the declaring module's declarations of keywords, each
	// file read through read.
	Sites func(keywords []string, read InputReader) []MacroSite
}

// MacroResult is what a macro expanded a declaration to.
type MacroResult struct {
	Source      string
	Spans       []MacroSpanMap
	Diagnostics []MacroDiagnostic
	// Inputs are the files the expansion read: the ones its request named
	// and the macro's own source files.
	Inputs []ExpansionInput
	// Requested are the files the request named that the run read, by
	// path from the declaring module's root, as `expand` received them.
	Requested []ExpansionInput
	// Failure, when set, is why the macro could not expand the
	// declaration (an effect it reaches, a limit it ran out of, a fault),
	// reported at the declaration with FailureHints.
	Failure      string
	FailureHints []string
	// Refused is set with Failure when the macro's module built and the
	// compiler refused or stopped its run: an effect it reaches, a limit,
	// a fault. The failure says all there is, so the declaration is
	// declined (MacroFailed).
	Refused bool
	// Unbuilt is set with Failure when the macro's module did not build.
	// The declaration is declined too, and the module's files keep their
	// own errors, which say why (InUnbuiltMacroModule).
	Unbuilt bool
}

// macroExpansion is what a macro's declaration keeps
// (KeywordDecl.Expansion), and every function it expanded to.
type macroExpansion struct {
	decl *ast.KeywordDecl
	// name is the macro's name in its module and module the module's
	// name, for diagnostics.
	name, module string
	// root is the declaring module's root, and file the declaring file's
	// path under it.
	root, file string
	// source is the expansion's text; base the band line its line 0 would
	// be at, so its line L is at base+L.
	source string
	base   int
	spans  []MacroSpanMap
	// errs are the diagnostics the expansion reports, as TypeErrors.
	errs []TypeError
	read []ExpansionInput
	// call is what the host was asked, host the host and requested the
	// files `expand` received, for the macro's ProgramCheck and
	// Navigation; nil when the expansion did not run.
	call      *MacroCall
	host      MacroHost
	requested []ExpansionInput
	// declined is whether the macro ran and declined the declaration:
	// it reported errors and expanded it to nothing (MacroFailed).
	declined bool
	// unbuilt is whether the macro's module did not build
	// (MacroResult.Unbuilt); the declaration is declined then too.
	unbuilt bool
	// dropped is whether the expansion's code was left out of the file
	// because a name it declares collides (checkExpansionCollisions): its
	// source is kept for `nomi expand`, and the declaration is declined.
	dropped bool
	// contract is how the expansion breaks the macro contract
	// (macroContractErrors), as the last check of the declaring file
	// found it: the declaration is declined when it is not empty.
	contractMu sync.Mutex
	contract   []TypeError
}

func (e *macroExpansion) inputs() []ExpansionInput { return e.read }

func (e *macroExpansion) info() *ExpansionInfo {
	in := &ExpansionInfo{Keyword: e.decl.Keyword, Decl: e.decl}
	seen := map[string]bool{}
	for _, m := range e.spans {
		if m.Span.File == "" || m.Span.File == e.file {
			continue
		}
		key := fmt.Sprint(m.Span)
		if seen[key] {
			continue
		}
		seen[key] = true
		in.Inputs = append(in.Inputs, ExpansionSpan{
			Path:  filepath.Join(e.root, filepath.FromSlash(m.Span.File)),
			Label: filepath.Base(m.Span.File),
			Line:  m.Span.Line, Col: m.Span.Col, EndLine: m.Span.EndLine, EndCol: m.Span.EndCol,
		})
	}
	return in
}

// MacroSource is the expansion a macro wrote for kd as the compiler
// checks it, with kd's doc comment on what kd names (attachDeclarationDoc),
// and whether a macro expanded it.
func MacroSource(kd *ast.KeywordDecl) (string, bool) {
	e := macroExpansionOf(kd)
	if e == nil {
		return "", false
	}
	return sourceWithDoc(e.source, kd), true
}

func macroExpansionOf(kd *ast.KeywordDecl) *macroExpansion {
	if kd == nil || kd.Expansion == nil {
		return nil
	}
	e, _ := kd.Expansion.Detail().(*macroExpansion)
	return e
}

// importedMacro finds the import that brings keyword into the file
// whose nodes are nodes: the macro's name in the file it imports from
// and that file's import path.
func importedMacro(nodes []ast.Node, keyword string) (name string, path []string, ok bool) {
	check := func(stmt *ast.ImportStmt) bool {
		for i, n := range stmt.Names {
			local := ast.ImportNodeName(n)
			if i < len(stmt.Aliases) && stmt.Aliases[i] != nil {
				local = ast.ImportNodeName(stmt.Aliases[i])
			}
			if local == keyword {
				name, path = ast.ImportNodeName(n), importStmtPath(stmt)
				return true
			}
		}
		return false
	}
	for _, n := range nodes {
		switch s := n.(type) {
		case *ast.ImportStmt:
			if check(s) {
				return name, path, true
			}
		case *ast.ImportBlock:
			for _, e := range s.Entries {
				if check(e) {
					return name, path, true
				}
			}
		}
	}
	return "", nil, false
}

// fileImportNamed finds the whole-file import that binds module, the file
// API object a qualified keyword (`sqlkit.query`) is written with: a
// path-only import (`import sqlkit`, `import lib/tables as t`) or one whose
// brace list holds `self`. It answers the import's path.
func fileImportNamed(nodes []ast.Node, module string) ([]string, bool) {
	check := func(stmt *ast.ImportStmt) ([]string, bool) {
		if len(stmt.Names) > 0 && !stmt.IncludeParent {
			return nil, false
		}
		if stmt.FileSegments > 0 && stmt.FileSegments < len(stmt.ModulePath) {
			// `std/maybe.Maybe.{self}` binds a type, not a file.
			return nil, false
		}
		path := importStmtPath(stmt)
		if len(path) == 0 {
			return nil, false
		}
		bound := path[len(path)-1]
		if stmt.ModuleAlias != nil {
			bound = ast.ImportNodeName(stmt.ModuleAlias)
		}
		return path, bound == module
	}
	for _, n := range nodes {
		switch s := n.(type) {
		case *ast.ImportStmt:
			if path, ok := check(s); ok {
				return path, true
			}
		case *ast.ImportBlock:
			for _, e := range s.Entries {
				if path, ok := check(e); ok {
					return path, true
				}
			}
		}
	}
	return nil, false
}

// declaredMacros are the names of the `pub macro` declarations of
// the file whose text is text, in order.
func declaredMacros(text string) []string {
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(text))
	var out []string
	for _, n := range nodes {
		if d, ok := n.(*ast.MacroDecl); ok && d.Public {
			out = append(out, d.Name)
		}
	}
	return out
}

// moduleRootOf is the root of the module the file at path is in: the
// nearest directory above it holding nomi.toml.
func moduleRootOf(path string) string {
	dir := filepath.Dir(path)
	for {
		if _, err := os.Stat(filepath.Join(dir, "nomi.toml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// expandMacro expands kd, a declaration written with a keyword no
// built-in declaration has, at site: into kd carrying its expansion,
// followed by the declarations it expanded to, at the band slot. The
// keyword is qualified by the file API object of a whole-file import
// (`sqlkit.query` after `import sqlkit`), or bare and brought by an item
// import (`query` after `import sqlkit.query`), as a function is. A keyword
// no import brings, or one the imported file does not declare, stays
// unexpanded, for the checker to refuse.
func expandMacro(kd *ast.KeywordDecl, nodes []ast.Node, site *ExpansionSite, slot int) []ast.Node {
	var (
		macroName  string
		importPath []string
		ok         bool
	)
	if kd.Module != "" {
		macroName = kd.Keyword
		importPath, ok = fileImportNamed(nodes, kd.Module)
	} else {
		macroName, importPath, ok = importedMacro(nodes, kd.Keyword)
	}
	// An in-memory program's root is its virtual files' `.`, which names no
	// module on disk a macro could come from.
	if !ok || !filepath.IsAbs(site.Root) || len(importPath) == 0 || importPath[0] == "std" {
		return []ast.Node{kd}
	}
	file, found := ImportFile(site.Root, importPath)
	if !found {
		return []ast.Node{kd}
	}
	if kd.Module != "" {
		// An item import of a name the file does not export is an error
		// at the import; a qualified keyword is checked here, and the
		// checker says what the file does declare.
		text, read := site.read(file)
		if !read || !slices.Contains(declaredMacros(text), macroName) {
			return []ast.Node{kd}
		}
	}
	macroRoot := moduleRootOf(file)
	macroModule := ""
	if m, err := LoadManifest(macroRoot); err == nil {
		macroModule = m.Name
	}
	e := &macroExpansion{decl: kd, name: macroName, module: macroModule, root: site.Root, file: site.ModulePath + ".nomi", base: slot}
	kd.Expansion = ast.NewExpansion(kd.Keyword, e)
	keywordErr := func(msg string, hints ...string) {
		te := TypeError{Line: kd.Line, Col: kd.Col, EndLine: kd.Line, EndCol: kd.KeywordEndCol(), Message: msg}
		for _, h := range hints {
			te = te.WithHint(h)
		}
		e.errs = append(e.errs, te)
	}
	if macroRoot == "" || sameDir(macroRoot, site.Root) {
		keywordErr(fmt.Sprintf("`%s` is a macro this module declares, and a module cannot use its own macros", kd.QualifiedKeyword()),
			"the compiler runs a macro's code before it checks the module that uses it, so move the macro to a module of its own, in a directory of its own, and add it with `nomi add <that directory>`")
		return []ast.Node{kd}
	}
	if slices.Contains(NomiTables, macroModule) {
		// Its configuration would be Nomi's own table.
		keywordErr(fmt.Sprintf("the macro `%s` cannot be used: its module is named `%s`, so its settings would be nomi.toml's `[%s]` table, which is Nomi's own", macroName, macroModule, macroModule),
			"rename the macro's module ([module].name in its nomi.toml); a macro module may not be named `"+strings.Join(NomiTables, "` or `")+"`")
		e.declined = true
		return []ast.Node{kd}
	}
	if !kd.Marked {
		// The declaration still expands, so this is the only error.
		e.errs = append(e.errs, unmarkedMacroError(kd))
	}
	if site.Macros == nil {
		keywordErr(fmt.Sprintf("this `%s` declaration cannot be expanded here: this build runs no macros", kd.QualifiedKeyword()))
		return []ast.Node{kd}
	}
	call := &MacroCall{
		File: file, Root: macroRoot, Module: macroModule, Name: macroName,
		Decl:     macroDeclaration(kd, e.file),
		Config:   site.Manifest.MacroConfig(macroModule),
		DeclRoot: site.Root,
		Read:     site.read,
		Sites: func(keywords []string, read InputReader) []MacroSite {
			return moduleSites(site.Root, read, keywords)
		},
	}
	res := site.Macros.Expand(call)
	e.read = res.Inputs
	// The call is kept without its reader and sites, which hold the build
	// that read them.
	kept := *call
	kept.Read, kept.Sites = nil, nil
	e.call, e.host, e.requested = &kept, site.Macros, res.Requested
	label := fmt.Sprintf("`%s` (module %s)", macroName, macroModule)
	if res.Failure != "" {
		keywordErr(fmt.Sprintf("the macro %s could not expand `%s %s`: %s", label, kd.QualifiedKeyword(), kd.Name, res.Failure), res.FailureHints...)
		e.declined, e.unbuilt = res.Refused || res.Unbuilt, res.Unbuilt
		return []ast.Node{kd}
	}
	for _, d := range res.Diagnostics {
		e.errs = append(e.errs, e.diagnosticError(d))
	}
	if res.Source == "" {
		e.declined = len(res.Diagnostics) > 0
		return []ast.Node{kd}
	}
	e.source, e.spans = res.Source, res.Spans
	illFormed := func(msg string) []ast.Node {
		keywordErr(fmt.Sprintf("the macro %s expanded `%s %s` to ill-formed Nomi, a defect in the macro: %s", label, kd.QualifiedKeyword(), kd.Name, msg),
			"`nomi expand` prints what the declaration expands to")
		e.source = ""
		return []ast.Node{kd}
	}
	if strings.Count(res.Source, "\n") >= synthLineStride-1 {
		return illFormed(fmt.Sprintf("it is %d lines long, and an expansion holds at most %d", strings.Count(res.Source, "\n")+1, synthLineStride-2))
	}
	generated, err := parser.Parse(lexer.Lex(res.Source))
	if err != nil {
		return illFormed(err.Error())
	}
	generated, msg := hideExpansionImports(generated, kd.Name)
	if msg != "" {
		return illFormed(msg)
	}
	attachDeclarationDoc(kd, generated)
	shiftLines(generated, slot)
	named := false
	for _, n := range generated {
		fn, ok := n.(*ast.FuncDef)
		if !ok {
			continue
		}
		fn.Expansion = kd.Expansion
		if fn.Name == kd.Name && !named {
			// The function the declaration names is at the declaration's
			// name, as a message's is: hover, go to definition and
			// references find it there.
			named = true
			// The keyword is the macro's, a reference to its
			// declaration, so the function's `fn` is at its name too.
			fn.Line, fn.Col, fn.FnCol = kd.Line, kd.NameCol, kd.NameCol
		}
	}
	return append([]ast.Node{kd}, generated...)
}

// diagnosticError is d as a TypeError: in the declaring file, or in the
// input file its span names (File, absolute). A span outside the module is
// placed at the declaration.
func (e *macroExpansion) diagnosticError(d MacroDiagnostic) TypeError {
	te := TypeError{Message: d.Message, Hints: slices.Clone(d.Hints)}
	e.place(&te, d.Span)
	return te
}

// place puts te at span.
func (e *macroExpansion) place(te *TypeError, span MacroSpan) {
	kd := e.decl
	file := filepath.ToSlash(filepath.Clean(span.File))
	switch {
	case span.Line <= 0 || file == ".." || strings.HasPrefix(file, "../") || filepath.IsAbs(span.File):
		te.Line, te.Col, te.EndLine, te.EndCol = kd.Line, kd.Col, kd.Line, kd.KeywordEndCol()
		return
	case file == e.file || span.File == "":
		te.File = ""
	default:
		te.File = filepath.Join(e.root, filepath.FromSlash(file))
	}
	te.Line, te.Col, te.EndLine, te.EndCol = span.Line, span.Col, span.EndLine, span.EndCol
}

// macroDeclaration is kd as std/macro's Declaration, in the file at
// path file under the module root.
func macroDeclaration(kd *ast.KeywordDecl, file string) MacroDeclaration {
	span := func(s ast.Span) MacroSpan {
		return MacroSpan{File: file, Line: s.StartLine, Col: s.StartCol, EndLine: s.EndLine, EndCol: s.EndCol}
	}
	typeText := func(t ast.TypeExpr) MacroTypeText {
		tt := MacroTypeText{Text: t.TypeString()}
		if hs, ok := t.(ast.HasSpan); ok {
			tt.Span = span(hs.GetSpan())
		}
		return tt
	}
	d := MacroDeclaration{
		Keyword: kd.Keyword, Name: kd.Name, Public: kd.Public, Doc: kd.Doc, File: file,
		Span:     MacroSpan{File: file, Line: kd.Line, Col: kd.Col, EndLine: kd.Line, EndCol: kd.KeywordEndCol()},
		NameSpan: MacroSpan{File: file, Line: kd.NameLine, Col: kd.NameCol, EndLine: kd.NameLine, EndCol: kd.NameCol + len(kd.Name)},
	}
	for _, p := range kd.Params {
		ep := MacroParam{Name: p.Name, Span: MacroSpan{File: file, Line: p.Line, Col: p.Col, EndLine: p.Line, EndCol: p.Col + len(p.Name)}}
		if p.TypeAnnotation != nil {
			ep.Type = typeText(p.TypeAnnotation)
			if s := ep.Type.Span; s.EndLine > 0 {
				ep.Span.EndLine, ep.Span.EndCol = s.EndLine, s.EndCol
			}
		}
		d.Params = append(d.Params, ep)
	}
	if kd.ReturnTypeExpr != nil {
		t := typeText(kd.ReturnTypeExpr)
		d.Result = &t
	}
	if b := kd.Body; b != nil {
		body := &MacroBody{Text: b.Value, Tag: kd.BodyTag}
		sl, sc := kd.BodyPosition(0)
		el, ec := kd.BodyPosition(len(b.Value))
		body.Span = MacroSpan{File: file, Line: sl, Col: sc, EndLine: el, EndCol: ec}
		body.StartLine, body.StartCol, body.IndentCol = sl, sc, sc
		if b.Triple {
			body.IndentCol = kd.BodyCol
		}
		if kd.BodyTag != "" {
			body.TagSpan = MacroSpan{File: file, Line: b.Line, Col: kd.TagCol(), EndLine: b.Line, EndCol: b.Col}
		}
		d.Body = body
	}
	return d
}

// moduleSites are the declarations of keywords in the module rooted at
// root, each .nomi file read through read (so each is an input of the
// declaring file), in path order. A directory that is a module of its own,
// or whose name starts with `.`, is not part of the module.
func moduleSites(root string, read InputReader, keywords []string) []MacroSite {
	if len(keywords) == 0 || root == "" {
		return nil
	}
	want := map[string]bool{}
	for _, k := range keywords {
		want[k] = true
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || fileExists(filepath.Join(path, "nomi.toml"))) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".nomi") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	var out []MacroSite
	for _, path := range files {
		text, ok := read(path)
		if !ok {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(text))
		for _, n := range nodes {
			if kd, ok := n.(*ast.KeywordDecl); ok && want[kd.Keyword] {
				out = append(out, MacroSite{Keyword: kd.Keyword, Name: kd.Name, File: rel,
					Span: MacroSpan{File: rel, Line: kd.NameLine, Col: kd.NameCol, EndLine: kd.NameLine, EndCol: kd.NameCol + len(kd.Name)}})
			}
		}
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// hideExpansionImports gives each import an expansion's source starts with
// an alias no source can write, `<alias>$<name>`, so it takes no name of
// the declaring file, and renames the expansion's uses of it. An
// expansion's import must be a whole-file one at the top; msg says what is
// wrong otherwise.
func hideExpansionImports(nodes []ast.Node, name string) (out []ast.Node, msg string) {
	renames := map[string]string{}
	i := 0
	for ; i < len(nodes); i++ {
		imp, ok := nodes[i].(*ast.ImportStmt)
		if !ok {
			break
		}
		if len(imp.Names) > 0 || imp.Pub {
			return nil, fmt.Sprintf("an expansion imports whole files, as `import %s`, and names their members through them", strings.Join(importStmtPath(imp), "/"))
		}
		alias := ""
		if imp.ModuleAlias != nil {
			alias = ast.ImportNodeName(imp.ModuleAlias)
		} else if len(imp.ModulePath) > 0 {
			alias = ast.ImportNodeName(imp.ModulePath[len(imp.ModulePath)-1])
		}
		hidden := alias + "$" + name
		renames[alias] = hidden
		imp.ModuleAlias = &ast.Ident{Name: hidden, Line: imp.Line, Col: imp.Col}
	}
	for _, n := range nodes[i:] {
		switch n.(type) {
		case *ast.ImportStmt, *ast.ImportBlock:
			return nil, "an expansion's imports come before its declarations"
		}
	}
	if len(renames) > 0 {
		for _, n := range nodes[i:] {
			ast.Inspect(n, func(m ast.Node) bool {
				switch v := m.(type) {
				case *ast.FieldAccess:
					if id, ok := v.Object.(*ast.Ident); ok && renames[id.Name] != "" {
						id.Name = renames[id.Name]
					}
				case *ast.QualifiedType:
					if renames[v.Module] != "" {
						v.Module = renames[v.Module]
					}
				}
				return true
			})
		}
	}
	return nodes, ""
}

// shiftLines moves every line of nodes by base, so the text's line L is at
// band line base+L.
func shiftLines(nodes []ast.Node, base int) {
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			walk(v.Elem())
		case reflect.Slice:
			for i := range v.Len() {
				walk(v.Index(i))
			}
		case reflect.Struct:
			t := v.Type()
			for i := range t.NumField() {
				f := t.Field(i)
				if !f.IsExported() {
					continue
				}
				fv := v.Field(i)
				if f.Type.Kind() == reflect.Int && strings.HasSuffix(f.Name, "Line") {
					if fv.Int() > 0 {
						fv.SetInt(fv.Int() + int64(base))
					}
					continue
				}
				walk(fv)
			}
		}
	}
	for i := range nodes {
		walk(reflect.ValueOf(&nodes[i]).Elem())
	}
}

// macroExpansions are the macro expansions among nodes, by the
// band line their text's line 0 is at.
func macroExpansions(nodes []ast.Node) map[int]*macroExpansion {
	var out map[int]*macroExpansion
	for _, n := range nodes {
		if kd, ok := n.(*ast.KeywordDecl); ok {
			if e := macroExpansionOf(kd); e != nil && e.source != "" && !e.dropped {
				if out == nil {
					out = map[int]*macroExpansion{}
				}
				out[e.base] = e
			}
		}
	}
	return out
}

// RelocateExpansionErrors moves each error in errs that is in the code a
// macro expanded a declaration of nodes to (in the file's own band) to
// what the user wrote: the span the expansion's span map gives the byte it
// is at, with the map's hint before the checker's own, or else the
// declaration, saying the expansion is ill-formed. Every other error is
// answered as it was.
//
// A related location in that code (the return type a mismatch names, a
// parameter's declaration) moves the same way: to the span its byte maps
// to, or else to the declaration, its message saying the location is in
// code the macro generated.
func RelocateExpansionErrors(nodes []ast.Node, errs []TypeError) []TypeError {
	exps := macroExpansions(nodes)
	if len(exps) == 0 {
		return errs
	}
	var out []TypeError
	for i, te := range errs {
		moved := false
		if e := expansionAt(exps, te.File, te.Line); e != nil {
			te, moved = e.relocate(te), true
		}
		cloned := false
		for j, r := range te.Related {
			e := expansionAt(exps, r.File, r.Line)
			if e == nil {
				continue
			}
			if !cloned {
				te.Related, cloned, moved = slices.Clone(te.Related), true, true
			}
			te.Related[j] = e.relocateRelated(r)
		}
		if !moved {
			continue
		}
		if out == nil {
			out = slices.Clone(errs)
		}
		out[i] = te
	}
	if out == nil {
		return errs
	}
	return out
}

// expansionAt is the expansion among exps whose generated code holds line
// of the file the expansions are in (file ""), or nil.
func expansionAt(exps map[int]*macroExpansion, file string, line int) *macroExpansion {
	if file != "" || !IsSynthesizedLine(line) {
		return nil
	}
	return exps[synthSlotBase(line)]
}

// synthSlotBase is the first line of the synthesized slot that holds line.
func synthSlotBase(line int) int {
	return synthLineBase + (line-synthLineBase)/synthLineStride*synthLineStride
}

// relocate moves te, an error at a position in e's generated code.
func (e *macroExpansion) relocate(te TypeError) TypeError {
	if m, ok := e.spanAt(te.Line, te.Col); ok {
		e.place(&te, m.Span)
		if m.Hint != "" {
			// The macro's hint is about what the user wrote; the
			// checker's are about the generated code (an impl for a type
			// the macro bounded), so the macro's comes first.
			te.Hints = append([]string{m.Hint}, te.Hints...)
		}
		return te
	}
	kd := e.decl
	te.File = ""
	te.Line, te.Col, te.EndLine, te.EndCol = kd.Line, kd.Col, kd.Line, kd.KeywordEndCol()
	te.Message = fmt.Sprintf("the macro `%s` (module %s) expanded `%s %s` to code with an error, a defect in the macro: %s", e.name, e.module, kd.QualifiedKeyword(), kd.Name, te.Message)
	return te.WithHint("`nomi expand` prints what the declaration expands to")
}

// relocateRelated moves r, a related location in e's generated code: to
// what the span map says it was made from, or else to the declaration's
// name, saying the location is in the code the macro generated.
func (e *macroExpansion) relocateRelated(r RelatedInfo) RelatedInfo {
	if m, ok := e.spanAt(r.Line, r.Col); ok && m.Span.Line > 0 {
		var te TypeError
		e.place(&te, m.Span)
		r.File, r.Line, r.Col, r.EndLine, r.EndCol = te.File, te.Line, te.Col, te.EndLine, te.EndCol
		return r
	}
	kd := e.decl
	r.File = ""
	r.Line, r.Col, r.EndLine, r.EndCol = kd.NameLine, kd.NameCol, kd.NameLine, kd.NameCol+len(kd.Name)
	r.Message += fmt.Sprintf(" (in the code the macro `%s` generated for `%s %s`; `nomi expand` prints it)", e.name, kd.QualifiedKeyword(), kd.Name)
	return r
}

// ExpansionRelated moves r, a related location that may be in the band of
// a macro expansion among nodes (the nodes of the file r names), as
// RelocateExpansionErrors moves one in the file's own errors. ok is false
// when r is in the band and no expansion there holds it: a position no one
// can show.
func ExpansionRelated(nodes []ast.Node, r RelatedInfo) (RelatedInfo, bool) {
	if !IsSynthesizedLine(r.Line) {
		return r, true
	}
	file := r.File
	r.File = ""
	e := expansionAt(macroExpansions(nodes), "", r.Line)
	if e == nil {
		return r, false
	}
	moved := e.relocateRelated(r)
	if moved.File == "" {
		moved.File = file
	}
	return moved, true
}

// spanAt is the innermost entry of the span map whose range of the source
// holds the band position line and col.
func (e *macroExpansion) spanAt(line, col int) (MacroSpanMap, bool) {
	offset := byteOffset(e.source, line-e.base, col)
	best := -1
	for i, m := range e.spans {
		if offset < m.Start || offset >= m.End && !(m.Start == m.End && offset == m.Start) {
			continue
		}
		if best < 0 || m.End-m.Start < e.spans[best].End-e.spans[best].Start {
			best = i
		}
	}
	if best < 0 {
		return MacroSpanMap{}, false
	}
	return e.spans[best], true
}

// byteOffset is the byte offset in text of 1-based line and byte column
// col.
func byteOffset(text string, line, col int) int {
	off := 0
	for l := 1; l < line; l++ {
		nl := strings.IndexByte(text[off:], '\n')
		if nl < 0 {
			return len(text)
		}
		off += nl + 1
	}
	return min(off+col-1, len(text))
}

// ExpansionTarget answers, for a position in the band of a macro
// expansion among nodes, the position of what it was made from in the
// declaring file: the span its map gives when that span is in the file,
// else the declaration's name. ok is false for any other position.
func ExpansionTarget(nodes []ast.Node, line, col int) (tl, tc int, ok bool) {
	if !IsSynthesizedLine(line) {
		return 0, 0, false
	}
	exps := macroExpansions(nodes)
	base := synthLineBase + (line-synthLineBase)/synthLineStride*synthLineStride
	e := exps[base]
	if e == nil {
		return 0, 0, false
	}
	if m, ok := e.spanAt(line, col); ok && m.Span.Line > 0 && filepath.ToSlash(filepath.Clean(m.Span.File)) == e.file {
		return m.Span.Line, m.Span.Col, true
	}
	return e.decl.NameLine, e.decl.NameCol, true
}
