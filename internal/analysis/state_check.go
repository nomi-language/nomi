package analysis

import (
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"unsafe"

	"github.com/nomi-language/nomi/internal/ast"
)

// CheckStdStateRoundTrip encodes s, whose paths are under dir, decodes it
// with the stdlib's directory at otherDir, and answers how the decoded
// state differs from s, or nil: every field of every object either holds,
// the strings under dir moved under otherDir; every pointer and map of s
// read back as one object of the decoded state, the same one wherever s
// shares it, or as itself when it is a package-level object; and no object
// written lies inside another, whose sharing the codec would lose. The
// tests of std's state call it (std/state_test.go).
func CheckStdStateRoundTrip(s StdState, dir, otherDir string) error {
	rec := &stateRecording{}
	data, err := encodeStdState(s, dir, rec)
	if err != nil {
		return err
	}
	loader := func(string, []string) ([]ast.Node, error) { return nil, nil }
	got, err := decodeStdState(data, otherDir, loader, rec)
	if err != nil {
		return fmt.Errorf("decoding: %w", err)
	}
	c := &stateComparer{dir: dir, other: otherDir, pairs: map[stateCheckKey]unsafe.Pointer{}, slices: map[stateSliceKey]unsafe.Pointer{}, seen: map[stateCheckKey]bool{}}
	add := func(enc []stateKey, encTypes []reflect.Type, dec []any) error {
		if len(enc) != len(dec) {
			return fmt.Errorf("%d pointers written, %d read", len(enc), len(dec))
		}
		for i, k := range enc {
			t := reflect.TypeOf(dec[i])
			if t.Kind() == reflect.Slice {
				continue
			}
			if encTypes != nil && encTypes[i] != t {
				return fmt.Errorf("pointer %d written as %s, read as %s", i, encTypes[i], t)
			}
			c.pairs[stateCheckKey{k.p, t}] = globalPointer(dec[i])
		}
		return nil
	}
	if err := add(rec.enc, nil, rec.dec); err != nil {
		return err
	}
	var astKeys []stateKey
	var astTypes []reflect.Type
	for _, p := range rec.astEnc {
		astKeys = append(astKeys, stateKey{p: globalPointer(p)})
		astTypes = append(astTypes, reflect.TypeOf(p))
	}
	if err := add(astKeys, astTypes, rec.astDec); err != nil {
		return err
	}
	if err := c.overlaps(); err != nil {
		return err
	}
	want := reflect.ValueOf(&stdState{files: s.Files, nodes: s.Nodes, ifaces: s.Ifaces, base: s.Base, errors: s.Errors}).Elem()
	have := reflect.ValueOf(&stdState{files: got.Files, nodes: got.Nodes, ifaces: got.Ifaces, base: got.Base, errors: got.Errors}).Elem()
	c.compare(want, have, "state")
	if len(c.errs) > 0 {
		if len(c.errs) > 20 {
			c.errs = append(c.errs[:20], fmt.Sprintf("and %d more", len(c.errs)-20))
		}
		return fmt.Errorf("the decoded state differs:\n%s", strings.Join(c.errs, "\n"))
	}
	return nil
}

// stateRecording is the pointers an encoding wrote and its decoding read,
// in order, for CheckStdStateRoundTrip.
type stateRecording struct {
	enc    []stateKey
	dec    []any
	astEnc []any
	astDec []any
}

type stateCheckKey struct {
	p unsafe.Pointer
	t reflect.Type
}

type stateSliceKey struct {
	p unsafe.Pointer
	t reflect.Type
	n int
}

type stateComparer struct {
	// slices maps each slice of the encoded state compared to the
	// elements it was read back as.
	slices     map[stateSliceKey]unsafe.Pointer
	dir, other string
	// pairs maps each pointer and map of the encoded state to what it was
	// read back as.
	pairs map[stateCheckKey]unsafe.Pointer
	seen  map[stateCheckKey]bool
	errs  []string
}

func (c *stateComparer) errorf(format string, args ...any) {
	c.errs = append(c.errs, fmt.Sprintf(format, args...))
}

