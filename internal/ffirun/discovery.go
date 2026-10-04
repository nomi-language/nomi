package ffirun

import (
	"bytes"
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	goprinter "go/printer"
	gotoken "go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	nomiast "github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ffitypes"
	"github.com/nomi-language/nomi/internal/lexer"
	nomiparser "github.com/nomi-language/nomi/internal/parser"
)

// DiscoveredPackage is a single Go import path referenced by source-level Nomi
// FFI bindings. The ImportPath is
// what gets imported in the generated wrapper; the Alias is a safe-for-Go
// identifier the wrapper uses to qualify registrations.
type DiscoveredPackage struct {
	ImportPath string
	Alias      string
	Owner      string
	OwnerFile  string
	OwnerLine  int
	OwnerCol   int
	InlineUsed bool
	Types      []DiscoveredType
	Exports    []DiscoveredExport
	GoDecls    []string
}

// DiscoveredType is one Go named type bound to an opaque Nomi host type.
type DiscoveredType struct {
	Key string
	// EntryKey is the key the runtime uses for this declaration when its OWN
	// file is loaded as the ENTRY, or "" when that is already Key.
	//
	// The two differ because `moduleNameForNomiFile` qualifies a non-entry
	// file's declarations with its module name — `ffi.RawBox` for ffi.nomi —
	// while the runtime keys the ENTRY's own declarations bare. So
	// `nomi run app/ffi.nomi` registered `ffi.RawBox` and then reported
	// `RawBox` unregistered, naming symbols the source plainly binds. See the
	// wrapper template's entry-scoped block.
	EntryKey    string
	LocalName   string
	TypeName    string
	GoTypeExpr  string
	Declaration string
	SourceFile  string
	SourceLine  int
	SourceCol   int
}

// DiscoveredExport is one Go function bound for direct registration by the
// generated wrapper. Key is the Nomi host declaration key passed to
// RegisterExternFunc; FuncName is the exported Go function name.
type DiscoveredExport struct {
	Key string
	// EntryKey is DiscoveredType.EntryKey for a function; see there.
	EntryKey    string
	FuncName    string
	WrapperName string
	ParamDecls  string
	ReturnDecl  string
	GoBody      string
	Declaration string
	Params      []nomiast.Param
	ReturnType  nomiast.TypeExpr
	Structs     map[string]*nomiast.StructDef
	SourceFile  string
	SourceLine  int
	SourceCol   int
}

// Discover scans Nomi source files under projectRoot for source-level Go FFI
// bindings:
//
//	gopkg "example.com/app/ffi" as ffi
//	fn echo_upper(s: String): String go ffi.EchoUpper
//
// The returned slice is sorted by import path for determinism (the codegen
// template depends on stable ordering for golden tests and reproducible cache
// hashes).
func Discover(projectRoot string) ([]DiscoveredPackage, error) {
	return DiscoverInScope(projectRoot, projectRoot)
}

// DiscoverInScope scans a Nomi module scope for source-level Go FFI bindings
// and first-party standard adapters selected by the imported logical modules.
// sourceRoot is usually the nearest nomi.toml directory for the entry path, or
// the entry's own directory when no nomi.toml is present. Local replace targets
// from the controlling Go module are also scanned so bindings declared in
// replaced Nomi dependencies are available to the wrapper.
func DiscoverInScope(projectRoot, sourceRoot string) ([]DiscoveredPackage, error) {
	files, err := collectNomiSourceFiles(projectRoot, sourceRoot)
	if err != nil {
		return nil, err
	}
	byImportPath := make(map[string]*DiscoveredPackage)
	for _, file := range files {
		if err := discoverNomiFile(projectRoot, file, byImportPath); err != nil {
			return nil, err
		}
	}
	out := make([]DiscoveredPackage, 0, len(byImportPath))
	for _, pkg := range byImportPath {
		pkg.Types = uniqueDiscoveredTypes(pkg.Types)
		pkg.Exports = uniqueDiscoveredExports(pkg.Exports)
		sort.Slice(pkg.Types, func(i, j int) bool { return pkg.Types[i].Key < pkg.Types[j].Key })
		sort.Slice(pkg.Exports, func(i, j int) bool { return pkg.Exports[i].Key < pkg.Exports[j].Key })
		if pkg.InlineUsed || len(pkg.Types) > 0 || len(pkg.Exports) > 0 {
			out = append(out, *pkg)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ImportPath < out[j].ImportPath })
	assignAliases(out)
	return out, nil
}

func uniqueDiscoveredTypes(types []DiscoveredType) []DiscoveredType {
	seen := make(map[string]bool, len(types))
	out := types[:0]
	for _, typ := range types {
		k := typ.Key + "\x00" + typ.TypeName
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, typ)
	}
	return out
}

