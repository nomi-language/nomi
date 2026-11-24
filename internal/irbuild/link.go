package irbuild

// Linkage names: what they are and why is the package doc, "Linkage
// names". ir.CheckLinkage holds the rule over every program the object
// harness runs: a symbol two modules name has a linkage name, and two
// symbols share one only when both define the same code.

import (
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/std"
)

// declName is a declaration's name: the module that declares it (`std/http`,
// `billing/invoice`, an entry's `main`) and its path in that module's file
// (analysis.DeclarationNames).
type declName struct {
	module, path string
	// file is the declaring module's source file, whose base name
	// qualifies the declaration's types at run time (irTypeSymName).
	file string
	// impl is the file-level impl block a function is declared in, nil for
	// any other declaration (implFuncOf).
	impl *ast.ImplBlock
}

func (d declName) String() string { return d.module + "." + d.path }

// stdLinkNames is the name of every declaration in the shared std analysis,
// by its node.
var stdLinkNames = perStd(func() map[ast.Node]declName {
	lib := std.Shared()
	out := map[ast.Node]declName{}
	for name, nodes := range lib.Nodes {
		addLinkNames(out, "std/"+name, lib.SourcePath(name), nodes)
	}
	return out
})

// addLinkNames files every declaration of one module's nodes under module,
// a function of a file-level impl block with its block.
func addLinkNames(into map[ast.Node]declName, module, file string, nodes []ast.Node) {
	for n, path := range analysis.DeclarationNames(nodes) {
		into[n] = declName{module: module, path: path, file: file}
	}
	for _, n := range nodes {
		if ib, isBlock := n.(*ast.ImplBlock); isBlock {
			for _, item := range ib.Items {
				switch item.(type) {
				case *ast.FuncDef, *ast.ExternFunc:
				default:
					continue
				}
				if d, named := into[item]; named {
					d.impl = ib
					into[item] = d
				}
			}
		}
	}
}

// programLinkNames is the name of every declaration of p's own files, by
// node. A listed std module lowered from the shared analysis is named by
// stdLinkNames, which already holds its nodes.
func programLinkNames(p *Program) map[ast.Node]declName {
	out := map[ast.Node]declName{}
	shared := programStdModuleNodes()
	for i := range p.Modules {
		m := &p.Modules[i]
		if name, inStd := strings.CutPrefix(m.Name, "std/"); inStd && m.twins == nil && sameNodes(shared[name], m.Nodes) {
			continue
		}
		addLinkNames(out, m.Name, m.Path, m.Nodes)
	}
	return out
}

func sameNodes(a, b []ast.Node) bool {
	return len(a) > 0 && len(a) == len(b) && a[0] == b[0]
}

// declLink is the linkage path of a declaration node, or "" for a node no
// module declares at its top level or in a body (a lambda).
func (g *gen) declLink(n ast.Node) string {
	if d, ok := g.declNameOf(n); ok {
		return d.String()
	}
	return ""
}

// declNameOf is a declaration node's name, from the program's files or
// std's.
func (g *gen) declNameOf(n ast.Node) (declName, bool) {
	if n == nil {
		return declName{}, false
	}
	if fd, ok := n.(*ast.FuncDef); ok {
		// A user file's `host fn` is lowered through a shell (hostShellOf).
		if ef, shell := hostExtOf(fd); shell {
			n = ef
		}
	}
	if links := g.programLinks(); links != nil {
		if d, ok := links[n]; ok {
			return d, true
		}
	}
	d, ok := stdLinkNames()[n]
	return d, ok
}

// programLinks is the program's declaration names: its registry's, or for a
// std instance's gen, which has none, its program's (stdinst.go).
func (g *gen) programLinks() map[ast.Node]declName {
	if g.reg != nil && g.reg.linkNames != nil {
		return g.reg.linkNames
	}
	return g.progLinks
}

