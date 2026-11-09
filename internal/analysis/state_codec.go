package analysis

//go:generate go run ./stategen/gen

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"unsafe"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ast/astcodec"
)

// The stdlib's state: what building the stdlib's modules from their
// interfaces leaves, written as bytes and read back whole, so a process
// skips the sweeps (the package doc, "The stdlib's state"; std/state.go).
// The encoder and decoder of each type are generated (state_codec_gen.go,
// by stategen) from the types stdState reaches; what is here is what they
// share.

// StdState is a stdlib built from its modules' interfaces, by module name:
// each module's analysis, the nodes its build checked and the interface
// nodes it was given, the stdlib's base and its errors.
type StdState struct {
	Files  map[string]*FileAnalysis
	Nodes  map[string][]ast.Node
	Ifaces map[string][]ast.Node
	Base   *StdBase
	Errors map[string][]TypeError
}

// stdState is what EncodeStdState writes: the stategen generator's root.
type stdState struct {
	files  map[string]*FileAnalysis
	nodes  map[string][]ast.Node
	ifaces map[string][]ast.Node
	base   *StdBase
	errors map[string][]TypeError
	// whys are stdBootWhys' entries for the state's functions, which
	// installing the facts stored (StdFacts.InstallModules).
	whys []stateBootWhy
	// generation is bodyGenerations when the state was written.
	generation uint64
}

// stateBootWhy is one entry of stdBootWhys.
type stateBootWhy struct {
	fd  *ast.FuncDef
	why bootCall
}

const (
	stateMagic = "NOMISTATE"
	// stateVersion is the format's version. The generated codec writes
	// every field of every type it reaches, so a state is read only by the
	// build that wrote it: a caller keys it by the binary (std's artifact).
	stateVersion = 1
)

// StdStateFormat names the code that writes and reads a state: the
// format's version and digests of the generated codec and of
// internal/ast/astcodec's, which writes the state's nodes. A binary reads
// a state another binary wrote only when their formats are equal
// (std/state.go).
func StdStateFormat() string {
	return fmt.Sprintf("%d/%s/%s", stateVersion, stateCodecDigest, astcodec.CodecDigest)
}

// stateGlobals is every package-level pointer, map and Type as the package
// started, which a state names by its place here rather than writing.
var stateGlobals = stateGlobalValues()

// stateGlobalIndex is stateGlobals by pointer.
var stateGlobalIndex = func() map[unsafe.Pointer]int {
	m := make(map[unsafe.Pointer]int, len(stateGlobals))
	for i, v := range stateGlobals {
		if p := globalPointer(v); p != nil {
			if _, ok := m[p]; !ok {
				m[p] = i
			}
		}
	}
	return m
}()

func globalPointer(v any) unsafe.Pointer {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map:
		if rv.IsNil() {
			return nil
		}
		return rv.UnsafePointer()
	}
	return nil
}

// mapPointer is a map's identity.
func mapPointer[M ~map[K]V, K comparable, V any](m M) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&m))
}

// canonicalStdTypes are the variables installCanonicalStdTypes points at
// std's declarations, with their modules and names.
func canonicalStdTypes() []struct {
	now    Type
	module string
	name   string
} {
	return []struct {
		now    Type
		module string
		name   string
	}{
		{TypeBool, "bool", "Bool"},
		{TypeOrdering, "comparable", "Ordering"},
		{TypeAssertionFailure, "assertions", "AssertionFailure"},
		{TypeCodepoint, "code_points", "Codepoint"},
		{TypeRange, "ranges", "Range"},
	}
}

// EncodeStdState writes s, whose files' paths are under dir, the directory
// the stdlib was found in: a path under it is written relative to it, so the
// state reads back under another. It refuses a stdlib whose build was not
// the first in this process to install the canonical std types
// (installCanonicalStdTypes): the state would point at another build's.
func EncodeStdState(s StdState, dir string) ([]byte, error) {
	return encodeStdState(s, dir, nil)
}

