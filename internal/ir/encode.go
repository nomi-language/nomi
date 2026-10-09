package ir

// THE IR FILE FORMAT: a linked program's modules, written to bytes and read
// back. `nomi build` appends one to a runner binary, and the runner decodes it
// and hands the modules to the VM in place of a lowering.
//
// IR AND NOT BYTECODE. Bytecode (internal/vm/bytecode.go) names process-local
// state — interned rt.TypeDesc descriptors, bound host adapters — and is
// compiled lazily per function in well under a millisecond per program. The
// expensive step a built binary skips is the front end and the lowering, and
// what comes out of the lowering is this graph.
//
// WHAT IS PRESERVED. Every field of every node, including the unexported
// ones, which is why this lives in package ir rather than beside it. Pointer
// identity is preserved wherever the package relies on it: a Symbol, a Func,
// a ValType, a Type and a Table are each written once and referred to
// by index, so two references to one pointer decode to one pointer, cycles
// (a struct whose field reaches its own type, a recursive FuncValue) decode
// to cycles, and the shared scalar types (IntType, ...) decode to the shared
// scalars. A Func's definition table (Func.Def) is written as it stands
// rather than recomputed, because it records the first instruction APPENDED,
// which need not be the first in block order.
//
// WHAT IS NOT. A Table's producer tokens: a decoded Table's types are keyed
// by their own pointers and its Symbol map is empty, because a token is the
// producer's identity (an AST node, a irbuild pointer) and no reader of a
// decoded image interns anything. A Table's declarations: a Decl is the
// builder's overload-selection record and no instruction names one. A
// Module's LintModuleAdded progress starts over.
//
// DETERMINISM. Everything is written in a traversal order fixed by the
// modules' own slices. The one map in the graph, Type.conforms, is written
// sorted: keys already numbered by the traversal first, by number, then the
// rest by name and form. Two unnumbered conformances that share a name and a
// form would make the order depend on map iteration; the determinism test
// encodes the whole stdlib twice to show no such pair exists today.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"sort"
)

// FormatVersion is the version the encoder writes and the only one the
// decoder reads. Bump it with any change to what is written.
const FormatVersion = 12

// formatMagic opens every encoded image.
const formatMagic = "NOMIIR\x00"

// Image is what a runner needs to start a program without lowering one: the
// linked modules, which of them is the entry, and the Go-bound host keys the
// program crosses under.
type Image struct {
	// Modules are the linked set, in link order: the program's own units,
	// then the stdlib modules and generic instances it links.
	Modules []*Module
	// Entry is the index into Modules of the entry unit, or -1.
	Entry int
	// HostKeys are the extern keys of the program's Go-bound `host fn`
	// bodies, sorted.
	HostKeys []string
	// HasMain reports whether the entry declares `fn main`. A program
	// without one runs nothing and exits 0, as `nomi run` does.
	HasMain bool
	// Root is the project root std/compiler's hosts resolve imports
	// against, the one the program was lowered with.
	Root string
}

// EntryModule is Modules[Entry], or nil.
func (im *Image) EntryModule() *Module {
	if im.Entry < 0 || im.Entry >= len(im.Modules) {
		return nil
	}
	return im.Modules[im.Entry]
}

// --- tags ---------------------------------------------------------------------

const (
	tagArith byte = iota + 1
	tagCall
	tagConcat
	tagDefer
	tagRunDefer
	tagFuncValue
	tagAssert
	tagRecord
	tagIter
	tagCompare
	tagConst
	tagBind
	tagMatch
	tagNoMatch
	tagNot
	tagProj
	tagMake
	tagSlot
	tagStore
	tagRef
	tagCopy
	tagRender
	tagTry
	tagTodo
)

const (
	termNone byte = iota
	termJump
	termBranch
	termReturn
)

// sharedValTypes are the package's scalar singletons. They take indices
// 1..len in every image, so a decoded reference to IntType is IntType.
var sharedValTypes = []*ValType{AnyType, UnitType, BoolType, IntType, FloatType,
	ByteType, StringType, BytesType, DecimalType}

// --- low-level writer ------------------------------------------------------------

