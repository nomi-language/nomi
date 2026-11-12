package analysis

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/parser"
)

// The checker's half of the errors for source written as another language
// writes it (the parser's half is internal/parser/confusion.go): a method
// call `xs.len()`, a bare call of an interface function `to_string(n)`,
// `nil`, and a list literal where a vector or a set is expected. Each adds
// a hint and a Fix to an error the checker reports anyway.

// withFix is e with fix as its Fix.
func (e TypeError) withFix(fix *parser.Fix) TypeError {
	e.Fix = fix
	return e
}

// nameFix is the Fix replacing the name n with text.
func nameFix(title string, n *ast.Ident, text string) *parser.Fix {
	return parser.Replace(title, n.Line, n.Col, n.Line, n.Col+len(n.Name), text)
}

// nullNames are the names other languages give the absent value.
var nullNames = map[string]bool{"nil": true, "null": true, "NULL": true, "undefined": true, "none": true, "Nothing": true}

// undefinedVariableConfusion is the hint and fix for an undefined name
// another language gives a literal: `true`, `false`, `nil`, `null`. ok is
// false for any other name.
func undefinedVariableConfusion(e TypeError, n *ast.Ident) (TypeError, bool) {
	switch {
	case n.Name == "true" || n.Name == "false":
		lit := strings.ToUpper(n.Name[:1]) + n.Name[1:]
		return e.WithHint("the Bool literal is '" + lit + "'").withFix(nameFix("Write `"+lit+"`", n, lit)), true
	case nullNames[n.Name]:
		return e.WithHint("Nomi has no null: a value that may be absent is a `Maybe<T>`, `None` or `Some(value)`").
			withFix(nameFix("Write `None`", n, "None")), true
	}
	return e, false
}

// methodAliases are the names other languages give a function Nomi names
// otherwise, tried after the name as written and its snake_case form.
var methodAliases = map[string][]string{
	"len":           {"length", "count"},
	"size":          {"length", "count"},
	"length":        {"length", "count"},
	"count":         {"count", "length"},
	"push":          {"append"},
	"add":           {"append", "insert"},
	"str":           {"to_string"},
	"tostring":      {"to_string"},
	"to_s":          {"to_string"},
	"string":        {"to_string"},
	"includes":      {"contains"},
	"has":           {"contains", "has_key"},
	"for_each":      {"each"},
	"foreach":       {"each"},
	"upper":         {"uppercase"},
	"lower":         {"lowercase"},
	"to_upper":      {"uppercase"},
	"to_lower":      {"lowercase"},
	"to_upper_case": {"uppercase"},
	"to_lower_case": {"lowercase"},
	"strip":         {"trim"},
	"is_empty":      {"is_empty"},
	"empty":         {"is_empty"},
	"first":         {"head", "first"},
	"keys":          {"keys"},
	"values":        {"values"},
	"get":           {"get"},
	"join":          {"join"},
	"split":         {"split"},
	"reverse":       {"reverse"},
	"sort":          {"sort"},
	"unwrap":        {"unwrap"},
	"to_list":       {"to_list"},
	"collect":       {"to_list"},
	"iter":          {"to_list"},
}

