package analysis

import (
	"errors"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/syntheticextern"
)

// Checking a file against interfaces. A file F of a program is built from
// its source, and every other file its check reads from that file's
// interface (interface.go), decoded from bytes:
//
//   - the files of F's import closure, the prelude's closure being the
//     shared stdlib's;
//   - of every other file, the impls F can see: an impl whose interface,
//     receiver and type arguments are all declared in F's closure, the
//     stdlib or F (the relevant impl set, docs/design "Impls"). Such a
//     file is given as its imports and those impl blocks alone.
//
// No other file of the program's module is read. A file of another module
// (a dependency) is read from its source, as the program's check reads it.
// The program facts F's check depends on are given to the build rather
// than worked out from its files, which are not all the program's: the
// application types declared in F's closure, and the diagnostics of the
// checks that read every file's declarations (entry placement and exported
// names). The diagnostics the program reduces over every file's bodies
// (application-field roots, unused whole-file imports, coherence, boot-only
// calls, message agreement) are the program's and are not a file's check.
//
// F that is not the program's entry is built as a file its build reaches
// (buildInputs.roots) from an entry with no declarations, so that it is
// checked as a file other than the entry, as in the program.

// errNotInClosure is the loader's answer for a file a check against
// interfaces does not read.
var errNotInClosure = errors.New("not read by this check")

// SourceProgram is a program checked from source, as checking its files
// against interfaces reads it: how the check was built, and what it found
// of each file's declarations.
type SourceProgram struct {
	// Root is the project root, Entry the entry's module key ("" when it
	// has none) and EntryPath its absolute path, as the check's build was
	// given them, and PlaceEntry whether the build checked the entry's
	// placement.
	Root, Entry, EntryPath string
	PlaceEntry             bool
	// VirtualManifest is the manifest the build was given, nil for one it
	// read from Root, and Manifest the one it used, nil for none.
	VirtualManifest, Manifest *Manifest
	// Files and Nodes are every file the build reached, the stdlib
	// excluded, the entry under Entry, as the check left them.
	Files map[string]*FileAnalysis
	Nodes map[string][]ast.Node
	// Source answers the nodes the build was given for the project file
	// with key, freshly made each call (a build changes the nodes it is
	// given): parsed, with Go bindings injected and keyword declarations
	// expanded, and for the entry before derive lowering. It answers false
	// for a file it cannot give, which is then read from its source
	// through Loader as the check read it.
	Source func(key string) ([]ast.Node, bool)
	// PrepareEntry lowers and synthesizes the entry's nodes as the check
	// did, answering the errors reported before analysis.
	PrepareEntry func(nodes []ast.Node) ([]ast.Node, []TypeError)
	// Loader loads the stdlib and the files of other modules.
	Loader FileLoader
	// The shared stdlib the check was built against.
	Primitives *Scope
	Modules    map[string]*Scope
	StdlibFAs  map[string]*FileAnalysis
	// AppTypes are the program's application types, or AppTypeNames
	// their declaring files' origins and names when the program's check is
	// not at hand (NewStoredInterfaces).
	AppTypes     []*StructType
	AppTypeNames []AppTypeName
	// Session marks a REPL session's program, typed a file at a time
	// (frontend.Checker.SessionSource): an inherent impl may extend a type
	// another of its files declares, as an input could extend a type an
	// earlier input declared when the inputs were one file, and the
	// entry's own boot adds its application type to AppTypeNames, since
	// each new file is the entry while it is checked.
	Session bool
	// ScopeNames are the names a session's next file has in scope though
	// it imports only the ones its text names: every function, value,
	// once and type earlier files left in force. A "did you mean" hint
	// for an undefined name in the entry offers them too
	// (FileDecls.ScopeNames).
	ScopeNames []string
}

// AppTypeName is an application type by its declaring file's origin and
// its name.
type AppTypeName struct{ Origin, Name string }

// appTypeNames answers the program's application types by name.
func (sp *SourceProgram) appTypeNames() []AppTypeName {
	if sp.AppTypeNames != nil {
		return sp.AppTypeNames
	}
	out := make([]AppTypeName, 0, len(sp.AppTypes))
	for _, st := range sp.AppTypes {
		out = append(out, AppTypeName{st.Origin, st.Name})
	}
	return out
}

// ProgramInterfaces are a program's files' interfaces, encoded, and what
// checking one of its files against them is given.
type ProgramInterfaces struct {
	sp *SourceProgram
	// encoded holds each project file's interface by module key, and
	// imports the keys of the project files it imports.
	encoded map[string][]byte
	imports map[string][]string
	// diags holds the placement and exported-name diagnostics of each file
	// by key, worked out over every file's declarations.
	diags map[string][]TypeError
	// manifestName and index classify an import as discovery does.
	manifestName string
	index        ModuleIndex
	// paths holds each project file's path by key, and headers each file's
	// impl headers once worked out (implHeaders).
	paths   map[string]string
	headers map[string][]ImplHeader
	// bodies holds the files a check reads with their bodies grafted onto
	// their interfaces (SetBodies).
	bodies map[string]bool
}

