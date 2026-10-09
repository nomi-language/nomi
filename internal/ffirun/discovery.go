package ffirun

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	nomiast "github.com/nomi-language/nomi/internal/ast"
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
	Types      []DiscoveredType
	Exports    []DiscoveredExport
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
	Declaration string
	Params      []nomiast.Param
	ReturnType  nomiast.TypeExpr
	Structs     map[string]*nomiast.StructDef
	SourceFile  string
	SourceLine  int
	SourceCol   int
	// AlsoDeclaredIn is every other file that declares this binding under the
	// same Key, for an entry-scoped one (EntryKey != ""). Two files share a Key
	// only when BindingModule falls back to their base name, with no go.mod
	// above them (hi.nomi and bin/hi.nomi), and discovery keeps one export for
	// both.
	// Either may be the entry, so the wrapper rekeys the binding when the
	// entry is SourceFile or any of these.
	AlsoDeclaredIn []string
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
	return discoverForEntry(projectRoot, sourceRoot, "")
}

// discoverForEntry is DiscoverInScope that also scans entry, the program's
// entry file, when the walk would not: an extensionless `#!` script. The walk
// takes only `.nomi` files, so other extensionless files beside the script
// (other scripts, a README) stay out of its program.
func discoverForEntry(projectRoot, sourceRoot, entry string) ([]DiscoveredPackage, error) {
	files, err := collectNomiSourceFiles(projectRoot, sourceRoot)
	if err != nil {
		return nil, err
	}
	if entry != "" && !isNomiSourceName(entry) {
		files = append(files, entry)
	}
	byImportPath := make(map[string]*DiscoveredPackage)
	for _, file := range files {
		if err := discoverNomiFile(file, byImportPath); err != nil {
			return nil, err
		}
	}
	out := make([]DiscoveredPackage, 0, len(byImportPath))
	for _, pkg := range byImportPath {
		pkg.Types = uniqueDiscoveredTypes(pkg.Types)
		pkg.Exports = uniqueDiscoveredExports(pkg.Exports)
		sort.Slice(pkg.Types, func(i, j int) bool { return pkg.Types[i].Key < pkg.Types[j].Key })
		sort.Slice(pkg.Exports, func(i, j int) bool { return pkg.Exports[i].Key < pkg.Exports[j].Key })
		if len(pkg.Types) > 0 || len(pkg.Exports) > 0 {
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
	seen := make(map[string]int, len(exports))
	out := exports[:0]
	for _, exp := range exports {
		k := exp.Key + "\x00" + exp.FuncName
		if i, ok := seen[k]; ok {
			kept := &out[i]
			if kept.EntryKey != "" && exp.SourceFile != kept.SourceFile && !slices.Contains(kept.AlsoDeclaredIn, exp.SourceFile) {
				kept.AlsoDeclaredIn = append(kept.AlsoDeclaredIn, exp.SourceFile)
			}
			continue
		}
		seen[k] = len(out)
		out = append(out, exp)
	}
	for i := range out {
		sort.Strings(out[i].AlsoDeclaredIn)
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
		if isNomiSourceName(d.Name()) {
			*files = append(*files, p)
		}
		return nil
	})
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// isNomiSourceName reports whether a file name has the `.nomi` extension.
func isNomiSourceName(name string) bool {
	return strings.HasSuffix(name, ".nomi")
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

func discoverNomiFile(file string, byImportPath map[string]*DiscoveredPackage) error {
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
	if len(aliases) == 0 {
		return nil
	}
	moduleName := moduleNameForNomiFile(file)
	collectForeignBindings(file, nodes, moduleName, "", aliases, byImportPath)
	return nil
}

func moduleNameForNomiFile(file string) string {
	base := filepath.Base(file)
	if base == "main.nomi" || strings.HasSuffix(base, "_test.nomi") {
		return ""
	}
	return BindingModule(file)
}

// BindingModule is the module part of the key a Go-bound declaration in a
// file other than the entry crosses into Go under: the file's path relative
// to the nearest directory at or above it that holds a nomi.toml or a go.mod,
// "/"-separated and without its .nomi extension. A file at that root is its
// base name (ffi.nomi declares ffi.open); a/util.nomi declares a/util.open.
//
// The path, not the base name, so that a/util.nomi and b/util.nomi in one
// program register under different keys: under one key the wrapper's host
// table does not compile, and each file's bindings must find their adapter in
// their own file. It is relative, so an image `nomi build` writes carries no
// path of the machine that built it. irbuild names a crossing with it and
// discovery keys the wrapper's adapter with it, both from the file's absolute
// path alone, so the two agree whichever file is the entry.
//
// With neither file above it, it is the base name.
func BindingModule(file string) string {
	name := strings.TrimSuffix(filepath.Base(file), ".nomi")
	for dir := filepath.Dir(file); ; {
		if isFile(filepath.Join(dir, "nomi.toml")) || isFile(filepath.Join(dir, "go.mod")) {
			rel, err := filepath.Rel(dir, file)
			if err != nil {
				return name
			}
			return strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return name
		}
		dir = parent
	}
}

func collectExternPackages(nodes []nomiast.Node, aliases map[string]*nomiast.ExternPackage) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *nomiast.ExternPackage:
			aliases[v.Alias] = v
		}
	}
}

func collectForeignBindings(file string, nodes []nomiast.Node, moduleName, owner string, aliases map[string]*nomiast.ExternPackage, byImportPath map[string]*DiscoveredPackage) {
	structBindings := make(map[string]*nomiast.StructDef)
	collectPlainStructBindings(nodes, structBindings)
	collectForeignTypeBindings(file, nodes, moduleName, owner, aliases, byImportPath)
	collectForeignFuncBindings(file, nodes, moduleName, owner, aliases, byImportPath, structBindings)
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

func collectForeignTypeBindings(file string, nodes []nomiast.Node, moduleName, owner string, aliases map[string]*nomiast.ExternPackage, byImportPath map[string]*DiscoveredPackage) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *nomiast.ExternType:
			key := externDeclKey(moduleName, owner, v.Name)
			if v.ForeignAlias == "" || v.ForeignName == "" {
				continue
			}
			pkgDecl := aliases[v.ForeignAlias]
			if pkgDecl == nil {
				continue
			}
			pkg := ensureDiscoveredPackage(byImportPath, pkgDecl, file)
			goTypeExpr := "*" + v.ForeignAlias + "." + v.ForeignName
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

func collectForeignFuncBindings(file string, nodes []nomiast.Node, moduleName, owner string, aliases map[string]*nomiast.ExternPackage, byImportPath map[string]*DiscoveredPackage, structBindings map[string]*nomiast.StructDef) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *nomiast.ImplBlock:
			collectForeignFuncBindings(file, v.Items, moduleName, implOwnerKey(v.Receiver, v.Interface), aliases, byImportPath, structBindings)
		case *nomiast.ExternFunc:
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
