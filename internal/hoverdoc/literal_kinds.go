package hoverdoc

import (
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// The `[` and `{` of an unnamed collection literal say which collection the
// checker made it and why. The brackets of `[1, 2]` never say whether it is
// a List, a Vector or a Set: the checker decides from the type the position
// expects, a List where nothing expects one (ast.ListLit's Kind), and
// records the expected type (FileAnalysis.ExpectedTypes). The hover names
// what expected it: the parameter the literal fills, the annotated binding
// or field it initializes, the result of the function it ends, or else the
// expected type alone.

// literalSource is what expected an unnamed literal's type, as the walk
// over the file found it.
type literalSource struct {
	arg  *lambdaArg // the call argument the literal is
	text string     // otherwise the words naming the source
}

// noteLiteral records where lit, when it is an unnamed collection literal,
// stands.
func (s *shapeSearch) noteLiteral(lit ast.Node, src literalSource) {
	switch l := lit.(type) {
	case *ast.ListLit:
		if l.TypeName != nil {
			return
		}
	case *ast.MapLit:
		if l.TypeName != nil {
			return
		}
	default:
		return
	}
	if s.literalSources == nil {
		s.literalSources = map[ast.Node]literalSource{}
	}
	s.literalSources[lit] = src
}

// noteLiteralSites records the literals n holds that its own syntax gives
// an expected type: an annotated binding's value, a struct literal's field
// values, a function's final expression.
func (s *shapeSearch) noteLiteralSites(n ast.Node) {
	switch n := n.(type) {
	case *ast.Binding:
		if n.TypeAnnotation != nil {
			s.noteLiteral(n.Value, literalSource{text: "the annotation `" + n.Name + ": " + n.TypeAnnotation.TypeString() + "`"})
		}
	case *ast.StructLit:
		for _, f := range n.Fields {
			s.noteLiteral(f.Value, literalSource{text: "the field `" + f.Name + "`"})
		}
	case *ast.FuncDef:
		if n.ReturnTypeExpr != nil {
			s.noteLiteral(blockResult(n.Body), literalSource{text: "the result type of `" + n.Name + "`"})
		}
	}
}

// literalKind is the line under an unnamed literal's type: the type, and
// what expected it.
func (s *shapeSearch) literalKind(n ast.Node, ty analysis.Type) string {
	what := aType(displayValueType(ty))
	what = strings.ToUpper(what[:1]) + what[1:]
	want := s.fa.ExpectedTypes[n]
	if src, ok := s.literalSources[n]; ok {
		if src.arg != nil {
			if from := s.literalParam(*src.arg); from != "" {
				return what + ", from " + from
			}
		} else if src.text != "" && want != nil {
			return what + ", from " + src.text
		}
	}
	if want == nil {
		return what + " (no expected type)"
	}
	return what + ", from the expected type `" + displayValueType(want) + "`"
}

// literalParam names the parameter a literal argument fills: "the
// parameter `tags: Set<String>` of `tag_all`".
func (s *shapeSearch) literalParam(arg lambdaArg) string {
	callee := calleeText(arg.call.Func)
	if callee == "" {
		return ""
	}
	if p, ok := declaredParam(s.calleeSymbol(arg.call.Func), arg.slot); ok {
		typeText := ""
		if p.TypeAnnotation != nil {
			typeText = p.TypeAnnotation.TypeString()
		}
		return "the parameter `" + ParamSignature(p, typeText) + "` of `" + callee + "`"
	}
	return "argument " + strconv.Itoa(arg.slot+1) + " of `" + callee + "`"
}

// listLiteralRule is the rule an unnamed bracket literal's kind follows.
const listLiteralRule = "An unnamed `[…]` builds the List, Vector or Set its position expects, and a List where nothing expects one."
