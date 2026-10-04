package ir

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
)

// DecodeImage reads an image EncodeImage wrote. A malformed or truncated
// input, a wrong magic, a version other than FormatVersion and a checksum
// mismatch are errors, never panics. A decoded image is a fresh graph that
// shares nothing with any other decoded image except the package's shared
// scalar types.
func DecodeImage(data []byte) (im Image, err error) {
	defer func() {
		// The decoder bounds-checks every index it reads; a panic here is a
		// decoder bug, reported as an error rather than taking down a runner.
		if r := recover(); r != nil {
			im, err = Image{}, fmt.Errorf("ir: decode: %v", r)
		}
	}()
	if len(data) < len(formatMagic) || string(data[:len(formatMagic)]) != formatMagic {
		return Image{}, errors.New("ir: decode: not a Nomi IR image")
	}
	r := &rbuf{b: data[len(formatMagic):]}
	if v := r.uv(); r.err == nil && v != FormatVersion {
		return Image{}, fmt.Errorf("ir: decode: format version %d, this build reads %d", v, FormatVersion)
	}
	sum := r.u32()
	n := r.uv()
	if r.err != nil || uint64(len(r.b)) != n {
		return Image{}, errors.New("ir: decode: truncated image")
	}
	if crc32.ChecksumIEEE(r.b) != sum {
		return Image{}, errors.New("ir: decode: checksum mismatch")
	}
	d := &decoder{}
	im = d.image(r)
	if d.err == nil && r.err != nil {
		d.err = r.err
	}
	if d.err != nil {
		return Image{}, d.err
	}
	return im, nil
}

// DecodeModules is DecodeImage for a module set.
func DecodeModules(data []byte) ([]*Module, error) {
	im, err := DecodeImage(data)
	return im.Modules, err
}

// --- low-level reader ------------------------------------------------------------

type rbuf struct {
	b    []byte
	err  error
	last Pos
	file string
}

func (r *rbuf) fail(what string) {
	if r.err == nil {
		r.err = errors.New("ir: decode: malformed " + what)
	}
	r.b = nil
}

