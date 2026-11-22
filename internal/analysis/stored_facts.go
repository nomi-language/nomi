package analysis

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
)

// Stored facts: what the program's reductions over bodies read of one file,
// kept with the file's record and named so another process can read it.
//
// The reductions are the boot-only check (CheckBootScope), the once-cycle
// check (CheckOnceCycles), the application-field roots (CheckAppRoots), the
// whole-file imports
// (CheckImplImports) and coherence's missing impls (FinalizeCoherence).
// Each reads every file's bodies, through summaries keyed by AST and
// *Symbol pointers that live only as long as one check. A file's FileFacts
// holds the same reading with every declaration named by its declaration
// name (DeclRef: the declaring file's module key and DeclarationNames'
// path), so facts made by different checks, in different processes, fit
// together. ReduceFacts runs the reductions over the facts of every file of
// a program and answers what the passes over the program's analyses
// answer, diagnostic for diagnostic (TestStoredReductionsMatchTheProgram).
//
// A check and ReduceFacts share each reduction's reading and rule, keyed
// by declaration in a check and by DeclRef here: the once-cycle graph and
// report (onceDeclsOf, onceCycleErrors, onceReport), the whole-file import
// decision (implImportsOf, foreignImplsOf, unusedImports), the missing-impl
// bucket and order (missingInBucket, missingInOrder) and the boot-only
// reading, graph and report (bootReading, bootGraphOf, bootGraph.report),
// where a call into std carries the callee's depth in std's own graph
// (stdBootFact) in both. The application-field roots are CheckAppRoots' own
// answer (AppFacts.Errors).
//
// A file's facts depend on the file's own check, and on nothing of
// another file's bodies: a call names its callee by DeclRef, and a call
// into std carries what std's facts say of the callee, which is the same
// in every program. So the facts of a file checked against its imports'
// interfaces (ProgramInterfaces.Check) are the facts of the file in the
// program's check from source, and a build that rechecks some files can
// reduce over their new facts and the others' stored ones.
//
// The application-field roots are kept only for a program with no
// application type (AppFacts.Exact): with one, a root's reads follow calls
// into other files' bodies by the walk itself (appReadWalker.contained),
// which no per-file reading reproduces yet.

// DeclRef names a declaration of a program's project file: the file's
// module key and the declaration's path in it (DeclarationNames). A
// function DeclarationNames does not name is named by its position, "@3:5".
type DeclRef struct {
	File, Name string
}

// FileFacts is one file's reading for the program's reductions.
type FileFacts struct {
	// Path is the file's path, and Base its name, as diagnostics name it
	// ("orders.nomi").
	Path, Base  string
	Boot        BootFacts[DeclRef]
	Once        OnceFacts[DeclRef]
	App         AppFacts
	ImplImports ImplImportFacts
	// Demands is the file's own ImplManifest, bucket by bucket in order,
	// with each position in the file itself written with no file (Path
	// holds it), which keeps the facts small (recording).
	Demands []DemandFact
}

// BootFacts is the boot-only check's reading of one file, each function
// keyed by K: its DeclRef in a file's stored facts, its declaration in a
// check (CheckBootScope).
type BootFacts[K comparable] struct {
	// Fns is each function the file defines with a body: the program's
	// graph is built from every file's.
	Fns []BootFnFact[K]
	// Visits is what the file's report visits, in order: each function and
	// each test body.
	Visits []BootVisit[K]
}

// BootFnFact is one function's calls outside lambdas, and every project
// function it calls, lambdas included. Entry says it is an entry boot (a
// top-level `fn boot` of a file that defines `fn main`), which a test may
// call.
type BootFnFact[K comparable] struct {
	Key    K
	Entry  bool
	Calls  []BootCallFact[K]
	Called []K
}

// BootTarget is a function a body calls or names: a project file's (Fn),
// or std's, with its depth from Supervisor.new (0 when it is not boot-only)
// and the steps of its chain, which are the same in every program
// (stdBootFact).
type BootTarget[K comparable] struct {
	Fn       K
	Std      bool
	StdDepth int
	StdSteps []string
	// Display is how a diagnostic names it (bootName).
	Display string
}

// BootCallFact is one call, to Supervisor.new or to Target.
type BootCallFact[K comparable] struct {
	Line, Col  int
	Supervisor bool
	Target     BootTarget[K]
}

// BootValueFact is a function named as a value.
type BootValueFact[K comparable] struct {
	Line, Col int
	Target    BootTarget[K]
}

// BootVisit is one function or test body the report visits, a function
// keyed by Key. A test body is a root; a function is one unless Boot (it is
// `boot`, a function a test group's boot calls, or an entry boot), and is
// `main`, or private and called by no function of the program.
type BootVisit[K comparable] struct {
	Test               bool
	Key                K
	Main, Public, Boot bool
	// Fn, when above zero, says the function is Fns[Fn-1], whose Calls are
	// its calls, and Calls is empty.
	Fn                 int
	Calls, LambdaCalls []BootCallFact[K]
	Values             []BootValueFact[K]
}