func uniqueDiscoveredExports(exports []DiscoveredExport) []DiscoveredExport {
	seen := make(map[string]bool, len(exports))
	out := exports[:0]
	for _, exp := range exports {
		k := exp.Key + "\x00" + exp.FuncName
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, exp)
	}
	return out
}

func collectNomiSourceFiles(projectRoot, sourceRoot string) ([]string, error) {
	var files []string
	if sourceRoot == "" {
		sourceRoot = projectRoot
	}
	roots := append([]string{sourceRoot}, localReplaceDirs(projectRoot)...)
	seenRoots := make(map[string]bool, len(roots))
	for _, root := range roots {
		clean, err := filepath.Abs(root)
		if err != nil {
			clean = root
		}
		if seenRoots[clean] {
			continue
		}
		seenRoots[clean] = true
		if err := collectNomiSourceFilesUnder(clean, &files); err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func findNomiPackageRoot(startDir, goRoot string) string {
	dir := startDir
	for {
		if info, err := os.Stat(filepath.Join(dir, "nomi.toml")); err == nil && !info.IsDir() {
			return dir
		}
		if samePath(dir, goRoot) {
			return startDir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return startDir
		}
		dir = parent
	}
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil {
		a = absA
	}
	if errB == nil {
		b = absB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func collectNomiSourceFilesUnder(root string, files *[]string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".bare", "vendor", "node_modules", "grammars", "dist":
				return filepath.SkipDir
			}
			if !samePath(p, root) {
				if info, err := os.Stat(filepath.Join(p, "nomi.toml")); err == nil && !info.IsDir() {
					return filepath.SkipDir
				}
			}
			if strings.HasPrefix(d.Name(), ".") && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".nomi") {
			*files = append(*files, p)
		}
		return nil
	})
}

func localReplaceDirs(projectRoot string) []string {
	data, err := os.ReadFile(filepath.Join(projectRoot, "go.mod"))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "replace ") || !strings.Contains(trimmed, "=>") {
			continue
		}
		parts := strings.Split(trimmed, "=>")
		if len(parts) != 2 {
			continue
		}
		source := strings.Fields(strings.TrimPrefix(strings.TrimSpace(parts[0]), "replace"))
		if len(source) == 0 || source[0] == "nomi" {
			continue
		}
		target := strings.Fields(strings.TrimSpace(parts[1]))
		if len(target) == 0 {
			continue
		}
		dir := target[0]
		if strings.HasPrefix(dir, ".") {
			dir = filepath.Join(projectRoot, dir)
		}
		if filepath.IsAbs(dir) || strings.HasPrefix(target[0], ".") {
			out = append(out, filepath.Clean(dir))
		}
	}
	return out
}

func discoverNomiFile(projectRoot, file string, byImportPath map[string]*DiscoveredPackage) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("ffirun: reading %s: %w", file, err)
	}
	nodes, parseErr := nomiparser.Parse(lexer.Lex(string(data)))
	if parseErr != nil {
		// Let the regular runtime/analyzer report syntax errors. Discovery only
		// controls whether the wrapper build path is needed.
		nodes, _ = nomiparser.ParseWithRecovery(lexer.Lex(string(data)))
	}
	aliases := make(map[string]*nomiast.ExternPackage)
	collectExternPackages(nodes, aliases)
	hasGoBlock := collectGoBlocks(file, nodes, aliases, byImportPath)
	if len(aliases) == 0 && !hasGoBlock {
		return nil
	}
	moduleName := moduleNameForNomiFile(projectRoot, file)
	collectForeignBindings(file, nodes, moduleName, "", aliases, byImportPath)
	return nil
}

func moduleNameForNomiFile(projectRoot, file string) string {
	base := filepath.Base(file)
	if base == "main.nomi" || strings.HasSuffix(base, "_test.nomi") {
		return ""
	}
	rel, err := filepath.Rel(projectRoot, file)
	if err != nil {
		return strings.TrimSuffix(base, ".nomi")
	}
	withoutExt := strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")
	return path.Base(withoutExt)
}

func collectExternPackages(nodes []nomiast.Node, aliases map[string]*nomiast.ExternPackage) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *nomiast.ExternPackage:
			aliases[v.Alias] = v
		case *nomiast.ImportStmt:
			if v.Extern && v.ExternAlias != "" && v.ExternAlias != "_" {
				aliases[v.ExternAlias] = externPackageFromImport(v)
			}
		case *nomiast.ImportBlock:
			collectExternImports(v.Entries, aliases)
		}
	}
}

