package hostgen

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/rt"
)

// ShapeKind is what a Nomi type is at the adapter boundary.
type ShapeKind int

const (
	SInt ShapeKind = iota + 1
	SFloat
	SBool
	SString
	SByte
	SBytes
	SUnit
	SList     // Elems[0]
	SOption   // Elems[0]
	SResult   // Elems[0] ok, Elems[1] err
	STuple    // Elems
	SStruct   // Name, Fields
	SDistinct // Name, Elems[0] inner or none for a marker
	SHost     // Name
	SFunc     // Elems params, Ret
	SMap      // Elems[0] key, Elems[1] value
	SEnum     // Name, Variants
)

// Shape is one Nomi type, resolved against its declarations.
type Shape struct {
	Kind   ShapeKind
	Name   string // qualified runtime identity: struct, distinct, host
	Elems  []*Shape
	Ret    *Shape
	Fields []ShapeField
	// Variants is an enum's variants in declaration order.
	Variants []ShapeVariant
	// Range is an Int's declared bounds, read off the refinement it is
	// declared as (IntRange); nil for a plain Int.
	Range *IntRange
}

// ShapeVariant is one enum variant: bare, one positional payload (Fields
// holds it, unnamed), or struct-shaped (Fields named, in declaration order).
type ShapeVariant struct {
	Name   string
	Shape  rt.VariantShape
	Fields []ShapeField
}

// ShapeField is one struct field in declaration order.
type ShapeField struct {
	Name  string
	Shape *Shape
}

// The runtime identities of the prelude types a boundary names: the
// module-qualified names the VM's records carry.
const (
	optionName   = "option.Option"
	resultName   = "results.Result"
	durationName = "duration.Duration"
	instantName  = "instant.Instant"
)

// String renders the shape the way a declaration spells it, for comments and
// refusals.
func (s *Shape) String() string {
	switch s.Kind {
	case SInt:
		return "Int"
	case SFloat:
		return "Float"
	case SBool:
		return "Bool"
	case SString:
		return "String"
	case SByte:
		return "Byte"
	case SBytes:
		return "Bytes"
	case SUnit:
		return "Unit"
	case SList:
		return "List<" + s.Elems[0].String() + ">"
	case SOption:
		return "Option<" + s.Elems[0].String() + ">"
	case SMap:
		return "Map<" + s.Elems[0].String() + ", " + s.Elems[1].String() + ">"
	case SResult:
		return "Result<" + s.Elems[0].String() + ", " + s.Elems[1].String() + ">"
	case STuple, SFunc:
		parts := make([]string, len(s.Elems))
		for i, e := range s.Elems {
			parts[i] = e.String()
		}
		out := "(" + strings.Join(parts, ", ") + ")"
		if s.Kind == SFunc {
			out += " -> " + s.Ret.String()
		}
		return out
	}
	return rt.ShortTypeName(s.Name)
}

// Slot is the record bank a value of this shape takes as a field.
func (s *Shape) Slot() rt.SlotType {
	switch s.Kind {
	case SInt:
		return rt.SlotInt
	case SFloat:
		return rt.SlotFloat
	case SBool:
		return rt.SlotBool
	case SByte:
		return rt.SlotByte
	case SString:
		return rt.SlotString
	case SBytes:
		return rt.SlotBytes
	}
	return rt.SlotRef
}

// Module is one parsed Nomi file: its module name (the qualifier its
// declarations carry at run time) and its top-level nodes.
type Module struct {
	Name  string
	Nodes []ast.Node
}

// Resolver turns a declared type expression into a Shape. Local resolves a
// name declared in the declaration's own module; Imported searches the
// modules the file imports from.
type Resolver struct {
	// Modules is every module a name may resolve in, by module name.
	Modules map[string]*Module
	cache   map[string]*Shape
}

// NewResolver indexes modules.
func NewResolver(mods ...*Module) *Resolver {
	r := &Resolver{Modules: map[string]*Module{}, cache: map[string]*Shape{}}
	for _, m := range mods {
		r.Modules[m.Name] = m
	}
	return r
}