// OnceFacts is the once-cycle check's reading of one file, each
// declaration keyed by K: its DeclRef in a file's stored facts, its node in
// a check (programOnceGraph).
type OnceFacts[K comparable] struct {
	// Decls is each `once` with a value and each function with a body the
	// file defines: the program's graph is built from every file's.
	Decls []OnceDeclFact[K]
	// Reports is the file's `once` bindings in the order its report visits
	// them.
	Reports []K
}

// OnceDeclFact is one `once`'s initializer or one function's body: what it
// reads and calls (onceRef), each target a project file's declaration.
// Display is how a chain names it; Line and Col are a `once`'s declaration,
// and TypeCycle says type inference reports it already.
type OnceDeclFact[K comparable] struct {
	Key       K
	Display   string
	Once      bool
	Line, Col int
	TypeCycle bool
	Refs      []OnceRefFact[K]
}

// OnceRefFact is one read of a `once` or call of a function, at Line.
type OnceRefFact[K comparable] struct {
	Target  K
	Read    bool
	Line    int
	Display string
}

// AppFacts is the application-field root check's answer for one file, when
// it is the file's own (Exact): the program declares no application type,
// so the answer is the calls of an entry boot the file's references make
// and its boot errors.
type AppFacts struct {
	Exact  bool
	Errors []TypeError
}

// ImplImportFacts is a file's stored reading for the whole-file import
// check (CheckImplImports): the imports it decides, the impl functions of
// other project files the file's references resolve or dispatch to, and
// its impl blocks for a type it does not declare. A check reads the same
// from each file's analysis, keyed by analysis and node (keptImplUses).
type ImplImportFacts struct {
	Imports []ImplImportFact[string]
	Uses    []DeclRef
	Foreign []ForeignImplFact[DeclRef]
}

// ImplImportFact is one whole-file import no name uses: the file it imports,
// keyed by F (the zero F for none of the program's), and its diagnostic.
type ImplImportFact[F comparable] struct {
	Target F
	Err    TypeError
}

// ForeignImplFact is an impl block for a type its file does not declare,
// its functions keyed by K.
type ForeignImplFact[K comparable] struct {
	Items        []K
	Recv, Iface  string
	HasInterface bool
}

// DemandFact is one recording of a file's ImplManifest.
type DemandFact struct {
	Iface, Type string
	Rec         Recording
}

// ProgramSupply is the supply side of a program's missing-impl check: the
// impl index's impls of each type with the project files' own laid over
// them, the pairs the identity index can judge, and the identity index
// (programDemands.supplyFor, for every type).
//
// A program built over a stdlib base (StdBase) keeps only what its own
// files change of the base's supply (OverBase): the impls and identity
// entries they add, the pairs they make judgeable (Covered) and those
// they make unjudgeable (Uncovered). The reduction lays them over the base
// it reduces with (ProgramSupply.over). The base holds the stdlib modules
// built so far in the process, which a lazily built stdlib grows, so a
// supply holding the base's part would depend on which modules a process
// happened to have built; what the program's files change does not, since
// an impl a program can reach is in a module it reaches.
type ProgramSupply struct {
	Impls     map[string]map[string]bool
	Covered   map[string]map[string]bool
	Uncovered map[string]map[string]bool
	Identity  map[ImplIdentityKey]bool
	OverBase  bool
}

// over answers the supply of typeName, and the identity index, with s laid
// over base when s holds only the program's part of it.
func (s *ProgramSupply) over(base *StdBase, typeName string, identity *map[ImplIdentityKey]bool) (impls, covered map[string]bool) {
	if !s.OverBase || base == nil {
		return s.Impls[typeName], cloneSet(s.Covered[typeName])
	}
	x := base.index
	impls = cloneSet(x.Impls[typeName])
	for iface := range s.Impls[typeName] {
		if impls == nil {
			impls = map[string]bool{}
		}
		impls[iface] = true
	}
	covered = cloneSet(x.implNamesWithIdentity[typeName])
	for iface := range s.Covered[typeName] {
		if covered == nil {
			covered = map[string]bool{}
		}
		covered[iface] = true
	}
	for iface := range s.Uncovered[typeName] {
		delete(covered, iface)
	}
	if *identity == nil {
		merged := make(map[ImplIdentityKey]bool, len(x.ImplsByIdentity)+len(s.Identity))
		for k, v := range x.ImplsByIdentity {
			if v {
				merged[k] = true
			}
		}
		for k := range s.Identity {
			merged[k] = true
		}
		*identity = merged
	}
	return impls, covered
}