func collectGoBlocks(file string, nodes []nomiast.Node, aliases map[string]*nomiast.ExternPackage, byImportPath map[string]*DiscoveredPackage) bool {
	found := false
	for _, n := range nodes {
		block, ok := n.(*nomiast.GoBlock)
		if !ok {
			continue
		}
		found = true
		imports, decls := parseGoBlockPrelude(block)
		for _, decl := range imports {
			aliases[decl.Alias] = decl
			pkg := ensureDiscoveredPackage(byImportPath, decl, file)
			pkg.InlineUsed = true
		}
		if len(decls) > 0 {
			pkg := ensureLocalGoPackage(byImportPath, file, block)
			pkg.InlineUsed = true
			pkg.GoDecls = append(pkg.GoDecls, decls...)
		}
	}
	return found
}

func parseGoBlockPrelude(block *nomiast.GoBlock) ([]*nomiast.ExternPackage, []string) {
	fset := gotoken.NewFileSet()
	src := "package main\n" + strings.TrimSpace(block.Body) + "\n"
	file, err := goparser.ParseFile(fset, "inline_go.nomi.go", src, goparser.ParseComments)
	if err != nil {
		return nil, nil
	}
	var imports []*nomiast.ExternPackage
	var decls []string
	for _, decl := range file.Decls {
		if gen, ok := decl.(*goast.GenDecl); ok && gen.Tok == gotoken.IMPORT {
			for _, spec := range gen.Specs {
				imp, ok := spec.(*goast.ImportSpec)
				if !ok || imp.Path == nil {
					continue
				}
				importPath, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					continue
				}
				alias := ""
				aliasLine, aliasCol := goBlockPos(fset, block, imp.Path.Pos())
				if imp.Name != nil {
					alias = imp.Name.Name
					aliasLine, aliasCol = goBlockPos(fset, block, imp.Name.Pos())
				} else {
					alias = defaultGoImportAlias(importPath)
				}
				pathLine, pathCol := goBlockPos(fset, block, imp.Path.Pos())
				imports = append(imports, &nomiast.ExternPackage{
					ImportPath:     importPath,
					ImportPathLine: pathLine,
					ImportPathCol:  pathCol,
					Alias:          alias,
					Line:           block.Line,
					Col:            block.Col,
					AliasLine:      aliasLine,
					AliasCol:       aliasCol,
				})
			}
			continue
		}
		var buf bytes.Buffer
		if err := goprinter.Fprint(&buf, fset, decl); err != nil {
			continue
		}
		decls = append(decls, strings.TrimSpace(buf.String()))
	}
	return imports, decls
}

func goBlockPos(fset *gotoken.FileSet, block *nomiast.GoBlock, pos gotoken.Pos) (int, int) {
	p := fset.Position(pos)
	line := block.BodyLine + p.Line - 2
	col := p.Column
	if p.Line == 2 {
		col = block.BodyCol + p.Column - 1
	}
	return line, col
}

func defaultGoImportAlias(importPath string) string {
	return sanitizeIdent(lastPathSegment(strings.TrimSuffix(importPath, "/")))
}

func collectExternImports(entries []*nomiast.ImportStmt, aliases map[string]*nomiast.ExternPackage) {
	for _, entry := range entries {
		if entry.Extern && entry.ExternAlias != "" && entry.ExternAlias != "_" {
			aliases[entry.ExternAlias] = externPackageFromImport(entry)
		}
	}
}

func externPackageFromImport(n *nomiast.ImportStmt) *nomiast.ExternPackage {
	aliasLine := n.ExternAliasLine
	aliasCol := n.ExternAliasCol
	if !n.ExternAliasExplicit {
		aliasLine = n.ExternPathLine
		aliasCol = n.ExternPathCol
	}
	return &nomiast.ExternPackage{
		ImportPath:     n.ExternPath,
		ImportPathLine: n.ExternPathLine,
		ImportPathCol:  n.ExternPathCol,
		Alias:          n.ExternAlias,
		Line:           n.Line,
		Col:            n.Col,
		AliasLine:      aliasLine,
		AliasCol:       aliasCol,
	}
}

func collectForeignBindings(file string, nodes []nomiast.Node, moduleName, owner string, aliases map[string]*nomiast.ExternPackage, byImportPath map[string]*DiscoveredPackage) {
	typeBindings := make(map[string]string)
	structBindings := make(map[string]*nomiast.StructDef)
	collectPlainStructBindings(nodes, structBindings)
	collectForeignTypeBindings(file, nodes, moduleName, owner, aliases, byImportPath, typeBindings)
	collectForeignFuncBindings(file, nodes, moduleName, owner, aliases, byImportPath, typeBindings, structBindings)
}