type wbuf struct {
	b    []byte
	last Pos
	// file is the string number of the last position's file, or noFile.
	file uint64
}

const noFile = ^uint64(0)

func (w *wbuf) byte1(v byte)     { w.b = append(w.b, v) }
func (w *wbuf) uv(v uint64)      { w.b = binary.AppendUvarint(w.b, v) }
func (w *wbuf) sv(v int64)       { w.b = binary.AppendVarint(w.b, v) }
func (w *wbuf) bool1(v bool)     { w.byte1(b2u(v)) }
func (w *wbuf) raw(p []byte)     { w.b = append(w.b, p...) }
func (w *wbuf) f64(v float64)    { w.b = binary.LittleEndian.AppendUint64(w.b, math.Float64bits(v)) }
func (w *wbuf) temp(t Temp)      { w.uv(uint64(t)) }
func (w *wbuf) block(id BlockID) { w.uv(uint64(id)) }

// slen writes a slice's length so that nil and empty stay apart: 0 is nil,
// n+1 is a slice of n.
func (w *wbuf) slen(n int, isNil bool) {
	if isNil {
		w.uv(0)
		return
	}
	w.uv(uint64(n) + 1)
}

func b2u(v bool) byte {
	if v {
		return 1
	}
	return 0
}

// --- encoder --------------------------------------------------------------------

type encoder struct {
	strs map[string]uint64
	strL []string
	syms map[*Symbol]uint64
	symL []*Symbol
	vts  map[*ValType]uint64
	vtL  []*ValType
	tys  map[*Type]uint64
	tyL  []*Type
	tabs map[*Table]uint64
	tabL []*Table
	fns  map[*Func]uint64
	fnL  []*Func

	fnBuf, vtBuf, tyBuf, modBuf wbuf
	// doneX is how many entries of each list drain has written.
	doneFn, doneVT, doneTy int
	err                    error
}

// EncodeImage writes im in the current format. The same image gives the same
// bytes.
func EncodeImage(im Image) ([]byte, error) {
	e := &encoder{
		strs: map[string]uint64{}, syms: map[*Symbol]uint64{},
		vts: map[*ValType]uint64{}, tys: map[*Type]uint64{},
		tabs: map[*Table]uint64{}, fns: map[*Func]uint64{},
	}
	for i, v := range sharedValTypes {
		e.vts[v] = uint64(i + 1)
		e.vtL = append(e.vtL, v)
	}
	e.doneVT = len(sharedValTypes)
	// No position has been written in any buffer yet, so the first one
	// names its file whatever the file's string number is.
	for _, b := range []*wbuf{&e.fnBuf, &e.vtBuf, &e.tyBuf, &e.modBuf} {
		b.file = noFile
	}
	w := &e.modBuf
	w.uv(uint64(len(im.Modules)))
	for _, m := range im.Modules {
		if m == nil {
			return nil, errors.New("ir: EncodeImage: a nil module")
		}
		e.module(w, m)
		// Drain after each module so a module's functions are numbered in
		// the order that module names them.
		e.drain()
	}
	w.sv(int64(im.Entry))
	keys := append([]string(nil), im.HostKeys...)
	sort.Strings(keys)
	w.uv(uint64(len(keys)))
	for _, k := range keys {
		w.uv(e.str(k))
	}
	w.bool1(im.HasMain)
	w.uv(e.str(im.Root))
	e.drain()
	if e.err != nil {
		return nil, e.err
	}

	var body wbuf
	body.uv(uint64(len(e.strL)))
	for _, s := range e.strL {
		body.uv(uint64(len(s)))
		body.raw([]byte(s))
	}
	body.uv(uint64(len(e.symL)))
	for _, s := range e.symL {
		body.uv(e.strs[s.name])
	}
	body.uv(uint64(len(e.tabL)))
	body.uv(uint64(len(e.vtL) - len(sharedValTypes)))
	body.uv(uint64(len(e.tyL)))
	body.uv(uint64(len(e.fnL)))
	for _, sec := range []*wbuf{&e.vtBuf, &e.tyBuf, &e.fnBuf, &e.modBuf} {
		body.uv(uint64(len(sec.b)))
		body.raw(sec.b)
	}

	out := make([]byte, 0, len(body.b)+32)
	out = append(out, formatMagic...)
	out = binary.AppendUvarint(out, FormatVersion)
	out = binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(body.b))
	out = binary.AppendUvarint(out, uint64(len(body.b)))
	return append(out, body.b...), nil
}