// FactsNamer names declarations by DeclRef: each declaration of the files
// it was made over.
//
// A file's declarations are named when a fact first asks for one of them:
// a file's facts name the few declarations of other files its bodies
// reach, and naming every file the check of the program's entry read took
// 20 to 60 ms at 50k lines. Which file a declaration is in is read from
// the files' declarations and their members (fileOf), which is every node
// another file can name; a declaration nested in a body is named with its
// own file only.
type FactsNamer struct {
	byNode   map[ast.Node]DeclRef
	implItem map[ast.Node]bool
	entry    string
	// pending holds the files not named yet, by module key, and fileOf the
	// file of each of their declarations once a miss has asked for it.
	pending map[string][]ast.Node
	fileOf  map[ast.Node]string
}

// NewFactsNamer names the declarations of files, a program's project files'
// nodes by module key: all of them for a check from source, or a file and
// what its check against interfaces read (FileCheck.FileNodes). entry is
// the program's entry's key.
func NewFactsNamer(files map[string][]ast.Node, entry string) *FactsNamer {
	n := &FactsNamer{byNode: map[ast.Node]DeclRef{}, implItem: map[ast.Node]bool{}, entry: entry, pending: map[string][]ast.Node{}}
	for key, nodes := range files {
		n.pending[key] = nodes
	}
	return n
}

// newEagerFactsNamer is NewFactsNamer naming every file at once, which the
// lazy one must agree with (StoredFactsForTest).
func newEagerFactsNamer(files map[string][]ast.Node, entry string) *FactsNamer {
	n := &FactsNamer{byNode: map[ast.Node]DeclRef{}, implItem: map[ast.Node]bool{}, entry: entry}
	for key, nodes := range files {
		n.add(key, nodes)
	}
	return n
}

// resolve names the file that declares node, if it is one not named yet.
func (n *FactsNamer) resolve(node ast.Node) {
	if len(n.pending) == 0 {
		return
	}
	if _, ok := n.byNode[node]; ok {
		return
	}
	if n.fileOf == nil {
		n.fileOf = map[ast.Node]string{}
		for key, nodes := range n.pending {
			for _, top := range nodes {
				n.fileOf[top] = key
				switch d := top.(type) {
				case *ast.ImplBlock:
					for _, it := range d.Items {
						n.fileOf[it] = key
					}
				case *ast.InterfaceDef:
					for i := range d.Methods {
						n.fileOf[&d.Methods[i]] = key
					}
				}
			}
		}
	}
	key, ok := n.fileOf[node]
	if !ok {
		return
	}
	nodes, pending := n.pending[key]
	if !pending {
		return
	}
	delete(n.pending, key)
	n.add(key, nodes)
}

func (n *FactsNamer) add(key string, nodes []ast.Node) {
	if n.pending != nil {
		delete(n.pending, key)
	}
	for node, name := range DeclarationNames(nodes) {
		n.byNode[node] = DeclRef{File: key, Name: name}
	}
	for _, node := range nodes {
		if d, ok := node.(*ast.ImplBlock); ok {
			for _, it := range d.Items {
				n.implItem[it] = true
			}
		}
	}
}

func (n *FactsNamer) ref(node ast.Node) (DeclRef, bool) {
	n.resolve(node)
	r, ok := n.byNode[node]
	return r, ok
}

// isImplItem reports whether node is a function of an impl block.
func (n *FactsNamer) isImplItem(node ast.Node) bool {
	n.resolve(node)
	return n.implItem[node]
}