// snakeCase is name in snake_case: `toUpperCase` is `to_upper_case`.
func snakeCase(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ownerOfValueType is the name a call on a value of type t is qualified
// by: `List` for a List<Int>, `String`, `Point` for a struct. "" when t
// names no owner.
func ownerOfValueType(t Type) string {
	switch rt := resolveTypeVar(t).(type) {
	case *ListType:
		return "List"
	case *MapType:
		return "Map"
	case *PrimitiveType:
		return rt.String()
	case *StructType:
		return rt.Name
	case *EnumType:
		return rt.Name
	case *DistinctType:
		return rt.Name
	}
	s := typeHeadName(t)
	return s
}

// typeHeadName is the name before t's type arguments: `Vector` for
// Vector<Int>.
func typeHeadName(t Type) string {
	if t == nil {
		return ""
	}
	s := t.String()
	if i := strings.IndexByte(s, '<'); i >= 0 {
		s = s[:i]
	}
	if s == "" || !unicode.IsUpper(rune(s[0])) || strings.ContainsAny(s, " ().{}[]") {
		return ""
	}
	return s
}

// methodCallOwner is the qualified function a method-style call `x.name(...)`
// on a value of type t means: the owner and the function's name, as
// `List`, `length`. It looks for name, its snake_case form and the names
// methodAliases gives it, on t's owner, then on the interfaces t's owner
// implements, then on Iter for a collection. ok is false when none is found.
func (c *checker) methodCallOwner(t Type, name string) (owner, fn string, ok bool) {
	head := ownerOfValueType(t)
	if head == "" {
		return "", "", false
	}
	cands := []string{name}
	if s := snakeCase(name); s != name {
		cands = append(cands, s)
	}
	cands = append(cands, methodAliases[strings.ToLower(snakeCase(name))]...)
	owners := []string{head}
	switch head {
	case "List", "Vector", "Set", "Map", "Range":
		owners = append(owners, "Iter")
	}
	for _, cand := range cands {
		for _, o := range owners {
			if c.reg.Lookup(o) == nil && o != head {
				continue
			}
			if sym, _ := c.resolveTypeMethodSymbol(o, cand, ""); sym != nil {
				return o, cand, true
			}
			if provs := c.interfacesWritingMethod(o, cand); len(provs) == 1 {
				return provs[0], cand, true
			}
			if o == "Iter" || len(c.interfacesWritingMethod(o, cand)) > 1 {
				if names := c.typeMemberNames(o); containsString(names, cand) {
					return o, cand, true
				}
			}
		}
	}
	return "", "", false
}

// methodCallConfusion is the hint and fix for `recv.name(args)`, a method
// call, which Nomi writes `Owner.name(recv, args)`. call is the call whose
// callee fa is; t is the receiver's type. ok is false when the owner is
// unknown.
func (c *checker) methodCallConfusion(e TypeError, call *ast.Call, fa *ast.FieldAccess, t Type) (TypeError, bool) {
	owner, fn, ok := c.methodCallOwner(t, fa.Field.Name)
	if !ok {
		head := ownerOfValueType(t)
		if head == "" {
			return e, false
		}
		hint := fmt.Sprintf("Nomi has no method calls: a function on a %s is called through its owner, `%s.name(value, ...)`", head, head)
		if dym := didYouMean(fa.Field.Name, c.typeMemberNames(head)); dym != "" {
			hint += "; " + dym
		}
		return e.WithHint(hint), true
	}
	recv := unparen(fa.Object)
	sp, ok := spanOf(fa.Object)
	if !ok {
		return e, false
	}
	text := calleeText(recv)
	if text == "?" || strings.Contains(text, "?") {
		text = "value"
	}
	hint := fmt.Sprintf("Nomi has no method calls: write `%s.%s(%s, ...)`, with the value first", owner, fn, text)
	if len(call.Args) == 0 {
		hint = fmt.Sprintf("Nomi has no method calls: write `%s.%s(%s)`", owner, fn, text)
	}
	// `recv.name(` becomes `Owner.fn(recv` and `, ` or nothing: the
	// call's own `)` closes it.
	lparenLine, lparenCol := fa.Field.Line, fa.Field.Col+len(fa.Field.Name)
	sep := ""
	if len(call.Args) > 0 {
		sep = ", "
	}
	fix := &parser.Fix{Title: fmt.Sprintf("Call `%s.%s`", owner, fn), Edits: []parser.Edit{
		{Start: parser.Pos{Line: sp.StartLine, Col: sp.StartCol}, End: parser.Pos{Line: sp.StartLine, Col: sp.StartCol}, Text: owner + "." + fn + "("},
		{Start: parser.Pos{Line: sp.EndLine, Col: sp.EndCol}, End: parser.Pos{Line: lparenLine, Col: lparenCol + 1}, Text: sep},
	}}
	return e.WithHint(hint).withFix(fix), true
}

// bareDispatchConfusion is the hint and fix for `name(args)` where name is
// a function of an interface and of no file in scope: Nomi calls it through
// the interface, `Display.to_string(n)`. ok is false when no interface in
// scope declares it.
func (c *checker) bareDispatchConfusion(e TypeError, n *ast.Ident) (TypeError, bool) {
	var ifaces []string
	for _, name := range c.reg.Names() {
		it, ok := c.reg.Lookup(name).(*InterfaceType)
		if !ok || it == nil || it.Name != name {
			continue
		}
		if interfaceMethodFuncType(it, n.Name) != nil {
			ifaces = append(ifaces, name)
		}
	}
	if len(ifaces) == 0 {
		return e, false
	}
	sort.Strings(ifaces)
	if len(ifaces) > 1 {
		return e.WithHint(fmt.Sprintf("Nomi calls an interface's function through the interface or a type: %s",
			strings.Join(qualifiedSpellings(ifaces, n.Name), " or "))), true
	}
	return e.WithHint(fmt.Sprintf("Nomi calls an interface's function through the interface or a type: `%s.%s(...)`", ifaces[0], n.Name)).
		withFix(parser.Replace("Call `"+ifaces[0]+"."+n.Name+"`", n.Line, n.Col, n.Line, n.Col, ifaces[0]+".")), true
}

func qualifiedSpellings(owners []string, fn string) []string {
	out := make([]string, len(owners))
	for i, o := range owners {
		out[i] = "`" + o + "." + fn + "(...)`"
	}
	return out
}

// methodCallError is the error msg at fa's field, with the method-call hint
// and fix when fa is called (`xs.len()`).
func (c *checker) methodCallError(fa *ast.FieldAccess, objTy Type, msg string) TypeError {
	e := TypeError{Line: fa.Field.Line, Col: fa.Field.Col, Message: msg}
	if call := c.methodCalls[fa]; call != nil {
		if ce, ok := c.methodCallConfusion(e, call, fa, objTy); ok {
			return ce
		}
	}
	return e
}