func encodeStdState(s StdState, dir string, rec *stateRecording) (data []byte, err error) {
	for _, c := range canonicalStdTypes() {
		fa := s.Files[c.module]
		if fa == nil || fa.ModuleScope == nil {
			continue
		}
		sym := fa.ModuleScope.LookupLocal(c.name)
		if sym == nil || sym.Type != c.now {
			return nil, fmt.Errorf("std/%s's %s is not the process's", c.module, c.name)
		}
	}
	st := &stdState{files: s.Files, nodes: s.Nodes, ifaces: s.Ifaces, base: s.Base, errors: s.Errors,
		generation: bodyGenerations.Load()}
	fds := map[*ast.FuncDef]bool{}
	for _, nodes := range s.Nodes {
		for _, n := range nodes {
			ast.Inspect(n, func(m ast.Node) bool {
				if fd, ok := m.(*ast.FuncDef); ok {
					fds[fd] = true
				}
				return true
			})
		}
	}
	stdBootWhys.Range(func(k, v any) bool {
		if fd := k.(*ast.FuncDef); fds[fd] {
			st.whys = append(st.whys, stateBootWhy{fd: fd, why: v.(bootCall)})
		}
		return true
	})
	e := &stateEncoder{w: astcodec.NewEncoder(), ids: map[stateKey]int{}, strs: map[string]int{}, dir: dir, rec: rec}
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(stateFailure)
			if !ok {
				panic(r)
			}
			data, err = nil, errors.New(f.msg)
		}
	}()
	e.ststdState(st)
	// Each file's kept declarations follow the rest of the state, in the
	// order the files were written, so a process reads them only when
	// something asks for them (stateRest).
	for _, x := range e.kept {
		e.stPkeptDecls(x.kept)
	}
	if rec != nil {
		rec.astEnc = e.w.Pointers()
	}
	var out []byte
	out = append(out, stateMagic...)
	out = binary.AppendUvarint(out, stateVersion)
	out = binary.AppendUvarint(out, uint64(len(e.ids)))
	out = binary.AppendUvarint(out, uint64(e.w.PointerCount()))
	out = binary.AppendUvarint(out, uint64(len(e.strL)))
	for _, s := range e.strL {
		out = binary.AppendUvarint(out, uint64(len(s)))
		out = append(out, s...)
	}
	return append(out, e.w.Bytes()...), nil
}

// DecodeStdState reads what EncodeStdState wrote, with the paths it wrote
// relative to the stdlib's directory under dir. data must not change
// afterwards: the state's strings are read in place. Every builder of the
// state loads modules with loader. It installs what the build installed
// for the process: the canonical std types and the facts' boot-only calls.
func DecodeStdState(data []byte, dir string, loader FileLoader) (StdState, error) {
	return decodeStdState(data, dir, loader, nil)
}

func decodeStdState(data []byte, dir string, loader FileLoader, rec *stateRecording) (StdState, error) {
	rest, ok := strings.CutPrefix(unsafe.String(unsafe.SliceData(data), len(data)), stateMagic)
	if !ok {
		return StdState{}, errors.New("analysis: not a stdlib state")
	}
	buf := data[len(data)-len(rest):]
	// Everything read stays live, so a collection while it is read would
	// find nothing to free.
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	w := astcodec.NewDecoder(buf)
	d := &stateDecoder{w: w, loader: loader}
	var st stdState
	if err := w.Run(func() {
		if v := w.Uint(); v != stateVersion {
			d.fail(fmt.Sprintf("version %d, not %d", v, stateVersion))
		}
		d.refs = make([]any, 0, d.count())
		w.Reserve(d.count())
		d.strs = make([]string, d.count())
		for i := range d.strs {
			n := d.count()
			b := w.Next(n)
			s := unsafe.String(unsafe.SliceData(b), n)
			if rel, ok := strings.CutPrefix(s, stateDirMark); ok {
				s = dir + filepath.FromSlash(rel)
			}
			d.strs[i] = s
		}
		d.ststdState(&st)
		if len(d.kept) == 0 && !w.Done() {
			d.fail("bytes after the state")
		}
	}); err != nil {
		return StdState{}, err
	}
	if len(d.kept) > 0 {
		r := &stateRest{d: d, files: d.kept}
		for _, x := range d.kept {
			x.keptRest = r
		}
		if rec != nil {
			r.load()
		}
	}
	if rec != nil {
		rec.dec, rec.astDec = d.refs, w.Pointers()
	}
	for {
		g := bodyGenerations.Load()
		if g >= st.generation || bodyGenerations.CompareAndSwap(g, st.generation) {
			break
		}
	}
	for _, w := range st.whys {
		stdBootWhys.Store(w.fd, w.why)
	}
	cache := make(map[string]*FileAnalysis, len(st.files))
	for name, fa := range st.files {
		cache["std/"+name] = fa
	}
	installCanonicalStdTypes(cache)
	return StdState{Files: st.files, Nodes: st.nodes, Ifaces: st.ifaces, Base: st.base, Errors: st.errors}, nil
}