// FileFactsOf reads the facts of the file with key, analyzed as fa with
// nodes, whose program's other project files namer names. appTypes says
// the program declares an application type, which a file checked against
// interfaces may not see.
func FileFactsOf(key string, fa *FileAnalysis, nodes []ast.Node, namer *FactsNamer, appTypes bool) *FileFacts {
	f := &FileFacts{Path: fa.FilePath, Base: filepath.Base(fa.FilePath)}
	own := DeclarationNames(nodes)
	ownRef := func(node ast.Node) DeclRef {
		if name, ok := own[node]; ok {
			return DeclRef{File: key, Name: name}
		}
		if r, ok := namer.ref(node); ok {
			return r
		}
		p := topLevelPos(node)
		return DeclRef{File: key, Name: fmt.Sprintf("@%d:%d", p.Line, p.Col)}
	}
	// The boot-only check.
	bootMu.Lock()
	read := bootReading[DeclRef]{fa: fa, s: fa.bootSummaryOf(), std: stdFnsOf(fa.Program()), key: func(fd *ast.FuncDef) (DeclRef, bool) {
		if name, isOwn := own[fd]; isOwn {
			return DeclRef{File: key, Name: name}, true
		}
		if r, ok := namer.ref(fd); ok {
			return r, true
		}
		return ownRef(fd), false
	}}
	f.Boot.Fns = read.fns()
	slices.SortFunc(f.Boot.Fns, func(a, b BootFnFact[DeclRef]) int { return strings.Compare(a.Key.Name, b.Key.Name) })
	f.Boot.Visits = read.visits(nodes, f.Boot.Fns)
	bootMu.Unlock()

	// The once-cycle check.
	f.Once = onceFactsOf(fa, nodes, namer, ownRef)

	// The application-field roots, when the file's answer is its own.
	if !appTypes {
		f.App = AppFacts{Exact: true, Errors: CheckAppRoots(fa, nodes)}
	}

	// The whole-file imports.
	scopes := map[*Scope]string{}
	for k, file := range fa.Program().filesByKey {
		if file != nil && file.ModuleScope != nil {
			scopes[file.ModuleScope] = k
		}
	}
	f.ImplImports.Imports = implImportsOf(fa, func(s *Scope) string {
		target, ok := scopes[s]
		if ok && (target == "" || target == OriginEntry) {
			// The program's check holds its entry under no key.
			target = namer.entry
		}
		return target
	})
	uses := map[DeclRef]bool{}
	implUsesOf(fa, func(node ast.Node) {
		if !namer.isImplItem(node) {
			return
		}
		if r, ok := namer.ref(node); ok && r.File != key {
			uses[r] = true
		}
	})
	for r := range uses {
		f.ImplImports.Uses = append(f.ImplImports.Uses, r)
	}
	sort.Slice(f.ImplImports.Uses, func(i, j int) bool {
		a, b := f.ImplImports.Uses[i], f.ImplImports.Uses[j]
		return a.File < b.File || a.File == b.File && a.Name < b.Name
	})
	f.ImplImports.Foreign = foreignImplsOf(fa, nodes, func(item ast.Node) DeclRef { return DeclRef{File: key, Name: own[item]} })

	// The demands.
	for _, iface := range sortedKeys(fa.ImplManifest) {
		for _, t := range sortedKeys(fa.ImplManifest[iface]) {
			for _, rec := range fa.ImplManifest[iface][t] {
				rec.Pos = f.shortPos(rec.Pos)
				if d := rec.Derive; d != nil {
					short := *d
					short.DerivePos, short.FieldPos = f.shortPos(d.DerivePos), f.shortPos(d.FieldPos)
					rec.Derive = &short
				}
				f.Demands = append(f.Demands, DemandFact{Iface: iface, Type: t, Rec: rec})
			}
		}
	}
	return f
}

// shortPos is pos with no file when it is in the file itself.
func (f *FileFacts) shortPos(pos Pos) Pos {
	if pos.File == f.Path && pos.File != "" {
		pos.File = ""
	}
	return pos
}

// longPos is pos as shortPos had it.
func (f *FileFacts) longPos(pos Pos) Pos {
	if pos.File == "" && (pos.Line != 0 || pos.Col != 0) {
		pos.File = f.Path
	}
	return pos
}

// recording is d's recording with its positions whole again. The derive
// context is the one the facts hold, so a recording read from one fact
// equals itself and no other file's, as in the program.
func (f *FileFacts) recording(d *DemandFact) Recording {
	rec := d.Rec
	rec.Pos = f.longPos(rec.Pos)
	if rec.Derive != nil && (rec.Derive.DerivePos.File == "" || rec.Derive.FieldPos.File == "") {
		rec.Derive.DerivePos, rec.Derive.FieldPos = f.longPos(rec.Derive.DerivePos), f.longPos(rec.Derive.FieldPos)
	}
	return rec
}

// stdFnsOf answers the analysis of the std file of p that declares a
// function with a body, or nil.
func stdFnsOf(p *Program) func(*ast.FuncDef) *FileAnalysis {
	files := map[*FileAnalysis]bool{}
	for _, f := range p.Files() {
		if f != nil && isStdFile(f) {
			files[f] = true
		}
	}
	return stdFnsIn(files)
}

// stdFnsIn answers the analysis of the std file among files, std files
// isStdFile indexed, that declares a function with a body, or nil.
func stdFnsIn(files map[*FileAnalysis]bool) func(*ast.FuncDef) *FileAnalysis {
	return func(fd *ast.FuncDef) *FileAnalysis {
		if f, ok := stdFnFile.Load(fd); ok && files[f.(*FileAnalysis)] {
			return f.(*FileAnalysis)
		}
		return nil
	}
}

// isStdFile reports whether f is a std file, and indexes its functions
// with bodies when it is (stdFnsIn). Each std analysis is indexed once per
// process: std's analyses are shared and never change.
func isStdFile(f *FileAnalysis) bool {
	if _, ok := stdIndexed.Load(f); ok {
		return true
	}
	if _, isStd := stdlibModuleForPath(f.FilePath); !isStd {
		return false
	}
	for _, sym := range f.Definitions {
		if fd, ok := sym.Node.(*ast.FuncDef); ok && sym.Kind == SymbolFunction && fd.Body != nil {
			stdFnFile.Store(fd, f)
		}
	}
	stdIndexed.Store(f, true)
	return true
}

// stdFnFile holds each indexed std function's analysis, and stdIndexed the
// std analyses indexed.
var stdFnFile, stdIndexed sync.Map

