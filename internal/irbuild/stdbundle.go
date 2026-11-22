package irbuild

// The stdlib's lowering as a bundle.
//
// The stdlib's lowering is the same in every process of one binary, and
// lowering it costs about 40 ms natively for the shared modules and 240 ms
// for the listed ones (programStdModules), several times that under
// js/wasm. A std bundle holds both, in two sections.
//
// The shared section is what buildStdlibIndex produced: per module, its IR
// as an object (ir.EncodeObject) and what lowering decided for each
// declaration (the outcome fields of each stdFunc and stdOnce, and the
// module's unlowered table), which names each IR function and cell by its
// linkage name. Each module's object and outcomes are a body of their own,
// and the section's table of contents names, per module, the modules whose
// objects define what its body names. A process reads the table when it
// reads the bundle, and decodes a module's body when it restores the
// module, which is when the stdlib has built it (stdlibIndex.restoring),
// with the bodies of the modules it depends on, all with one ir.Linker
// (stdObjects.decode), so a reference from one module to another's
// declaration is to the symbol the other defines. It builds each module's
// view as buildStdlibIndex does, which reads std's analysis and is cheap,
// and restores the module's outcome in place of lowering it
// (restoreStdModule), in the same order, so the index it files is the one a
// lowering would file. An index holds the IR of every module decoded when
// it was made, and one made before a later decode is replaced
// (stdlibIndex.staleObjects); a program links the modules of its own
// closure among them (stdlibIndex.linkedShared).
//
// The listed section is one lowering of every listed module
// (stdbundlelisted.go), its modules objects too, decoded only when a
// program first reaches one of them, with a linker over the shared
// modules' (ir.NewLinkerOver): its symbols name the shared modules' by
// linkage name, and it names the shared modules it needs decoded first.
//
// Where a bundle comes from:
//
//   - Embedded, in a binary built with the nomi_stdbundle tag and the
//     overlay internal/irbuild/stdbundlegen wrote just before the build
//     (scripts/build-tour-wasm.sh and scripts/release.sh). Such a binary never
//     lowers the stdlib. A bundle that does not match it is reported on
//     stderr, since the build that embedded it is broken, and the binary
//     lowers the stdlib as one without a bundle does.
//   - Otherwise the artifact cache (internal/artifactcache), under a name
//     made from the binary's build ID. A process that finds no bundle
//     lowers the shared modules and writes the shared section; one that
//     finds no listed section lowers the listed modules when a program first
//     reaches one and writes the bundle again with both.
//
// A bundle is checked before it is used: its format version, ir's, the
// stdlib digest, the host registry's digest and the fixtures flag must be
// this process's, each section's checksum must hold, and every module,
// declaration and `once` it names must be the one this process's view holds
// at that place. A bundle that fails a check is ignored, and a cached one is
// written again. Positions name std files as `std/<file>` in the bundle and
// as this process's SourcePath once decoded, so a bundle written where std
// lives at one path is used where it lives at another.
//
// A process that lowers the stdlib still runs on the decoded bundle, so
// its modules are linked by name as a later process's are and every test
// is a test of the bundle; TestStdBundle_DecodesToTheLowering and
// TestStdBundle_ListedDecodesToTheLowering compare the two directly.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"github.com/nomi-language/nomi/std"
)

const (
	stdBundleMagic = "NOMISTDB"
	// stdBundleVersion versions this file's own layout; ir.FormatVersion
	// versions the images inside it.
	stdBundleVersion = 7
	// stdBundleUnused is how long a cached bundle no process has used is
	// kept: each build of the compiler writes its own.
	stdBundleUnused = 24 * time.Hour
)

// stdBundleState is the bundle this process's stdlib lowering came from,
// set once by loadStdlibIndex (under stdlibOnce) and read after it.
var stdBundleState struct {
	// from is "embedded", "cache" or "lowered". Read by tests.
	from string
	// shared is the bundle's shared section, the one this process's index
	// decodes from or encodes to; nil when it does not encode.
	shared []byte
	// listed is the bundle's listed section as the bundle frames it
	// (frameSection), nil when it has none.
	listed []byte
	// path is where this binary's bundle is cached, "" for no cache.
	path string
	// objects is the shared section's modules, each decoded from its
	// object when the process needs it, which the listed section links
	// over; nil when the process runs on a lowering that did not encode.
	objects *stdObjects
}

// stdObjects is the shared section's modules, each decoded from its object
// when the process first needs it (decode), all with one linker.
type stdObjects struct {
	// mu serializes decoding, which writes linker and the maps below, with
	// what reads them: a decode over linker (linkOver) and an index's copy
	// of the decoded modules (decodedModules).
	mu sync.Mutex
	// linker links every module decoded. What is linked against the shared
	// stdlib by name links over it (ir.NewLinkerOver).
	linker *ir.Linker
	// recs is the section's table of contents, one record per module in the
	// bundle's order, and byName finds a record by its module's name.
	recs   []*stdModuleRecord
	byName map[string]*stdModuleRecord
	// mods are the modules decoded so far.
	mods map[*ir.Module]bool
	// linkedFuncs and linkedCells are every decoded function and cell by
	// linkage name: what a record's outcomes name.
	linkedFuncs map[string]*ir.Func
	linkedCells map[string]*ir.Symbol
	// definedIn is the record whose object defines each of them.
	definedIn map[string]*stdModuleRecord
	// rename names a decoded position's file as this process's SourcePath,
	// and badFile is a name it found that is no std file.
	rename  ir.DecodeOption
	badFile string
	// gen counts the decodes that decoded a module, so an index made before
	// one knows it lacks that module (stdlibIndex.objectsGen).
	gen atomic.Int64
}

// stdObjectsDecoded counts, process-wide, the shared modules' objects this
// process decoded: modules, the functions they define once linked, and the
// objects' bytes. Read by tests.
var stdObjectsDecoded struct{ modules, funcs, bytes atomic.Int64 }

// StdObjectsDecodedForTest is what this process decoded of the shared std
// modules' objects so far (stdObjectsDecoded). It panics outside a test
// binary.
func StdObjectsDecodedForTest() (modules, funcs, bytes int) {
	if !testing.Testing() {
		panic("irbuild.StdObjectsDecodedForTest called outside a test")
	}
	d := &stdObjectsDecoded
	return int(d.modules.Load()), int(d.funcs.Load()), int(d.bytes.Load())
}