func (r *rbuf) uv() uint64 {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.fail("varint")
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *rbuf) sv() int64 {
	v, n := binary.Varint(r.b)
	if n <= 0 {
		r.fail("varint")
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *rbuf) byte1() byte {
	if len(r.b) < 1 {
		r.fail("byte")
		return 0
	}
	v := r.b[0]
	r.b = r.b[1:]
	return v
}

func (r *rbuf) bool1() bool { return r.byte1() != 0 }

func (r *rbuf) u32() uint32 {
	if len(r.b) < 4 {
		r.fail("word")
		return 0
	}
	v := binary.LittleEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v
}

func (r *rbuf) f64() float64 {
	if len(r.b) < 8 {
		r.fail("float")
		return 0
	}
	v := binary.LittleEndian.Uint64(r.b)
	r.b = r.b[8:]
	return math.Float64frombits(v)
}

func (r *rbuf) bytes(n uint64) []byte {
	if uint64(len(r.b)) < n {
		r.fail("bytes")
		return nil
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v
}

func (r *rbuf) temp() Temp {
	v := r.uv()
	if v > math.MaxUint32 {
		r.fail("temporary")
		return NoTemp
	}
	return Temp(v)
}

func (r *rbuf) block() BlockID {
	v := r.uv()
	if v > math.MaxUint32 {
		r.fail("block id")
		return 0
	}
	return BlockID(v)
}

// slen reads a length slen wrote: nil reports a nil slice. n is capped by the
// bytes left, since every element takes at least one byte.
func (r *rbuf) slen() (n int, isNil bool) {
	v := r.uv()
	if v == 0 {
		return 0, true
	}
	if v-1 > uint64(len(r.b)) {
		r.fail("length")
		return 0, true
	}
	return int(v - 1), false
}

func (r *rbuf) count() int {
	v := r.uv()
	if v > uint64(len(r.b))+1<<20 {
		r.fail("count")
		return 0
	}
	return int(v)
}

// --- decoder --------------------------------------------------------------------

type decoder struct {
	strs []string
	syms []*Symbol
	tabs []*Table
	vts  []*ValType
	tys  []*Type
	fns  []*Func
	err  error
}

func (d *decoder) fail(format string, args ...any) {
	if d.err == nil {
		d.err = fmt.Errorf("ir: decode: "+format, args...)
	}
}

func (d *decoder) image(r *rbuf) Image {
	d.strs = make([]string, r.count())
	for i := range d.strs {
		d.strs[i] = string(r.bytes(r.uv()))
	}
	d.syms = make([]*Symbol, r.count())
	for i := range d.syms {
		d.syms[i] = &Symbol{name: d.str(r.uv())}
	}
	d.tabs = make([]*Table, r.count())
	for i := range d.tabs {
		d.tabs[i] = NewTable()
	}
	d.vts = append([]*ValType(nil), sharedValTypes...)
	for n := r.count(); n > 0 && r.err == nil; n-- {
		d.vts = append(d.vts, &ValType{})
	}
	d.tys = make([]*Type, r.count())
	for i := range d.tys {
		d.tys[i] = &Type{}
	}
	d.fns = make([]*Func, r.count())
	for i := range d.fns {
		f := &Func{Region: &Region{}}
		f.Region.owner = f
		d.fns[i] = f
	}
	if r.err != nil {
		return Image{}
	}
	sec := func() *rbuf { return &rbuf{b: r.bytes(r.uv())} }
	vtSec, tySec, fnSec, modSec := sec(), sec(), sec(), sec()
	if r.err != nil {
		return Image{}
	}
	for _, t := range d.vts[len(sharedValTypes):] {
		d.valTypeRec(vtSec, t)
	}
	for _, t := range d.tys {
		d.typeRec(tySec, t)
	}
	for _, f := range d.fns {
		d.funcBody(fnSec, f)
	}
	var im Image
	n := modSec.count()
	for i := 0; i < n && modSec.err == nil; i++ {
		im.Modules = append(im.Modules, d.module(modSec))
	}
	im.Entry = int(modSec.sv())
	if im.Entry < -1 || im.Entry >= len(im.Modules) {
		d.fail("entry %d outside %d modules", im.Entry, len(im.Modules))
	}
	for k := modSec.count(); k > 0 && modSec.err == nil; k-- {
		im.HostKeys = append(im.HostKeys, d.str(modSec.uv()))
	}
	im.HasMain = modSec.bool1()
	im.Root = d.str(modSec.uv())
	for _, s := range []*rbuf{vtSec, tySec, fnSec, modSec} {
		if s.err != nil {
			d.fail("%v", s.err)
		} else if len(s.b) != 0 {
			d.fail("%d trailing bytes in a section", len(s.b))
		}
	}
	return im
}

func (d *decoder) str(i uint64) string {
	if i >= uint64(len(d.strs)) {
		d.fail("string %d outside %d", i, len(d.strs))
		return ""
	}
	return d.strs[i]
}

func (d *decoder) sym(i uint64) *Symbol {
	if i == 0 {
		return nil
	}
	if i > uint64(len(d.syms)) {
		d.fail("symbol %d outside %d", i, len(d.syms))
		return nil
	}
	return d.syms[i-1]
}

func (d *decoder) vt(i uint64) *ValType {
	if i == 0 {
		return nil
	}
	if i > uint64(len(d.vts)) {
		d.fail("value type %d outside %d", i, len(d.vts))
		return nil
	}
	return d.vts[i-1]
}

func (d *decoder) ty(i uint64) *Type {
	if i == 0 {
		return nil
	}
	if i > uint64(len(d.tys)) {
		d.fail("type %d outside %d", i, len(d.tys))
		return nil
	}
	return d.tys[i-1]
}

func (d *decoder) tab(i uint64) *Table {
	if i == 0 {
		return nil
	}
	if i > uint64(len(d.tabs)) {
		d.fail("table %d outside %d", i, len(d.tabs))
		return nil
	}
	return d.tabs[i-1]
}

func (d *decoder) fn(i uint64) *Func {
	if i == 0 {
		return nil
	}
	if i > uint64(len(d.fns)) {
		d.fail("function %d outside %d", i, len(d.fns))
		return nil
	}
	return d.fns[i-1]
}

func (d *decoder) pos(r *rbuf) Pos {
	flags := r.byte1()
	if flags&1 == 0 {
		r.file = d.str(r.uv())
	}
	var p Pos
	p.file = r.file
	p.synth = flags&2 != 0
	p.line = int32(int64(r.last.line) + r.sv())
	p.col = int32(r.uv())
	p.endLine = int32(int64(p.line) + r.sv())
	p.endCol = int32(int64(p.col) + r.sv())
	r.last = p
	return p
}

func (d *decoder) temps(r *rbuf) []Temp {
	n, isNil := r.slen()
	if isNil {
		return nil
	}
	ts := make([]Temp, n)
	for i := range ts {
		ts[i] = r.temp()
	}
	return ts
}

func (d *decoder) strList(r *rbuf) []string {
	n, isNil := r.slen()
	if isNil {
		return nil
	}
	ss := make([]string, n)
	for i := range ss {
		ss[i] = d.str(r.uv())
	}
	return ss
}

func (d *decoder) symList(r *rbuf) []*Symbol {
	n, isNil := r.slen()
	if isNil {
		return nil
	}
	ss := make([]*Symbol, n)
	for i := range ss {
		ss[i] = d.sym(r.uv())
	}
	return ss
}

func (d *decoder) module(r *rbuf) *Module {
	m := &Module{name: d.str(r.uv())}
	if n, isNil := r.slen(); !isNil {
		m.funcs = make([]*Func, n)
		for i := range m.funcs {
			if m.funcs[i] = d.fn(r.uv()); m.funcs[i] == nil {
				d.fail("%s: a nil function", m.name)
			}
		}
	}
	if n, isNil := r.slen(); !isNil {
		m.cells = make([]*Cell, n)
		for i := range m.cells {
			m.cells[i] = &Cell{owner: m, sym: d.sym(r.uv()), ty: d.ty(r.uv()), initializer: d.fn(r.uv())}
		}
	}
	if n, isNil := r.slen(); !isNil {
		m.tests = make([]TestCase, n)
		for i := range m.tests {
			tc := &m.tests[i]
			tc.name = d.str(r.uv())
			tc.fn = d.fn(r.uv())
			tc.group.Boot = d.sym(r.uv())
			tc.group.Startup = d.sym(r.uv())
			tc.group.VirtualClock = r.bool1()
		}
	}
	m.boot = d.sym(r.uv())
	m.testBoots = d.symList(r)
	if n, isNil := r.slen(); !isNil {
		m.impls = make([]Impl, n)
		for i := range m.impls {
			m.impls[i] = Impl{Method: d.sym(r.uv()), Type: d.sym(r.uv()), Func: d.sym(r.uv())}
		}
	}
	if n, isNil := r.slen(); !isNil {
		m.displays = make([]DisplayImpl, n)
		for i := range m.displays {
			m.displays[i] = DisplayImpl{Type: d.sym(r.uv()), Func: d.sym(r.uv())}
		}
	}
	for _, table := range []*[]DisplayImpl{&m.rowDebugs, &m.equates, &m.hashes} {
		if n, isNil := r.slen(); !isNil {
			*table = make([]DisplayImpl, n)
			for i := range *table {
				(*table)[i] = DisplayImpl{Type: d.sym(r.uv()), Func: d.sym(r.uv())}
			}
		}
	}
	return m
}

func (d *decoder) valTypeRec(r *rbuf, t *ValType) {
	t.kind = ValKind(r.byte1())
	t.sym = d.sym(r.uv())
	if n, isNil := r.slen(); !isNil {
		t.elems = make([]*ValType, n)
		for i := range t.elems {
			t.elems[i] = d.vt(r.uv())
		}
	}
	t.names = d.strList(r)
	t.result = d.vt(r.uv())
	if r.byte1() != 0 {
		l := &Layout{Fields: d.fields(r)}
		if n, isNil := r.slen(); !isNil {
			l.Variants = make([]Variant, n)
			for i := range l.Variants {
				v := &l.Variants[i]
				v.Name = d.str(r.uv())
				v.Form = VariantForm(r.byte1())
				v.Fields = d.fields(r)
			}
		}
		t.layout = l
	}
}

func (d *decoder) fields(r *rbuf) []Field {
	n, isNil := r.slen()
	if isNil {
		return nil
	}
	fs := make([]Field, n)
	for i := range fs {
		fs[i] = Field{Name: d.str(r.uv()), Type: d.vt(r.uv())}
	}
	return fs
}

func (d *decoder) typeRec(r *rbuf, t *Type) {
	t.owner = d.tab(r.uv())
	t.name = d.str(r.uv())
	t.form = TypeForm(r.byte1())
	t.val = d.vt(r.uv())
	if n, isNil := r.slen(); !isNil {
		t.embeds = make([]*Type, n)
		for i := range t.embeds {
			t.embeds[i] = d.ty(r.uv())
		}
	}
	if n, isNil := r.slen(); !isNil {
		t.conforms = make(map[*Type]bool, n)
		for ; n > 0; n-- {
			if k := d.ty(r.uv()); k != nil {
				t.conforms[k] = true
			}
		}
	}
	if t.owner != nil {
		// A decoded type is its own token: nothing interns into a decoded
		// table, and Table.Types() still counts it.
		t.owner.types[t] = t
	}
}

func (d *decoder) funcBody(r *rbuf, f *Func) {
	f.Region.pos = d.pos(r)
	f.Region.label = d.str(r.uv())
	f.name = d.str(r.uv())
	f.sym = d.sym(r.uv())
	next := r.uv()
	if next > math.MaxUint32 {
		d.fail("temporary count %d", next)
	}
	f.next = Temp(next)
	if n, isNil := r.slen(); !isNil {
		f.params = make([]Param, n)
		for i := range f.params {
			f.params[i] = Param{Sym: d.sym(r.uv()), Temp: r.temp(), Shape: ValShape(r.byte1())}
		}
	}
	if n, isNil := r.slen(); !isNil {
		f.types = make([]*ValType, n)
		for i := range f.types {
			f.types[i] = d.vt(r.uv())
		}
	}
	var flat []Instr
	if n, isNil := r.slen(); !isNil {
		f.blocks = make([]*Block, n)
		for bi := range f.blocks {
			b := &Block{id: BlockID(bi), owner: f}
			b.pos = d.pos(r)
			b.label = d.str(r.uv())
			if b.hasFault = r.bool1(); b.hasFault {
				b.fault = r.block()
				b.faultPos = d.pos(r)
			}
			if k, isNil := r.slen(); !isNil {
				b.instrs = make([]Instr, k)
				for i := range b.instrs {
					b.instrs[i] = d.instr(r)
					flat = append(flat, b.instrs[i])
				}
			}
			b.term = d.term(r)
			f.blocks[bi] = b
		}
	}
	if n, isNil := r.slen(); !isNil {
		f.defs = make([]Instr, n)
		for i := range f.defs {
			at := r.uv()
			if at == 0 {
				continue
			}
			if at > uint64(len(flat)) {
				d.fail("%s: definition %d outside %d instructions", f.name, at, len(flat))
				continue
			}
			f.defs[i] = flat[at-1]
		}
	}
}

func (d *decoder) term(r *rbuf) Term {
	switch tag := r.byte1(); tag {
	case termNone:
		return nil
	case termJump:
		return &Jump{pos: d.pos(r), target: r.block()}
	case termBranch:
		return &Branch{pos: d.pos(r), cond: r.temp(), ifTrue: r.block(), ifFalse: r.block()}
	case termReturn:
		return &Return{pos: d.pos(r), val: r.temp(), hasVal: r.bool1(), ctl: Ctl(r.byte1())}
	default:
		d.fail("terminator tag %d", tag)
		r.fail("terminator")
		return nil
	}
}

func (d *decoder) call(r *rbuf) *Call {
	return &Call{pos: d.pos(r), dst: r.temp(), form: CalleeForm(r.byte1()),
		callee: d.sym(r.uv()), fn: r.temp(), keyAt: int(r.sv()), args: d.temps(r),
		tail: r.bool1(), crosses: r.bool1()}
}

// instr reads one instruction. Go evaluates a composite literal's elements
// in order, which is the order the encoder wrote them.
func (d *decoder) instr(r *rbuf) Instr {
	switch tag := r.byte1(); tag {
	case tagArith:
		return &Arith{pos: d.pos(r), dst: r.temp(), lhs: r.temp(), rhs: r.temp(),
			op: ArithOp(r.byte1()), dom: Domain(r.byte1()), over: Overflow(r.byte1())}
	case tagCall:
		return d.call(r)
	case tagConcat:
		return &Concat{pos: d.pos(r), dst: r.temp(), parts: d.temps(r)}
	case tagDefer:
		n := &Defer{pos: d.pos(r), id: int(r.sv())}
		if r.byte1() != 0 {
			n.call = d.call(r)
		}
		return n
	case tagRunDefer:
		return &RunDefer{pos: d.pos(r), id: int(r.sv())}
	case tagFuncValue:
		return &FuncValue{pos: d.pos(r), dst: r.temp(), body: d.fn(r.uv()), self: r.bool1(),
			captures: d.temps(r)}
	case tagAssert:
		n := &Assert{pos: d.pos(r), dst: r.temp(), subj: r.temp(), kw: AssertKeyword(r.byte1()),
			text: d.str(r.uv()), mismatch: r.bool1(), answer: r.temp()}
		if r.byte1() != 0 {
			b := &AssertBinding{Name: d.str(r.uv()), Expr: d.str(r.uv()), Val: r.temp()}
			if k, isNil := r.slen(); !isNil {
				b.Stages = make([]AssertStage, k)
				for i := range b.Stages {
					b.Stages[i] = AssertStage{Text: d.str(r.uv()), Val: r.temp()}
				}
			}
			n.binding = b
		}
		return n
	case tagRecord:
		return &Record{pos: d.pos(r), val: r.temp(), kind: RecordKind(r.byte1()),
			text: d.str(r.uv()), suppress: r.bool1()}
	case tagIter:
		return &Iter{pos: d.pos(r), dst: r.temp(), op: IterOp(r.byte1()),
			over: IterDomain(r.byte1()), sig: r.bool1(), args: d.temps(r)}
	case tagCompare:
		return &Compare{pos: d.pos(r), dst: r.temp(), op: CompareOp(r.byte1()),
			shape: ValShape(r.byte1()), lhs: r.temp(), rhs: r.temp(), rank: r.bool1()}
	case tagConst:
		return &Const{pos: d.pos(r), dst: r.temp(), kind: ConstKind(r.byte1()), num: r.sv(),
			flt: r.f64(), text: d.str(r.uv()), typ: d.sym(r.uv())}
	case tagBind:
		return &Bind{pos: d.pos(r), dst: r.temp(), src: r.temp(), sym: d.sym(r.uv())}
	case tagMatch:
		return &Match{pos: d.pos(r), dst: r.temp(), subj: r.temp(), arg: r.temp(),
			kind: MatchKind(r.byte1()), sym: d.sym(r.uv()), text: d.str(r.uv()),
			idx: int(r.sv()), embeds: d.sym(r.uv())}
	case tagNoMatch:
		return &NoMatch{pos: d.pos(r), key: r.temp()}
	case tagTodo:
		return &Todo{pos: d.pos(r), dst: r.temp(), reason: d.str(r.uv())}
	case tagNot:
		return &Not{pos: d.pos(r), dst: r.temp(), val: r.temp()}
	case tagProj:
		return &Proj{pos: d.pos(r), dst: r.temp(), subj: r.temp(), kind: ProjKind(r.byte1()),
			sym: d.sym(r.uv()), text: d.str(r.uv()), idx: int(r.sv()), faults: r.bool1(),
			shape: ValShape(r.byte1()), field: d.str(r.uv()), embeds: d.sym(r.uv())}
	case tagMake:
		return &Make{pos: d.pos(r), dst: r.temp(), kind: MakeKind(r.byte1()),
			typ: d.sym(r.uv()), text: d.str(r.uv()), names: d.strList(r), ops: d.temps(r),
			tail: r.temp(), incl: r.bool1(), embeds: d.sym(r.uv())}
	case tagSlot:
		return &Slot{pos: d.pos(r), slot: r.temp(), ty: d.ty(r.uv())}
	case tagStore:
		return &Store{pos: d.pos(r), kind: StoreKind(r.byte1()), sym: d.sym(r.uv()), src: r.temp()}
	case tagRef:
		return &Ref{pos: d.pos(r), dst: r.temp(), kind: RefKind(r.byte1()), sym: d.sym(r.uv())}
	case tagCopy:
		return &Copy{pos: d.pos(r), dst: r.temp(), src: r.temp()}
	case tagRender:
		n := &Render{pos: d.pos(r), dst: r.temp(), src: r.temp(), kind: RenderKind(r.byte1()),
			erased: r.bool1()}
		if k, isNil := r.slen(); !isNil {
			n.impls = make([]DebugImpl, k)
			for i := range n.impls {
				n.impls[i] = DebugImpl{Type: d.str(r.uv()), Fn: d.sym(r.uv())}
			}
		}
		return n
	case tagTry:
		return &Try{pos: d.pos(r), src: r.temp(), text: d.str(r.uv())}
	default:
		d.fail("instruction tag %d", tag)
		r.fail("instruction")
		return &Copy{pos: At("?", 1, 1)}
	}
}