// SupplyOf answers the supply side of the missing-impl check of fa's
// program, a program checked from source, or nil when it has no impl index.
func SupplyOf(fa *FileAnalysis) *ProgramSupply {
	p := fa.Program()
	if p == nil || p.ImplIndex == nil {
		return nil
	}
	idx := p.ImplIndex
	extra := map[string]map[string]bool{}
	for _, file := range p.Files() {
		if file == nil || p.stdBase.Owns(file) {
			continue
		}
		for t, ifaces := range file.Impls {
			if extra[t] == nil {
				extra[t] = map[string]bool{}
			}
			for i := range ifaces {
				extra[t][i] = true
			}
		}
	}
	s := &ProgramSupply{Impls: map[string]map[string]bool{}, Covered: map[string]map[string]bool{}, Identity: map[ImplIdentityKey]bool{}}
	for k, v := range idx.ImplsByIdentity {
		if v {
			s.Identity[k] = true
		}
	}
	types := map[string]bool{}
	for t := range idx.Impls {
		types[t] = true
	}
	for t := range extra {
		types[t] = true
	}
	for t := range types {
		impls := cloneSet(idx.Impls[t])
		covered := cloneSet(idx.implNamesWithIdentity[t])
		for iface := range extra[t] {
			if impls == nil {
				impls = map[string]bool{}
			}
			if !idx.Impls[t][iface] {
				delete(covered, iface)
			}
			impls[iface] = true
		}
		if len(impls) > 0 {
			s.Impls[t] = impls
		}
		if len(covered) > 0 {
			s.Covered[t] = covered
		}
	}
	if p.stdBase != nil {
		return s.overBase(p.stdBase)
	}
	return s
}

// overBase answers the part of s, a program's whole supply, that the
// program's own files change of base's (ProgramSupply.OverBase).
func (s *ProgramSupply) overBase(base *StdBase) *ProgramSupply {
	x := base.index
	out := &ProgramSupply{Impls: map[string]map[string]bool{}, Covered: map[string]map[string]bool{},
		Uncovered: map[string]map[string]bool{}, Identity: map[ImplIdentityKey]bool{}, OverBase: true}
	add := func(m map[string]map[string]bool, t, iface string) {
		if m[t] == nil {
			m[t] = map[string]bool{}
		}
		m[t][iface] = true
	}
	for t, ifaces := range s.Impls {
		for iface := range ifaces {
			if !x.Impls[t][iface] {
				add(out.Impls, t, iface)
			}
		}
	}
	for t, ifaces := range s.Covered {
		for iface := range ifaces {
			if !x.implNamesWithIdentity[t][iface] {
				add(out.Covered, t, iface)
			}
		}
	}
	for t, ifaces := range x.implNamesWithIdentity {
		for iface, v := range ifaces {
			if v && !s.Covered[t][iface] {
				add(out.Uncovered, t, iface)
			}
		}
	}
	for k := range s.Identity {
		if !x.ImplsByIdentity[k] {
			out.Identity[k] = true
		}
	}
	return out
}

func cloneSet(m map[string]bool) map[string]bool {
	if m == nil {
		return nil
	}
	out := make(map[string]bool, len(m))
	for k, v := range m {
		if v {
			out[k] = true
		}
	}
	return out
}

// stdContext is what the reductions read of the stdlib: its base's demands
// (StdBase), the same for every program in a process. The facts hold no
// file of the stdlib, so a reduction without them would read a program's
// demands of std's impls, and std's of the program's, as unmet.
type stdContext struct {
	base *StdBase
}

// stdContextOf answers the stdlib fa's program reads, and false when the
// program is not built over a stdlib base holding every std file, the only
// form the reductions over facts read.
func stdContextOf(fa *FileAnalysis) (stdContext, bool) {
	if fa == nil {
		return stdContext{}, false
	}
	p := fa.Program()
	if p == nil || p.stdBase == nil || !p.stdBase.ownsAll(p.Files()) {
		return stdContext{}, false
	}
	return stdContext{base: p.stdBase}, true
}

// Reductions is what the program's reductions over bodies report, by
// module key, and coherence's missing impls, which the entry reports.
// AppRoots holds a file's answer only when every file's AppFacts is exact
// (AppExact).
type Reductions struct {
	Boot, Once, AppRoots, ImplImports map[string][]TypeError
	Coherence                         []TypeError
	AppExact                          bool
}

// Clean reports whether r holds no diagnostic, its application-field roots
// left out when appTypes says the program has an application type, whose
// roots no file's facts answer.
func (r *Reductions) Clean(appTypes bool) bool {
	if len(r.Coherence) > 0 {
		return false
	}
	if !appTypes && (!r.AppExact || len(r.AppRoots) > 0) {
		return false
	}
	return len(r.Boot) == 0 && len(r.Once) == 0 && len(r.ImplImports) == 0
}