// stdDecodeEagerly, set by a test before the process's first lowering,
// decodes every shared module's object when the bundle is read, so the
// test can hold a lowering over objects decoded when needed to one over
// all of them (TestStdObjects_DecodedWhenNeededLowersAsAllDecoded).
var stdDecodeEagerly bool

// decode decodes the objects of the modules named and of every module they
// depend on at any depth (stdModuleRecord.deps), those not decoded yet, in
// the bundle's order, and then reads their records' outcomes. It is the one
// place a shared module's object is decoded: restoring a module the stdlib
// has built decodes it, and decoding the listed section decodes the shared
// modules it names.
//
// The dependencies may form cycles (the members of an import cycle name
// each other's functions). The set is closed first, and the linker links a
// name an object reads before the object that defines it is decoded to the
// symbol that object then defines, so the order within the set does not
// matter; the outcomes, which name functions by linkage name, are resolved
// once every object of the set is decoded. A module that does not decode is
// a defect: the section's checksum held, so the bundle's writer and this
// reader disagree.
func (o *stdObjects) decode(names ...string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	want := make([]bool, len(o.recs))
	var visit func(rec *stdModuleRecord)
	visit = func(rec *stdModuleRecord) {
		if want[rec.at] || rec.decoded {
			return
		}
		want[rec.at] = true
		for _, d := range rec.deps {
			visit(o.recs[d])
		}
	}
	for _, name := range names {
		rec := o.byName[name]
		if rec == nil {
			return fmt.Errorf("the std bundle has no module %s", name)
		}
		visit(rec)
	}
	var resolve []func() error
	decoded := false
	for i, rec := range o.recs {
		if !want[i] {
			continue
		}
		res, err := o.readBody(rec)
		if err != nil {
			return fmt.Errorf("decoding %s from the std bundle: %w", rec.name, err)
		}
		resolve = append(resolve, res...)
		decoded = true
	}
	for _, res := range resolve {
		if err := res(); err != nil {
			return err
		}
	}
	if o.badFile != "" {
		return fmt.Errorf("the bundle names %s, which is not a std file", o.badFile)
	}
	for i, rec := range o.recs {
		if want[i] {
			rec.decoded, rec.body = true, nil
		}
	}
	if decoded {
		o.gen.Add(1)
	}
	return nil
}

// decodeAll decodes every module's object.
func (o *stdObjects) decodeAll() error {
	names := make([]string, len(o.recs))
	for i, rec := range o.recs {
		names[i] = rec.name
	}
	return o.decode(names...)
}

