package analysis

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
)

// DeclarationNames names each declaration in one file's nodes by its path in
// the file, the part of a declaration name after the file's Origin:
//
//	fn, type, interface, host fn     parse, Invoice, Show
//	an interface method               Show.show
//	an inherent impl member           Invoice.total
//	an interface impl member          impl Display for Invoice.to_string
//	a `once`                          once rates, once Invoice.rates
//	a test                            test "adds two"
//	a declaration inside a body       parse/step, parse/Point
//
// A name depends only on the declaration's own text and the declarations
// that enclose it, never on a position, so editing one body leaves every
// other declaration's name as it was. Two declarations whose paths would
// be equal (a `test` name used twice, two blocks' local `Point`) are told
// apart by a `#2`, `#3` suffix in source order. A lambda has no name.
//
// The IR builder builds linkage names from these (internal/irbuild's
// link.go), and objects encoded apart link by them.
func DeclarationNames(nodes []ast.Node) map[ast.Node]string {
	n := &declNamer{out: map[ast.Node]string{}, used: map[string]int{}}
	for _, node := range nodes {
		n.top(node)
	}
	return n.out
}

type declNamer struct {
	out  map[ast.Node]string
	used map[string]int
}

func (n *declNamer) name(node ast.Node, path string) string {
	n.used[path]++
	if c := n.used[path]; c > 1 {
		path += "#" + strconv.Itoa(c)
	}
	n.out[node] = path
	return path
}

func (n *declNamer) top(node ast.Node) {
	switch d := node.(type) {
	case *ast.FuncDef:
		n.body(d.Body, n.name(d, d.Name))
	case *ast.ExternFunc:
		n.name(d, d.Name)
	case *ast.StructDef:
		n.name(d, d.Name)
	case *ast.EnumDef:
		n.name(d, d.Name)
	case *ast.TypeDef:
		n.name(d, d.Name)
	case *ast.TypeAlias:
		n.name(d, d.Name)
	case *ast.ExternType:
		n.name(d, d.Name)
	case *ast.InterfaceDef:
		iface := n.name(d, d.Name)
		for i := range d.Methods {
			m := &d.Methods[i]
			n.name(m, iface+"."+m.Name)
			if b, ok := m.Body.(*ast.Block); ok {
				n.body(b, iface+"."+m.Name)
			}
		}
	case *ast.OnceBinding:
		n.name(d, "once "+d.Name)
	case *ast.TestDecl:
		n.name(d, "test "+strconv.Quote(d.Name))
	case *ast.ImplBlock:
		owner := typeExprText(d.Receiver)
		if d.Interface != nil {
			owner = "impl " + typeExprText(d.Interface) + " for " + owner
		}
		n.name(d, owner)
		for _, item := range d.Items {
			switch it := item.(type) {
			case *ast.FuncDef:
				n.body(it.Body, n.name(it, owner+"."+it.Name))
			case *ast.ExternFunc:
				n.name(it, owner+"."+it.Name)
			case *ast.OnceBinding:
				n.name(it, "once "+owner+"."+it.Name)
			}
		}
	}
}

// body names the functions and types declared inside a body, under the
// enclosing declaration's path.
func (n *declNamer) body(b *ast.Block, parent string) {
	if b == nil {
		return
	}
	ast.Inspect(b, func(m ast.Node) bool {
		switch d := m.(type) {
		case *ast.FuncDef:
			n.body(d.Body, n.name(d, parent+"/"+d.Name))
			return false
		case *ast.StructDef:
			n.name(d, parent+"/"+d.Name)
		case *ast.EnumDef:
			n.name(d, parent+"/"+d.Name)
		case *ast.TypeDef:
			n.name(d, parent+"/"+d.Name)
		case *ast.ImplBlock:
			owner := typeExprText(d.Receiver)
			if d.Interface != nil {
				owner = "impl " + typeExprText(d.Interface) + " for " + owner
			}
			owner = parent + "/" + owner
			n.name(d, owner)
			for _, item := range d.Items {
				if fd, ok := item.(*ast.FuncDef); ok {
					n.body(fd.Body, n.name(fd, owner+"."+fd.Name))
				}
			}
			return false
		}
		return true
	})
}

func typeExprText(te ast.TypeExpr) string {
	if te == nil {
		return "_"
	}
	return te.TypeString()
}