func collectPlainStructBindings(nodes []nomiast.Node, structBindings map[string]*nomiast.StructDef) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *nomiast.StructDef:
			if len(v.TypeParams) == 0 {
				structBindings[v.Name] = v
			}
		}
	}
}

func collectForeignTypeBindings(file string, nodes []nomiast.Node, moduleName, owner string, aliases map[string]*nomiast.ExternPackage, byImportPath map[string]*DiscoveredPackage, typeBindings map[string]string) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *nomiast.ExternType:
			key := externDeclKey(moduleName, owner, v.Name)
			if v.GoBody != "" {
				foreignAlias, foreignName, ok := parseInlineGoTypeSelector(v.GoBody)
				if !ok {
					continue
				}
				if foreignAlias == "" {
					pkg := ensureLocalGoPackage(byImportPath, file, v)
					goTypeExpr := "*" + foreignName
					typeBindings[v.Name] = goTypeExpr
					pkg.Types = append(pkg.Types, DiscoveredType{
						Key:         key,
						EntryKey:    entryScopedKey(moduleName, owner, v.Name),
						LocalName:   v.Name,
						TypeName:    foreignName,
						GoTypeExpr:  goTypeExpr,
						Declaration: fmt.Sprintf("host type %s", v.Name),
						SourceFile:  file,
						SourceLine:  v.GoBodyLine,
						SourceCol:   v.GoBodyCol,
					})
					continue
				}
				pkgDecl := aliases[foreignAlias]
				if pkgDecl == nil {
					continue
				}
				pkg := ensureDiscoveredPackage(byImportPath, pkgDecl, file)
				goTypeExpr := "*" + foreignAlias + "." + foreignName
				typeBindings[v.Name] = goTypeExpr
				pkg.Types = append(pkg.Types, DiscoveredType{
					Key:         key,
					EntryKey:    entryScopedKey(moduleName, owner, v.Name),
					LocalName:   v.Name,
					TypeName:    foreignName,
					GoTypeExpr:  goTypeExpr,
					Declaration: fmt.Sprintf("host type %s", v.Name),
					SourceFile:  file,
					SourceLine:  v.GoBodyLine,
					SourceCol:   v.GoBodyCol,
				})
				continue
			}
			if v.ForeignAlias == "" || v.ForeignName == "" {
				continue
			}
			pkgDecl := aliases[v.ForeignAlias]
			if pkgDecl == nil {
				continue
			}
			pkg := ensureDiscoveredPackage(byImportPath, pkgDecl, file)
			goTypeExpr := "*" + v.ForeignAlias + "." + v.ForeignName
			typeBindings[v.Name] = goTypeExpr
			pkg.Types = append(pkg.Types, DiscoveredType{
				Key:         key,
				EntryKey:    entryScopedKey(moduleName, owner, v.Name),
				LocalName:   v.Name,
				TypeName:    v.ForeignName,
				GoTypeExpr:  goTypeExpr,
				Declaration: fmt.Sprintf("host type %s", v.Name),
				SourceFile:  file,
				SourceLine:  v.ForeignNameLine,
				SourceCol:   v.ForeignNameCol,
			})
		}
	}
}

func collectForeignFuncBindings(file string, nodes []nomiast.Node, moduleName, owner string, aliases map[string]*nomiast.ExternPackage, byImportPath map[string]*DiscoveredPackage, typeBindings map[string]string, structBindings map[string]*nomiast.StructDef) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *nomiast.ImplBlock:
			collectForeignFuncBindings(file, v.Items, moduleName, implOwnerKey(v.Receiver, v.Interface), aliases, byImportPath, typeBindings, structBindings)
		case *nomiast.ExternFunc:
			key := externDeclKey(moduleName, owner, v.Name)
			if v.GoBody != "" {
				usedAliases := inlineGoUsedAliases(v.GoBody, aliases)
				for _, alias := range usedAliases {
					pkgDecl := aliases[alias]
					if pkgDecl == nil {
						continue
					}
					pkg := ensureDiscoveredPackage(byImportPath, pkgDecl, file)
					pkg.InlineUsed = true
				}
				var pkg *DiscoveredPackage
				if len(usedAliases) > 0 {
					pkgDecl := aliases[usedAliases[0]]
					if pkgDecl == nil {
						continue
					}
					pkg = ensureDiscoveredPackage(byImportPath, pkgDecl, file)
				} else {
					pkg = ensureLocalGoPackage(byImportPath, file, v)
				}
				if pkg == nil {
					continue
				}
				paramDecls, returnDecl, ok := inlineGoSignature(v, typeBindings, structBindings)
				if !ok {
					continue
				}
				pkg.Exports = append(pkg.Exports, DiscoveredExport{
					Key:         key,
					EntryKey:    entryScopedKey(moduleName, owner, v.Name),
					WrapperName: inlineWrapperName(key),
					ParamDecls:  paramDecls,
					ReturnDecl:  returnDecl,
					GoBody:      v.GoBody,
					Declaration: nomiExternFuncSource(v),
					Params:      v.Params,
					ReturnType:  v.ReturnTypeExpr,
					Structs:     cloneStructBindings(structBindings),
					SourceFile:  file,
					SourceLine:  v.GoBodyLine,
					SourceCol:   v.GoBodyCol,
				})
				continue
			}
			if v.ForeignAlias == "" || v.ForeignName == "" {
				continue
			}
			pkgDecl := aliases[v.ForeignAlias]
			if pkgDecl == nil {
				continue
			}
			pkg := ensureDiscoveredPackage(byImportPath, pkgDecl, file)
			pkg.Exports = append(pkg.Exports, DiscoveredExport{
				Key:         externDeclKey(moduleName, owner, v.Name),
				EntryKey:    entryScopedKey(moduleName, owner, v.Name),
				FuncName:    v.ForeignName,
				Declaration: nomiExternFuncSource(v),
				Params:      v.Params,
				ReturnType:  v.ReturnTypeExpr,
				Structs:     cloneStructBindings(structBindings),
				SourceFile:  file,
				SourceLine:  v.ForeignNameLine,
				SourceCol:   v.ForeignNameCol,
			})
		}
	}
}

