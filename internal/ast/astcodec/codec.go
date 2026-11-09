package astcodec

//go:generate go run ./gen

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"unsafe"

	"github.com/nomi-language/nomi/internal/ast"
)

// Encoder writes values to a byte buffer. One Encoder numbers the pointers
// of everything written to it, so nodes written by two calls may share.
type Encoder struct {
	buf []byte
	ids map[any]int
}

// NewEncoder answers an empty Encoder.
func NewEncoder() *Encoder {
	return &Encoder{ids: map[any]int{}}
}

// Bytes answers what has been written.
func (e *Encoder) Bytes() []byte { return e.buf }

// Node writes n, which may be nil.
func (e *Encoder) Node(n ast.Node) { e.value(n) }

// Nodes writes a list of nodes.
func (e *Encoder) Nodes(ns []ast.Node) {
	e.len(ns == nil, len(ns))
	for _, n := range ns {
		e.value(n)
	}
}

// Uint, Int, String and Bool write a value of their kind.
func (e *Encoder) Uint(v uint64)   { e.uint(v) }
func (e *Encoder) Int(v int64)     { e.int(v) }
func (e *Encoder) String(s string) { e.string(s) }
func (e *Encoder) Bool(b bool)     { e.bool(b) }

// Pointers answers every pointer written so far, in the order of their
// numbers: what a Decoder's Pointers answers, pointer for pointer, once it
// has read the same bytes.
func (e *Encoder) Pointers() []any {
	out := make([]any, len(e.ids))
	for p, id := range e.ids {
		out[id] = p
	}
	return out
}

// Len writes a slice's length, telling a nil slice from an empty one.
func (e *Encoder) Len(isNil bool, n int) { e.len(isNil, n) }

// Value writes v, nil or a value of one of the forms an interface field
// may hold (a node, by pointer or by value), as an interface field is
// written.
func (e *Encoder) Value(v any) { e.value(v) }

func (e *Encoder) uint(v uint64) { e.buf = binary.AppendUvarint(e.buf, v) }
func (e *Encoder) int(v int64)   { e.buf = binary.AppendVarint(e.buf, v) }
func (e *Encoder) float(v float64) {
	e.buf = binary.LittleEndian.AppendUint64(e.buf, math.Float64bits(v))
}

func (e *Encoder) bool(b bool) {
	if b {
		e.buf = append(e.buf, 1)
	} else {
		e.buf = append(e.buf, 0)
	}
}

func (e *Encoder) string(s string) {
	e.uint(uint64(len(s)))
	e.buf = append(e.buf, s...)
}

// len writes a slice's length, 0 for a nil slice.
func (e *Encoder) len(isNil bool, n int) {
	if isNil {
		e.uint(0)
		return
	}
	e.uint(uint64(n) + 1)
}

// ptr writes the non-nil pointer p by its number, and reports whether this
// is its first time, when the caller writes what it points to.
func (e *Encoder) ptr(p any) bool {
	if id, ok := e.ids[p]; ok {
		e.uint(uint64(id) + 2)
		return false
	}
	id := len(e.ids)
	e.ids[p] = id
	e.uint(1)
	return true
}

// sharedLen reads the length of a slice the encoder wrote by its number
// when it shared the elements of one written before (Encoder.slice), -1
// for a nil slice: no element follows it, so it is checked against the
// whole data rather than what is left of it.
func (d *Decoder) sharedLen() int {
	n := d.uint()
	if n == 0 {
		return -1
	}
	if n-1 > uint64(len(d.buf)) {
		d.fail("a slice longer than the data")
	}
	return int(n - 1)
}

// SliceLen is sharedLen, for another codec that shares slices as this one
// does.
func (d *Decoder) SliceLen() int { return d.sharedLen() }

// fits fails unless n elements, which take a byte each at least, follow.
func (d *Decoder) fits(n int) {
	if n > len(d.buf)-d.off {
		d.fail("a slice longer than the data")
	}
}

// Fits is fits, for another codec.
func (d *Decoder) Fits(n int) { d.fits(n) }

// sliceKey is a slice's identity: its elements' address, its length and
// its type.
type sliceKey struct {
	p unsafe.Pointer
	n int
	t string
}

// slice writes a slice of n elements at p, of type t, by its number, and
// reports whether this is its first time, when the caller writes its
// elements: slices that share their elements read back sharing them.
func (e *Encoder) slice(p unsafe.Pointer, n int, t string) bool {
	return e.ptr(sliceKey{p, n, t})
}

func (e *Encoder) bigInt(v *big.Int) {
	if v == nil {
		e.uint(0)
		return
	}
	e.uint(1)
	e.string(v.Text(16))
}

// expansion writes a keyword declaration's mark. Its detail, what the
// expanding keyword keeps, is the expansion's own and is not written: a
// decoded declaration is read for what it declares.
func (e *Encoder) expansion(x *ast.Expansion) {
	if x == nil {
		e.uint(0)
		return
	}
	e.uint(1)
	e.string(x.Keyword)
}

// Decoder reads what an Encoder wrote.
type Decoder struct {
	buf  []byte
	off  int
	refs []any
	err  error
}

// NewDecoder answers a Decoder over data.
func NewDecoder(data []byte) *Decoder { return &Decoder{buf: data} }

// errCorrupt is the error a Decoder answers for bytes it cannot read.
var errCorrupt = errors.New("astcodec: corrupt data")

// decodeFailure is what fail panics with, and Run recovers.
type decodeFailure struct{ msg string }

func (d *Decoder) fail(msg string) {
	panic(decodeFailure{msg})
}