// Shape resolves t as written in module mod.
func (r *Resolver) Shape(mod *Module, t ast.TypeExpr) (*Shape, error) {
	switch x := t.(type) {
	case nil:
		return &Shape{Kind: SUnit}, nil
	case *ast.SimpleType:
		return r.named(mod, x.Name, nil)
	case *ast.GenericType:
		return r.named(mod, x.Name, x.Params)
	case *ast.QualifiedType:
		target, ok := r.Modules[x.Module]
		if !ok {
			// `Json.DecodeError` is a dotted type name declared in this
			// module or one it imports, not a module qualifier.
			if member, isSimple := x.Member.(*ast.SimpleType); isSimple {
				dotted := x.Module + "." + member.Name
				if home, _ := r.lookup(mod, dotted); home != nil {
					return r.named(mod, dotted, nil)
				}
			}
			return nil, fmt.Errorf("type %s: module %q is not loaded", x.TypeString(), x.Module)
		}
		return r.Shape(target, x.Member)
	case *ast.ConjunctionType:
		// `NonNegativeInt and Int64`: an intersection of Int refinements is an
		// Int within every member's bounds.
		var rng *IntRange
		for _, m := range x.Members {
			ms, err := r.Shape(mod, m)
			if err != nil {
				return nil, err
			}
			if ms.Kind != SInt {
				return nil, fmt.Errorf("type %s: only an intersection of Int refinements crosses the host boundary", x.TypeString())
			}
			rng = rng.intersect(ms.Range)
		}
		if rng != nil {
			rng = &IntRange{Min: rng.Min, Max: rng.Max, Via: x.TypeString()}
		}
		return &Shape{Kind: SInt, Range: rng}, nil
	case *ast.FuncType:
		elems := make([]*Shape, len(x.Params))
		for i, p := range x.Params {
			s, err := r.Shape(mod, p)
			if err != nil {
				return nil, err
			}
			elems[i] = s
		}
		if x.Return == nil {
			return &Shape{Kind: STuple, Elems: elems}, nil
		}
		ret, err := r.Shape(mod, x.Return)
		if err != nil {
			return nil, err
		}
		return &Shape{Kind: SFunc, Elems: elems, Ret: ret}, nil
	}
	return nil, fmt.Errorf("type %s: no adapter boundary form", t.TypeString())
}