func cloneStructBindings(in map[string]*nomiast.StructDef) map[string]*nomiast.StructDef {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*nomiast.StructDef, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func ensureDiscoveredPackage(byImportPath map[string]*DiscoveredPackage, decl *nomiast.ExternPackage, file string) *DiscoveredPackage {
	pkg := byImportPath[decl.ImportPath]
	if pkg == nil {
		pkg = &DiscoveredPackage{
			ImportPath: decl.ImportPath,
			Owner:      decl.Alias,
			OwnerFile:  file,
			OwnerLine:  decl.ImportPathLine,
			OwnerCol:   decl.ImportPathCol,
		}
		byImportPath[decl.ImportPath] = pkg
	}
	return pkg
}

func ensureLocalGoPackage(byImportPath map[string]*DiscoveredPackage, file string, n nomiast.Node) *DiscoveredPackage {
	pkg := byImportPath[""]
	if pkg == nil {
		pkg = &DiscoveredPackage{
			ImportPath: "",
			OwnerFile:  file,
			OwnerLine:  n.LineNum(),
		}
		byImportPath[""] = pkg
	}
	return pkg
}

// implOwnerKey is the `owner` segment an impl block's declarations key on:
// the receiver's BASE name, plus the interface's instantiation when the
// interface takes type arguments.
//
// This is externDeclKey's `owner` argument. It reads both the receiver and the
// interface, and the two halves pull in opposite directions:
//
//   - The receiver's `<T>` is a BINDER: a module has at most one
//     `impl Box<T>`, so the parameter list cannot tell two declarations apart,
//     and keying on "Box<T>.peek" would name a key no call crosses under.
//   - The interface's `<X, Score>` is an INSTANTIATION: there is one impl per
//     instantiation, so two `impl Add<X, Score> for Score` blocks need
//     "Score.Add<X, Score>.add" and "Score.Add<Y, Score>.add"; dropping it
//     collapses both `add`s onto one key.
//
// The result is `<recv>.<iface>.<name>`, or `<recv>.<name>` when the interface
// is not instantiated: the key the program's host crossing names, which the
// wrapper's host table answers under.
func implOwnerKey(recv, iface nomiast.TypeExpr) string {
	base := implReceiverName(recv)
	if base == "" {
		return ""
	}
	if key := implInterfaceKey(iface); key != "" {
		return base + "." + key
	}
	return base
}

// implReceiverName is the base name of an impl block's receiver type
// (`Generator` for `impl Generator<T>`).
func implReceiverName(recv nomiast.TypeExpr) string {
	switch t := recv.(type) {
	case *nomiast.SimpleType:
		return t.Name
	case *nomiast.GenericType:
		return t.Name
	case *nomiast.QualifiedType:
		return implReceiverName(t.Member)
	}
	return ""
}

// implInterfaceKey is the interface instantiation an impl block names, or ""
// when the interface takes no type arguments. irbuild.stdImplKey has the same
// body; both take TypeString() off ast.GenericType, so the two strings cannot
// drift.
//
// "" for a non-generic interface is not a shortcut. A non-generic interface
// can be implemented for a receiver at most once, so the instantiation adds no
// information — and adding it anyway would change the key every shipped
// adapter binding already registers under.
func implInterfaceKey(iface nomiast.TypeExpr) string {
	switch t := iface.(type) {
	case *nomiast.GenericType:
		if len(t.Params) == 0 {
			return ""
		}
		return t.TypeString()
	case *nomiast.QualifiedType:
		if implInterfaceKey(t.Member) == "" {
			return ""
		}
		return t.TypeString()
	}
	return ""
}

// entryScopedKey is the key the runtime uses for a declaration when its own
// file is loaded AS THE ENTRY, and "" when that is already externDeclKey's
// answer.
//
// Only a MODULE-qualified declaration has a second key. An owner-qualified one
// (`impl Box { host fn … }`) keys on the receiver type, which does not change
// with which file is the entry, and a file with no module name is already the
// entry's shape.
func entryScopedKey(moduleName, owner, name string) string {
	if owner != "" || moduleName == "" {
		return ""
	}
	return name
}

func externDeclKey(moduleName, owner, name string) string {
	if owner != "" {
		return owner + "." + name
	}
	if moduleName != "" {
		return moduleName + "." + name
	}
	return name
}

func nomiExternFuncSource(fn *nomiast.ExternFunc) string {
	var b strings.Builder
	b.WriteString("host fn ")
	b.WriteString(fn.Name)
	if len(fn.TypeParams) > 0 {
		b.WriteString("<")
		for i, tp := range fn.TypeParams {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(tp.Name)
		}
		b.WriteString(">")
	}
	b.WriteString("(")
	for i, param := range fn.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(param.Name)
		if param.TypeAnnotation != nil {
			b.WriteString(": ")
			b.WriteString(param.TypeAnnotation.TypeString())
		}
	}
	b.WriteString(")")
	if fn.ReturnTypeExpr != nil {
		b.WriteString(": ")
		b.WriteString(fn.ReturnTypeExpr.TypeString())
	}
	return b.String()
}