// EncodeModules is EncodeImage for a module set with no entry.
func EncodeModules(mods []*Module) ([]byte, error) {
	return EncodeImage(Image{Modules: mods, Entry: -1})
}

func (e *encoder) fail(format string, args ...any) {
	if e.err == nil {
		e.err = fmt.Errorf("ir: encode: "+format, args...)
	}
}

// drain writes every Func, ValType and Type numbered but not yet written.
// Writing one may number more, so it loops until all are written.
func (e *encoder) drain() {
	// Each buffer already holds the records of the entries before its
	// cursor; resume where the last drain stopped.
	fn, vt, ty := e.doneFn, e.doneVT, e.doneTy
	for fn < len(e.fnL) || vt < len(e.vtL) || ty < len(e.tyL) {
		for ; fn < len(e.fnL); fn++ {
			e.funcBody(&e.fnBuf, e.fnL[fn])
		}
		for ; vt < len(e.vtL); vt++ {
			e.valTypeRec(&e.vtBuf, e.vtL[vt])
		}
		for ; ty < len(e.tyL); ty++ {
			e.typeRec(&e.tyBuf, e.tyL[ty])
		}
	}
	e.doneFn, e.doneVT, e.doneTy = fn, vt, ty
}

func (e *encoder) str(s string) uint64 {
	if i, ok := e.strs[s]; ok {
		return i
	}
	i := uint64(len(e.strL))
	e.strs[s] = i
	e.strL = append(e.strL, s)
	return i
}

// sym answers a symbol's reference: 0 for nil, else its number plus one.
func (e *encoder) sym(s *Symbol) uint64 {
	if s == nil {
		return 0
	}
	if i, ok := e.syms[s]; ok {
		return i
	}
	e.str(s.name)
	i := uint64(len(e.symL) + 1)
	e.syms[s] = i
	e.symL = append(e.symL, s)
	return i
}

func (e *encoder) vt(t *ValType) uint64 {
	if t == nil {
		return 0
	}
	if i, ok := e.vts[t]; ok {
		return i
	}
	i := uint64(len(e.vtL) + 1)
	e.vts[t] = i
	e.vtL = append(e.vtL, t)
	return i
}

func (e *encoder) ty(t *Type) uint64 {
	if t == nil {
		return 0
	}
	if i, ok := e.tys[t]; ok {
		return i
	}
	i := uint64(len(e.tyL) + 1)
	e.tys[t] = i
	e.tyL = append(e.tyL, t)
	return i
}

func (e *encoder) tab(t *Table) uint64 {
	if t == nil {
		return 0
	}
	if i, ok := e.tabs[t]; ok {
		return i
	}
	i := uint64(len(e.tabL) + 1)
	e.tabs[t] = i
	e.tabL = append(e.tabL, t)
	return i
}

func (e *encoder) fn(f *Func) uint64 {
	if f == nil {
		return 0
	}
	if i, ok := e.fns[f]; ok {
		return i
	}
	i := uint64(len(e.fnL) + 1)
	e.fns[f] = i
	e.fnL = append(e.fnL, f)
	return i
}

// pos writes a position as a delta from the previous one in the same buffer:
// most positions share a file and sit a few lines apart.
func (e *encoder) pos(w *wbuf, p Pos) {
	file := e.str(p.file)
	var flags byte
	if file == w.file {
		flags |= 1
	}
	if p.synth {
		flags |= 2
	}
	w.byte1(flags)
	if flags&1 == 0 {
		w.uv(file)
		w.file = file
	}
	w.sv(int64(p.line) - int64(w.last.line))
	w.uv(uint64(uint32(p.col)))
	w.sv(int64(p.endLine) - int64(p.line))
	w.sv(int64(p.endCol) - int64(p.col))
	w.last = p
}