// overlaps reports an object written that lies inside another one: a
// pointer to a field of a struct, which the decoded state would hold as an
// object of its own.
func (c *stateComparer) overlaps() error {
	type span struct {
		lo, hi uintptr
		t      reflect.Type
	}
	var spans []span
	for k := range c.pairs {
		if k.t.Kind() != reflect.Pointer {
			continue
		}
		lo := uintptr(k.p)
		spans = append(spans, span{lo, lo + k.t.Elem().Size(), k.t})
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].lo != spans[j].lo {
			return spans[i].lo < spans[j].lo
		}
		return spans[i].hi > spans[j].hi
	})
	for i := 1; i < len(spans); i++ {
		prev, cur := spans[i-1], spans[i]
		if cur.lo < prev.hi && cur.hi > cur.lo {
			return fmt.Errorf("a %s lies inside a %s the state writes", cur.t, prev.t)
		}
	}
	return nil
}

// translate answers v, a value of the encoded state, as the decoded state
// holds it: its pointers and maps the ones they were read back as, its
// strings under the other directory.
func (c *stateComparer) translate(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer, reflect.Map:
		if v.IsNil() {
			return v
		}
		k := stateCheckKey{v.UnsafePointer(), v.Type()}
		if p, ok := c.pairs[k]; ok {
			return reflect.NewAt(v.Type().Elem(), p)
		}
		return v
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(c.translate(v.Elem()))
		return out
	case reflect.String:
		return reflect.ValueOf(c.moved(v.String())).Convert(v.Type())
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := range v.Len() {
			out.Index(i).Set(c.translate(readable(v.Index(i))))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		for i := range v.NumField() {
			f := reflect.NewAt(out.Field(i).Type(), unsafe.Pointer(out.Field(i).UnsafeAddr())).Elem()
			f.Set(c.translate(readable(v.Field(i))))
		}
		return out
	}
	return v
}

// moved is s with the encoded state's directory replaced by the other.
func (c *stateComparer) moved(s string) string {
	if rel, ok := strings.CutPrefix(s, c.dir); ok && (rel == "" || rel[0] == '/' || rel[0] == '\\') {
		return c.other + rel
	}
	return s
}

// readable is v, a field possibly unexported, as a value that can be read
// and copied.
// Every value the comparer reads is made addressable here, so its fields,
// exported or not, are too.
func readable(v reflect.Value) reflect.Value {
	if v.CanAddr() {
		return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
	}
	cp := reflect.New(v.Type()).Elem()
	cp.Set(v)
	return cp
}