// stateDirMark starts a string written relative to the stdlib's directory.
const stateDirMark = "\x00std:"

// stateKey is a pointer's identity in the table: its address and its type,
// since a struct and its first field share an address.
type stateKey struct {
	p    unsafe.Pointer
	kind int
	// n is a slice's length.
	n int
}

// stateFailure is what the encoder panics with on a state it cannot write.
type stateFailure struct{ msg string }

type stateEncoder struct {
	w    *astcodec.Encoder
	ids  map[stateKey]int
	strs map[string]int
	strL []string
	dir  string
	rec  *stateRecording
	// kept are the files whose kept declarations are written after the
	// state (hookFileDeclsKept).
	kept []*FileDecls
}

func (e *stateEncoder) failf(format string, args ...any) {
	panic(stateFailure{"analysis: the stdlib's state: " + fmt.Sprintf(format, args...)})
}

// ref writes the non-nil pointer p of the given kind: a package-level
// object by its place in stateGlobals, one written before by its number.
// It reports whether this is p's first time, when the caller writes what
// it points to.
func (e *stateEncoder) ref(p unsafe.Pointer, kind int) bool {
	if i, ok := stateGlobalIndex[p]; ok {
		e.w.Uint(2)
		e.w.Uint(uint64(i))
		return false
	}
	return e.refKey(stateKey{p: p, kind: kind})
}

// refSlice is ref for a slice of n elements at p, which is no
// package-level object.
func (e *stateEncoder) refSlice(p unsafe.Pointer, n, kind int) bool {
	return e.refKey(stateKey{p: p, kind: kind, n: n})
}

func (e *stateEncoder) refKey(k stateKey) bool {
	if id, ok := e.ids[k]; ok {
		e.w.Uint(uint64(id) + 3)
		return false
	}
	e.ids[k] = len(e.ids)
	if e.rec != nil {
		e.rec.enc = append(e.rec.enc, k)
	}
	e.w.Uint(1)
	return true
}

// str writes s by its number in the string table, a path under the
// stdlib's directory relative to it, with slashes, so a state written on
// one system reads back on another.
func (e *stateEncoder) str(s string) {
	if i, ok := e.strs[s]; ok {
		e.w.Uint(uint64(i))
		return
	}
	stored := s
	if e.dir != "" {
		if rel, ok := strings.CutPrefix(s, e.dir); ok && (rel == "" || rel[0] == '/' || rel[0] == '\\') {
			stored = stateDirMark + filepath.ToSlash(rel)
		} else if strings.Contains(s, e.dir) {
			e.failf("%q names the stdlib's directory inside it", s)
		}
	}
	i := len(e.strL)
	e.strs[s] = i
	e.strL = append(e.strL, stored)
	e.w.Uint(uint64(i))
}

func (e *stateEncoder) float(v float64) { e.w.Uint(math.Float64bits(v)) }

func (e *stateEncoder) bigInt(v *big.Int) {
	if v == nil {
		e.w.Uint(0)
		return
	}
	e.w.Uint(1)
	e.str(v.Text(16))
}

// The hooks: fields of functions, written as the stdlib's build leaves
// them.

// hookBuilderLoader writes nothing: every builder of a stdlib's build loads
// with that build's loader, which the decoder is given.
func (e *stateEncoder) hookBuilderLoader(x *builder) {}

// hookDefaultSiteSet writes nothing: a default's type is set once, by the
// inference phase the build ran (OnceTable.infer).
func (e *stateEncoder) hookDefaultSiteSet(x *defaultSite) {}

// hookProgramProgramErrors writes nothing: the stdlib's declarations do
// not move, so its program's errors are its Errors.
func (e *stateEncoder) hookProgramProgramErrors(x *Program) {}

