package astcodec

import (
	"bytes"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ast/astcodec/codecgen"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// TestGeneratedCodecIsCurrent regenerates codec_gen.go from internal/ast's
// types and requires the checked-in file to match. A node type or a field
// added, removed or reordered fails here until
// `go generate ./internal/ast/astcodec` is rerun.
func TestGeneratedCodecIsCurrent(t *testing.T) {
	want, err := codecgen.Generate("..")
	if err != nil {
		t.Fatalf("generation fails: %v", err)
	}
	got, err := os.ReadFile(codecgen.OutputFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale (%d bytes checked in, %d generated); run "+
			"`go generate ./internal/ast/astcodec` from the repository root", codecgen.OutputFile, len(got), len(want))
	}
}

// filler sets every exported field of a value to something other than its
// zero value, so that a field the codec does not write decodes differently.
type filler struct {
	n int
	// impls lists, per interface type, the forms that implement it, and
	// next which one an interface field gets next, so that every form is
	// placed somewhere.
	impls map[reflect.Type][]reflect.Type
	next  map[reflect.Type]int
}

func (f *filler) formsOf(iface reflect.Type) []reflect.Type {
	if got, ok := f.impls[iface]; ok {
		return got
	}
	var out []reflect.Type
	for _, v := range forms {
		if t := reflect.TypeOf(v); t.Implements(iface) {
			out = append(out, t)
		}
	}
	f.impls[iface] = out
	return out
}

func (f *filler) fill(v reflect.Value, depth int) {
	f.n++
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(f.n))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(f.n))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(f.n) + 0.5)
	case reflect.String:
		v.SetString("s" + strings.Repeat("x", f.n%5))
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				f.fill(v.Field(i), depth)
			}
		}
	case reflect.Pointer:
		switch v.Type() {
		case reflect.TypeOf((*big.Int)(nil)):
			v.Set(reflect.ValueOf(new(big.Int).Lsh(big.NewInt(int64(f.n)), 80)))
			return
		case reflect.TypeOf((*ast.Expansion)(nil)):
			v.Set(reflect.ValueOf(ast.NewExpansion("message", nil)))
			return
		}
		if depth > 2 {
			return
		}
		p := reflect.New(v.Type().Elem())
		f.fill(p.Elem(), depth+1)
		v.Set(p)
	case reflect.Interface:
		if depth > 2 {
			return
		}
		choices := f.formsOf(v.Type())
		if len(choices) == 0 {
			return
		}
		t := choices[f.next[v.Type()]%len(choices)]
		f.next[v.Type()]++
		var x reflect.Value
		if t.Kind() == reflect.Pointer {
			x = reflect.New(t.Elem())
			f.fill(x.Elem(), depth+1)
		} else {
			x = reflect.New(t).Elem()
			f.fill(x, depth+1)
		}
		v.Set(x)
	case reflect.Slice:
		if depth > 2 {
			v.Set(reflect.MakeSlice(v.Type(), 0, 0))
			return
		}
		s := reflect.MakeSlice(v.Type(), 2, 2)
		for i := range 2 {
			f.fill(s.Index(i), depth+1)
		}
		v.Set(s)
	}
}

// TestInterfaceCodecCoversEveryNode builds a value of every form a node
// field may hold with every exported field set, at every depth the filler
// reaches, writes each and reads it back, and requires the two to be equal.
// A field the codec leaves out decodes as its zero value and fails here.
func TestInterfaceCodecCoversEveryNode(t *testing.T) {
	f := &filler{impls: map[reflect.Type][]reflect.Type{}, next: map[reflect.Type]int{}}
	for _, form := range forms {
		ft := reflect.TypeOf(form)
		var v reflect.Value
		if ft.Kind() == reflect.Pointer {
			v = reflect.New(ft.Elem())
			f.fill(v.Elem(), 0)
		} else {
			v = reflect.New(ft).Elem()
			f.fill(v, 0)
		}
		e := NewEncoder()
		e.value(v.Interface())
		d := NewDecoder(e.Bytes())
		var got any
		if err := d.Run(func() { got = d.value() }); err != nil {
			t.Fatalf("%s: %v", ft, err)
		}
		if !d.Done() {
			t.Fatalf("%s: %d bytes left after decoding", ft, len(e.Bytes())-d.off)
		}
		if !reflect.DeepEqual(got, v.Interface()) {
			t.Errorf("%s does not survive the codec: a field is not written", ft)
		}
	}
}

// TestCodecRoundTripsSourceTrees encodes the parsed tree of every .nomi
// file under std/ and tests/ and requires each to decode to an equal tree.
func TestCodecRoundTripsSourceTrees(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	n := 0
	for _, dir := range []string{"std", "tests"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, de os.DirEntry, err error) error {
			if err != nil || de.IsDir() || filepath.Ext(path) != ".nomi" {
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			nodes, _, _ := parser.ParseResilient(lexer.Lex(string(src)))
			got, err := Decode(Encode(nodes))
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if !reflect.DeepEqual(got, nodes) {
				t.Fatalf("%s decodes to a different tree", path)
			}
			n++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n < 100 {
		t.Fatalf("round-tripped only %d files", n)
	}
}

// TestDecodeRefusesCorruptData cuts and flips encoded bytes and requires
// an error or a tree, never a panic.
func TestDecodeRefusesCorruptData(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "std", "maybe.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, _, _ := parser.ParseResilient(lexer.Lex(string(src)))
	data := Encode(nodes)
	for cut := 0; cut < len(data); cut += 1 + len(data)/200 {
		if _, err := Decode(data[:cut]); err == nil {
			t.Fatalf("a tree cut to %d of %d bytes decodes", cut, len(data))
		}
	}
	for i := 0; i < len(data); i += 1 + len(data)/300 {
		flipped := bytes.Clone(data)
		flipped[i] ^= 0xa5
		_, _ = Decode(flipped)
	}
}