// ReduceFacts runs the program's reductions over its project files' facts,
// by module key, with the program's supply and the stdlib over reads: over
// is an analysis of one of the program's files, checked from source or
// against interfaces. It answers false, and reduces nothing, when over's
// program is not built over a stdlib base holding every std file, nil over
// included: the stdlib's demands are then nowhere to read.
func ReduceFacts(files map[string]*FileFacts, supply *ProgramSupply, over *FileAnalysis) (*Reductions, bool) {
	std, ok := stdContextOf(over)
	if !ok {
		return nil, false
	}
	keys := sortedKeys(files)
	r := &Reductions{Boot: map[string][]TypeError{}, Once: map[string][]TypeError{}, AppRoots: map[string][]TypeError{}, ImplImports: map[string][]TypeError{}, AppExact: true}
	reduceBoot(files, keys, r)
	reduceOnce(files, keys, r)
	for _, key := range keys {
		if !files[key].App.Exact {
			r.AppExact = false
			r.AppRoots = map[string][]TypeError{}
			break
		}
		if errs := files[key].App.Errors; len(errs) > 0 {
			r.AppRoots[key] = slices.Clone(errs)
		}
	}
	reduceImplImports(files, keys, std, r)
	r.Coherence = reduceCoherence(files, keys, supply, std)
	return r, true
}

// reduceBoot is CheckBootScope over facts: the program's graph of every
// file's functions (bootGraphOf), then each file's report.
func reduceBoot(files map[string]*FileFacts, keys []string, r *Reductions) {
	read := make([]bootFile[DeclRef], len(keys))
	for i, key := range keys {
		read[i] = bootFile[DeclRef]{base: files[key].Base, fns: files[key].Boot.Fns}
	}
	g := bootGraphOf(read)
	for _, key := range keys {
		if errs := g.report(files[key].Boot); len(errs) > 0 {
			r.Boot[key] = errs
		}
	}
}

// reduceImplImports is CheckImplImports over facts: the uses and demands
// of every file, the stdlib base's demands among them, and each file's
// imports decided by unusedImports.
func reduceImplImports(files map[string]*FileFacts, keys []string, std stdContext, r *Reductions) {
	usedBy := map[DeclRef]map[string]bool{}
	pairs := map[implPair]bool{}
	for _, key := range keys {
		f := files[key]
		for _, u := range f.ImplImports.Uses {
			if usedBy[u] == nil {
				usedBy[u] = map[string]bool{}
			}
			usedBy[u][key] = true
		}
		for _, d := range f.Demands {
			pairs[implPair{typeName: d.Type, iface: d.Iface}] = true
		}
	}
	demanded := func(p implPair) bool {
		return pairs[p] || std.base.pairs[p]
	}
	foreignOf := func(target string) ([]ForeignImplFact[DeclRef], bool) {
		if f := files[target]; f != nil {
			return f.ImplImports.Foreign, true
		}
		return nil, false
	}
	for _, key := range keys {
		var errs []TypeError
		imports := files[key].ImplImports.Imports
		for i, unused := range unusedImports(imports, foreignOf, usedBy, demanded) {
			if unused {
				errs = append(errs, imports[i].Err)
			}
		}
		if len(errs) > 0 {
			r.ImplImports[key] = errs
		}
	}
}

// reduceCoherence is FinalizeCoherence over facts: programDemands' buckets
// with the files in key order, then the stdlib base's.
func reduceCoherence(files map[string]*FileFacts, keys []string, supply *ProgramSupply, std stdContext) []TypeError {
	if supply == nil {
		return nil
	}
	type bucket struct {
		recs   []Recording
		wanted bool
	}
	buckets := map[implPair]*bucket{}
	get := func(p implPair) *bucket {
		b := buckets[p]
		if b == nil {
			b = &bucket{}
			buckets[p] = b
		}
		return b
	}
	if std.base != nil {
		for _, p := range std.base.wanted {
			get(p).wanted = true
		}
	}
	// A bucket is wanted when a recording outside the stdlib demands it, in a
	// file outside the stdlib.
	for _, key := range keys {
		f := files[key]
		for i := range f.Demands {
			d := &f.Demands[i]
			rec := f.recording(d)
			b := get(implPair{typeName: d.Type, iface: d.Iface})
			b.recs = append(b.recs, rec)
			if !isStdlibRecording(rec) {
				b.wanted = true
			}
		}
	}
	missing := map[implPair]TypeError{}
	// identity is the identity index with the base's laid under the
	// supply's, made on first use (ProgramSupply.over).
	var identity map[ImplIdentityKey]bool
	for p, b := range buckets {
		if !b.wanted {
			continue
		}
		recs := slices.Clone(b.recs)
		if std.base != nil {
			recs = append(recs, std.base.demands[p.iface][p.typeName]...)
		}
		_, e, ok := missingInBucket(p, recs, func() *ProjectImplIndex {
			impls, covered := supply.over(std.base, p.typeName, &identity)
			ids := supply.Identity
			if identity != nil {
				ids = identity
			}
			return &ProjectImplIndex{
				Impls:                 map[string]map[string]bool{p.typeName: impls},
				ImplsByIdentity:       ids,
				implNamesWithIdentity: map[string]map[string]bool{p.typeName: covered},
			}
		})
		if ok {
			missing[p] = e
		}
	}
	return missingInOrder(missing)
}