// NewProgramInterfaces extracts and writes the interface of every file of
// sp that Source gives.
func NewProgramInterfaces(sp *SourceProgram) *ProgramInterfaces {
	pi := &ProgramInterfaces{
		sp:      sp,
		encoded: map[string][]byte{},
		imports: map[string][]string{},
		paths:   map[string]string{},
		headers: map[string][]ImplHeader{},
	}
	if sp.Manifest != nil {
		pi.manifestName = sp.Manifest.Name
	}
	if sp.Root != "" {
		pi.index, _ = BuildModuleIndex(sp.Root)
	}
	ifaces := map[string]*FileInterface{}
	for key, fa := range sp.Files {
		nodes, ok := sp.Source(key)
		if !ok {
			continue
		}
		ifaces[key] = ExtractInterface(key, fa.FilePath, nodes)
		pi.encoded[key] = ifaces[key].Encode()
		pi.paths[key] = fa.FilePath
	}
	for key, fi := range ifaces {
		pi.imports[key] = pi.projectKeys(importsOf(fi.Nodes))
	}
	pi.diags = programDeclDiags(sp)
	return pi
}

// StoredInterface is one project file's interface as a store keeps it for
// checking the program's files against interfaces later
// (NewStoredInterfaces): the encoded interface, its file's path, its hash,
// the keys of the project files it imports, and its impl headers.
type StoredInterface struct {
	Encoded []byte
	Path    string
	Hash    string
	Imports []string
	Headers []ImplHeader
}

// Stored answers every project file's interface for a store, its impl
// headers resolved from the program's check from source, which a check
// against interfaces answers alike (TestImplHeadersFromInterfacesMatchTheProgram).
// ok is false when a file's placement or export-name diagnostics are not
// empty, which a store does not keep.
func (pi *ProgramInterfaces) Stored() (out map[string]StoredInterface, ok bool) {
	for _, diags := range pi.diags {
		if len(diags) > 0 {
			return nil, false
		}
	}
	out = map[string]StoredInterface{}
	for _, key := range pi.Keys() {
		fi, err := DecodeInterface(pi.encoded[key])
		if err != nil {
			return nil, false
		}
		headers, known := pi.headers[key]
		if !known {
			headers = ResolveImplHeaders(pi.sp.Files[key], pi.sp.Nodes[key], fi.Nodes, pi.keyOfPath)
		}
		out[key] = StoredInterface{Encoded: pi.encoded[key], Path: pi.paths[key], Hash: fi.Hash,
			Imports: pi.imports[key], Headers: headers}
	}
	return out, true
}

// NewStoredInterfaces is the ProgramInterfaces of a program whose files'
// interfaces a store kept (Stored), for checking a file of it against
// them with no check of the whole program: sp describes how the program
// is built, with no Files or Nodes, and its Source answers the files to
// check. A file whose text changed is given its new interface in files.
// The program's placement and export-name diagnostics are none, as Stored
// requires.
func NewStoredInterfaces(sp *SourceProgram, files map[string]StoredInterface) *ProgramInterfaces {
	pi := &ProgramInterfaces{
		sp:      sp,
		encoded: map[string][]byte{},
		imports: map[string][]string{},
		paths:   map[string]string{},
		headers: map[string][]ImplHeader{},
		diags:   map[string][]TypeError{},
	}
	if sp.Manifest != nil {
		pi.manifestName = sp.Manifest.Name
	}
	if sp.Root != "" {
		pi.index, _ = BuildModuleIndex(sp.Root)
	}
	for key, f := range files {
		pi.encoded[key] = f.Encoded
		pi.imports[key] = f.Imports
		pi.paths[key] = f.Path
		pi.headers[key] = f.Headers
	}
	return pi
}

// AddFile gives pi the project file with key at path, whose nodes sp's
// Source answers, one file at a time: a program that grows by files, as a
// REPL session's does, with no check of the whole program. Its interface
// is extracted and written, the files it imports are recorded, and its
// impl headers are worked out by its first check (Check) or by the first
// check that asks for them. A key added again is replaced. It answers
// false when Source cannot give the file.
func (pi *ProgramInterfaces) AddFile(key, path string) bool {
	nodes, ok := pi.sp.Source(key)
	if !ok {
		return false
	}
	fi := ExtractInterface(key, path, nodes)
	pi.encoded[key] = fi.Encode()
	pi.paths[key] = path
	pi.imports[key] = pi.projectKeys(importsOf(fi.Nodes))
	delete(pi.headers, key)
	return true
}

// RemoveFile takes the file with key out of pi: no later check reads it.
func (pi *ProgramInterfaces) RemoveFile(key string) {
	delete(pi.encoded, key)
	delete(pi.paths, key)
	delete(pi.imports, key)
	delete(pi.headers, key)
}

// SetEntry makes the file with key the program's entry, which a check
// builds as the entry is built: its derives lowered and its entry checks
// run. A program that grows by files makes each new file its entry in
// turn.
func (pi *ProgramInterfaces) SetEntry(key, path string) {
	pi.sp.Entry, pi.sp.EntryPath = key, path
}

// SetBodies makes the checks after it read the files with the keys given
// with their bodies: each one's interface with its source's bodies grafted
// on (GraftBodies), and its bodies checked by the check that reads it, so a
// lowering of the checked file can lower an instance of a generic
// declaration of that file the earlier lowerings did not make. Nil reads
// every file as its interface alone.
func (pi *ProgramInterfaces) SetBodies(keys map[string]bool) { pi.bodies = keys }