func parseInlineGoTypeSelector(body string) (alias, name string, ok bool) {
	expr := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(body), "*"))
	parts := strings.Split(expr, ".")
	if len(parts) == 1 && parts[0] != "" {
		return "", parts[0], true
	}
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func inlineGoUsedAliases(body string, aliases map[string]*nomiast.ExternPackage) []string {
	var out []string
	for alias := range aliases {
		if alias == "" || alias == "_" {
			continue
		}
		if strings.Contains(body, alias+".") {
			out = append(out, alias)
		}
	}
	sort.Strings(out)
	return out
}

func inlineWrapperName(key string) string {
	return "__nomi_inline_" + sanitizeIdent(strings.NewReplacer(".", "_", "/", "_", "-", "_").Replace(key))
}

func inlineGoSignature(fn *nomiast.ExternFunc, typeBindings map[string]string, structBindings map[string]*nomiast.StructDef) (string, string, bool) {
	params := make([]string, 0, len(fn.Params))
	for i, param := range fn.Params {
		if param.TypeAnnotation == nil {
			return "", "", false
		}
		goType, ok := nomiTypeExprGoType(param.TypeAnnotation, typeBindings, structBindings, true)
		if !ok {
			return "", "", false
		}
		name := param.Name
		if name == "" || !isGoIdent(name) || name == "_" {
			name = fmt.Sprintf("arg%d", i)
		}
		params = append(params, name+" "+goType)
	}
	ret, ok := nomiReturnTypeGoDecl(fn.ReturnTypeExpr, typeBindings, structBindings)
	if !ok {
		return "", "", false
	}
	return strings.Join(params, ", "), ret, true
}

func nomiReturnTypeGoDecl(t nomiast.TypeExpr, typeBindings map[string]string, structBindings map[string]*nomiast.StructDef) (string, bool) {
	if t == nil || typeExprString(t) == "Unit" {
		return "", true
	}
	if name, params, ok := genericTypeParts(t); ok && name == "Result" && len(params) == 2 {
		if nomiTypeExprContainsPlainStruct(params[0], structBindings) {
			return "(any, error)", true
		}
		okType, ok := nomiTypeExprGoType(params[0], typeBindings, structBindings, true)
		if !ok {
			return "", false
		}
		if typeExprString(params[0]) == "Unit" {
			return "error", true
		}
		return "(" + okType + ", error)", true
	}
	if nomiTypeExprContainsPlainStruct(t, structBindings) {
		return "any", true
	}
	return nomiTypeExprGoType(t, typeBindings, structBindings, true)
}