const factsMagic = "NOMIFACTS5"

// Encode writes f.
func (f *FileFacts) Encode() []byte {
	return gobEncode(factsMagic, f)
}

// DecodeFileFacts reads what Encode wrote.
func DecodeFileFacts(data []byte) (*FileFacts, error) {
	f := &FileFacts{}
	return f, gobDecode(factsMagic, data, f)
}

const supplyMagic = "NOMISUPPLY3"

// Encode writes s.
func (s *ProgramSupply) Encode() []byte {
	return gobEncode(supplyMagic, s)
}

// DecodeProgramSupply reads what Encode wrote.
func DecodeProgramSupply(data []byte) (*ProgramSupply, error) {
	s := &ProgramSupply{}
	return s, gobDecode(supplyMagic, data, s)
}

func gobEncode(magic string, v any) []byte {
	var b bytes.Buffer
	b.WriteString(magic)
	if gob.NewEncoder(&b).Encode(v) != nil {
		return nil
	}
	return b.Bytes()
}

func gobDecode(magic string, data []byte, v any) error {
	rest, ok := bytes.CutPrefix(data, []byte(magic))
	if !ok {
		return errors.New("analysis: not " + magic)
	}
	return gob.NewDecoder(bytes.NewReader(rest)).Decode(v)
}

// StoredFactsForTest answers, for full, a program dm checked: its project
// files' facts read from the check from source and from each file's check
// against interfaces, each encoded, and what the program's reductions
// reported (whole) beside what ReduceFacts answers over the facts from
// source, decoded from their encoding (stored). The two facts must be
// equal and the two reductions must be equal
// (TestStoredReductionsMatchTheProgram); stored is nil when the program has
// no stdlib base.
func (dm *DocumentManager) StoredFactsForTest(full *CheckedProgram) (fromSource, fromInterfaces map[string][]byte, whole, stored *Reductions) {
	project := map[string][]ast.Node{}
	for key, nodes := range full.Nodes {
		if !IsStdlibKey(key) && full.Files[key] != nil {
			project[key] = nodes
		}
	}
	namer := NewFactsNamer(project, full.Entry)
	entry := full.Files[full.Entry]
	appTypes := len(entry.Program().AppTypes) > 0
	fromSource, fromInterfaces = map[string][]byte{}, map[string][]byte{}
	decoded := map[string]*FileFacts{}
	for key, nodes := range project {
		data := FileFactsOf(key, full.Files[key], nodes, namer, appTypes).Encode()
		fromSource[key] = data
		f, err := DecodeFileFacts(data)
		if err != nil {
			panic(err)
		}
		decoded[key] = f
	}
	dm.lockBuild(true)
	pi := NewProgramInterfaces(dm.sourceProgram(full))
	for _, key := range pi.Keys() {
		fc := pi.Check(key)
		files := map[string][]ast.Node{key: fc.Nodes}
		for k, n := range fc.FileNodes {
			files[k] = n
		}
		fromInterfaces[key] = FileFactsOf(key, fc.FA, fc.Nodes, NewFactsNamer(files, full.Entry), appTypes).Encode()
		if eager := FileFactsOf(key, fc.FA, fc.Nodes, newEagerFactsNamer(files, full.Entry), appTypes).Encode(); !bytes.Equal(eager, fromInterfaces[key]) {
			// The lazy namer must name what the eager one names: the
			// difference fails the comparison with the program's facts.
			fromInterfaces[key] = append([]byte("the lazy namer named another declaration than the eager one: "), eager...)
		}
	}
	dm.analyzeMu.Unlock()

	c := full.checks
	whole = &Reductions{Boot: map[string][]TypeError{}, Once: map[string][]TypeError{}, AppRoots: map[string][]TypeError{}, ImplImports: map[string][]TypeError{}, Coherence: c.coherence}
	whole.AppExact = !appTypes
	for key := range project {
		if errs := c.bootScope[key]; len(errs) > 0 {
			whole.Boot[key] = errs
		}
		if errs := c.onceCycles[key]; len(errs) > 0 {
			whole.Once[key] = errs
		}
		if errs := c.implImports[key]; len(errs) > 0 {
			whole.ImplImports[key] = errs
		}
		if errs := c.appRoots[key]; len(errs) > 0 && whole.AppExact {
			whole.AppRoots[key] = errs
		}
	}
	supply := SupplyOf(entry)
	if supply != nil {
		var err error
		if supply, err = DecodeProgramSupply(supply.Encode()); err != nil {
			panic(err)
		}
	}
	stored, _ = ReduceFacts(decoded, supply, entry)
	return fromSource, fromInterfaces, whole, stored
}