// linkOver runs link with a linker over the shared modules', for objects
// that name theirs by linkage name, while no module decodes. It fails when
// mods, the shared lowering they are to link with, holds a module the
// objects did not decode (a module restoring lowered, which no object
// defines).
func (o *stdObjects) linkOver(mods []*ir.Module, link func(*ir.Linker) error) error {
	if o == nil {
		return errors.New("the shared stdlib is not decoded from its objects")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, m := range mods {
		if !o.mods[m] {
			return fmt.Errorf("the shared stdlib's %s is not one its objects decoded", m.Name())
		}
	}
	return link(ir.NewLinkerOver(o.linker))
}

// decodedModules is mods, by namespace key, with every module decoded
// added under its key in pkgOf, and the decodes they reflect (gen). A key
// mods holds keeps its module.
func (o *stdObjects) decodedModules(mods map[string]*ir.Module, pkgOf map[string]string) (map[string]*ir.Module, int64) {
	out := make(map[string]*ir.Module, len(mods))
	for pkg, m := range mods {
		out[pkg] = m
	}
	if o == nil {
		return out, 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, rec := range o.recs {
		if pkg := pkgOf[rec.name]; rec.decoded && rec.mod != nil && out[pkg] == nil {
			out[pkg] = rec.mod
		}
	}
	return out, o.gen.Load()
}

// linkedShared is the shared std modules a program links, by namespace
// key, in key order, taken from have (an index's irModules): the program's
// closure, which depends on the program alone, never on what else the
// process decoded. from is the program's modules (its own, the listed
// modules it links, its std instances).
//
// The closure starts from the base modules every stdlib is built with, the
// std modules the program's files import (imports, importedStdModules of
// each), and each shared module whose object defines a linkage name from's
// modules write, and adds, until nothing is added, every module each one
// imports at any depth (std.StdLib.ModuleImports) and each one's
// dependencies in the bundle (stdModuleRecord.deps). A module the closure
// leaves out declares no type a value of the program can have: a value is
// made by code the program runs, which is in from or in a module of the
// closure, and that code's module declares its type or imports the module
// that does; a host function's or an exported function's values have the
// types its declaration names, which its module imports. So a call
// through an interface selects no impl of a module outside it, and the
// walk that prunes an image (vm.PruneImage) follows impls only in the
// modules a program can reach, the same in every process. An index lowered
// without a bundle has no table of contents, and its program links have
// whole.
func (x *stdlibIndex) linkedShared(have map[string]*ir.Module, imports []string, from ...[]*ir.Module) []*ir.Module {
	pkgs := make([]string, 0, len(have))
	if o := x.objects; o != nil && x.lib != nil {
		in := map[string]bool{}
		queue := append(std.BaseModules(), imports...)
		queue = append(queue, o.definers(from)...)
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			if in[name] {
				continue
			}
			in[name] = true
			queue = append(queue, x.lib.ModuleImports(name)...)
			if rec := o.byName[name]; rec != nil {
				for _, d := range rec.deps {
					queue = append(queue, o.recs[d].name)
				}
			}
		}
		for name := range in {
			if pkg, ok := x.modulePkg[name]; ok && have[pkg] != nil {
				pkgs = append(pkgs, pkg)
			}
		}
	} else {
		for pkg := range have {
			pkgs = append(pkgs, pkg)
		}
	}
	sort.Strings(pkgs)
	out := make([]*ir.Module, len(pkgs))
	for i, pkg := range pkgs {
		out[i] = have[pkg]
	}
	return out
}

// programStdImports is the std modules p's files import, the listed std
// modules declared with it among them (moduleStdImports).
func programStdImports(p *Program) []string {
	var out []string
	for i := range p.Modules {
		out = append(out, moduleStdImports(&p.Modules[i])...)
	}
	return out
}

// moduleStdImports is the std modules m's file imports (importedStdModules),
// which seed the closure of shared modules its program links.
func moduleStdImports(m *Module) []string {
	return importedStdModules(m.Nodes, strings.HasPrefix(m.Name, "std/"))
}

// definers is the name of each shared module whose object defines a
// linkage name one of mods' modules writes.
func (o *stdObjects) definers(mods [][]*ir.Module) []string {
	var links []string
	for _, ms := range mods {
		for _, m := range ms {
			for _, s := range ir.ModuleSymbols(m) {
				if s.Link() != "" {
					links = append(links, s.Link())
				}
			}
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	seen := map[*stdModuleRecord]bool{}
	var out []string
	for _, link := range links {
		if rec := o.definedIn[link]; rec != nil && !seen[rec] {
			seen[rec] = true
			out = append(out, rec.name)
		}
	}
	return out
}

// generation is how many decodes have decoded a module (gen), 0 for none.
func (o *stdObjects) generation() int64 {
	if o == nil {
		return 0
	}
	return o.gen.Load()
}

// loadStdlibIndex is the shared stdlib lowering: decoded from the embedded
// bundle or the cached one, else lowered, written to the cache and decoded
// from what was written.
func loadStdlibIndex() *stdlibIndex {
	st := &stdBundleState
	r := takeStdBundleRead()
	if r.from == "embedded" {
		idx, err := restoreStdShared(r.objects)
		if err == nil {
			st.from, st.shared, st.listed, st.objects = "embedded", r.shared, r.listed, r.objects
			return idx
		}
		fmt.Fprintf(os.Stderr, "nomi: the std bundle this binary embeds is not its own, so it lowers the stdlib: %v\n", err)
		r = &stdBundleRead{path: stdBundlePath()}
		r.readCache()
	}
	st.path = r.path
	if r.from == "cache" {
		idx, err := restoreStdShared(r.objects)
		if err == nil {
			st.from, st.shared, st.listed, st.objects = "cache", r.shared, r.listed, r.objects
			return idx
		}
		stdBundleDebug("%s not used: %v", st.path, err)
	}
	st.from = "lowered"
	idx, shared, err := lowerStdBundle()
	if err != nil {
		if testing.Testing() {
			panic("irbuild: the std lowering does not encode as a bundle: " + err.Error())
		}
		stdBundleDebug("no bundle: %v", err)
		return idx
	}
	st.shared = shared
	writeStdBundle(shared, nil)
	// The process runs on the decoded lowering, as every later process of
	// the binary does.
	dec, objects, err := decodeStdShared(shared)
	if err != nil {
		if testing.Testing() {
			panic("irbuild: the std bundle this process wrote does not decode: " + err.Error())
		}
		stdBundleDebug("the bundle this process wrote does not decode: %v", err)
		return idx
	}
	st.objects = objects
	return dec
}

// stdBundleRead is a bundle found, checked and its shared section's table
// of contents read, which needs no stdlib, so it can run while the stdlib
// builds (PrefetchStdBundle): the embedded bundle when it reads, else the
// cached one when it does. Restoring the records (restoreStdShared) reads
// the stdlib, and decodes each module's object as it restores the module.
type stdBundleRead struct {
	// from is "embedded" or "cache", or "" when neither decoded.
	from           string
	shared, listed []byte
	objects        *stdObjects
	// path is where this binary's bundle is cached, "" for no cache; unset
	// when the embedded bundle decoded.
	path string
}

// readStdBundle reads and decodes the bundle loadStdlibIndex restores.
func readStdBundle() *stdBundleRead {
	r := &stdBundleRead{}
	if len(embeddedStdBundle) > 0 {
		objects, shared, listed, err := readStdBundleSections(embeddedStdBundle)
		if err == nil {
			r.from, r.objects, r.shared, r.listed = "embedded", objects, shared, listed
			return r
		}
		fmt.Fprintf(os.Stderr, "nomi: the std bundle this binary embeds is not its own, so it lowers the stdlib: %v\n", err)
	}
	r.path = stdBundlePath()
	r.readCache()
	return r
}

// readCache reads and decodes the bundle cached at r.path.
func (r *stdBundleRead) readCache() {
	if r.path == "" {
		return
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		return
	}
	objects, shared, listed, err := readStdBundleSections(data)
	if err != nil {
		stdBundleDebug("%s not used: %v", r.path, err)
		return
	}
	r.from, r.objects, r.shared, r.listed = "cache", objects, shared, listed
	if info, err := os.Stat(r.path); err == nil {
		artifactcache.Touch(r.path, info)
	}
}

// stdBundlePrefetch is the bundle read PrefetchStdBundle started, which the
// first loadStdlibIndex takes.
var stdBundlePrefetch struct {
	mu sync.Mutex
	// taken is set once a load has taken the read or made its own, after
	// which no read is started ahead.
	taken bool
	read  chan *stdBundleRead
}

// PrefetchStdBundle starts reading and decoding the std bundle on another
// goroutine, unless the process has read it already, so the first lowering
// finds it decoded: a caller about to check and lower a program calls it
// first, and the read runs while the stdlib's analysis builds (std.Shared).
func PrefetchStdBundle() {
	p := &stdBundlePrefetch
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.taken || p.read != nil {
		return
	}
	read := make(chan *stdBundleRead, 1)
	p.read = read
	go func() { read <- readStdBundle() }()
}

// takeStdBundleRead is the read PrefetchStdBundle started, waiting for it,
// or a read made now.
func takeStdBundleRead() *stdBundleRead {
	p := &stdBundlePrefetch
	p.mu.Lock()
	read := p.read
	p.read, p.taken = nil, true
	p.mu.Unlock()
	if read != nil {
		return <-read
	}
	return readStdBundle()
}

// writeStdBundle writes a bundle of shared and listed (nil for none) to
// this binary's place in the cache, if it has one, and prunes the bundles of
// binaries no process has used for a day.
func writeStdBundle(shared, listed []byte) {
	path := stdBundleState.path
	if path == "" {
		return
	}
	if err := artifactcache.WriteFile(path, assembleStdBundle(thisStdBundleHead(), shared, listed)); err != nil {
		stdBundleDebug("writing %s: %v", path, err)
	}
	artifactcache.Prune(filepath.Dir(path), filepath.Base(path), stdBundleUnused)
}

// stdBundleDebug reports why a bundle was not used, under
// NOMI_DEBUG_STD_BUNDLE=1. A bundle is a cache: a miss is never an error.
func stdBundleDebug(format string, args ...any) {
	if os.Getenv("NOMI_DEBUG_STD_BUNDLE") == "1" {
		fmt.Fprintf(os.Stderr, "nomi: std bundle: "+format+"\n", args...)
	}
}

// stdBundlePath is where this binary's bundle is cached, or "" when there is
// no cache or the binary has no build ID to name it by.
func stdBundlePath() string {
	root, id := artifactcache.Root(), artifactcache.CompilerID()
	if root == "" || id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%t", id, stdBundleVersion, std.FixturesLoaded())))
	return filepath.Join(root, "std", hex.EncodeToString(sum[:16])+".bundle")
}

// StdBundle lowers the stdlib in this process, the shared modules and every
// listed one, and answers it as a bundle: what stdbundlegen writes for a
// binary to embed.
func StdBundle() ([]byte, error) {
	_, shared, err := lowerStdBundle()
	if err != nil {
		return nil, err
	}
	all, needs := lowerListedStd()
	if all.all.err != nil {
		return nil, all.all.err
	}
	listed, err := encodeListedSection(all, needs)
	if err != nil {
		return nil, err
	}
	// Named for no binary, so the bytes depend only on the sources.
	head := thisStdBundleHead()
	head.builder = "stdbundlegen"
	return assembleStdBundle(head, shared, listed), nil
}

// lowerStdBundle lowers the stdlib (buildStdlibIndex) and encodes it as a
// shared section.
//
// Each module is recorded once every module is filed, so a declaration's
// outcome is the one the whole stdlib gives it: filing another module's
// declaration of one spelling may refuse it (addOverload), and a process
// that restores only the modules its programs reach files only those.
func lowerStdBundle() (*stdlibIndex, []byte, error) {
	type lowered struct {
		name     string
		v        *stdModuleView
		irModule *ir.Module
	}
	var mods []lowered
	idx := buildStdlibIndexBy(lowerSharedStdModule, func(name string, v *stdModuleView, irModule *ir.Module) {
		mods = append(mods, lowered{name, v, irModule})
	})
	recs := make([]*stdModuleRecord, len(mods))
	for i, m := range mods {
		recs[i] = recordStdModule(m.name, m.v, m.irModule)
	}
	markStdUnits(recs, idx)
	data, err := encodeSharedSection(recs)
	return idx, data, err
}

// markStdUnits marks each record lowered in one unit with the record
// before it (lowerStdUnit), which idx, the lowering they record, says by
// giving the earlier module the later one in its scope.
func markStdUnits(recs []*stdModuleRecord, idx *stdlibIndex) {
	for i := 1; i < len(recs); i++ {
		recs[i].withPrev = idx.earlierMods[recs[i-1].name][recs[i].name]
	}
}

// stdModuleRecord is what lowering one module decided: the module's IR,
// its unlowered table, and each declaration's outcome, in the view's order.
// Read from a bundle, a record is its entry in the shared section's table
// of contents until its module is decoded (stdObjects.decode): name,
// withPrev, at, deps and body, with the rest unset.
type stdModuleRecord struct {
	name      string
	mod       *ir.Module
	unlowered map[string]string
	cands     []stdCandRecord
	onces     []stdOnceRecord
	// withPrev is set when the module was lowered in one unit, an import
	// cycle, with the record before it (markStdUnits), so restoring the
	// bundle gives it the scope its lowering had (earlierMods).
	withPrev bool
	// at is the record's place in the table of contents, and deps the
	// places of the records whose objects define a function or cell this
	// record's object or outcomes name by linkage name, which are decoded
	// with it.
	at   int
	deps []int
	// body is the record's object and outcomes as the section holds them,
	// until decoded is set.
	body    []byte
	decoded bool
}

// stdCandRecord is the fields of one stdFunc that lowering its module sets,
// taken once every module is filed (lowerStdBundle). The rest are the
// view's, which a process builds from std's analysis.
type stdCandRecord struct {
	key     string
	hasDecl bool

	defaultFill       []bool
	rtCall, rtHostPkg string
	rtFrame           bool
	body              bool
	pkg, why          string
	irBody            *ir.Func
	irSlot            map[int]*ir.Func
}

// stdOnceRecord is the fields of one stdOnce that lowering sets.
type stdOnceRecord struct {
	key     string
	settled bool
	why     string
	irCell  *ir.Symbol
}

func recordStdModule(name string, v *stdModuleView, irModule *ir.Module) *stdModuleRecord {
	rec := &stdModuleRecord{name: name, mod: irModule, unlowered: v.unlowered}
	for _, c := range v.cands {
		f := c.f
		rec.cands = append(rec.cands, stdCandRecord{
			key: f.key, hasDecl: c.fd != nil,
			defaultFill: f.defaultFill,
			rtCall:      f.rtCall, rtHostPkg: f.rtHostPkg, rtFrame: f.rtFrame,
			body: f.body, pkg: f.pkg, why: f.why,
			irBody: f.irBody, irSlot: f.irSlot,
		})
	}
	for _, o := range v.onces {
		rec.onces = append(rec.onces, stdOnceRecord{key: o.key, settled: o.settled, why: o.why, irCell: o.irCell})
	}
	return rec
}

// restoreStdModule is lowerStdlibModule answered from rec: v's declarations
// take the outcomes rec holds, and its module is rec's.
func restoreStdModule(v *stdModuleView, rec *stdModuleRecord) ([]*stdFunc, []*stdOnce, *ir.Module, error) {
	if rec.name != v.module {
		return nil, nil, nil, fmt.Errorf("the bundle has module %s where this stdlib lowers %s", rec.name, v.module)
	}
	if len(rec.cands) != len(v.cands) || len(rec.onces) != len(v.onces) {
		return nil, nil, nil, fmt.Errorf("%s: the bundle has %d functions and %d onces, this stdlib %d and %d",
			v.module, len(rec.cands), len(rec.onces), len(v.cands), len(v.onces))
	}
	funcs := make([]*stdFunc, len(v.cands))
	for i, c := range v.cands {
		r, f := &rec.cands[i], c.f
		if r.key != f.key || r.hasDecl != (c.fd != nil) {
			return nil, nil, nil, fmt.Errorf("%s: the bundle's function %d is %s, this stdlib's %s", v.module, i, r.key, f.key)
		}
		f.defaultFill = r.defaultFill
		f.rtCall, f.rtHostPkg, f.rtFrame = r.rtCall, r.rtHostPkg, r.rtFrame
		f.body, f.pkg, f.why = r.body, r.pkg, r.why
		f.irBody, f.irSlot = r.irBody, r.irSlot
		funcs[i] = f
	}
	for i, o := range v.onces {
		r := &rec.onces[i]
		if r.key != o.key {
			return nil, nil, nil, fmt.Errorf("%s: the bundle's once %d is %s, this stdlib's %s", v.module, i, r.key, o.key)
		}
		o.settled, o.why, o.irCell = r.settled, r.why, r.irCell
	}
	v.unlowered = rec.unlowered
	return funcs, v.onces, rec.mod, nil
}

// --- the bundle's bytes -------------------------------------------------------

// The layout:
//
//	magic "NOMISTDB"
//	uvarint stdBundleVersion, uvarint ir.FormatVersion
//	string std.Digest(), string stdHostDigest(), bool std.FixturesLoaded()
//	string the writer's build ID (informational)
//	the shared section: uint32 its CRC-32, then its bytes
//	bool whether a listed section follows; if so its CRC-32 and bytes
//
// The shared section is its table of contents: uvarint the record count and
// each record's entry. An entry is the module's name, whether it was
// lowered in one unit with the record before it, uvarint the count of its
// dependencies and each one's place in the table (the records whose objects
// define what this one names), and bytes its body. A body is bytes its
// object ("" for a module with no IR), its unlowered table (absent, or
// count and sorted pairs), its declarations and its onces. An *ir.Func or
// an irCell is written as its symbol's linkage name ("" for nil), which the
// object of the record or of one of its dependencies defines, and no name
// is defined by two objects, so a name is in the same module whichever
// modules a process decodes first. Reading the section reads the entries
// and keeps each body's bytes; a body is decoded with its dependencies
// (stdObjects.decode). The listed section is stdbundlelisted.go's.

// stdHostDigest is a checksum of the host function registry the lowering
// reads (stdlibHostFunc): a binary whose registry differs lowers std
// differently. It sums each RtFuncs row's name, Go symbol and Go signature,
// from which the row's projection is computed, so a process reading a
// bundle projects none of the rows it does not call.
var stdHostDigest = sync.OnceValue(func() string {
	var b strings.Builder
	for _, row := range stdlibbindings.RtFuncs() {
		b.WriteString(row.Name)
		b.WriteByte('=')
		rv := reflect.ValueOf(row.Fn)
		if rv.Kind() == reflect.Func {
			if fn := goruntime.FuncForPC(rv.Pointer()); fn != nil {
				b.WriteString(fn.Name())
			}
		}
		b.WriteByte(' ')
		b.WriteString(rv.Type().String())
		b.WriteByte('\n')
	}
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(b.String())))
})

// stdBundleHead is what a bundle's header says it was made for.
type stdBundleHead struct {
	version, format       uint64
	stdDigest, hostDigest string
	fixtures              bool
	// builder is the writer's build ID, for a person reading the file.
	builder string
}

// thisStdBundleHead is the header this process writes and reads.
func thisStdBundleHead() stdBundleHead {
	return stdBundleHead{version: stdBundleVersion, format: ir.FormatVersion, stdDigest: std.Digest(),
		hostDigest: stdHostDigest(), fixtures: std.FixturesLoaded(), builder: artifactcache.CompilerID()}
}

// assembleStdBundle is a bundle of the sections shared and listed (nil for
// none) under head.
func assembleStdBundle(head stdBundleHead, shared, listed []byte) []byte {
	w := &bundleWriter{b: []byte(stdBundleMagic)}
	w.uv(head.version)
	w.uv(head.format)
	w.str(head.stdDigest)
	w.str(head.hostDigest)
	w.bool1(head.fixtures)
	w.str(head.builder)
	w.b = append(w.b, frameSection(shared)...)
	w.bool1(listed != nil)
	if listed != nil {
		w.b = append(w.b, frameSection(listed)...)
	}
	return w.b
}

// frameSection is data as a bundle holds a section: its CRC-32, then data.
func frameSection(data []byte) []byte {
	w := &bundleWriter{b: binary.LittleEndian.AppendUint32(nil, crc32.ChecksumIEEE(data))}
	w.str(string(data))
	return w.b
}

// unframeSection is the section framed holds, checked against its CRC-32.
func unframeSection(framed []byte) ([]byte, error) {
	r := &bundleReader{b: framed}
	if len(r.b) < 4 {
		return nil, errors.New("malformed std bundle: section")
	}
	sum := binary.LittleEndian.Uint32(r.b)
	r.b = r.b[4:]
	data := r.bytes()
	if r.err == nil && len(r.b) != 0 {
		r.fail("section")
	}
	if r.err != nil {
		return nil, r.err
	}
	if crc32.ChecksumIEEE(data) != sum {
		return nil, errors.New("std bundle checksum mismatch")
	}
	return data, nil
}

// splitStdBundle checks data's header and answers its shared section,
// checked against its checksum, and its listed section framed
// (frameSection), checked when it is decoded (decodeListedSection), since
// only a program that reaches a listed module needs it.
func splitStdBundle(data []byte) (shared, listed []byte, err error) {
	if len(data) < len(stdBundleMagic) || string(data[:len(stdBundleMagic)]) != stdBundleMagic {
		return nil, nil, errors.New("not a std bundle")
	}
	r := &bundleReader{b: data[len(stdBundleMagic):]}
	want := thisStdBundleHead()
	if v := r.uv(); r.err == nil && v != want.version {
		return nil, nil, fmt.Errorf("bundle version %d, this build reads %d", v, want.version)
	}
	if v := r.uv(); r.err == nil && v != want.format {
		return nil, nil, fmt.Errorf("IR format version %d, this build reads %d", v, want.format)
	}
	if d := r.str(); r.err == nil && d != want.stdDigest {
		return nil, nil, fmt.Errorf("made from another stdlib (%s, this one is %s)", d, want.stdDigest)
	}
	if d := r.str(); r.err == nil && d != want.hostDigest {
		return nil, nil, fmt.Errorf("made with other host functions (%s, these are %s)", d, want.hostDigest)
	}
	if f := r.bool1(); r.err == nil && f != want.fixtures {
		return nil, nil, errors.New("made with the other fixtures setting")
	}
	r.str()
	framed := r.framed()
	if r.bool1() {
		listed = r.framed()
	}
	if r.err == nil && len(r.b) != 0 {
		r.fail("trailing bytes")
	}
	if r.err != nil {
		return nil, nil, r.err
	}
	if shared, err = unframeSection(framed); err != nil {
		return nil, nil, err
	}
	return shared, listed, nil
}

type bundleWriter struct{ b []byte }

func (w *bundleWriter) uv(v uint64) { w.b = binary.AppendUvarint(w.b, v) }
func (w *bundleWriter) str(s string) {
	w.uv(uint64(len(s)))
	w.b = append(w.b, s...)
}
func (w *bundleWriter) bool1(v bool) {
	if v {
		w.b = append(w.b, 1)
	} else {
		w.b = append(w.b, 0)
	}
}

type bundleReader struct {
	b   []byte
	err error
}

func (r *bundleReader) fail(what string) {
	if r.err == nil {
		r.err = errors.New("malformed std bundle: " + what)
	}
	r.b = nil
}

func (r *bundleReader) uv() uint64 {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.fail("varint")
		return 0
	}
	r.b = r.b[n:]
	return v
}

