package analysis

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ast/astcodec"
)

// A file's interface is what another file's check reads of it: its
// declarations with their bodies taken out (docs/design, "A file's
// interface"). Checking a file against interfaces (ProgramInterfaces.Check)
// builds the file from its source and every file it reads from their
// interfaces, and answers the same analysis and diagnostics for the file as
// a check of the whole program from source;
// FuzzCheckFromInterfacesMatchesFull holds it there.
//
// An interface holds the file's declarations as the parser and the
// keyword expansions leave them, the nodes a build is given for the file,
// with what no other file reads taken out:
//
//   - the body of every function outside an interface declaration. A body
//     is replaced by an empty block that keeps the `break` and `continue`
//     the function's own scope holds, which is the one fact of a body
//     another file's check reads (FuncIterSignals);
//   - the value of every `once` written with a type, replaced by `todo`.
//     An unannotated `once` keeps its value, which its type is inferred
//     from (once_type.go), and so does a `noun`'s;
//   - every test declaration and every attached test.
//
// What stays is registered as the file's source would be: the build runs
// the sweeps over it, so the symbols, types and impls another file reads
// are the ones the full check makes. Interface default bodies stay, since
// registering the interface reads them (TakesSelfApart, BuildsSelf), and
// so do parameter and field defaults, which a caller's check reads for
// their presence.

// FileInterface is one file's interface.
type FileInterface struct {
	// Key is the file's module key, and Path the absolute path its
	// positions and its symbols' SourceFile name.
	Key, Path string
	// Nodes are the declarations, bodies taken out.
	Nodes []ast.Node
	// Hash is the interface hash: two texts of the file with the same hash
	// differ only in bodies, layout and comments (interfaceFingerprint).
	Hash string
}

// ExtractInterface answers the interface of the file with module key and
// path whose nodes are as a build is given them: parsed, with the Go
// bindings injected and the keyword declarations expanded. nodes are not
// changed.
func ExtractInterface(key, path string, nodes []ast.Node) *FileInterface {
	sum := sha256.Sum256([]byte(interfaceFingerprint(nodes)))
	// A copy, through the codec, to take the bodies out of.
	copied, err := astcodec.Decode(astcodec.Encode(nodes))
	if err != nil {
		panic("analysis: a tree does not survive the syntax codec: " + err.Error())
	}
	out := make([]ast.Node, 0, len(copied))
	for _, n := range copied {
		if _, isTest := n.(*ast.TestDecl); isTest {
			continue
		}
		stripBodies(n)
		out = append(out, n)
	}
	if nodes == nil {
		// A file with no nodes (empty, or only comments) is one no build
		// loads (discovery passes over it), which an interface of none,
		// rather than of an empty list, keeps it. A file of test
		// declarations alone keeps an empty list and is loaded.
		out = nil
	}
	return &FileInterface{Key: key, Path: path, Nodes: out, Hash: hex.EncodeToString(sum[:])}
}

// stripBodies takes out of the declaration n what an interface leaves out.
func stripBodies(n ast.Node) {
	ast.Inspect(n, func(m ast.Node) bool {
		clearAttachedTests(m)
		switch m := m.(type) {
		case *ast.InterfaceDef:
			// Its default bodies stay; their attached tests go.
			ast.Inspect(m, func(d ast.Node) bool {
				clearAttachedTests(d)
				return true
			})
			return false
		case *ast.FuncDef:
			if m.Body != nil {
				m.Body = signalBody(m.Body)
			}
		case *ast.OnceBinding:
			if m.TypeAnnotation != nil && m.Expansion == nil && m.Value != nil {
				line, col := nodeLineCol(m.Value)
				m.Value = &ast.Todo{Line: line, Col: col}
			}
			return false
		}
		return true
	})
}

// signalBody is the body a stripped function keeps: an empty block at
// body's position holding a `break` and a `continue` when body's own scope
// holds one.
func signalBody(body *ast.Block) *ast.Block {
	s := OwnScopeSignals(body)
	out := &ast.Block{Line: body.Line, Col: body.Col, EndLine: body.EndLine, EndCol: body.EndCol}
	if s.Break {
		out.Stmts = append(out.Stmts, &ast.Break{Line: body.Line, Col: body.Col})
	}
	if s.Continue {
		out.Stmts = append(out.Stmts, &ast.Continue{Line: body.Line, Col: body.Col})
	}
	return out
}

var attachedTestsType = reflect.TypeFor[[]ast.AttachedTest]()

// clearAttachedTests drops the tests attached to n, a declaration that may
// carry some.
func clearAttachedTests(n ast.Node) {
	v := reflect.ValueOf(n)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return
	}
	if f := v.Elem().FieldByName("AttachedTests"); f.IsValid() && f.Type() == attachedTestsType {
		f.SetZero()
	}
}

// interfaceMagic starts an encoded interface, and interfaceVersion is its
// format's version: it changes whenever what an interface holds or how it
// is written does, so an interface written by another format is a miss.
const (
	interfaceMagic   = "NOMIIF"
	interfaceVersion = 2
)

// Encode writes fi as bytes.
func (fi *FileInterface) Encode() []byte {
	e := astcodec.NewEncoder()
	e.String(interfaceMagic)
	e.Uint(interfaceVersion)
	e.String(fi.Key)
	e.String(fi.Path)
	e.String(fi.Hash)
	e.Nodes(fi.Nodes)
	return e.Bytes()
}