// Supply answers the supply side of the missing-impl check of the program
// whose entry has key: the entry built from its source against every other
// file's interface, which registers every file's impls and checks no body.
// A build that relinks after an edit that adds, removes or changes an impl
// reads it instead of the program's check (SupplyOf); the two agree
// (TestStoredReductionsMatchTheProgram).
func (pi *ProgramInterfaces) Supply(key string) *ProgramSupply {
	all := map[string]bool{}
	for k := range pi.encoded {
		if k != key {
			all[k] = true
		}
	}
	fa, _, _ := pi.build(key, all, nil)
	if fa == nil {
		return nil
	}
	return SupplyOf(fa)
}

// ImportClosure answers the keys of the files the file with key imports,
// directly or through others, read from the interfaces' imports (the file's
// own included), with key itself; Closure reads the file's imports from its
// source instead.
func (pi *ProgramInterfaces) ImportClosure(key string) map[string]bool {
	seen := map[string]bool{key: true}
	queue := slices.Clone(pi.imports[key])
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		if seen[k] {
			continue
		}
		if _, ok := pi.encoded[k]; !ok {
			continue
		}
		seen[k] = true
		queue = append(queue, pi.imports[k]...)
	}
	return seen
}

// Importers answers the keys of the files that import one of keys,
// directly or through others, keys excluded.
func (pi *ProgramInterfaces) Importers(keys []string) map[string]bool {
	by := map[string][]string{}
	for k, imps := range pi.imports {
		if _, ok := pi.encoded[k]; !ok {
			continue
		}
		for _, imp := range imps {
			by[imp] = append(by[imp], k)
		}
	}
	out := map[string]bool{}
	queue := slices.Clone(keys)
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		for _, from := range by[k] {
			if !out[from] {
				out[from] = true
				queue = append(queue, from)
			}
		}
	}
	for _, k := range keys {
		delete(out, k)
	}
	return out
}

// StoredOfEncoded is StoredOf with no impl headers, which StoredOf works
// out when the file has none yet.
func (pi *ProgramInterfaces) StoredOfEncoded(key string) (StoredInterface, bool) {
	data, ok := pi.encoded[key]
	if !ok {
		return StoredInterface{}, false
	}
	fi, err := DecodeInterface(data)
	if err != nil {
		return StoredInterface{}, false
	}
	return StoredInterface{Encoded: data, Path: pi.paths[key], Hash: fi.Hash, Imports: pi.imports[key]}, true
}

// StoredOf answers the interface of the file with key as a store keeps it
// (Stored), its impl headers worked out by its check, or by building it
// when it has had none.
func (pi *ProgramInterfaces) StoredOf(key string) (StoredInterface, bool) {
	data, ok := pi.encoded[key]
	if !ok {
		return StoredInterface{}, false
	}
	fi, err := DecodeInterface(data)
	if err != nil {
		return StoredInterface{}, false
	}
	return StoredInterface{Encoded: data, Path: pi.paths[key], Hash: fi.Hash, Imports: pi.imports[key], Headers: pi.implHeaders(key)}, true
}

// SupplyDiffForTest answers how the supply of full, a program dm checked,
// worked out from its interfaces (ProgramInterfaces.Supply) differs from
// the program's (SupplyOf), or "".
func (dm *DocumentManager) SupplyDiffForTest(full *CheckedProgram) string {
	want := SupplyOf(full.Files[full.Entry])
	dm.lockBuild(true)
	pi := NewProgramInterfaces(dm.sourceProgram(full))
	got := pi.Supply(full.Entry)
	dm.analyzeMu.Unlock()
	if want == nil || got == nil {
		if (want == nil) != (got == nil) {
			return fmt.Sprintf("one supply is nil: from the program %v, from interfaces %v", want == nil, got == nil)
		}
		return ""
	}
	diff := func(name string, a, b map[string]map[string]bool) string {
		for t, ifaces := range a {
			for i := range ifaces {
				if !b[t][i] {
					return fmt.Sprintf("%s: %s %s only from the program", name, t, i)
				}
			}
		}
		for t, ifaces := range b {
			for i := range ifaces {
				if !a[t][i] {
					return fmt.Sprintf("%s: %s %s only from interfaces", name, t, i)
				}
			}
		}
		return ""
	}
	if d := diff("impls", want.Impls, got.Impls); d != "" {
		return d
	}
	if d := diff("covered", want.Covered, got.Covered); d != "" {
		return d
	}
	if d := diff("uncovered", want.Uncovered, got.Uncovered); d != "" {
		return d
	}
	if want.OverBase != got.OverBase {
		return fmt.Sprintf("over a base: from the program %v, from interfaces %v", want.OverBase, got.OverBase)
	}
	for k := range want.Identity {
		if !got.Identity[k] {
			return fmt.Sprintf("identity %+v only from the program", k)
		}
	}
	for k := range got.Identity {
		if !want.Identity[k] {
			return fmt.Sprintf("identity %+v only from interfaces", k)
		}
	}
	return ""
}