func (e *encoder) temps(w *wbuf, ts []Temp) {
	w.slen(len(ts), ts == nil)
	for _, t := range ts {
		w.temp(t)
	}
}

func (e *encoder) strList(w *wbuf, ss []string) {
	w.slen(len(ss), ss == nil)
	for _, s := range ss {
		w.uv(e.str(s))
	}
}

func (e *encoder) symList(w *wbuf, ss []*Symbol) {
	w.slen(len(ss), ss == nil)
	for _, s := range ss {
		w.uv(e.sym(s))
	}
}

func (e *encoder) module(w *wbuf, m *Module) {
	w.uv(e.str(m.name))
	w.slen(len(m.funcs), m.funcs == nil)
	for _, f := range m.funcs {
		w.uv(e.fn(f))
	}
	w.slen(len(m.cells), m.cells == nil)
	for _, c := range m.cells {
		if c.owner != m {
			e.fail("%s: cell %s belongs to another module", m.name, c.sym.Name())
		}
		w.uv(e.sym(c.sym))
		w.uv(e.ty(c.ty))
		w.uv(e.fn(c.initializer))
	}
	w.slen(len(m.tests), m.tests == nil)
	for _, tc := range m.tests {
		w.uv(e.str(tc.name))
		w.uv(e.fn(tc.fn))
		w.uv(e.sym(tc.group.Boot))
		w.uv(e.sym(tc.group.Startup))
		w.bool1(tc.group.VirtualClock)
	}
	w.uv(e.sym(m.boot))
	w.uv(e.sym(m.mainFailure))
	e.symList(w, m.testBoots)
	w.slen(len(m.impls), m.impls == nil)
	for _, im := range m.impls {
		w.uv(e.sym(im.Method))
		w.uv(e.sym(im.Type))
		w.uv(e.sym(im.Func))
	}
	w.slen(len(m.displays), m.displays == nil)
	for _, d := range m.displays {
		w.uv(e.sym(d.Type))
		w.uv(e.sym(d.Func))
	}
	for _, table := range [][]DisplayImpl{m.rowDebugs, m.equates, m.hashes} {
		w.slen(len(table), table == nil)
		for _, d := range table {
			w.uv(e.sym(d.Type))
			w.uv(e.sym(d.Func))
		}
	}
}

func (e *encoder) valTypeRec(w *wbuf, t *ValType) {
	w.byte1(byte(t.kind))
	w.uv(e.sym(t.sym))
	w.slen(len(t.elems), t.elems == nil)
	for _, el := range t.elems {
		w.uv(e.vt(el))
	}
	e.strList(w, t.names)
	w.uv(e.vt(t.result))
	switch {
	case t.layout == nil:
		w.byte1(0)
	default:
		w.byte1(1)
		e.fields(w, t.layout.Fields)
		w.slen(len(t.layout.Variants), t.layout.Variants == nil)
		for _, v := range t.layout.Variants {
			w.uv(e.str(v.Name))
			w.byte1(byte(v.Form))
			e.fields(w, v.Fields)
		}
	}
}

func (e *encoder) fields(w *wbuf, fs []Field) {
	w.slen(len(fs), fs == nil)
	for _, f := range fs {
		w.uv(e.str(f.Name))
		w.uv(e.vt(f.Type))
	}
}

func (e *encoder) typeRec(w *wbuf, t *Type) {
	w.uv(e.tab(t.owner))
	w.uv(e.str(t.name))
	w.byte1(byte(t.form))
	w.uv(e.vt(t.val))
	w.slen(len(t.embeds), t.embeds == nil)
	for _, s := range t.embeds {
		w.uv(e.ty(s))
	}
	keys := make([]*Type, 0, len(t.conforms))
	// A false entry is never written by Conforms, so the map is the set of
	// its true keys.
	for k, ok := range t.conforms {
		if ok {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		ai, aok := e.tys[a]
		bi, bok := e.tys[b]
		if aok != bok {
			return aok
		}
		if aok {
			return ai < bi
		}
		if a.name != b.name {
			return a.name < b.name
		}
		return a.form < b.form
	})
	w.slen(len(keys), t.conforms == nil)
	for _, k := range keys {
		w.uv(e.ty(k))
	}
}