// decodeForCheck answers the nodes a check reads of the file with key: its
// interface, with its bodies when SetBodies asks for them.
func (pi *ProgramInterfaces) decodeForCheck(key string) []ast.Node {
	nodes := pi.decode(key)
	if !pi.bodies[key] {
		return nodes
	}
	src, ok := pi.sp.Source(key)
	if !ok {
		return nodes
	}
	if _, err := GraftBodies(nodes, src); err != nil {
		return pi.decode(key)
	}
	return nodes
}

// ImplHeadersOf answers the impl headers of the file with key, worked out
// once (implHeaders).
func (pi *ProgramInterfaces) ImplHeadersOf(key string) []ImplHeader { return pi.implHeaders(key) }

// Closure answers the module keys of the project files a check of the file
// with key reads whole: the files it imports, and theirs.
func (pi *ProgramInterfaces) Closure(key string) map[string]bool { return pi.closure(key) }

// Keys answers the module keys of the files with an interface, sorted.
func (pi *ProgramInterfaces) Keys() []string {
	keys := make([]string, 0, len(pi.encoded))
	for key := range pi.encoded {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// projectKeys answers the module keys of the program's files that paths
// name, as discovery classifies an import, in order and without repeats.
func (pi *ProgramInterfaces) projectKeys(paths [][]string) []string {
	var keys []string
	for _, mp := range paths {
		if len(mp) == 0 {
			continue
		}
		key := ""
		if rest, self := SelfNameImport(mp, pi.manifestName); self {
			key = strings.Join(rest, "/")
		} else {
			key = strings.Join(ShortNameImport(mp, pi.index, pi.manifestName), "/")
		}
		if _, ok := pi.encoded[key]; ok && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// decode answers the interface of the file with key, decoded afresh.
func (pi *ProgramInterfaces) decode(key string) []ast.Node {
	fi, err := DecodeInterface(pi.encoded[key])
	if err != nil {
		panic("analysis: an interface written in this process does not decode: " + err.Error())
	}
	return fi.Nodes
}

// source answers the nodes of the file with key, which has an interface.
func (pi *ProgramInterfaces) source(key string) []ast.Node {
	nodes, _ := pi.sp.Source(key)
	return nodes
}

// closure answers the module keys of the files F's check reads whole: the
// project files F imports, and theirs, F excluded.
func (pi *ProgramInterfaces) closure(key string) map[string]bool {
	seen := map[string]bool{}
	queue := pi.projectKeys(importsOf(pi.source(key)))
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		if seen[k] || k == key {
			continue
		}
		seen[k] = true
		queue = append(queue, pi.imports[k]...)
	}
	return seen
}

// ImplHeader is one impl of a file, as the files a check reads decide
// whether it is relevant: the ordinal of the interface's top-level node
// that brings it (an impl block, or the declaration a derive synthesizes
// it from), and the module keys of the project files that declare its
// interface, receiver and type arguments. A name declared outside the
// project (the stdlib) or declared nowhere names no file.
type ImplHeader struct {
	Node  int
	Files []string
}

// relevantTo reports whether h's impl is one a check that reads the files
// in visible can see: each file its header names is among them.
func (h ImplHeader) relevantTo(visible map[string]bool) bool {
	for _, k := range h.Files {
		if !visible[k] {
			return false
		}
	}
	return true
}

// relevantImpls answers, for each project file outside visible, the
// ordinals of its interface's nodes whose impls are relevant to a check
// that reads visible (ImplHeader.relevantTo).
func (pi *ProgramInterfaces) relevantImpls(visible map[string]bool) map[string]map[int]bool {
	out := map[string]map[int]bool{}
	for _, key := range pi.Keys() {
		if visible[key] {
			continue
		}
		for _, h := range pi.implHeaders(key) {
			if !h.relevantTo(visible) {
				continue
			}
			if out[key] == nil {
				out[key] = map[int]bool{}
			}
			out[key][h.Node] = true
		}
	}
	return out
}

// implHeaders answers the impl headers of the file with key, worked out
// once from the file built against its import closure's interfaces with no
// other file's impls: what the names in a header mean depends on the file
// and its imports alone.
func (pi *ProgramInterfaces) implHeaders(key string) []ImplHeader {
	if h, ok := pi.headers[key]; ok {
		return h
	}
	fa, nodes, _ := pi.build(key, pi.closure(key), nil)
	h := ResolveImplHeaders(fa, nodes, pi.decode(key), pi.keyOfPath)
	pi.headers[key] = h
	return h
}

// SetImplHeaders gives the impl headers of the file with key, as an
// earlier check of the same interface and closure worked them out, so a
// check does not build the file to find them.
func (pi *ProgramInterfaces) SetImplHeaders(key string, h []ImplHeader) {
	pi.headers[key] = h
}

// keyOfPath answers the module key of the project file at path, or "".
func (pi *ProgramInterfaces) keyOfPath(path string) string {
	for k, p := range pi.paths {
		if p == path {
			return k
		}
	}
	return ""
}

// ResolveImplHeaders answers the impl headers of a file: fa and nodes are
// its analysis and nodes as a build left them, and iface its interface's
// nodes, whose ordinals the headers name. keyOf answers the module key of
// the project file at a path, "" for any other.
func ResolveImplHeaders(fa *FileAnalysis, nodes, iface []ast.Node, keyOf func(path string) string) []ImplHeader {
	ordinal := map[Pos]int{}
	for i, n := range iface {
		ordinal[topLevelPos(n)] = i
	}
	var out []ImplHeader
	add := func(pos Pos, files []string) {
		if i, ok := ordinal[pos]; ok {
			out = append(out, ImplHeader{Node: i, Files: files})
		}
	}
	for _, n := range nodes {
		block, ok := n.(*ast.ImplBlock)
		if !ok || block.Interface == nil {
			continue
		}
		files := implHeaderFiles(fa, block, keyOf)
		add(Pos{Line: block.Line, Col: block.Col}, files)
		if block.SynthOriginLine != 0 {
			add(Pos{Line: block.SynthOriginLine, Col: block.SynthOriginCol}, files)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}

// implHeaderFiles answers the module keys of the project files that
// declare what the header of block, an interface impl of the file fa
// analyzes, names: its interface, receiver and type arguments, sorted.
func implHeaderFiles(fa *FileAnalysis, block *ast.ImplBlock, keyOf func(path string) string) []string {
	params := map[string]bool{}
	for _, tp := range block.Generics {
		params[tp.Name] = true
	}
	named := map[string]bool{}
	for _, te := range []ast.TypeExpr{block.Interface, block.Receiver} {
		ast.Inspect(te, func(m ast.Node) bool {
			var name string
			var pos Pos
			switch t := m.(type) {
			case *ast.SimpleType:
				name, pos = t.Name, Pos{Line: t.Line, Col: t.Col}
			case *ast.GenericType:
				name, pos = t.Name, Pos{Line: t.Line, Col: t.Col}
			default:
				return true
			}
			if params[name] {
				return true
			}
			ref := fa.References[pos]
			if ref == nil && IsSynthesizedLine(pos.Line) {
				// A synthesized impl's header (a derive's) records no
				// reference; its names mean what they mean in the file.
				ref = lookupTypeName(fa.ModuleScope, name)
			}
			sym := resolvedSymbol(ref)
			if sym == nil || sym.SourceFile == "" || implicitTypeParam(sym) {
				return true
			}
			if k := keyOf(sym.SourceFile); k != "" {
				named[k] = true
			}
			return true
		})
	}
	files := make([]string, 0, len(named))
	for k := range named {
		files = append(files, k)
	}
	sort.Strings(files)
	return files
}

// implicitTypeParam reports whether sym is a type parameter a generic
// receiver names without a `<T>` clause (`impl Named for Box<T>`): a type
// symbol with no declaration and no type, which names no file.
func implicitTypeParam(sym *Symbol) bool {
	return sym.Kind == SymbolType && sym.Node == nil && sym.Type == nil
}

// lookupTypeName answers the symbol a type name means in scope: a bare
// name, or `m.T` through the module m binds.
func lookupTypeName(scope *Scope, name string) *Symbol {
	if scope == nil {
		return nil
	}
	head, rest, dotted := strings.Cut(name, ".")
	sym := scope.Lookup(head)
	for dotted && sym != nil {
		var next string
		next, rest, dotted = strings.Cut(rest, ".")
		switch {
		case sym.Kind == SymbolModule && sym.ModuleScope != nil:
			sym = sym.ModuleScope.Symbols[next]
		default:
			sym = resolvedSymbol(sym)
			if sym == nil {
				return nil
			}
			sym = sym.Members[next]
		}
	}
	return sym
}

// implsOnly keeps of nodes, a file's interface, its imports and the
// declarations at the ordinals given.
func implsOnly(nodes []ast.Node, ordinals map[int]bool) []ast.Node {
	var out []ast.Node
	for i, n := range nodes {
		switch n.(type) {
		case *ast.ImportStmt, *ast.ImportBlock:
			out = append(out, n)
			continue
		}
		if ordinals[i] {
			out = append(out, n)
		}
	}
	return out
}

// dependencyImport is an import of the project file with key that binds
// no name.
func dependencyImport(key string) *ast.ImportStmt {
	var path []ast.Node
	for _, seg := range strings.Split(key, "/") {
		path = append(path, &ast.Ident{Name: seg, Line: 1, Col: 1})
	}
	return &ast.ImportStmt{ModulePath: path, Line: 1, Col: 1}
}

// topLevelPos is a top-level declaration node's Line and Col.
func topLevelPos(n ast.Node) Pos {
	v := reflect.ValueOf(n)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return Pos{}
	}
	line, col := v.Elem().FieldByName("Line"), v.Elem().FieldByName("Col")
	if !line.IsValid() || !col.IsValid() || line.Kind() != reflect.Int || col.Kind() != reflect.Int {
		return Pos{}
	}
	return Pos{Line: int(line.Int()), Col: int(col.Int())}
}

// programDeclDiags works out the diagnostics of the checks that read every
// file's declarations, by module key: the placement of entry callbacks,
// reported with the entry, and exported names, reported with each file.
func programDeclDiags(sp *SourceProgram) map[string][]TypeError {
	out := map[string][]TypeError{}
	files := map[string][]ast.Node{}
	for key, nodes := range sp.Nodes {
		if key != sp.Entry {
			files[key] = nodes
		}
	}
	placed := ""
	if sp.PlaceEntry {
		placed = sp.Entry
	}
	out[sp.Entry] = CheckEntryPlacement(sp.Manifest, sp.Nodes[sp.Entry], placed, files)
	// In the order a build checks them: the other files by key, then the
	// entry.
	keys := make([]string, 0, len(sp.Files))
	for key := range sp.Files {
		if key != sp.Entry && !IsStdlibKey(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	keys = append(keys, sp.Entry)
	exports := make([]ExportFile, 0, len(keys))
	scratch := map[string]*FileAnalysis{}
	for _, key := range keys {
		scratch[key] = &FileAnalysis{}
		exports = append(exports, ExportFile{Path: sp.Files[key].FilePath, Nodes: sp.Nodes[key], FA: scratch[key]})
	}
	CheckExportNames(exports)
	for _, key := range keys {
		out[key] = append(out[key], scratch[key].TypeErrors...)
	}
	return out
}

// FileCheck is one file checked against interfaces: its analysis and
// nodes, and its own diagnostics by the check that found them.
type FileCheck struct {
	FA    *FileAnalysis
	Nodes []ast.Node
	// Build and Check are its build's and its check's errors: CheckTypes,
	// and for a file other than the entry the placement of its `break` and
	// `continue`. EntryChecks are the entry's own checks (its iteration,
	// concurrency and task-lifetime rules) and FrontEnd the errors reported
	// before analysis, both for the program's entry only.
	Build, Check, EntryChecks, FrontEnd []TypeError
	// Files and FileNodes are the other project files the check's build
	// read, by module key: its closure from their interfaces, and the
	// files whose impls it can see as those impls alone. A lowering of F
	// from its check (irbuild's per-file lowering) declares them.
	Files     map[string]*FileAnalysis
	FileNodes map[string][]ast.Node
	// Bodies are the keys of the files of Files read with their bodies
	// (ProgramInterfaces.SetBodies).
	Bodies []string
	// namer names the declarations of the files the check read, made once
	// for the check's facts (stored_edit.go).
	namer *FactsNamer
	// closure and relevant are the files the check read whole and those it
	// read the impls of, at the ordinals given (CheckAfter).
	closure  map[string]bool
	relevant map[string]map[int]bool
	// reused counts the files the check took from an earlier check of its
	// file (CheckAfter).
	reused int
}

// Check checks the file with key, which has an interface, against the
// program's interfaces.
func (pi *ProgramInterfaces) Check(key string) *FileCheck {
	closure := pi.closure(key)
	visible := map[string]bool{key: true}
	for k := range closure {
		visible[k] = true
	}
	return pi.check(key, closure, pi.relevantImpls(visible), nil)
}

// CheckAfter is Check of the file with key after an edit of the files
// edited, given prev, the file's check before the edit: it takes from prev
// the analyses of the files the edit cannot reach, as a kept program's
// recheck keeps the files that do not import an edited one
// (recheckDependents), and builds the others from their interfaces again.
// A file the edit can reach is an edited one, one that imports a file
// built again, directly or through others, one whose analysis holds
// something of a file built again, one the check now reads otherwise (whole
// where it read its impls alone, or other impls of it), and one that can
// see an impl of headers, the impls the edit added, removed or changed. The
// entry of the program checks every file through its closure, so after a
// leaf's signature edit it builds the leaf and its importers again rather
// than every file. It answers Check when prev has nothing to give. A
// caller with readers of prev's analyses holds the view lock: the build
// hands the kept analyses its own program.
func (pi *ProgramInterfaces) CheckAfter(key string, prev *FileCheck, edited []string, headers []ImplHeader) *FileCheck {
	closure := pi.closure(key)
	visible := map[string]bool{key: true}
	for k := range closure {
		visible[k] = true
	}
	relevant := pi.relevantImpls(visible)
	if prev == nil || prev.closure == nil || len(prev.Bodies) > 0 || len(pi.bodies) > 0 || prev.FA == nil || prev.FA.Program() == nil {
		return pi.check(key, closure, relevant, nil)
	}
	fresh := map[string]bool{}
	for _, k := range edited {
		fresh[k] = true
	}
	for k := range prev.Files {
		switch {
		case closure[k] != prev.closure[k]:
			fresh[k] = true
		case !closure[k] && relevant[k] == nil:
			fresh[k] = true
		case !closure[k] && !maps.Equal(relevant[k], prev.relevant[k]):
			fresh[k] = true
		}
		if fresh[k] || len(headers) == 0 {
			continue
		}
		seen := pi.ImportClosure(k)
		for _, h := range headers {
			if h.relevantTo(seen) {
				fresh[k] = true
				break
			}
		}
	}
	for grew := true; grew; {
		grew = false
		paths := map[string]bool{}
		impls := map[ast.Node]bool{}
		for k := range fresh {
			if fa := prev.Files[k]; fa != nil && fa.FilePath != "" {
				paths[fa.FilePath] = true
			}
			if path := pi.paths[k]; path != "" {
				paths[path] = true
			}
			for _, n := range prev.FileNodes[k] {
				if blk, ok := n.(*ast.ImplBlock); ok {
					for _, fn := range implBlockFuncDefs(blk) {
						impls[fn] = true
					}
					for _, ext := range implBlockExternFuncs(blk) {
						impls[ext] = true
					}
				}
			}
		}
		for k, fa := range prev.Files {
			if fresh[k] {
				continue
			}
			reaches := fa == nil || holdsFrom(fa, fresh, paths, impls)
			for _, imported := range fa.ImportedModules {
				reaches = reaches || fresh[imported]
			}
			if reaches {
				fresh[k] = true
				grew = true
			}
		}
	}
	old := prev.FA.Program()
	funcs := map[*FileAnalysis][]*ast.FuncDef{}
	for fn, fa := range old.ScopedFunctionFiles {
		funcs[fa] = append(funcs[fa], fn)
	}
	reused := map[string]reusedFile{}
	for k, fa := range prev.Files {
		if !fresh[k] && (closure[k] || relevant[k] != nil) {
			reused[k] = reusedFile{fa: fa, nodes: prev.FileNodes[k], funcs: funcs[fa]}
		}
	}
	if len(reused) == 0 {
		return pi.check(key, closure, relevant, nil)
	}
	return pi.check(key, closure, relevant, &reusedFrom{files: reused, onces: old.Onces, revision: old.revision})
}

// reusedFrom is what a check takes from an earlier check of its file
// (CheckAfter): the analyses of the files the edit cannot reach, and the
// earlier program's `once` table and revision.
type reusedFrom struct {
	files    map[string]reusedFile
	onces    *OnceTable
	revision int
}

// check is Check over closure and relevant, worked out, taking the
// analyses reuse holds when it is not nil.
func (pi *ProgramInterfaces) check(key string, closure map[string]bool, relevant map[string]map[int]bool, reuse *reusedFrom) *FileCheck {
	fa, nodes, frontEnd, files, fileNodes := pi.buildAll(key, closure, relevant, reuse)
	out := &FileCheck{FA: fa, Nodes: nodes, Build: slices.Clone(fa.TypeErrors), Files: files, FileNodes: fileNodes,
		closure: closure, relevant: relevant}
	if reuse != nil {
		out.reused = len(reuse.files)
	}
	if key == pi.sp.Entry {
		out.Check = CheckTypes(fa, nodes)
		out.EntryChecks = AnalyzeIterSensitivity(fa, nodes)
		out.EntryChecks = append(out.EntryChecks, CheckConcurrentScope(fa, nodes)...)
		out.EntryChecks = append(out.EntryChecks, CheckTaskLifetime(fa, nodes)...)
		out.FrontEnd = frontEnd
	} else {
		out.Check = checkSibling(fa, nodes)
	}
	// A file read with its bodies has them checked, so they are typed for
	// a lowering; it passed its own check when it was checked as itself.
	for k := range pi.bodies {
		if fa := files[k]; fa != nil {
			CheckTypes(fa, fileNodes[k])
			MarkTailCalls(fileNodes[k])
			out.Bodies = append(out.Bodies, k)
		}
	}
	sort.Strings(out.Bodies)
	MarkTailCalls(nodes)
	if _, ok := pi.headers[key]; !ok {
		// Its impl headers, which a check of another file asks for, are
		// what this build resolved: another file's impls change no name in
		// a header.
		pi.headers[key] = ResolveImplHeaders(fa, nodes, pi.decode(key), pi.keyOfPath)
	}
	return out
}

// build builds the file with key from its source, the files of closure
// from their interfaces, and of each file in relevant its imports and the
// interface nodes at the ordinals given, and answers its analysis and
// nodes, and for the program's entry the errors reported before analysis.
// Its bodies are not checked.
func (pi *ProgramInterfaces) build(key string, closure map[string]bool, relevant map[string]map[int]bool) (*FileAnalysis, []ast.Node, []TypeError) {
	fa, nodes, frontEnd, _, _ := pi.buildAll(key, closure, relevant, nil)
	return fa, nodes, frontEnd
}

// buildAll is build, answering besides the build's other project files
// with their nodes, by key.
func (pi *ProgramInterfaces) buildAll(key string, closure map[string]bool, relevant map[string]map[int]bool, reuse *reusedFrom) (*FileAnalysis, []ast.Node, []TypeError, map[string]*FileAnalysis, map[string][]ast.Node) {
	sp := pi.sp
	visible := map[string]bool{key: true}
	for k := range closure {
		visible[k] = true
	}
	var roots []string
	for k := range relevant {
		roots = append(roots, k)
	}
	sort.Strings(roots)
	// The entry of F's build, when F is not the program's: no declarations,
	// and an import of F only when F has no nodes. Discovery passes over a
	// file with none, and the importing file's build loads it on demand,
	// as in the program's check.
	var entryNodes []ast.Node
	if key != sp.Entry {
		if pi.source(key) == nil {
			entryNodes = []ast.Node{dependencyImport(key)}
		} else {
			roots = append([]string{key}, roots...)
		}
	}

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		k := strings.Join(modulePath, "/")
		if IsStdlibKey(k) || len(modulePath) > 0 && modulePath[0] == "std" || filepath.Clean(projectRoot) != filepath.Clean(sp.Root) {
			return sp.Loader(projectRoot, modulePath)
		}
		if _, ok := pi.encoded[k]; !ok {
			// A file the check from source did not read from Source, or a
			// directory the program's files are under.
			nodes, err := sp.Loader(projectRoot, modulePath)
			if err != nil && DiskDirectory(projectRoot, modulePath) == nil {
				keys := make([]string, 0, len(pi.encoded))
				for key := range pi.encoded {
					keys = append(keys, key)
				}
				if dir := VirtualDirectory(modulePath, keys, nil); dir != nil {
					return nil, dir
				}
			}
			return nodes, err
		}
		switch {
		case k == key:
			return pi.source(k), nil
		case closure[k]:
			return pi.decodeForCheck(k), nil
		case relevant[k] != nil:
			return implsOnly(pi.decodeForCheck(k), relevant[k]), nil
		}
		return nil, errNotInClosure
	}

	// The program's application types declared in what the check reads.
	apps := sp.appTypeNames()
	in := &buildInputs{
		roots: roots,
		appTypes: func(files map[string]*FileAnalysis) []*StructType {
			var out []*StructType
			for _, app := range apps {
				for k, fa := range files {
					if !visible[k] || fa.Origin != app.Origin || fa.ModuleScope == nil {
						continue
					}
					if sym := fa.ModuleScope.Symbols[app.Name]; sym != nil {
						if st, ok := sym.Type.(*StructType); ok {
							out = append(out, st)
						}
					}
				}
			}
			return out
		},
		programDiags: func(k string) []TypeError { return pi.diags[k] },
		session:      sp.Session,
		scopeNames:   sp.ScopeNames,
	}
	if reuse != nil {
		in.reused = reuse.files
	}

	if key == sp.Entry {
		prog, frontEnd := sp.PrepareEntry(pi.source(key))
		fa, siblings, siblingNodes := buildProjectWithCache(prog, sp.Primitives, sp.Modules, sp.StdlibFAs, sp.Root, sp.EntryPath, key, sp.PlaceEntry, loader, sp.VirtualManifest, in)
		reuse.carryOnces(fa)
		files, fileNodes := projectFilesOf(siblings, siblingNodes, key)
		return fa, prog, frontEnd, files, fileNodes
	}
	_, siblings, siblingNodes := buildProjectWithCache(entryNodes, sp.Primitives, sp.Modules, sp.StdlibFAs, sp.Root, "", "", false, loader, sp.VirtualManifest, in)
	reuse.carryOnces(siblings[key])
	files, fileNodes := projectFilesOf(siblings, siblingNodes, key)
	return siblings[key], siblingNodes[key], nil, files, fileNodes
}

// carryOnces gives the program fa's build made what the earlier program
// knew of the reused files' `once` bindings, as checkProgramFrom does: the
// types it inferred and the cycles their values read themselves through,
// since none of them reads a file built again.
func (r *reusedFrom) carryOnces(fa *FileAnalysis) {
	if r == nil || fa == nil || fa.Program() == nil {
		return
	}
	kept := map[*FileAnalysis]bool{}
	for _, f := range r.files {
		kept[f.fa] = true
	}
	now := fa.Program()
	for n, site := range r.onces.sites {
		if site.fa == nil || !kept[site.fa] {
			continue
		}
		if r.onces.checked[n] {
			now.Onces.checked[n] = true
		}
		if cycle := r.onces.cycles[n]; cycle != nil {
			now.Onces.cycles[n] = cycle
		}
		if reads := r.onces.reads[n]; reads != nil {
			if now.Onces.reads == nil {
				now.Onces.reads = map[*ast.OnceBinding][]*ast.OnceBinding{}
			}
			now.Onces.reads[n] = reads
		}
	}
	now.revision = r.revision + 1
}

// projectFilesOf keeps of a build's files those of the project other than
// key's.
func projectFilesOf(fas map[string]*FileAnalysis, nodes map[string][]ast.Node, key string) (map[string]*FileAnalysis, map[string][]ast.Node) {
	files, fileNodes := map[string]*FileAnalysis{}, map[string][]ast.Node{}
	for k, fa := range fas {
		if k == key || k == "" || IsStdlibKey(k) {
			continue
		}
		files[k], fileNodes[k] = fa, nodes[k]
	}
	return files, fileNodes
}

// sourceProgram describes full, a program dm checked, for checking its
// files against interfaces: every project file whose text full holds has
// an interface.
func (dm *DocumentManager) sourceProgram(full *CheckedProgram) *SourceProgram {
	text := func(key string) (string, bool) {
		t, ok := full.Texts[key]
		return t, ok
	}
	sp := dm.textSourceProgram(full.Root, full.Entry, text, full.Inputs, nil)
	sp.Files, sp.Nodes = full.Files, full.Nodes
	sp.AppTypes = full.Files[full.Entry].Program().AppTypes
	return sp
}

// textSourceProgram describes the program under root whose entry has
// module key entry, for checking its files against interfaces: text
// answers each project file's text, inputs holds the texts its
// expansions read when known, and own, when not nil, is set to how many
// of the entry's nodes are its declarations each time the entry is
// prepared (CheckedProgram.entryOwn). It has no Files or Nodes, and no
// application types.
func (dm *DocumentManager) textSourceProgram(root, entry string, text func(key string) (string, bool), inputs map[string]ExpansionInput, own *int) *SourceProgram {
	dm.mu.RLock()
	primitives, modules, stdlibFAs := dm.primitives, dm.modules, dm.stdlibFAs
	dm.mu.RUnlock()
	manifest, _ := LoadManifest(root)
	externs := dm.syntheticExternsForRoot(root)
	read := func(path string) (string, bool) {
		if in, ok := inputs[path]; ok {
			return in.Text, in.Found
		}
		return dm.readInput(path)
	}
	return &SourceProgram{
		Root:       root,
		Entry:      entry,
		EntryPath:  filepath.Join(root, filepath.FromSlash(entry)+".nomi"),
		PlaceEntry: true,
		Manifest:   manifest,
		Source: func(key string) ([]ast.Node, bool) {
			text, ok := text(key)
			if !ok {
				return nil, false
			}
			nodes := parseFile(text).nodes
			expand := func(nodes []ast.Node) []ast.Node {
				return ExpandDeclarations(nodes, dm.expansionSite(root, key, manifest, read))
			}
			if key == entry {
				nodes = expand(nodes)
				path := filepath.Join(root, filepath.FromSlash(key)+".nomi")
				if injected, err := syntheticextern.Inject(nodes, externs, isFFIEntryPath(path)); err == nil {
					nodes = injected
				}
				return nodes, true
			}
			if injected, err := syntheticextern.Inject(nodes, externs, false); err == nil {
				nodes = injected
			}
			return expand(nodes), true
		},
		PrepareEntry: func(nodes []ast.Node) ([]ast.Node, []TypeError) {
			nodes, lowerErrs := LowerDerives(nodes)
			if own != nil {
				*own = len(nodes)
			}
			testNameErrs := CheckDuplicateTestNames(nodes)
			prog, _ := SynthesizeDerives(nodes)
			return SynthesizeUniversalDebug(prog), append(lowerErrs, testNameErrs...)
		},
		Loader:     dm.makeLoader(),
		Primitives: primitives,
		Modules:    modules,
		StdlibFAs:  stdlibFAs,
	}
}

// CheckFromInterfacesForTest checks each project file of full, a program
// dm checked, against the interfaces of the program's files (extracted,
// written and read back), and answers the program made of those checks:
// each file's analysis, nodes and own diagnostics from its check against
// interfaces, and the program's diagnostics, which no one file's check
// makes, from full. A check against interfaces is right when the answer
// dumps as full does (FuzzCheckFromInterfacesMatchesFull).
func (dm *DocumentManager) CheckFromInterfacesForTest(full *CheckedProgram) *CheckedProgram {
	dm.lockBuild(true)
	defer dm.analyzeMu.Unlock()
	pi := NewProgramInterfaces(dm.sourceProgram(full))
	files := map[string]*FileCheck{}
	for _, key := range pi.Keys() {
		files[key] = pi.Check(key)
	}
	return full.withFiles(files)
}

// withFiles answers p with the files' analyses, nodes and own diagnostics
// replaced by those in files, and its program's diagnostics and facts as p
// holds them.
func (p *CheckedProgram) withFiles(files map[string]*FileCheck) *CheckedProgram {
	out := &CheckedProgram{
		Root: p.Root, Entry: p.Entry,
		Texts:     p.Texts,
		Files:     map[string]*FileAnalysis{},
		Nodes:     map[string][]ast.Node{},
		Syntax:    p.Syntax,
		Inputs:    p.Inputs,
		factsFrom: p,
	}
	for key, fa := range p.Files {
		out.Files[key], out.Nodes[key] = fa, p.Nodes[key]
		if f := files[key]; f != nil {
			out.Files[key], out.Nodes[key] = f.FA, f.Nodes
		}
	}
	c := p.checks
	local := func(key string) (build, check, entryChecks, frontEnd []TypeError) {
		if f := files[key]; f != nil {
			return f.Build, f.Check, f.EntryChecks, f.FrontEnd
		}
		if key == p.Entry {
			return c.build[key], c.check[key], c.entryChecks, c.frontEnd
		}
		return c.build[key], c.check[key], nil, nil
	}
	out.Errors = c.errorsWith(p, local)
	return out
}

// ImplHeadersForTest answers each project file's impl headers of full, a
// program dm checked, worked out twice: from the file built against its
// closure's interfaces, and from the program's check from source. The two
// must agree (TestImplHeadersFromInterfacesMatchTheProgram).
func (dm *DocumentManager) ImplHeadersForTest(full *CheckedProgram) (fromInterfaces, fromSource map[string][]ImplHeader) {
	dm.lockBuild(true)
	defer dm.analyzeMu.Unlock()
	pi := NewProgramInterfaces(dm.sourceProgram(full))
	fromInterfaces, fromSource = map[string][]ImplHeader{}, map[string][]ImplHeader{}
	for _, key := range pi.Keys() {
		fromInterfaces[key] = pi.implHeaders(key)
		fromSource[key] = ResolveImplHeaders(full.Files[key], full.Nodes[key], pi.decode(key), pi.keyOfPath)
	}
	return fromInterfaces, fromSource
}
