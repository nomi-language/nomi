package irbuild

// The stdlib's lowering as a bundle.
//
// The stdlib's lowering is the same in every process of one binary, and
// lowering it costs about 40 ms natively for the shared modules and 240 ms
// for the listed ones (programStdModules), several times that under
// js/wasm. A std bundle holds both, in two sections.
//
// The shared section is what buildStdlibIndex produced: the modules' IR, one
// ir image, so references between them stay pointers, and per module what
// lowering decided for each declaration (the outcome fields of each stdFunc
// and stdOnce, and the module's unlowered table). A process with a bundle
// builds each module's view as buildStdlibIndex does, which reads std's
// analysis and is cheap, and restores the module's outcome in place of
// lowering it (restoreStdModule), in the same order, so the index it files
// is the one a lowering would file.
//
// The listed section is one lowering of every listed module
// (stdbundlelisted.go), decoded only when a program first reaches one of
// them. Its symbols name the shared modules' by linkage name.
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
// Under `go test`, a process that lowers the stdlib still runs on the
// decoded bundle, so every test is a test of the bundle;
// TestStdBundle_DecodesToTheLowering and
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
	stdBundleVersion = 2
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
}

// loadStdlibIndex is the shared stdlib lowering: decoded from the embedded
// bundle or the cached one, else lowered and written to the cache.
func loadStdlibIndex() *stdlibIndex {
	st := &stdBundleState
	r := takeStdBundleRead()
	if r.from == "embedded" {
		idx, err := restoreStdShared(r.recs)
		if err == nil {
			st.from, st.shared, st.listed = "embedded", r.shared, r.listed
			return idx
		}
		fmt.Fprintf(os.Stderr, "nomi: the std bundle this binary embeds is not its own, so it lowers the stdlib: %v\n", err)
		r = &stdBundleRead{path: stdBundlePath()}
		r.readCache()
	}
	st.path = r.path
	if r.from == "cache" {
		idx, err := restoreStdShared(r.recs)
		if err == nil {
			st.from, st.shared, st.listed = "cache", r.shared, r.listed
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
	if testing.Testing() {
		// Every test runs on the decoded lowering, as every later process
		// of a built binary does.
		dec, err := decodeStdShared(shared)
		if err != nil {
			panic("irbuild: the std bundle this process wrote does not decode: " + err.Error())
		}
		return dec
	}
	return idx
}

// stdBundleRead is a bundle found and its shared section's records decoded,
// which needs no stdlib, so it can run while the stdlib builds
// (PrefetchStdBundle): the embedded bundle when it decodes, else the cached
// one when it does. Restoring the records (restoreStdShared) reads the
// stdlib.
type stdBundleRead struct {
	// from is "embedded" or "cache", or "" when neither decoded.
	from           string
	shared, listed []byte
	recs           []*stdModuleRecord
	// path is where this binary's bundle is cached, "" for no cache; unset
	// when the embedded bundle decoded.
	path string
}

// readStdBundle reads and decodes the bundle loadStdlibIndex restores.
func readStdBundle() *stdBundleRead {
	r := &stdBundleRead{}
	if len(embeddedStdBundle) > 0 {
		recs, shared, listed, err := readStdBundleSections(embeddedStdBundle)
		if err == nil {
			r.from, r.recs, r.shared, r.listed = "embedded", recs, shared, listed
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
	recs, shared, listed, err := readStdBundleSections(data)
	if err != nil {
		stdBundleDebug("%s not used: %v", r.path, err)
		return
	}
	r.from, r.recs, r.shared, r.listed = "cache", recs, shared, listed
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
	data, err := encodeSharedSection(recs)
	return idx, data, err
}

// stdModuleRecord is what lowering one module decided: the module's IR,
// its unlowered table, and each declaration's outcome, in the view's order.
type stdModuleRecord struct {
	name      string
	mod       *ir.Module
	unlowered map[string]string
	cands     []stdCandRecord
	onces     []stdOnceRecord
}

// stdCandRecord is the fields of one stdFunc that lowering its module sets,
// taken once every module is filed (lowerStdBundle). The rest are the
// view's, which a process builds from std's analysis.
type stdCandRecord struct {
	key     string
	hasDecl bool

	tailRec           bool
	arityMin          int
	defaultFill       []bool
	rtCall, rtHostPkg string
	rtFrame           bool
	body              bool
	pkg, why          string
	irBody            *ir.Func
	irArity, irSlot   map[int]*ir.Func
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
			tailRec: f.tailRec, arityMin: f.arityMin, defaultFill: f.defaultFill,
			rtCall: f.rtCall, rtHostPkg: f.rtHostPkg, rtFrame: f.rtFrame,
			body: f.body, pkg: f.pkg, why: f.why,
			irBody: f.irBody, irArity: f.irArity, irSlot: f.irSlot,
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
		f.tailRec, f.arityMin, f.defaultFill = r.tailRec, r.arityMin, r.defaultFill
		f.rtCall, f.rtHostPkg, f.rtFrame = r.rtCall, r.rtHostPkg, r.rtFrame
		f.body, f.pkg, f.why = r.body, r.pkg, r.why
		f.irBody, f.irArity, f.irSlot = r.irBody, r.irArity, r.irSlot
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
// The shared section is bytes the ir image of every module, then uvarint
// the record count and each record. A record is the module's name, its index
// in the image's modules plus one (0 for none), its unlowered table (absent,
// or count and sorted pairs), its declarations and its onces. An *ir.Func is
// written as its module's index plus one (0 for nil) and its index in that
// module's functions; an irCell as its module and its index in that module's
// cells. The listed section is stdbundlelisted.go's.

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
	modIdx := map[*ir.Module]int{}
	type at struct{ mod, i int }
	funcAt := map[*ir.Func]at{}
	cellAt := map[*ir.Symbol]at{}
	for _, rec := range recs {
		if rec.mod == nil {
			continue
		}
		mi := len(mods)
		modIdx[rec.mod] = mi
		mods = append(mods, rec.mod)
		for i, f := range rec.mod.Funcs() {
			funcAt[f] = at{mi, i}
		}
		for i, c := range rec.mod.Cells() {
			cellAt[c.Sym()] = at{mi, i}
		}
	}
	var bad []string
	image, err := ir.EncodeImage(ir.Image{Modules: mods, Entry: -1}, ir.WithFileNames(stdImageName(&bad)))
	if err != nil {
		return nil, err
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("the std lowering names files outside std: %s", strings.Join(bad, ", "))
	}
	w := &bundleWriter{}
	w.str(string(image))
	w.uv(uint64(len(recs)))
	var missing []string
	fn := func(f *ir.Func, what string) {
		if f == nil {
			w.uv(0)
			return
		}
		p, ok := funcAt[f]
		if !ok {
			missing = append(missing, what)
			w.uv(0)
			return
		}
		w.uv(uint64(p.mod + 1))
		w.uv(uint64(p.i))
	}
	fnMap := func(m map[int]*ir.Func, what string) {
		if m == nil {
			w.uv(0)
			return
		}
		w.uv(uint64(len(m) + 1))
		keys := make([]int, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		for _, k := range keys {
			w.uv(uint64(k))
			fn(m[k], fmt.Sprintf("%s %d", what, k))
		}
	}
	for _, rec := range recs {
		w.str(rec.name)
		if rec.mod == nil {
			w.uv(0)
		} else {
			w.uv(uint64(modIdx[rec.mod] + 1))
		}
		if rec.unlowered == nil {
			w.uv(0)
		} else {
			w.uv(uint64(len(rec.unlowered) + 1))
			keys := make([]string, 0, len(rec.unlowered))
			for k := range rec.unlowered {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				w.str(k)
				w.str(rec.unlowered[k])
			}
		}
		w.uv(uint64(len(rec.cands)))
		for i := range rec.cands {
			c := &rec.cands[i]
			w.str(c.key)
			w.bool1(c.hasDecl)
			w.bool1(c.tailRec)
			w.uv(uint64(c.arityMin))
			if c.defaultFill == nil {
				w.uv(0)
			} else {
				w.uv(uint64(len(c.defaultFill) + 1))
				for _, v := range c.defaultFill {
					w.bool1(v)
				}
			}
			w.str(c.rtCall)
			w.str(c.rtHostPkg)
			w.bool1(c.rtFrame)
			w.bool1(c.body)
			w.str(c.pkg)
			w.str(c.why)
			fn(c.irBody, rec.name+" "+c.key)
			fnMap(c.irArity, rec.name+" "+c.key+" arity")
			fnMap(c.irSlot, rec.name+" "+c.key+" slot")
		}
		w.uv(uint64(len(rec.onces)))
		for _, o := range rec.onces {
			w.str(o.key)
			w.bool1(o.settled)
			w.str(o.why)
			if o.irCell == nil {
				w.uv(0)
				continue
			}
			p, ok := cellAt[o.irCell]
			if !ok {
				missing = append(missing, rec.name+" once "+o.key)
				w.uv(0)
				continue
			}
			w.uv(uint64(p.mod + 1))
			w.uv(uint64(p.i))
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("the std lowering points outside its modules: %s", strings.Join(missing, ", "))
	}
	return w.b, nil
}

// decodeStdBundle checks data and builds the stdlib index from its shared
// section.
//
// It restores every module, which it builds first (std.All), and checks the
// bundle lowers them in this process's order, which a process restoring
// only the modules its programs reach cannot (decodeStdShared).
func decodeStdBundle(data []byte) (*stdlibIndex, error) {
	shared, _, err := splitStdBundle(data)
	if err != nil {
		return nil, err
	}
	lib := std.All()
	idx, err := decodeStdShared(shared)
	if err != nil {
		return nil, err
	}
	if idx, err = idx.restoring(lib, false); err != nil {
		return nil, err
	}
	var want []string
	for _, name := range stdLoweringOrder(lib.ModuleNames(), lib) {
		if len(lib.Nodes[name]) > 0 && !isProgramStd(name) {
			want = append(want, name)
		}
	}
	if !slices.Equal(idx.order, want) {
		return nil, fmt.Errorf("the bundle lowers the stdlib's modules in another order")
	}
	return idx, nil
}

// readStdBundleSections checks data and decodes its shared section's
// records, answering the sections too.
func readStdBundleSections(data []byte) (recs []*stdModuleRecord, shared, listed []byte, err error) {
	defer func() {
		// The reader bounds-checks what it reads; a panic is a bug in this
		// file, and the bundle is still only a cache.
		if r := recover(); r != nil {
			recs, err = nil, fmt.Errorf("decoding the std bundle: %v", r)
		}
	}()
	shared, listed, err = splitStdBundle(data)
	if err != nil {
		return nil, nil, nil, err
	}
	recs, err = readSharedSection(shared)
	if err != nil {
		return nil, nil, nil, err
	}
	return recs, shared, listed, nil
}

// decodeStdShared builds the stdlib index from a shared section, with the
// modules the shared stdlib has built restored and the others pending,
// restored when a program reaches them (stdlibIndex.withModules). Every
// module's IR is the image's, which a program links whole.
func decodeStdShared(data []byte) (idx *stdlibIndex, err error) {
	defer func() {
		// The reader bounds-checks what it reads; a panic is a bug in this
		// file, and the bundle is still only a cache.
		if r := recover(); r != nil {
			idx, err = nil, fmt.Errorf("decoding the std bundle: %v", r)
		}
	}()
	recs, err := readSharedSection(data)
	if err != nil {
		return nil, err
	}
	return restoreStdShared(recs)
}

// restoreStdShared is decodeStdShared from a shared section's records.
func restoreStdShared(recs []*stdModuleRecord) (idx *stdlibIndex, err error) {
	defer func() {
		if r := recover(); r != nil {
			idx, err = nil, fmt.Errorf("decoding the std bundle: %v", r)
		}
	}()
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
	idx.pending = make(map[string]*stdModuleRecord, len(recs))
	done := map[string]bool{}
	for _, rec := range recs {
		if !lowered[rec.name] || done[rec.name] {
			return nil, fmt.Errorf("the bundle has module %s, which this stdlib does not lower", rec.name)
		}
		earlier := make(map[string]bool, len(done))
		for m := range done {
			earlier[m] = true
		}
		idx.earlierMods[rec.name] = earlier
		done[rec.name] = true
		idx.order = append(idx.order, rec.name)
		idx.pending[rec.name] = rec
		pkg := idx.modulePkg[rec.name]
		if rec.mod != nil {
			idx.irModules[pkg] = rec.mod
		}
		if len(rec.unlowered) > 0 {
			idx.unlowered[pkg] = rec.unlowered
		}
	}
	return idx.restoring(lib, false)
}

// readSharedSection reads a shared section's records, with their IR
// decoded.
func readSharedSection(data []byte) ([]*stdModuleRecord, error) {
	r := &bundleReader{b: data}
	var badFile string
	im, err := ir.DecodeImage(r.bytes(), ir.WithDecodedFileNames(stdSourceName(&badFile)))
	if r.err != nil {
		return nil, r.err
	}
	if err != nil {
		return nil, err
	}
	if badFile != "" {
		return nil, fmt.Errorf("the bundle names %s, which is not a std file", badFile)
	}
	mods := im.Modules
	fn := func() *ir.Func {
		m := r.uv()
		if m == 0 {
			return nil
		}
		i := r.uv()
		if r.err != nil || m > uint64(len(mods)) || i >= uint64(len(mods[m-1].Funcs())) {
			r.fail("function reference")
			return nil
		}
		return mods[m-1].Funcs()[i]
	}
	fnMap := func() map[int]*ir.Func {
		n := r.uv()
		if n == 0 {
			return nil
		}
		out := map[int]*ir.Func{}
		for ; n > 1 && r.err == nil; n-- {
			k := int(r.uv())
			if f := fn(); f != nil {
				out[k] = f
			} else {
				r.fail("function reference")
			}
		}
		return out
	}
	var recs []*stdModuleRecord
	for n := r.count(); n > 0 && r.err == nil; n-- {
		rec := &stdModuleRecord{name: r.str()}
		if m := r.uv(); m > uint64(len(mods)) {
			r.fail("module reference")
		} else if m > 0 {
			rec.mod = mods[m-1]
		}
		if n := r.uv(); n > 0 {
			rec.unlowered = map[string]string{}
			for ; n > 1 && r.err == nil; n-- {
				k := r.str()
				rec.unlowered[k] = r.str()
			}
		}
		for n := r.count(); n > 0 && r.err == nil; n-- {
			c := stdCandRecord{key: r.str(), hasDecl: r.bool1(), tailRec: r.bool1(), arityMin: int(r.uv())}
			if n := r.uv(); n > 0 {
				c.defaultFill = make([]bool, 0, min(n-1, uint64(len(r.b))))
				for ; n > 1 && r.err == nil; n-- {
					c.defaultFill = append(c.defaultFill, r.bool1())
				}
			}
			c.rtCall, c.rtHostPkg, c.rtFrame = r.str(), r.str(), r.bool1()
			c.body, c.pkg, c.why = r.bool1(), r.str(), r.str()
			c.irBody = fn()
			c.irArity = fnMap()
			c.irSlot = fnMap()
			rec.cands = append(rec.cands, c)
		}
		for n := r.count(); n > 0 && r.err == nil; n-- {
			o := stdOnceRecord{key: r.str(), settled: r.bool1(), why: r.str()}
			if m := r.uv(); m > 0 {
				i := r.uv()
				if r.err != nil || m > uint64(len(mods)) || i >= uint64(len(mods[m-1].Cells())) {
					r.fail("cell reference")
				} else {
					o.irCell = mods[m-1].Cells()[i].Sym()
				}
			}
			rec.onces = append(rec.onces, o)
		}
		recs = append(recs, rec)
	}
	if r.err == nil && len(r.b) != 0 {
		r.fail("trailing bytes")
	}
	if r.err != nil {
		return nil, r.err
	}
	return recs, nil
}