func (e *encoder) funcBody(w *wbuf, f *Func) {
	e.pos(w, f.Region.pos)
	w.uv(e.str(f.Region.label))
	w.uv(e.str(f.name))
	w.uv(e.sym(f.sym))
	w.uv(uint64(f.next))
	w.slen(len(f.params), f.params == nil)
	for _, p := range f.params {
		w.uv(e.sym(p.Sym))
		w.temp(p.Temp)
		w.byte1(byte(p.Shape))
	}
	w.slen(len(f.types), f.types == nil)
	for _, t := range f.types {
		w.uv(e.vt(t))
	}
	// Number every instruction in block order, for the definition table.
	flat := map[Instr]uint64{}
	w.slen(len(f.blocks), f.blocks == nil)
	for bi, b := range f.blocks {
		if b.id != BlockID(bi) {
			e.fail("%s: block %d has id %d", f.name, bi, b.id)
		}
		if b.owner != f {
			e.fail("%s: block %d belongs to another function", f.name, bi)
		}
		e.pos(w, b.pos)
		w.uv(e.str(b.label))
		w.bool1(b.hasFault)
		if b.hasFault {
			w.block(b.fault)
			e.pos(w, b.faultPos)
		}
		w.slen(len(b.instrs), b.instrs == nil)
		for _, in := range b.instrs {
			if _, twice := flat[in]; twice {
				// One node at two places would decode as two nodes.
				e.fail("%s: %s appears twice in the graph", f.name, in)
			}
			flat[in] = uint64(len(flat) + 1)
			e.instr(w, in)
		}
		e.term(w, b.term)
	}
	w.slen(len(f.defs), f.defs == nil)
	for t, d := range f.defs {
		if d == nil {
			w.uv(0)
			continue
		}
		at, ok := flat[d]
		if !ok {
			e.fail("%s: the definition of %s is not in any block", f.name, Temp(t))
		}
		w.uv(at)
	}
}

func (e *encoder) term(w *wbuf, t Term) {
	switch n := t.(type) {
	case nil:
		w.byte1(termNone)
	case *Jump:
		w.byte1(termJump)
		e.pos(w, n.pos)
		w.block(n.target)
	case *Branch:
		w.byte1(termBranch)
		e.pos(w, n.pos)
		w.temp(n.cond)
		w.block(n.ifTrue)
		w.block(n.ifFalse)
	case *Return:
		w.byte1(termReturn)
		e.pos(w, n.pos)
		w.temp(n.val)
		w.bool1(n.hasVal)
		w.byte1(byte(n.ctl))
	default:
		e.fail("unknown terminator %T", t)
	}
}

func (e *encoder) call(w *wbuf, n *Call) {
	e.pos(w, n.pos)
	w.temp(n.dst)
	w.byte1(byte(n.form))
	w.uv(e.sym(n.callee))
	w.temp(n.fn)
	w.sv(int64(n.keyAt))
	e.temps(w, n.args)
	w.bool1(n.tail)
	w.bool1(n.crosses)
}