// errNotAnInterface is DecodeInterface's error for bytes Encode did not
// write in this format.
var errNotAnInterface = errors.New("analysis: not an interface of this format")

// DecodeInterface reads an interface Encode wrote. Bytes it cannot read are
// an error.
func DecodeInterface(data []byte) (*FileInterface, error) {
	if !bytes.HasPrefix(data, []byte{byte(len(interfaceMagic))}) {
		return nil, errNotAnInterface
	}
	d := astcodec.NewDecoder(data)
	fi := &FileInterface{}
	var ok bool
	err := d.Run(func() {
		if d.String() != interfaceMagic || d.Uint() != interfaceVersion {
			return
		}
		fi.Key, fi.Path, fi.Hash = d.String(), d.String(), d.String()
		fi.Nodes = d.Nodes()
		ok = d.Done()
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNotAnInterface
	}
	return fi, nil
}

// GraftBodies puts back into iface, a module's interface nodes as
// ExtractInterface left them, what extraction took out of the same
// module's nodes source: each function's body, each annotated `once`'s
// value and each declaration's attached tests. iface's nodes keep their
// identity, so the analysis built from them reads the bodies where it reads
// the declarations. It answers source's test declarations, which iface
// does not hold, and an error, having changed nothing, when source is not
// the text iface was extracted from.
func GraftBodies(iface, source []ast.Node) ([]*ast.TestDecl, error) {
	var own []ast.Node
	var tests []*ast.TestDecl
	for _, n := range source {
		if td, ok := n.(*ast.TestDecl); ok {
			tests = append(tests, td)
			continue
		}
		own = append(own, n)
	}
	if len(own) != len(iface) {
		return nil, errors.New("analysis: the source has other declarations than the interface")
	}
	var to, from graftSites
	for i := range iface {
		to.collect(iface[i])
		from.collect(own[i])
	}
	if len(to.fns) != len(from.fns) || len(to.onces) != len(from.onces) || len(to.attached) != len(from.attached) {
		return nil, errors.New("analysis: the source has other declarations than the interface")
	}
	for i, fd := range to.fns {
		if src := from.fns[i]; fd.Name != src.Name || fd.Line != src.Line || fd.Col != src.Col || (fd.Body == nil) != (src.Body == nil) {
			return nil, errors.New("analysis: the source's function " + src.Name + " is not the interface's " + fd.Name)
		}
	}
	for i, o := range to.onces {
		if src := from.onces[i]; o.Line != src.Line || o.Col != src.Col {
			return nil, errors.New("analysis: the source's onces are not the interface's")
		}
	}
	for i, n := range to.attached {
		if topLevelPos(n) != topLevelPos(from.attached[i]) {
			return nil, errors.New("analysis: the source's declarations are not the interface's")
		}
	}
	for i, fd := range to.fns {
		if fd.Body != nil {
			fd.Body = from.fns[i].Body
		}
	}
	for i, o := range to.onces {
		if src := from.onces[i]; src.TypeAnnotation != nil && src.Expansion == nil && src.Value != nil {
			o.Value = src.Value
		}
	}
	for i, n := range to.attached {
		tests := reflect.ValueOf(from.attached[i]).Elem().FieldByName("AttachedTests")
		reflect.ValueOf(n).Elem().FieldByName("AttachedTests").Set(tests)
	}
	return tests, nil
}

// graftSites are a declaration's places extraction changes, in one order
// for the source and its interface: its functions outside interface
// declarations, its onces, and its nodes that may carry attached tests.
type graftSites struct {
	fns      []*ast.FuncDef
	onces    []*ast.OnceBinding
	attached []ast.Node
}

func (g *graftSites) collect(n ast.Node) {
	// A declaration's attached tests are nodes too, which an interface
	// does not hold: each is set aside while the walk passes the
	// declaration, so the walk does not count what they declare.
	var set, held []reflect.Value
	defer func() {
		for i, f := range set {
			f.Set(held[i])
		}
	}()
	hold := func(m ast.Node) {
		if carriesAttachedTests(m) {
			g.attached = append(g.attached, m)
			f := reflect.ValueOf(m).Elem().FieldByName("AttachedTests")
			saved := reflect.New(f.Type()).Elem()
			saved.Set(f)
			set, held = append(set, f), append(held, saved)
			f.SetZero()
		}
	}
	ast.Inspect(n, func(m ast.Node) bool {
		hold(m)
		switch m := m.(type) {
		case *ast.InterfaceDef:
			// Its default bodies are in the interface; their attached
			// tests are not.
			ast.Children(m, func(c ast.Node) {
				ast.Inspect(c, func(d ast.Node) bool {
					hold(d)
					return true
				})
			})
			return false
		case *ast.FuncDef:
			g.fns = append(g.fns, m)
			return false
		case *ast.OnceBinding:
			g.onces = append(g.onces, m)
			return false
		}
		return true
	})
}

// carriesAttachedTests reports whether n is a declaration that may carry
// attached tests.
func carriesAttachedTests(n ast.Node) bool {
	v := reflect.ValueOf(n)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return false
	}
	f := v.Elem().FieldByName("AttachedTests")
	return f.IsValid() && f.Type() == attachedTestsType
}