// Run calls read, answering an error instead of a panic when the bytes do
// not decode. Every read of a Decoder goes through it.
func (d *Decoder) Run(read func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(decodeFailure)
			if !ok {
				// A type assertion over a corrupt form panics with a
				// runtime error: corrupt data all the same.
				if _, rt := r.(interface{ RuntimeError() }); !rt {
					panic(r)
				}
				f.msg = fmt.Sprint(r)
			}
			err = fmt.Errorf("%w at byte %d: %s", errCorrupt, d.off, f.msg)
		}
	}()
	read()
	return nil
}

// Node reads a node.
func (d *Decoder) Node() ast.Node {
	v := d.value()
	if v == nil {
		return nil
	}
	n, ok := v.(ast.Node)
	if !ok {
		d.fail(fmt.Sprintf("a %T where a node belongs", v))
	}
	return n
}

// Nodes reads a list of nodes.
func (d *Decoder) Nodes() []ast.Node {
	n := d.len()
	if n < 0 {
		return nil
	}
	out := make([]ast.Node, n)
	for i := range out {
		out[i] = d.Node()
	}
	return out
}

// Uint, Int, String and Bool read a value of their kind.
func (d *Decoder) Uint() uint64   { return d.uint() }
func (d *Decoder) Int() int64     { return d.int() }
func (d *Decoder) String() string { return d.string() }
func (d *Decoder) Bool() bool     { return d.bool() }

// Len reads a slice's length, -1 for a nil slice.
func (d *Decoder) Len() int { return d.len() }

// Pointers answers every pointer read so far, in the order of their
// numbers.
func (d *Decoder) Pointers() []any { return d.refs }

// Reserve makes room for n pointers, the number the Encoder wrote, so
// reading them grows nothing.
func (d *Decoder) Reserve(n int) {
	if n > len(d.buf) {
		d.fail("more pointers than bytes")
	}
	d.refs = make([]any, 0, n)
}

// PointerCount is how many pointers have been written.
func (e *Encoder) PointerCount() int { return len(e.ids) }

// Value reads what Encoder.Value wrote.
func (d *Decoder) Value() any { return d.value() }

// Fail stops the decoding as a corrupt one: Run answers an error saying
// msg.
func (d *Decoder) Fail(msg string) { d.fail(msg) }

// Next answers the next n bytes, which the caller reads itself.
func (d *Decoder) Next(n int) []byte {
	if n < 0 || n > len(d.buf)-d.off {
		d.fail("a short run of bytes")
	}
	b := d.buf[d.off : d.off+n]
	d.off += n
	return b
}

// Remaining is how many bytes are left to read.
func (d *Decoder) Remaining() int { return len(d.buf) - d.off }

// Done reports whether every byte has been read.
func (d *Decoder) Done() bool { return d.off == len(d.buf) }

func (d *Decoder) uint() uint64 {
	// Most numbers are a byte.
	if d.off < len(d.buf) {
		if b := d.buf[d.off]; b < 0x80 {
			d.off++
			return uint64(b)
		}
	}
	return d.uintSlow()
}

func (d *Decoder) uintSlow() uint64 {
	v, n := binary.Uvarint(d.buf[d.off:])
	if n <= 0 {
		d.fail("a bad unsigned number")
	}
	d.off += n
	return v
}

func (d *Decoder) int() int64 {
	v, n := binary.Varint(d.buf[d.off:])
	if n <= 0 {
		d.fail("a bad number")
	}
	d.off += n
	return v
}

func (d *Decoder) float() float64 {
	if len(d.buf)-d.off < 8 {
		d.fail("a short float")
	}
	v := math.Float64frombits(binary.LittleEndian.Uint64(d.buf[d.off:]))
	d.off += 8
	return v
}

func (d *Decoder) bool() bool {
	if d.off >= len(d.buf) {
		d.fail("a short bool")
	}
	b := d.buf[d.off]
	d.off++
	if b > 1 {
		d.fail("a bad bool")
	}
	return b == 1
}

func (d *Decoder) string() string {
	n := d.uint()
	if n > uint64(len(d.buf)-d.off) {
		d.fail("a short string")
	}
	s := string(d.buf[d.off : d.off+int(n)])
	d.off += int(n)
	return s
}

// len reads a slice's length, -1 for a nil slice.
func (d *Decoder) len() int {
	n := d.uint()
	if n == 0 {
		return -1
	}
	// Every element takes a byte at least.
	if n-1 > uint64(len(d.buf)-d.off) {
		d.fail("a slice longer than the data")
	}
	return int(n - 1)
}

// ptr reads a pointer: what an earlier one decoded to, or nil and whether
// it is a new one, which the caller decodes and appends to refs.
func (d *Decoder) ptr() (any, bool) {
	v := d.uint()
	switch {
	case v == 0:
		return nil, false
	case v == 1:
		return nil, true
	}
	id := v - 2
	if id >= uint64(len(d.refs)) {
		d.fail("a pointer to nothing decoded")
	}
	return d.refs[id], false
}

func (d *Decoder) bigInt() *big.Int {
	if d.uint() == 0 {
		return nil
	}
	v, ok := new(big.Int).SetString(d.string(), 16)
	if !ok {
		d.fail("a bad big number")
	}
	return v
}

func (d *Decoder) expansion() *ast.Expansion {
	if d.uint() == 0 {
		return nil
	}
	return ast.NewExpansion(d.string(), nil)
}

// Encode writes nodes as bytes.
func Encode(nodes []ast.Node) []byte {
	e := NewEncoder()
	e.Nodes(nodes)
	return e.Bytes()
}

// Decode reads nodes Encode wrote.
func Decode(data []byte) ([]ast.Node, error) {
	d := NewDecoder(data)
	var nodes []ast.Node
	if err := d.Run(func() {
		nodes = d.Nodes()
		if !d.Done() {
			d.fail("bytes after the nodes")
		}
	}); err != nil {
		return nil, err
	}
	return nodes, nil
}