func (r *Resolver) named(mod *Module, name string, params []ast.TypeExpr) (*Shape, error) {
	arity := func(n int) error {
		if len(params) != n {
			return fmt.Errorf("type %s takes %d type argument(s), got %d", name, n, len(params))
		}
		return nil
	}
	args := func() ([]*Shape, error) {
		out := make([]*Shape, len(params))
		for i, p := range params {
			s, err := r.Shape(mod, p)
			if err != nil {
				return nil, err
			}
			out[i] = s
		}
		return out, nil
	}
	scalar := map[string]ShapeKind{
		"Int": SInt, "Float": SFloat, "Bool": SBool, "String": SString,
		"Byte": SByte, "Bytes": SBytes, "Unit": SUnit,
	}
	if k, ok := scalar[name]; ok {
		return &Shape{Kind: k}, arity(0)
	}
	switch name {
	case "List", "Option", "Result", "Map":
		kinds := map[string]ShapeKind{"List": SList, "Option": SOption, "Result": SResult, "Map": SMap}
		n := 1
		if name == "Result" || name == "Map" {
			n = 2
		}
		if err := arity(n); err != nil {
			return nil, err
		}
		elems, err := args()
		if err != nil {
			return nil, err
		}
		return &Shape{Kind: kinds[name], Elems: elems}, nil
	case "Vector", "Set":
		// No binding the generator serves uses these yet, and an adapter for
		// them is written when one does rather than guessed at now.
		return nil, fmt.Errorf("type %s: the adapter generator does not convert this type yet", name)
	}
	home, decl := r.lookup(mod, name)
	if _, isHost := decl.(*ast.ExternType); len(params) != 0 && !isHost {
		// A generic host type is one Go handle whatever its type arguments
		// (std/regex's `Regex<C>`, whose argument only the checker reads), so
		// it crosses as its handle; any other generic type has a layout per
		// instance, which no adapter builds.
		return nil, fmt.Errorf("type %s: a generic user type does not cross the host boundary", name)
	}
	if decl == nil {
		return nil, fmt.Errorf("type %s: no declaration in module %s or its imports", name, mod.Name)
	}
	qualified := home.Name + "." + name
	if s, ok := r.cache[qualified]; ok {
		return s, nil
	}
	switch d := decl.(type) {
	case *ast.StructDef:
		if len(d.TypeParams) != 0 {
			return nil, fmt.Errorf("type %s: a generic struct does not cross the host boundary", qualified)
		}
		s := &Shape{Kind: SStruct, Name: qualified}
		r.cache[qualified] = s
		for _, f := range d.Fields {
			fs, err := r.Shape(home, f.TypeAnnotation)
			if err != nil {
				delete(r.cache, qualified)
				return nil, fmt.Errorf("%s.%s: %w", qualified, f.Name, err)
			}
			s.Fields = append(s.Fields, ShapeField{Name: f.Name, Shape: fs})
		}
		return s, nil
	case *ast.TypeDef:
		if d.Refinement && d.InnerTypeExpr != nil {
			// A refinement is its parent's value at run time, and
			// so its base's (spec §4 *Refinements*): it crosses as that. An
			// Int refinement carries the bounds its chain states (IntRange).
			inner, err := r.Shape(home, d.InnerTypeExpr)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", qualified, err)
			}
			if inner.Kind == SInt {
				rng := inner.Range
				for _, p := range d.MoreParents {
					ps, err := r.Shape(home, p)
					if err != nil {
						return nil, fmt.Errorf("%s: %w", qualified, err)
					}
					rng = rng.intersect(ps.Range)
				}
				rng = rng.intersect(ruleRange(d.Rule))
				if rng != nil {
					rng = &IntRange{Min: rng.Min, Max: rng.Max, Via: name}
				}
				inner = &Shape{Kind: SInt, Range: rng}
			}
			r.cache[qualified] = inner
			return inner, nil
		}
		s := &Shape{Kind: SDistinct, Name: qualified}
		if d.InnerTypeExpr != nil {
			inner, err := r.Shape(home, d.InnerTypeExpr)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", qualified, err)
			}
			s.Elems = []*Shape{inner}
		}
		r.cache[qualified] = s
		return s, nil
	case *ast.ExternType:
		s := &Shape{Kind: SHost, Name: qualified}
		r.cache[qualified] = s
		return s, nil
	case *ast.EnumDef:
		// An enum resolves; whether it crosses is decided by the Go side. The
		// marshaller projects only Bool, Option and Result as variants, so an
		// enum crosses only where the Go type is rt's tagged-struct encoding
		// of it (rt.Json, rt.Ordering, rt.Restart, ...); emit.go refuses the
		// rest.
		if len(d.TypeParams) != 0 {
			return nil, fmt.Errorf("type %s: a generic enum does not cross the host boundary", qualified)
		}
		s := &Shape{Kind: SEnum, Name: qualified}
		r.cache[qualified] = s
		for _, v := range d.Variants {
			sv, err := r.variant(home, qualified, v)
			if err != nil {
				delete(r.cache, qualified)
				return nil, err
			}
			s.Variants = append(s.Variants, sv)
		}
		return s, nil
	}
	return nil, fmt.Errorf("type %s: unsupported declaration %T", qualified, decl)
}

// variant resolves one enum variant's payload in the enum's home module.
// The enum's descriptor needs every payload's bank, so a payload that does
// not resolve fails the whole enum.
func (r *Resolver) variant(home *Module, enum string, v ast.EnumVariant) (ShapeVariant, error) {
	sv := ShapeVariant{Name: v.Name}
	switch v.Kind {
	case "bare":
		sv.Shape = rt.VariantBare
	case "positional":
		sv.Shape = rt.VariantPositional
		ps, err := r.Shape(home, v.DataTypeExpr)
		if err != nil {
			return sv, fmt.Errorf("%s.%s: %w", enum, v.Name, err)
		}
		sv.Fields = []ShapeField{{Shape: ps}}
	case "struct":
		sv.Shape = rt.VariantFields
		for _, f := range v.Fields {
			fs, err := r.Shape(home, f.TypeAnnotation)
			if err != nil {
				return sv, fmt.Errorf("%s.%s.%s: %w", enum, v.Name, f.Name, err)
			}
			sv.Fields = append(sv.Fields, ShapeField{Name: f.Name, Shape: fs})
		}
	default:
		return sv, fmt.Errorf("type %s: variant %s is %s, which does not cross the host boundary", enum, v.Name, v.Kind)
	}
	return sv, nil
}