func (c *stateComparer) compare(want, have reflect.Value, path string) {
	if len(c.errs) > 100 {
		return
	}
	if want.Type() != have.Type() {
		c.errorf("%s: a %s read back as a %s", path, want.Type(), have.Type())
		return
	}
	switch want.Kind() {
	case reflect.Bool:
		if want.Bool() != have.Bool() {
			c.errorf("%s: %v read back as %v", path, want.Bool(), have.Bool())
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if want.Int() != have.Int() {
			c.errorf("%s: %d read back as %d", path, want.Int(), have.Int())
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if want.Uint() != have.Uint() {
			c.errorf("%s: %d read back as %d", path, want.Uint(), have.Uint())
		}
	case reflect.Float32, reflect.Float64:
		if want.Float() != have.Float() {
			c.errorf("%s: %v read back as %v", path, want.Float(), have.Float())
		}
	case reflect.String:
		if c.moved(want.String()) != have.String() {
			c.errorf("%s: %q read back as %q", path, want.String(), have.String())
		}
	case reflect.Func:
		if want.IsNil() != have.IsNil() {
			c.errorf("%s: a function read back as nil or the other way", path)
		}
	case reflect.Chan, reflect.UnsafePointer:
		if want.IsNil() != have.IsNil() || (!want.IsNil() && want.Pointer() != have.Pointer()) {
			c.errorf("%s: a %s read back otherwise", path, want.Kind())
		}
	case reflect.Interface:
		if want.IsNil() || have.IsNil() {
			if want.IsNil() != have.IsNil() {
				c.errorf("%s: nil read back as not nil or the other way", path)
			}
			return
		}
		c.compare(readable(want.Elem()), readable(have.Elem()), path)
	case reflect.Pointer, reflect.Map:
		if want.IsNil() || have.IsNil() {
			if want.IsNil() != have.IsNil() {
				c.errorf("%s: nil read back as not nil or the other way", path)
			}
			return
		}
		if w, ok := want.Interface().(*big.Int); ok {
			// The AST's numbers are written by their values.
			if w.Cmp(have.Interface().(*big.Int)) != 0 {
				c.errorf("%s: %v read back as %v", path, w, have.Interface())
			}
			return
		}
		k := stateCheckKey{want.UnsafePointer(), want.Type()}
		p, ok := c.pairs[k]
		if !ok {
			// A package-level object is the process's own.
			if _, global := stateGlobalIndex[want.UnsafePointer()]; !global {
				c.errorf("%s: a %s the state does not write", path, want.Type())
				return
			}
			p = want.UnsafePointer()
		}
		if have.UnsafePointer() != p {
			c.errorf("%s: a %s read back as another object than the one it was written as", path, want.Type())
			return
		}
		if c.seen[k] {
			return
		}
		c.seen[k] = true
		if _, global := stateGlobalIndex[want.UnsafePointer()]; global && have.UnsafePointer() == want.UnsafePointer() {
			return
		}
		if want.Kind() == reflect.Pointer {
			if want.Type().Elem().Kind() == reflect.Struct && isSyncType(want.Type().Elem()) {
				return
			}
			c.compare(want.Elem(), have.Elem(), path)
			return
		}
		if want.Len() != have.Len() {
			c.errorf("%s: a map of %d read back with %d", path, want.Len(), have.Len())
			return
		}
		it := want.MapRange()
		for it.Next() {
			key := readable(it.Key())
			hv := have.MapIndex(c.translate(key))
			if !hv.IsValid() {
				c.errorf("%s: key %v is not read back", path, describe(key))
				continue
			}
			c.compare(readable(it.Value()), readable(hv), fmt.Sprintf("%s[%v]", path, describe(key)))
		}
	case reflect.Slice:
		if want.IsNil() != have.IsNil() {
			c.errorf("%s: a nil slice read back as an empty one or the other way", path)
			return
		}
		if want.Len() != have.Len() {
			c.errorf("%s: %d elements read back as %d", path, want.Len(), have.Len())
			return
		}
		if want.Len() > 0 {
			// Slices that share their elements read back sharing them.
			k := stateSliceKey{want.UnsafePointer(), want.Type(), want.Len()}
			if p, ok := c.slices[k]; ok {
				if p != have.UnsafePointer() {
					c.errorf("%s: a shared slice read back apart", path)
				}
				return
			}
			c.slices[k] = have.UnsafePointer()
		}
		for i := range want.Len() {
			c.compare(readable(want.Index(i)), readable(have.Index(i)), fmt.Sprintf("%s[%d]", path, i))
		}
	case reflect.Array:
		for i := range want.Len() {
			c.compare(readable(want.Index(i)), readable(have.Index(i)), fmt.Sprintf("%s[%d]", path, i))
		}
	case reflect.Struct:
		if isSyncType(want.Type()) {
			return
		}
		if want.Type().PkgPath() == "sync/atomic" {
			// An atomic value is compared by what it holds.
			load := func(v reflect.Value) reflect.Value { return v.Addr().MethodByName("Load").Call(nil)[0] }
			c.compare(readable(load(want)), readable(load(have)), path)
			return
		}
		for i := range want.NumField() {
			if want.Type() == reflect.TypeFor[FileDecls]() && want.Type().Field(i).Name == "keptRest" {
				// Set only on a file read from a state, which reads its
				// kept declarations through it.
				continue
			}
			c.compare(readable(want.Field(i)), readable(have.Field(i)), path+"."+want.Type().Field(i).Name)
		}
	default:
		c.errorf("%s: a %s", path, want.Kind())
	}
}

// isSyncType reports whether t is a lock or a sync.Once, whose state the
// codec does not compare: a lock is not held, and a Once's is its hook's.
func isSyncType(t reflect.Type) bool {
	return t.PkgPath() == "sync"
}

func describe(v reflect.Value) string {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return "nil"
		}
		return describe(v.Elem())
	case reflect.Pointer, reflect.Map:
		if v.IsNil() {
			return "nil"
		}
		return fmt.Sprintf("%s@%p", v.Type(), v.UnsafePointer())
	}
	if v.CanInterface() {
		return fmt.Sprintf("%v", v.Interface())
	}
	return v.Type().String()
}