// linkName is the linkage name of the symbol a table interns for token, or
// "" for one only its own module names (ir.Table.SetLinkage).
func (g *gen) linkName(token any, name string) string {
	switch t := token.(type) {
	case *ast.FuncDef:
		if inst := g.stdInstCur; inst != nil && inst.fd == t {
			if im := inst.f.ifaceDefault; im != nil {
				// A std interface default's instance at a receiver: the
				// declaration is the interface's function, which the
				// instance's synthesized FuncDef presents (stdifacedefault.go).
				return within(g.declLink(im), "<"+stdInstLinkArgs(inst)+">")
			}
			// A std generic body's instance, in its own gen (stdinst.go).
			return within(g.declLink(t), "<"+stdInstLinkArgs(inst)+">")
		}
		return g.declLink(t)
	case *ast.OnceBinding:
		return g.declLink(t)
	case *implItem:
		return g.implItemLink(t)
	case *ifaceMethod:
		if t.decl == nil {
			return ""
		}
		return g.declLink(t.decl)
	case *ifaceDefaultInst:
		return g.ifaceDefaultLink(t)
	case *monoInst:
		if t.tpl == nil {
			return ""
		}
		var b strings.Builder
		writeInstArgs(&b, t.args, t.reads)
		return within(g.declLink(t.tpl.decl), "<"+b.String()+">")
	case *onceDef:
		return g.declLink(t.decl)
	case *stdOnce:
		return g.declLink(t.decl)
	case instMethodToken:
		return within(g.declLink(t.tpl), "<"+t.args+">."+t.method)
	case stdIfaceMethodToken:
		// Named by the interface alone, as std declares it: one name in
		// every program and every object, which the linker makes one symbol.
		return within(g.declLink(t.decl), "."+t.method)
	case irFieldDefaultFill:
		if t.def == nil {
			return ""
		}
		return within(g.typeLink(t.def), "."+t.variant+"."+t.field+" default")
	case irSlotDispatchToken:
		return within(g.linkName(t.method, name), " default "+strconv.Itoa(t.slot+1))
	case irIfaceSlotDefault:
		link := within(g.declLink(t.im), "<"+t.kinds+"> default "+strconv.Itoa(t.slot+1))
		if t.recv >= 0 {
			return within(link, " by "+strconv.Itoa(t.recv+1))
		}
		return link
	case irKeyedSlotDefault:
		return within(t.callee.Link(), " default "+strconv.Itoa(t.slot+1)+" by "+strconv.Itoa(t.key+1))
	case irFileSlotDefault:
		return within(t.callee.Link(), " default "+strconv.Itoa(t.slot+1))
	case irSlotDefault:
		if inst := g.stdInstCur; inst != nil && inst.slot >= 0 && inst.view == t.f {
			// A generic std function's default at an instance's type
			// arguments, in its own gen (stdInstances.defaultAccessor).
			return within(g.declLink(inst.fd), "<"+stdInstLinkArgs(inst)+"> default "+strconv.Itoa(t.slot+1))
		}
		return within(stdFuncLink(g, t.f), " default "+strconv.Itoa(t.slot+1))
	case irBootDefaultToken:
		return within(g.declLink(t.boot), " startup default")
	case literalCellKey:
		// A program file's typed literal cell, which a refinement
		// literal's build reads from outside the file's module
		// (relinkRefinementFuncs): named by the file's module and the
		// cell's own name, which holds the literal's text and position.
		// A std module's cells stay unnamed, since a generic std body is
		// lowered once shared and again per program instance.
		if g.stdModule != "" || g.stdInstCur != nil {
			return ""
		}
		return within(g.unitLinkModule(), "."+name)
	}
	return ""
}

// unitLinkModule is the module part of the linkage names of this unit's
// file's declarations, or "" when it declares none.
func (g *gen) unitLinkModule() string {
	for _, n := range g.nodes {
		if d, ok := g.declNameOf(n); ok {
			return d.module
		}
	}
	return ""
}

// stdInstLinkArgs spells a std instance's type arguments (writeInstArgs).
func stdInstLinkArgs(inst *stdInst) string {
	var b strings.Builder
	writeInstArgs(&b, inst.args, inst.reads)
	return b.String()
}

// within is base with suffix, or "" when base is: a name made from a
// declaration with no linkage name has none either.
func within(base, suffix string) string {
	if base == "" {
		return ""
	}
	return base + suffix
}

// stdFuncLink names a std function: its declaration's linkage path, or its
// index key for a function the std index synthesized.
func stdFuncLink(g *gen, f *stdFunc) string {
	if f == nil {
		return ""
	}
	if f.decl != nil {
		if s := g.declLink(f.decl); s != "" {
			return s
		}
	}
	return "std/" + f.module + "." + f.key
}

// typeLink names a declared type: its declaration's linkage path with its
// type arguments, or its spelling for a process-wide std type with no
// declaration of its own.
func (g *gen) typeLink(d *typeDef) string {
	return "type " + portableArgsKey([]kind{{tag: tagNamed, def: d}})
}

// implItemLink names one function of an impl block by the block's identity:
// its declaration, its receiver and the arguments of the generic interface
// it implements (`impl Holds<Int> for Box` and `impl Holds<String> for Box`
// are two blocks), then the member's name with, for an instance of a
// generic member, its own type arguments. No substitution frame is part of
// it: a block's type parameters are its receiver's arguments, and the
// interface's are its interface arguments.
func (g *gen) implItemLink(it *implItem) string {
	d := it.impl
	if d == nil {
		if it.decl != nil {
			return g.declLink(it.decl)
		}
		return ""
	}
	var base string
	switch {
	case d.decl != nil:
		base = g.declLink(d.decl)
		if base == "" {
			return ""
		}
	case d.ifaceName != "":
		base = "impl " + d.ifaceName + " for " + d.recv.nomi()
	default:
		return ""
	}
	var b strings.Builder
	b.WriteString(base)
	// The receiver and interface arguments: an instance of a generic block
	// is one block per receiver, all of one declaration.
	b.WriteString("[" + portableArgsKey([]kind{d.recv}))
	if len(d.instArgs) > 0 {
		b.WriteString("|" + portableArgsKey(d.instArgs))
	}
	b.WriteString("]")
	b.WriteString("." + it.name)
	if it.instKey != "" {
		b.WriteString("<" + it.instKey + ">")
	}
	return b.String()
}

// linkImport is the symbol a declaration lowered once for the process
// already has under link, or nil: what makes a call into a linked module
// name the linked module's function (programstdlink.go), or a std module
// lowered again inside its import cycle keep its symbols (stdUnitSyms), or
// a std interface's dispatch symbol name the one the shared stdlib's
// objects dispatch on (stdObjects.undefined).
func (g *gen) linkImport(link string) *ir.Symbol {
	if g.stdUnitSyms != nil {
		return g.stdUnitSyms[link]
	}
	if g.reg != nil {
		if s := g.reg.imports[link]; s != nil {
			return s
		}
	}
	if g.stdProgBuilds() || g.twins != nil {
		// A gen that records dispatch entries for a program records them
		// on the symbol the process's shared objects dispatch on, and a
		// std module checked from edited source, lowered beside those
		// objects, dispatches on it.
		return stdBundleState.objects.undefined(link)
	}
	return nil
}