// lookup finds the declaration of a type name: the module's own first, then
// the modules its imports name. A std file imports every type it names.
func (r *Resolver) lookup(mod *Module, name string) (*Module, ast.Node) {
	if d := typeDecl(mod.Nodes, name); d != nil {
		return mod, d
	}
	for _, imported := range importedModules(mod.Nodes) {
		m, ok := r.Modules[imported]
		if !ok {
			continue
		}
		if d := typeDecl(m.Nodes, name); d != nil {
			return m, d
		}
	}
	return nil, nil
}

func typeDecl(nodes []ast.Node, name string) ast.Node {
	for _, n := range nodes {
		switch d := n.(type) {
		case *ast.StructDef:
			if d.Name == name {
				return d
			}
		case *ast.EnumDef:
			if d.Name == name {
				return d
			}
		case *ast.TypeDef:
			if d.Name == name {
				return d
			}
		case *ast.ExternType:
			if d.Name == name {
				return d
			}
		}
	}
	return nil
}

// importedModules is the first path segment of every import entry, which is
// the module a stdlib file names its siblings by (`duration.Duration`).
func importedModules(nodes []ast.Node) []string {
	var out []string
	add := func(s *ast.ImportStmt) {
		if s == nil || len(s.ModulePath) == 0 {
			return
		}
		name := identName(s.ModulePath[0])
		if name == "std" && len(s.ModulePath) > 1 {
			name = identName(s.ModulePath[1])
		}
		if name != "" {
			out = append(out, name)
		}
	}
	for _, n := range nodes {
		switch d := n.(type) {
		case *ast.ImportStmt:
			add(d)
		case *ast.ImportBlock:
			for _, e := range d.Entries {
				add(e)
			}
		}
	}
	return out
}

func identName(n ast.Node) string {
	switch x := n.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.TypeIdent:
		return x.Name
	}
	return ""
}

// HostFunc is one `host fn` declaration with the key candidates a binding
// table may name it by.
type HostFunc struct {
	Module   *Module
	Receiver string
	Decl     *ast.ExternFunc
	Keys     []string
}

// HostFuncs enumerates a module's `host fn` declarations, top level and in
// `impl` blocks.
func HostFuncs(mod *Module) []HostFunc {
	var out []HostFunc
	var walk func(receiver, iface string, nodes []ast.Node)
	walk = func(receiver, iface string, nodes []ast.Node) {
		for _, n := range nodes {
			switch d := n.(type) {
			case *ast.ImplBlock:
				recv := baseName(d.Receiver)
				if recv == "" {
					continue
				}
				inst := ""
				if g, ok := d.Interface.(*ast.GenericType); ok {
					inst = g.TypeString()
				}
				walk(recv, inst, d.Items)
			case *ast.ExternFunc:
				out = append(out, HostFunc{Module: mod, Receiver: receiver, Decl: d, Keys: keys(mod.Name, receiver, iface, d.Name)})
			}
		}
	}
	walk("", "", mod.Nodes)
	return out
}

// keys is hostpair.Pairing.Keys: every spelling a binding table may register
// the declaration under, most specific first.
func keys(module, receiver, iface, name string) []string {
	if receiver == "" {
		return []string{module + "." + name}
	}
	var bare []string
	if iface != "" {
		bare = append(bare, receiver+"."+iface+"."+name)
	}
	bare = append(bare, receiver+"."+name)
	out := make([]string, 0, 2*len(bare))
	for _, k := range bare {
		out = append(out, module+"."+k)
	}
	return append(out, bare...)
}

func baseName(t ast.TypeExpr) string {
	switch x := t.(type) {
	case *ast.SimpleType:
		return x.Name
	case *ast.GenericType:
		return x.Name
	case *ast.QualifiedType:
		return baseName(x.Member)
	}
	return ""
}