func (e *encoder) instr(w *wbuf, in Instr) {
	switch n := in.(type) {
	case *Arith:
		w.byte1(tagArith)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.temp(n.lhs)
		w.temp(n.rhs)
		w.byte1(byte(n.op))
		w.byte1(byte(n.dom))
		w.byte1(byte(n.over))
	case *Call:
		w.byte1(tagCall)
		e.call(w, n)
	case *Concat:
		w.byte1(tagConcat)
		e.pos(w, n.pos)
		w.temp(n.dst)
		e.temps(w, n.parts)
	case *Defer:
		w.byte1(tagDefer)
		e.pos(w, n.pos)
		w.sv(int64(n.id))
		if n.call == nil {
			w.byte1(0)
		} else {
			w.byte1(1)
			e.call(w, n.call)
		}
	case *RunDefer:
		w.byte1(tagRunDefer)
		e.pos(w, n.pos)
		w.sv(int64(n.id))
	case *FuncValue:
		w.byte1(tagFuncValue)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.uv(e.fn(n.body))
		w.bool1(n.self)
		e.temps(w, n.captures)
	case *Assert:
		w.byte1(tagAssert)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.temp(n.subj)
		w.byte1(byte(n.kw))
		w.uv(e.str(n.text))
		w.bool1(n.mismatch)
		w.temp(n.answer)
		if n.binding == nil {
			w.byte1(0)
		} else {
			b := n.binding
			w.byte1(1)
			w.uv(e.str(b.Name))
			w.uv(e.str(b.Expr))
			w.temp(b.Val)
			w.slen(len(b.Stages), b.Stages == nil)
			for _, s := range b.Stages {
				w.uv(e.str(s.Text))
				w.temp(s.Val)
			}
		}
	case *Record:
		w.byte1(tagRecord)
		e.pos(w, n.pos)
		w.temp(n.val)
		w.byte1(byte(n.kind))
		w.uv(e.str(n.text))
		w.bool1(n.suppress)
	case *Iter:
		w.byte1(tagIter)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.byte1(byte(n.op))
		w.byte1(byte(n.over))
		w.bool1(n.sig)
		e.temps(w, n.args)
	case *Compare:
		w.byte1(tagCompare)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.byte1(byte(n.op))
		w.byte1(byte(n.shape))
		w.temp(n.lhs)
		w.temp(n.rhs)
		w.bool1(n.rank)
	case *Const:
		w.byte1(tagConst)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.byte1(byte(n.kind))
		w.sv(n.num)
		w.f64(n.flt)
		w.uv(e.str(n.text))
		w.uv(e.sym(n.typ))
	case *Bind:
		w.byte1(tagBind)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.temp(n.src)
		w.uv(e.sym(n.sym))
	case *Match:
		w.byte1(tagMatch)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.temp(n.subj)
		w.temp(n.arg)
		w.byte1(byte(n.kind))
		w.uv(e.sym(n.sym))
		w.uv(e.str(n.text))
		w.sv(int64(n.idx))
		w.uv(e.sym(n.embeds))
	case *NoMatch:
		w.byte1(tagNoMatch)
		e.pos(w, n.pos)
		w.temp(n.key)
	case *Todo:
		w.byte1(tagTodo)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.uv(e.str(n.reason))
	case *Not:
		w.byte1(tagNot)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.temp(n.val)
	case *Proj:
		w.byte1(tagProj)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.temp(n.subj)
		w.byte1(byte(n.kind))
		w.uv(e.sym(n.sym))
		w.uv(e.str(n.text))
		w.sv(int64(n.idx))
		w.bool1(n.faults)
		w.byte1(byte(n.shape))
		w.uv(e.str(n.field))
		w.uv(e.sym(n.embeds))
	case *Make:
		w.byte1(tagMake)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.byte1(byte(n.kind))
		w.uv(e.sym(n.typ))
		w.uv(e.str(n.text))
		e.strList(w, n.names)
		e.temps(w, n.ops)
		w.temp(n.tail)
		w.bool1(n.incl)
		w.uv(e.sym(n.embeds))
	case *Slot:
		w.byte1(tagSlot)
		e.pos(w, n.pos)
		w.temp(n.slot)
		w.uv(e.ty(n.ty))
	case *Store:
		w.byte1(tagStore)
		e.pos(w, n.pos)
		w.byte1(byte(n.kind))
		w.uv(e.sym(n.sym))
		w.temp(n.src)
	case *Ref:
		w.byte1(tagRef)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.byte1(byte(n.kind))
		w.uv(e.sym(n.sym))
	case *Copy:
		w.byte1(tagCopy)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.temp(n.src)
	case *Render:
		w.byte1(tagRender)
		e.pos(w, n.pos)
		w.temp(n.dst)
		w.temp(n.src)
		w.byte1(byte(n.kind))
		w.bool1(n.erased)
		w.slen(len(n.impls), n.impls == nil)
		for _, d := range n.impls {
			w.uv(e.str(d.Type))
			w.uv(e.str(d.Inst))
			w.uv(e.sym(d.Fn))
		}
	case *Try:
		w.byte1(tagTry)
		e.pos(w, n.pos)
		w.temp(n.src)
		w.uv(e.str(n.text))
	default:
		e.fail("unknown instruction %T", in)
	}
}