func nomiTypeExprGoType(t nomiast.TypeExpr, typeBindings map[string]string, structBindings map[string]*nomiast.StructDef, allowCallbacks bool) (string, bool) {
	switch v := t.(type) {
	case *nomiast.SimpleType:
		return simpleNomiTypeGoType(v.Name, typeBindings, structBindings)
	case *nomiast.QualifiedType:
		return nomiTypeExprGoType(v.Member, typeBindings, structBindings, allowCallbacks)
	case *nomiast.GenericType:
		switch v.Name {
		case "List":
			if len(v.Params) != 1 {
				return "", false
			}
			elem, ok := nomiTypeExprGoType(v.Params[0], typeBindings, structBindings, true)
			if !ok {
				return "", false
			}
			return "[]" + elem, true
		case "Map":
			if len(v.Params) != 2 {
				return "", false
			}
			key, ok := nomiTypeExprGoType(v.Params[0], typeBindings, structBindings, true)
			if !ok {
				return "", false
			}
			if !isProjectedGoMapKeyType(key) {
				return "", false
			}
			val, ok := nomiTypeExprGoType(v.Params[1], typeBindings, structBindings, true)
			if !ok {
				return "", false
			}
			return "map[" + key + "]" + val, true
		case "Maybe":
			if len(v.Params) != 1 {
				return "", false
			}
			elem, ok := nomiTypeExprGoType(v.Params[0], typeBindings, structBindings, true)
			if !ok {
				return "", false
			}
			return "*" + elem, true
		}
	case *nomiast.FuncType:
		if !allowCallbacks {
			return "", false
		}
		params := make([]string, 0, len(v.Params))
		for _, param := range v.Params {
			goType, ok := nomiTypeExprGoType(param, typeBindings, structBindings, false)
			if !ok {
				return "", false
			}
			params = append(params, goType)
		}
		ret, ok := nomiReturnTypeGoDecl(v.Return, typeBindings, structBindings)
		if !ok {
			return "", false
		}
		if ret == "" {
			return "func(" + strings.Join(params, ", ") + ")", true
		}
		return "func(" + strings.Join(params, ", ") + ") " + ret, true
	case *nomiast.AnonStructType:
		return nomiStructFieldsGoType(v.Fields, typeBindings, structBindings)
	}
	return "", false
}

func isProjectedGoMapKeyType(goType string) bool {
	if strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "map[") || strings.HasPrefix(goType, "func(") {
		return false
	}
	return goType != ""
}

// goCodegenTimeAlias is the name the generated wrapper imports "time"
// under (see codegen.go's UsesStdTime, which detects the qualifier in the
// emitted decls), so a projected stdlib type has to be spelled with it.
const goCodegenTimeAlias = "stdtime"

// goCodegenPkgQualifier maps a projected type's import path onto the
// qualifier the generated file spells it under. "" accepts the default.
func goCodegenPkgQualifier(importPath string) string {
	if importPath == "time" {
		return goCodegenTimeAlias
	}
	return ""
}

// simpleNomiTypeGoType is the Go type the generated wrapper declares for a
// named Nomi type. The projected scalars come from internal/ffitypes — the
// same table the runtime's preflight and the go/ast preflight read — so the
// signature this emits and the signature those two check are one rule.
//
// Dynamic and Unit are handled here rather than there because they have a
// codegen spelling but no PROJECTION: any Go type can carry a Dynamic and a
// Unit carries no value, so neither has a unique expectation for a checker
// to hold a binding to — but the wrapper still needs something to write in
// the slot.
func simpleNomiTypeGoType(name string, typeBindings map[string]string, structBindings map[string]*nomiast.StructDef) (string, bool) {
	if want, projected := ffitypes.Expect(&nomiast.SimpleType{Name: name}); projected {
		return want.Render(goCodegenPkgQualifier), true
	}
	switch name {
	case ffitypes.NomiDynamic:
		return "any", true
	case ffitypes.NomiUnit:
		return "struct{}", true
	}
	if goType, ok := typeBindings[name]; ok {
		return goType, true
	}
	if def, ok := structBindings[name]; ok {
		return nomiStructFieldsGoType(def.Fields, typeBindings, structBindings)
	}
	return "", false
}

func nomiStructFieldsGoType(fields []nomiast.StructField, typeBindings map[string]string, structBindings map[string]*nomiast.StructDef) (string, bool) {
	if len(fields) == 0 {
		return "struct{}", true
	}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		if field.TypeAnnotation == nil {
			return "", false
		}
		goType, ok := nomiTypeExprGoType(field.TypeAnnotation, typeBindings, structBindings, true)
		if !ok {
			return "", false
		}
		parts = append(parts, goStructFieldName(field.Name)+" "+goType)
	}
	return "struct { " + strings.Join(parts, "; ") + " }", true
}

