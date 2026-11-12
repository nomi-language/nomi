package hoverdoc

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A punned `name:` is one token that is two things: the label of a field
// or parameter, and a binding. In a struct literal or update (`Birth{on:}`,
// `{..b, on:}`) and a named argument (`json(status:)`) it reads the binding
// `name`; in a struct pattern (`{on:} = birth`) it binds a new `name` to the
// field. The checker records the label in FileAnalysis.PunnedFieldLabels
// and the binding in References or Definitions at the same position. Hover
// shows both, go-to-definition offers both declarations, and rename of
// either unpuns the token (internal/lsp rename.go).

// PunKind is where a punned label stands.
type PunKind int

const (
	PunLiteral PunKind = iota
	PunUpdate
	PunPattern
	PunArgument
)

// Pun is a punned `name:` and the two things it is.
type Pun struct {
	Kind PunKind
	// Pos is the name's position; the token is the name, its `:` after it.
	Pos  analysis.Pos
	Name string
	// Owner is the struct, variant or callee the label belongs to, as
	// code: "`Birth`", "`Shape.Circle`", "`Response.json`", or words for
	// an anonymous struct.
	Owner string
	// Field is the field the label names (nil for an argument); Callee and
	// Param are the function and the parameter an argument's label names
	// (HasParam false when its declaration is not known).
	Field    *analysis.Symbol
	Callee   *analysis.Symbol
	Param    ast.Param
	HasParam bool
	// Binding is the binding the token reads, or for a pattern the one it
	// binds.
	Binding *analysis.Symbol
}

// PunAt is the punned label at pos, if one stands there.
func PunAt(fa *analysis.FileAnalysis, nodes []ast.Node, pos analysis.Pos) (Pun, bool) {
	if fa == nil || len(fa.PunnedFieldLabels) == 0 {
		return Pun{}, false
	}
	var found Pun
	ok := false
	on := func(line, col int, name string) bool {
		return line == pos.Line && pos.Col >= col && pos.Col < col+len(name)
	}
	s := shapeSearch{fa: fa, nodes: nodes}
	for _, n := range nodes {
		ast.Inspect(n, func(c ast.Node) bool {
			if ok || c == nil {
				return !ok
			}
			switch c := c.(type) {
			case *ast.StructLit:
				for _, f := range c.Fields {
					if analysis.IsPunnedField(f) && on(f.Line, f.Col, f.Name) {
						kind := PunLiteral
						if c.Spread != nil {
							kind = PunUpdate
						}
						found, ok = Pun{Kind: kind, Pos: analysis.Pos{Line: f.Line, Col: f.Col}, Name: f.Name, Owner: s.structOwner(c, c.TypeName)}, true
					}
				}
			case *ast.StructPattern:
				for _, f := range c.Fields {
					punning := f.Pattern == nil && f.Binding == f.Name && f.NameLine > 0 && f.NameLine == f.BindingLine && f.NameCol == f.BindingCol
					if punning && on(f.NameLine, f.NameCol, f.Name) {
						found, ok = Pun{Kind: PunPattern, Pos: analysis.Pos{Line: f.NameLine, Col: f.NameCol}, Name: f.Name, Owner: s.patternOwner(c)}, true
					}
				}
			case *ast.Call:
				for _, a := range c.Args {
					na, named := a.(*ast.NamedArg)
					if !named {
						continue
					}
					id, isIdent := na.Value.(*ast.Ident)
					if !isIdent || id.Line != na.Line || id.Col != na.Col || !on(na.Line, na.Col, na.Name) {
						continue
					}
					p := Pun{Kind: PunArgument, Pos: analysis.Pos{Line: na.Line, Col: na.Col}, Name: na.Name, Callee: s.calleeSymbol(c.Func)}
					if callee := calleeText(c.Func); callee != "" {
						p.Owner = "`" + callee + "`"
					}
					for i, name := range paramNamesOf(p.Callee) {
						if name == na.Name {
							p.Param, p.HasParam = declaredParam(p.Callee, i)
						}
					}
					found, ok = p, true
				}
			}
			return !ok
		})
	}
	if !ok {
		return Pun{}, false
	}
	label := fa.PunnedFieldLabels[found.Pos]
	if label == nil {
		return Pun{}, false
	}
	if found.Kind != PunArgument {
		found.Field = label
	}
	if found.Kind == PunPattern {
		found.Binding = fa.Definitions[found.Pos]
	} else {
		found.Binding = fa.References[found.Pos]
	}
	if found.Binding == nil {
		return Pun{}, false
	}
	return found, true
}

// structOwner names the struct or variant a struct literal builds.
func (s *shapeSearch) structOwner(n ast.Node, name ast.TypeExpr) string {
	if name != nil {
		if sym := s.nameSymbol(name); sym != nil && sym.Kind == analysis.SymbolEnumVariant {
			return "`" + variantName(sym) + "`"
		}
		return "`" + name.TypeString() + "`"
	}
	if lit, isLit := n.(*ast.StructLit); isLit {
		if st := s.fa.TargetStructs[lit]; st != nil {
			return "`" + displayValueType(st) + "`"
		}
	}
	return s.typeOwner(s.fa.ExprTypes[n])
}

// patternOwner names the struct or variant a struct pattern matches.
func (s *shapeSearch) patternOwner(p *ast.StructPattern) string {
	if p.TypeName != nil {
		return s.structOwner(p, p.TypeName)
	}
	return s.typeOwner(s.fa.PatternTypes[p])
}

// typeOwner names a struct type, or says it is anonymous.
func (s *shapeSearch) typeOwner(t analysis.Type) string {
	if st, ok := analysis.ResolveTypeVar(t).(*analysis.StructType); ok && st.Name != "" {
		return "`" + displayValueType(st) + "`"
	}
	return "an anonymous struct"
}

// Render is the pun's hover: its type, then the label and the binding.
func (p Pun) Render() string {
	ty := p.Binding.Type
	if ty == nil && p.Field != nil {
		ty = p.Field.Type
	}
	head := p.Name
	if t := displayValueType(ty); t != "" {
		head += ": " + t
	}
	label := "field `" + p.Name + "`"
	if p.Kind == PunArgument {
		label = "parameter `" + p.Name + "`"
	}
	if p.Owner != "" {
		label += " of " + p.Owner
	}
	binding := bindingWord(p.Binding) + " `" + p.Name + "`"
	if p.Binding.Resolved == nil && p.Binding.Pos.Line > 0 && p.Kind != PunPattern {
		binding += " (line " + strconv.Itoa(p.Binding.Pos.Line) + ")"
	}
	var text string
	switch p.Kind {
	case PunLiteral:
		text = label + ", filled from the " + binding
	case PunUpdate:
		text = label + ", replaced by the " + binding
	case PunArgument:
		text = label + ", from the " + binding
	case PunPattern:
		text = label + ", bound to a new binding `" + p.Name + "`"
	}
	return fence(head) + "\n\n" + text + ". `" + p.Name + ":` is short for `" + p.Name + ": " + p.Name + "`."
}

// bindingWord says what kind of name a pun's binding is.
func bindingWord(sym *analysis.Symbol) string {
	switch sym.Kind {
	case analysis.SymbolParam:
		return "parameter"
	case analysis.SymbolOnce:
		return "`once` binding"
	}
	return "binding"
}