// count reads a length no larger than what remains.
func (r *bundleReader) count() int {
	n := r.uv()
	if n > uint64(len(r.b)) {
		r.fail("length")
		return 0
	}
	return int(n)
}

func (r *bundleReader) bytes() []byte {
	n := r.count()
	if r.err != nil {
		return nil
	}
	v := r.b[:n:n]
	r.b = r.b[n:]
	return v
}

func (r *bundleReader) str() string { return string(r.bytes()) }

func (r *bundleReader) bool1() bool {
	if len(r.b) < 1 {
		r.fail("bool")
		return false
	}
	v := r.b[0]
	r.b = r.b[1:]
	if v > 1 {
		r.fail("bool")
	}
	return v == 1
}

// framed reads a section as frameSection wrote it, unchecked.
func (r *bundleReader) framed() []byte {
	start := r.b
	if len(r.b) < 4 {
		r.fail("section")
		return nil
	}
	r.b = r.b[4:]
	r.bytes()
	if r.err != nil {
		return nil
	}
	return start[: len(start)-len(r.b) : len(start)-len(r.b)]
}

// stdImageName names a std source file in a bundle as `std/<file>`
// (std.ImageName), recording in bad a path that is not one.
func stdImageName(bad *[]string) func(string) string {
	return func(path string) string {
		name, ok := std.ImageName(path)
		if !ok {
			*bad = append(*bad, path)
			return path
		}
		return name
	}
}