func nomiTypeExprContainsPlainStruct(t nomiast.TypeExpr, structBindings map[string]*nomiast.StructDef) bool {
	switch v := t.(type) {
	case *nomiast.SimpleType:
		_, ok := structBindings[v.Name]
		return ok
	case *nomiast.QualifiedType:
		return nomiTypeExprContainsPlainStruct(v.Member, structBindings)
	case *nomiast.GenericType:
		for _, param := range v.Params {
			if nomiTypeExprContainsPlainStruct(param, structBindings) {
				return true
			}
		}
	case *nomiast.FuncType:
		for _, param := range v.Params {
			if nomiTypeExprContainsPlainStruct(param, structBindings) {
				return true
			}
		}
		return nomiTypeExprContainsPlainStruct(v.Return, structBindings)
	case *nomiast.AnonStructType:
		return true
	}
	return false
}

// goStructFieldName is internal/ffitypes' pairing, named locally so the call
// sites below stay short. See ffitypes.GoFieldName.
func goStructFieldName(name string) string { return ffitypes.GoFieldName(name) }

func genericTypeParts(t nomiast.TypeExpr) (string, []nomiast.TypeExpr, bool) {
	switch v := t.(type) {
	case *nomiast.GenericType:
		return v.Name, v.Params, true
	case *nomiast.QualifiedType:
		return genericTypeParts(v.Member)
	}
	return "", nil, false
}

func typeExprString(t nomiast.TypeExpr) string {
	if t == nil {
		return "Unit"
	}
	return t.TypeString()
}

func isGoIdent(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		ok := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return name[0] < '0' || name[0] > '9'
}

// assignAliases hands each discovered package a Go identifier that
// the wrapper template uses to qualify its registrations. Two-
// pass:
//
//  1. Count occurrences of each base (sanitized last path segment).
//  2. Unique base (count == 1) keeps its plain name; colliding base
//     names get numeric suffixes starting at 1 (`db1`, `db2`, …) so
//     both members of a collision are visibly numbered. Numbering
//     in sort order so the assignment is deterministic for golden
//     tests.
//
// Identifier sanitization: replace any non-identifier rune with
// underscore so a hyphenated module path ("foo-bar") produces a
// legal Go identifier ("foo_bar").
func assignAliases(packages []DiscoveredPackage) {
	counts := make(map[string]int, len(packages))
	bases := make([]string, len(packages))
	for i := range packages {
		if packages[i].ImportPath == "" {
			bases[i] = ""
			continue
		}
		if packages[i].InlineUsed && (packages[i].Owner == "_" || packages[i].Owner == ".") {
			bases[i] = packages[i].Owner
			continue
		}
		if packages[i].InlineUsed {
			base := sanitizeIdent(packages[i].Owner)
			if base == "" {
				base = sanitizeIdent(lastPathSegment(packages[i].ImportPath))
			}
			if base == "" {
				base = fmt.Sprintf("pkg%d", i)
			}
			bases[i] = base
			counts[base]++
			continue
		}
		base := sanitizeIdent(packages[i].Owner)
		if base == "" || base == "_" {
			base = sanitizeIdent(lastPathSegment(packages[i].ImportPath))
		}
		if base == "" {
			base = fmt.Sprintf("pkg%d", i)
		}
		bases[i] = base
		counts[base]++
	}
	seqs := make(map[string]int, len(counts))
	for i := range packages {
		base := bases[i]
		if packages[i].ImportPath == "" || base == "_" || base == "." || packages[i].InlineUsed {
			packages[i].Alias = base
			continue
		}
		if counts[base] == 1 {
			packages[i].Alias = base
			continue
		}
		seqs[base]++
		packages[i].Alias = fmt.Sprintf("%s%d", base, seqs[base])
	}
}

// lastPathSegment returns the part of importPath after the last "/".
// Stand-in for filepath.Base that doesn't apply OS-specific
// separator handling to a Go import path (which is always /).
func lastPathSegment(importPath string) string {
	for i := len(importPath) - 1; i >= 0; i-- {
		if importPath[i] == '/' {
			return importPath[i+1:]
		}
	}
	return importPath
}

// sanitizeIdent maps any rune that isn't valid in a Go identifier
// to "_". Leaves valid runes (ASCII letters, digits — after the
// first position — and underscores) alone. Ensures the first rune
// isn't a digit by prefixing "_" if needed.
func sanitizeIdent(s string) string {
	if s == "" {
		return ""
	}
	out := make([]rune, 0, len(s))
	for i, r := range s {
		ok := r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(i > 0 && r >= '0' && r <= '9')
		if !ok {
			r = '_'
		}
		out = append(out, r)
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = append([]rune{'_'}, out...)
	}
	return string(out)
}
