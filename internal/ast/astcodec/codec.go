// Package astcodec writes syntax trees as bytes and reads them back: the
// node half of a file's interface (analysis.FileInterface). The encoder and
// decoder of each node type are generated from internal/ast's types
// (codec_gen.go, by codecgen), so every exported field is written;
// TestInterfaceCodecCoversEveryNode holds that to a tree of every shape.
//
// A pointer the tree holds twice is written once and then by its number,
// so a decoded tree shares what the encoded one shares. Decoding bytes the
// encoder did not write, or bytes cut short, is an error, never a panic.
package astcodec

//go:generate go run ./gen

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"

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

// Done reports whether every byte has been read.
func (d *Decoder) Done() bool { return d.off == len(d.buf) }

func (d *Decoder) uint() uint64 {
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