// stdSourceName is stdImageName's inverse: this process's SourcePath for a
// `std/<file>` name, recording in bad a name that is not one.
func stdSourceName(bad *string) func(string) string {
	return func(name string) string {
		rel, ok := strings.CutPrefix(name, "std/")
		if !ok {
			*bad = name
			return name
		}
		return (*std.StdLib)(nil).SourcePath(strings.TrimSuffix(rel, ".nomi"))
	}
}

// encodeSharedSection writes recs, the modules buildStdlibIndex lowered in
// its order, as a shared section.
func encodeSharedSection(recs []*stdModuleRecord) ([]byte, error) {
	var mods []*ir.Module
	for _, rec := range recs {
		if rec.mod != nil {
			mods = append(mods, rec.mod)
		}
	}
	// A process decodes each module's object apart, so a symbol two modules
	// name without a linkage name would be two symbols there.
	if err := ir.CheckLinkage(mods, nil); err != nil {
		return nil, err
	}
	// Each linkage name a module defines, and the module. A process decodes
	// the modules it needs in the order it needs them, and the linker keeps
	// the first definition of a name, so a name two modules defined would be
	// in whichever was decoded first.
	funcs := map[string]*ir.Func{}
	cells := map[string]*ir.Symbol{}
	definer := map[string]int{}
	var twice []string
	define := func(link string, at int) {
		if prev, ok := definer[link]; ok && prev != at {
			twice = append(twice, fmt.Sprintf("%s (%s and %s)", link, recs[prev].name, recs[at].name))
			return
		}
		definer[link] = at
	}
	for at, rec := range recs {
		if rec.mod == nil {
			continue
		}
		for _, f := range rec.mod.Funcs() {
			if s := f.Sym(); s != nil && s.Link() != "" {
				define(s.Link(), at)
				if funcs[s.Link()] == nil {
					funcs[s.Link()] = f
				}
			}
		}
		for _, c := range rec.mod.Cells() {
			if s := c.Sym(); s.Link() != "" {
				define(s.Link(), at)
				if cells[s.Link()] == nil {
					cells[s.Link()] = s
				}
			}
		}
	}
	if len(twice) > 0 {
		sort.Strings(twice)
		return nil, fmt.Errorf("the std lowering defines names in two modules: %s", strings.Join(twice, ", "))
	}
	var bad, missing []string
	w := &bundleWriter{}
	w.uv(uint64(len(recs)))
	for at, rec := range recs {
		b := &bundleWriter{}
		deps := map[int]bool{}
		names := func(link string) {
			if d, ok := definer[link]; ok && d != at {
				deps[d] = true
			}
		}
		// fn writes f by its linkage name, which must be the name of f
		// itself among the modules' functions.
		fn := func(f *ir.Func, what string) {
			if f == nil {
				b.str("")
				return
			}
			link := ""
			if f.Sym() != nil {
				link = f.Sym().Link()
			}
			if link == "" || funcs[link] != f {
				missing = append(missing, what)
			}
			names(link)
			b.str(link)
		}
		fnMap := func(m map[int]*ir.Func, what string) {
			if m == nil {
				b.uv(0)
				return
			}
			b.uv(uint64(len(m) + 1))
			keys := make([]int, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Ints(keys)
			for _, k := range keys {
				b.uv(uint64(k))
				fn(m[k], fmt.Sprintf("%s %d", what, k))
			}
		}
		if rec.mod == nil {
			b.str("")
		} else {
			obj, err := ir.EncodeObject(rec.mod, ir.WithFileNames(stdImageName(&bad)))
			if err != nil {
				return nil, err
			}
			b.str(string(obj))
			for _, s := range ir.ModuleSymbols(rec.mod) {
				if s.Link() != "" {
					names(s.Link())
				}
			}
		}
		if rec.unlowered == nil {
			b.uv(0)
		} else {
			b.uv(uint64(len(rec.unlowered) + 1))
			keys := make([]string, 0, len(rec.unlowered))
			for k := range rec.unlowered {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				b.str(k)
				b.str(rec.unlowered[k])
			}
		}
		b.uv(uint64(len(rec.cands)))
		for i := range rec.cands {
			c := &rec.cands[i]
			b.str(c.key)
			b.bool1(c.hasDecl)
			if c.defaultFill == nil {
				b.uv(0)
			} else {
				b.uv(uint64(len(c.defaultFill) + 1))
				for _, v := range c.defaultFill {
					b.bool1(v)
				}
			}
			b.str(c.rtCall)
			b.str(c.rtHostPkg)
			b.bool1(c.rtFrame)
			b.bool1(c.body)
			b.str(c.pkg)
			b.str(c.why)
			fn(c.irBody, rec.name+" "+c.key)
			fnMap(c.irSlot, rec.name+" "+c.key+" slot")
		}
		b.uv(uint64(len(rec.onces)))
		for _, o := range rec.onces {
			b.str(o.key)
			b.bool1(o.settled)
			b.str(o.why)
			if o.irCell == nil {
				b.str("")
				continue
			}
			if o.irCell.Link() == "" || cells[o.irCell.Link()] != o.irCell {
				missing = append(missing, rec.name+" once "+o.key)
			}
			names(o.irCell.Link())
			b.str(o.irCell.Link())
		}
		w.str(rec.name)
		w.bool1(rec.withPrev)
		sorted := make([]int, 0, len(deps))
		for d := range deps {
			sorted = append(sorted, d)
		}
		sort.Ints(sorted)
		w.uv(uint64(len(sorted)))
		for _, d := range sorted {
			w.uv(uint64(d))
		}
		w.str(string(b.b))
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("the std lowering names files outside std: %s", strings.Join(bad, ", "))
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("the std lowering points outside its modules: %s", strings.Join(missing, ", "))
	}
	return w.b, nil
}

// decodeStdBundle checks data and builds the stdlib index from its shared
// section.
//
// It restores every module, which it builds first (std.All), so it decodes
// every module's object, and checks the bundle lowers them in this
// process's order, which a process restoring only the modules its programs
// reach cannot (decodeStdShared).
func decodeStdBundle(data []byte) (*stdlibIndex, error) {
	shared, _, err := splitStdBundle(data)
	if err != nil {
		return nil, err
	}
	lib := std.All()
	idx, _, err := decodeStdShared(shared)
	if err != nil {
		return nil, err
	}
	if idx, err = idx.restoring(lib, false); err != nil {
		return nil, err
	}
	var want []string
	for _, unit := range stdLoweringUnits(stdLoweringOrder(lib.ModuleNames(), lib), lib) {
		for _, name := range unit {
			if len(lib.Nodes[name]) > 0 && !isProgramStd(name) {
				want = append(want, name)
			}
		}
	}
	if !slices.Equal(idx.order, want) {
		return nil, fmt.Errorf("the bundle lowers the stdlib's modules in another order")
	}
	return idx, nil
}

// readStdBundleSections checks data and reads its shared section's table of
// contents, answering the sections too.
func readStdBundleSections(data []byte) (objects *stdObjects, shared, listed []byte, err error) {
	defer func() {
		// The reader bounds-checks what it reads; a panic is a bug in this
		// file, and the bundle is still only a cache.
		if r := recover(); r != nil {
			objects, err = nil, fmt.Errorf("decoding the std bundle: %v", r)
		}
	}()
	shared, listed, err = splitStdBundle(data)
	if err != nil {
		return nil, nil, nil, err
	}
	objects, err = readSharedSection(shared)
	if err != nil {
		return nil, nil, nil, err
	}
	return objects, shared, listed, nil
}

// decodeStdShared builds the stdlib index from a shared section, with the
// modules the shared stdlib has built restored, their objects decoded, and
// the others pending, restored when a program reaches them
// (stdlibIndex.withModules), and answers the objects the section's modules
// decode from.
func decodeStdShared(data []byte) (idx *stdlibIndex, objects *stdObjects, err error) {
	defer func() {
		// The reader bounds-checks what it reads; a panic is a bug in this
		// file, and the bundle is still only a cache.
		if r := recover(); r != nil {
			idx, objects, err = nil, nil, fmt.Errorf("decoding the std bundle: %v", r)
		}
	}()
	objects, err = readSharedSection(data)
	if err != nil {
		return nil, nil, err
	}
	idx, err = restoreStdShared(objects)
	if err != nil {
		return nil, nil, err
	}
	return idx, objects, nil
}

// restoreStdShared is decodeStdShared from a shared section's table of
// contents.
func restoreStdShared(objects *stdObjects) (idx *stdlibIndex, err error) {
	defer func() {
		if r := recover(); r != nil {
			idx, err = nil, fmt.Errorf("decoding the std bundle: %v", r)
		}
	}()
	recs := objects.recs
	lib := std.Shared()
	names := lib.ModuleNames()
	lowered := map[string]bool{}
	for _, name := range names {
		if !isProgramStd(name) {
			lowered[name] = true
		}
	}
	if len(recs) != len(lowered) {
		return nil, fmt.Errorf("the bundle has %d modules, this stdlib %d", len(recs), len(lowered))
	}
	idx = newStdlibIndex(names)
	idx.objects = objects
	idx.pending = make(map[string]*stdModuleRecord, len(recs))
	// A module's scope is every module of an earlier unit and the other
	// members of its own, as buildStdlibIndexBy gave it.
	done := map[string]bool{}
	var unit []string
	closeUnit := func() {
		for _, name := range unit {
			scope := make(map[string]bool, len(done)+len(unit))
			for m := range done {
				scope[m] = true
			}
			for _, m := range unit {
				if m != name {
					scope[m] = true
				}
			}
			idx.earlierMods[name] = scope
		}
		for _, name := range unit {
			done[name] = true
		}
		unit = nil
	}
	seen := map[string]bool{}
	for _, rec := range recs {
		if !lowered[rec.name] || seen[rec.name] {
			return nil, fmt.Errorf("the bundle has module %s, which this stdlib does not lower", rec.name)
		}
		if !rec.withPrev {
			closeUnit()
		}
		unit = append(unit, rec.name)
		seen[rec.name] = true
		idx.order = append(idx.order, rec.name)
		idx.pending[rec.name] = rec
	}
	closeUnit()
	return idx.restoring(lib, false)
}

// readSharedSection reads a shared section's table of contents: each
// record's entry, with its body kept to decode when the process needs the
// module (stdObjects.decode).
func readSharedSection(data []byte) (*stdObjects, error) {
	r := &bundleReader{b: data}
	objects := &stdObjects{
		linker:      ir.NewLinker(),
		byName:      map[string]*stdModuleRecord{},
		mods:        map[*ir.Module]bool{},
		linkedFuncs: map[string]*ir.Func{},
		linkedCells: map[string]*ir.Symbol{},
		definedIn:   map[string]*stdModuleRecord{},
	}
	objects.rename = ir.WithDecodedFileNames(stdSourceName(&objects.badFile))
	n := r.count()
	for i := 0; i < n && r.err == nil; i++ {
		rec := &stdModuleRecord{name: r.str(), withPrev: r.bool1(), at: i}
		for d := r.count(); d > 0 && r.err == nil; d-- {
			dep := r.uv()
			if dep >= uint64(n) || int(dep) == i {
				r.fail("dependency")
				break
			}
			rec.deps = append(rec.deps, int(dep))
		}
		rec.body = r.bytes()
		if r.err == nil && objects.byName[rec.name] != nil {
			r.fail("module " + rec.name + " twice")
		}
		objects.recs = append(objects.recs, rec)
		objects.byName[rec.name] = rec
	}
	if r.err == nil && len(r.b) != 0 {
		r.fail("trailing bytes")
	}
	if r.err != nil {
		return nil, r.err
	}
	if stdDecodeEagerly {
		if err := objects.decodeAll(); err != nil {
			return nil, err
		}
	}
	return objects, nil
}

// readBody decodes rec's object, linking it, and reads its outcomes,
// answering what resolves the functions and cells they name once every
// object decode decodes with it is linked. o.mu is held.
func (o *stdObjects) readBody(rec *stdModuleRecord) (resolve []func() error, err error) {
	defer func() {
		// The reader bounds-checks what it reads; a panic is a bug in this
		// file.
		if p := recover(); p != nil {
			resolve, err = nil, fmt.Errorf("%v", p)
		}
	}()
	r := &bundleReader{b: rec.body}
	// fn reads a function's linkage name, "" for none unless required, and
	// sets the function it names once the names resolve.
	fn := func(required bool, set func(*ir.Func)) {
		link := r.str()
		if link == "" {
			if required {
				r.fail("function reference")
			}
			return
		}
		resolve = append(resolve, func() error {
			f := o.linkedFuncs[link]
			if f == nil {
				return fmt.Errorf("%s: the std bundle names function %s, which no decoded module defines", rec.name, link)
			}
			set(f)
			return nil
		})
	}
	if obj := r.bytes(); r.err == nil && len(obj) > 0 {
		m, err := decodeObject(o.linker, obj, o.rename)
		if err != nil {
			return nil, fmt.Errorf("its object: %w", err)
		}
		rec.mod = m
		o.mods[m] = true
		stdObjectsDecoded.modules.Add(1)
		stdObjectsDecoded.funcs.Add(int64(len(m.Funcs())))
		stdObjectsDecoded.bytes.Add(int64(len(obj)))
		for _, f := range m.Funcs() {
			if s := f.Sym(); s != nil && s.Link() != "" {
				o.linkedFuncs[s.Link()] = f
				o.definedIn[s.Link()] = rec
			}
		}
		for _, c := range m.Cells() {
			if s := c.Sym(); s.Link() != "" {
				o.linkedCells[s.Link()] = s
				o.definedIn[s.Link()] = rec
			}
		}
	}
	if n := r.uv(); n > 0 {
		rec.unlowered = map[string]string{}
		for ; n > 1 && r.err == nil; n-- {
			k := r.str()
			rec.unlowered[k] = r.str()
		}
	}
	for n := r.count(); n > 0 && r.err == nil; n-- {
		c := stdCandRecord{key: r.str(), hasDecl: r.bool1()}
		if n := r.uv(); n > 0 {
			c.defaultFill = make([]bool, 0, min(n-1, uint64(len(r.b))))
			for ; n > 1 && r.err == nil; n-- {
				c.defaultFill = append(c.defaultFill, r.bool1())
			}
		}
		c.rtCall, c.rtHostPkg, c.rtFrame = r.str(), r.str(), r.bool1()
		c.body, c.pkg, c.why = r.bool1(), r.str(), r.str()
		rec.cands = append(rec.cands, c)
		at := len(rec.cands) - 1
		fn(false, func(f *ir.Func) { rec.cands[at].irBody = f })
		if n := r.uv(); n > 0 {
			slots := map[int]*ir.Func{}
			rec.cands[at].irSlot = slots
			for ; n > 1 && r.err == nil; n-- {
				k := int(r.uv())
				fn(true, func(f *ir.Func) { slots[k] = f })
			}
		}
	}
	// Its capacity is the count, so a pointer into it holds.
	count := r.count()
	list := make([]stdOnceRecord, 0, count)
	for ; count > 0 && r.err == nil; count-- {
		list = append(list, stdOnceRecord{key: r.str(), settled: r.bool1(), why: r.str()})
		if link := r.str(); link != "" {
			once := &list[len(list)-1]
			resolve = append(resolve, func() error {
				if once.irCell = o.linkedCells[link]; once.irCell == nil {
					return fmt.Errorf("%s: the std bundle names cell %s, which no decoded module defines", rec.name, link)
				}
				return nil
			})
		}
	}
	if len(list) > 0 {
		rec.onces = list
	}
	if r.err == nil && len(r.b) != 0 {
		r.fail("trailing bytes")
	}
	if r.err != nil {
		return nil, r.err
	}
	return resolve, nil
}