// hookFileDeclsKept writes whether x has kept declarations, which are
// written after the state.
func (e *stateEncoder) hookFileDeclsKept(x *FileDecls) {
	e.w.Bool(x.kept != nil)
	if x.kept != nil {
		e.kept = append(e.kept, x)
	}
}

// hookOnceTableInferOnce writes whether the inference phase ran.
func (e *stateEncoder) hookOnceTableInferOnce(x *OnceTable) { e.w.Bool(x.inferred) }

type stateDecoder struct {
	// kept are the files whose kept declarations follow the state.
	kept   []*FileDecls
	w      *astcodec.Decoder
	refs   []any
	strs   []string
	loader FileLoader
}

func (d *stateDecoder) fail(msg string) { d.w.Fail(msg) }

// count reads a count of things each of which takes a byte at least.
func (d *stateDecoder) count() int {
	n := d.w.Uint()
	if n > uint64(d.w.Remaining()) {
		d.fail("a count past the data")
	}
	return int(n)
}

// ref reads a pointer: what an earlier one decoded to, a package-level
// object, or nil and whether it is a new one, which the caller decodes and
// adds.
func (d *stateDecoder) ref() (any, bool) {
	v := d.w.Uint()
	switch v {
	case 0:
		return nil, false
	case 1:
		return nil, true
	case 2:
		i := d.w.Uint()
		if i >= uint64(len(stateGlobals)) {
			d.fail("a global past the table")
		}
		return stateGlobals[i], false
	}
	id := v - 3
	if id >= uint64(len(d.refs)) {
		d.fail("a pointer to nothing decoded")
	}
	return d.refs[id], false
}

func (d *stateDecoder) add(v any) {
	d.refs = append(d.refs, v)
}

func (d *stateDecoder) str() string {
	i := d.w.Uint()
	if i >= uint64(len(d.strs)) {
		d.fail("a string past the table")
	}
	return d.strs[i]
}

func (d *stateDecoder) float() float64 { return math.Float64frombits(d.w.Uint()) }

func (d *stateDecoder) bigInt() *big.Int {
	if d.w.Uint() == 0 {
		return nil
	}
	v, ok := new(big.Int).SetString(d.str(), 16)
	if !ok {
		d.fail("a bad big number")
	}
	return v
}

func (d *stateDecoder) hookBuilderLoader(x *builder) { x.loader = d.loader }

func (d *stateDecoder) hookDefaultSiteSet(x *defaultSite) {
	x.set = func(Type) {
		panic("analysis: a default of the stdlib's state is inferred again")
	}
}

func (d *stateDecoder) hookProgramProgramErrors(x *Program) {
	x.programErrors = func() []TypeError { return x.Errors }
}

func (d *stateDecoder) hookFileDeclsKept(x *FileDecls) {
	if d.w.Bool() {
		d.kept = append(d.kept, x)
	}
}

// stateRest is the part of a stdlib's state read when something first asks
// for it: every file's kept declarations, which only checking a module's
// bodies reads (rebuildBodies, CheckDeferredBodies). It goes on with the
// decoder the state was read with, whose pointer and string tables the
// kept declarations name.
type stateRest struct {
	once  sync.Once
	d     *stateDecoder
	files []*FileDecls
}

// load reads the kept declarations into their files, once. A state reads
// back whole or the stdlib is built instead (std/state.go), so this part
// not reading is a bug in the codec, and panics.
func (r *stateRest) load() {
	r.once.Do(func() {
		d := r.d
		if err := d.w.Run(func() {
			for _, x := range r.files {
				x.kept = d.stPkeptDecls()
			}
			if !d.w.Done() {
				d.fail("bytes after the state")
			}
		}); err != nil {
			panic("analysis: the stdlib's state's kept declarations do not read back: " + err.Error())
		}
		r.d = nil
	})
}

// keptDecls is what a project build kept of the file's declarations, read
// from the stdlib's state first when the file was.
func (fd *FileDecls) keptDecls() *keptDecls {
	if r := fd.keptRest; r != nil {
		r.load()
	}
	return fd.kept
}

func (d *stateDecoder) hookOnceTableInferOnce(x *OnceTable) {
	if d.w.Bool() {
		x.inferOnce.Do(func() {})
	}
}
