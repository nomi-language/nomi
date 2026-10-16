package analysis

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/rt"
)

// checkRepeatedKeys reports a key given twice in one map literal or map
// pattern, and an element given twice in one set literal, when the two are
// the same constant: `{1 => "a", 1 => "b"}`, `#{"x", "x"}`,
// `{"k" => a, "k" => b} = m`. At run time the later map entry replaces the
// earlier one and a set holds the element once, so the earlier one is dead
// code; a pattern would match one value twice. Keys whose equality is only
// known at run time (`{x => 1, y => 2}`) are left alone.
//
// A constant is a value whose equality to another constant is certain at
// compile time (constKey). The check runs after the walk, since a variant's
// enum and a literal's type come from the checker.
func (c *checker) checkRepeatedKeys(nodes []ast.Node) {
	seen := map[ast.Node]bool{}
	check := func(keys []ast.Node, message func(display string) string) {
		first := map[string]ast.Node{}
		for _, k := range keys {
			if k == nil {
				continue
			}
			canon, display, ok := c.constKey(k)
			if !ok {
				continue
			}
			if prev, dup := first[canon]; dup {
				e := errAt(k, message(display))
				if r, ok := relatedAt(prev, "first given here"); ok {
					e = e.WithRelated(r)
				}
				c.report(e)
				continue
			}
			first[canon] = k
		}
	}
	mapLit := func(display string) string {
		return fmt.Sprintf("key `%s` is given twice in this map literal; the later entry would replace the earlier", display)
	}
	mapPat := func(display string) string {
		return fmt.Sprintf("key `%s` is given twice in this map pattern; match its value once", display)
	}
	setLit := func(display string) string {
		return fmt.Sprintf("element `%s` is given twice in this set literal; a set holds it once", display)
	}
	patternKeys := func(entries []ast.MapPatternEntry) []ast.Node {
		keys := make([]ast.Node, len(entries))
		for i, e := range entries {
			keys[i] = e.Key
		}
		return keys
	}
	for _, root := range nodes {
		ast.Inspect(root, func(n ast.Node) bool {
			if seen[n] {
				return false
			}
			seen[n] = true
			switch v := n.(type) {
			case *ast.MapLit:
				keys := make([]ast.Node, len(v.Entries))
				for i, e := range v.Entries {
					keys[i] = e.Key
				}
				check(keys, mapLit)
			case *ast.SetLit:
				check(v.Items, setLit)
			case *ast.MapPattern:
				check(patternKeys(v.Entries), mapPat)
			}
			return true
		})
	}
}

// constKey is a canonical spelling of n when n is a constant, one equal to
// another constant exactly when the spellings are equal, and n as an error
// message shows it. The constants are:
//
//   - Int, Float, Decimal, String and Codepoint literals, and a negated Int,
//     Float or Decimal literal, by value: `0xFF` is `255`, `1.0` is `1.00`,
//     `-0.0` is `0.0` (keys match by `==`), and `1.0d` is `1.00d`. A
//     literal counts only when the checker typed it as that primitive.
//   - `True` and `False`.
//   - a variant with no payload (`None`, `Direction.North`,
//     `.North`), and a variant constructor applied to constants
//     (`Some(1)`, `.Move(1, 2)`), of an enum with no `impl Equatable` of
//     its own or a std enum. A hand-written `equal?` decides equality for
//     the others, and nothing about it is known here.
//   - a tuple of constants.
func (c *checker) constKey(n ast.Node) (canon, display string, ok bool) {
	n = ungroupExpr(n)
	switch v := n.(type) {
	case *ast.IntLit:
		if !c.typedAs(v, TypeInt) {
			return "", "", false
		}
		return "i:" + strconv.FormatInt(v.Value, 10), literalText(v.Lexeme, strconv.FormatInt(v.Value, 10)), true
	case *ast.FloatLit:
		if !c.typedAs(v, TypeFloat) {
			return "", "", false
		}
		return floatCanon(v.Value), literalText(v.Lexeme, strconv.FormatFloat(v.Value, 'g', -1, 64)), true
	case *ast.DecimalLit:
		if !c.typedAs(v, TypeDecimal) {
			return "", "", false
		}
		d, err := rt.ParseDecimalLexeme(v.Lexeme)
		if err != nil {
			return "", "", false
		}
		return decimalCanon(d), v.Lexeme, true
	case *ast.StringLit:
		if !c.typedAs(v, TypeString) {
			return "", "", false
		}
		return "s:" + v.Value, stringText(v.Value), true
	case *ast.CodepointLit:
		if !c.typedAs(v, TypeCodepoint) {
			return "", "", false
		}
		return "c:" + strconv.Itoa(int(v.Value)), "'" + v.Lexeme + "'", true
	case *ast.Unary:
		if v.Op != "-" {
			return "", "", false
		}
		inner := ungroupExpr(v.Right)
		switch lit := inner.(type) {
		case *ast.IntLit:
			if !c.typedAs(lit, TypeInt) || !c.typedAs(v, TypeInt) {
				return "", "", false
			}
			return "i:" + strconv.FormatInt(-lit.Value, 10), "-" + literalText(lit.Lexeme, strconv.FormatInt(lit.Value, 10)), true
		case *ast.FloatLit:
			if !c.typedAs(lit, TypeFloat) || !c.typedAs(v, TypeFloat) {
				return "", "", false
			}
			return floatCanon(-lit.Value), "-" + literalText(lit.Lexeme, strconv.FormatFloat(lit.Value, 'g', -1, 64)), true
		case *ast.DecimalLit:
			if !c.typedAs(lit, TypeDecimal) || !c.typedAs(v, TypeDecimal) {
				return "", "", false
			}
			d, err := rt.ParseDecimalLexeme(lit.Lexeme)
			if err != nil {
				return "", "", false
			}
			return decimalCanon(d.Negate()), "-" + lit.Lexeme, true
		}
		return "", "", false
	case *ast.TupleLit:
		canons := make([]string, len(v.Items))
		displays := make([]string, len(v.Items))
		for i, item := range v.Items {
			ic, id, ok := c.constKey(item)
			if !ok {
				return "", "", false
			}
			canons[i], displays[i] = ic, id
		}
		return "t(" + strings.Join(canons, ",") + ")", "(" + strings.Join(displays, ", ") + ")", true
	case *ast.TypeIdent, *ast.FieldAccess, *ast.DotVariant:
		// `True` and `False` are std's host types that Bool embeds.
		if id, isIdent := v.(*ast.TypeIdent); isIdent && c.namesBoolSingleton(id) {
			return "b:" + id.Name, id.Name, true
		}
		et, name, spelled, ok := c.variantRef(v)
		if !ok || !variantHasNoPayload(et, name) {
			return "", "", false
		}
		return "v:" + enumKey(et) + "." + name, spelled, true
	case *ast.Call:
		if len(v.TypeArgs) > 0 || len(v.Args) == 0 {
			return "", "", false
		}
		et, name, spelled, ok := c.variantRef(ungroupExpr(v.Func))
		if !ok {
			return "", "", false
		}
		if callTy, isEnum := resolveTypeVar(c.fa.ExprTypes[v]).(*EnumType); !isEnum || enumKey(callTy) != enumKey(et) {
			return "", "", false
		}
		canons := make([]string, len(v.Args))
		displays := make([]string, len(v.Args))
		for i, arg := range v.Args {
			ac, ad, ok := c.constKey(arg)
			if !ok {
				return "", "", false
			}
			canons[i], displays[i] = ac, ad
		}
		return "v:" + enumKey(et) + "." + name + "(" + strings.Join(canons, ",") + ")",
			spelled + "(" + strings.Join(displays, ", ") + ")", true
	}
	return "", "", false
}

// variantRef reports whether n names an enum variant: a bare name that
// resolves to one (`None`), a type-qualified one (`Direction.North`), or a
// dot-leading one the checker resolved (`.North`). It returns the enum the
// checker gave the expression (or, for a constructor, its result), the
// variant's name and n as written. The enum must have no `impl Equatable`
// of its own unless std declares it.
func (c *checker) variantRef(n ast.Node) (*EnumType, string, string, bool) {
	var name, spelled string
	switch v := n.(type) {
	case *ast.TypeIdent:
		sym, ok := c.fa.References[Pos{Line: v.Line, Col: v.Col}]
		if !ok || typedResolvedSymbol(sym).Kind != SymbolEnumVariant {
			return nil, "", "", false
		}
		name, spelled = v.Name, v.Name
	case *ast.FieldAccess:
		qual, ok := typeQualifier(v.Object)
		if !ok || v.Field == nil {
			return nil, "", "", false
		}
		name, spelled = v.Field.Name, qual+"."+v.Field.Name
	case *ast.DotVariant:
		if v.ResolvedEnum == "" {
			return nil, "", "", false
		}
		name, spelled = v.Name, "."+v.Name
	default:
		return nil, "", "", false
	}
	et := variantEnum(c.fa.ExprTypes[n])
	if et == nil || !enumHasVariant(et, name) {
		return nil, "", "", false
	}
	if !IsStdlibKey(et.Origin) && c.hasEquatableImpl(et) {
		return nil, "", "", false
	}
	return et, name, spelled, true
}

// namesBoolSingleton reports whether id names std's `True` or `False`: the
// Bool variant, or the host type of that name that Bool embeds.
func (c *checker) namesBoolSingleton(id *ast.TypeIdent) bool {
	if id.Name != "True" && id.Name != "False" {
		return false
	}
	sym, ok := c.fa.References[Pos{Line: id.Line, Col: id.Col}]
	if !ok {
		return false
	}
	real := typedResolvedSymbol(sym)
	if real.Type == TypeTrue || real.Type == TypeFalse {
		return true
	}
	et, isEnum := real.Type.(*EnumType)
	return isEnum && real.Kind == SymbolEnumVariant && et.Name == "Bool" && IsStdlibKey(et.Origin)
}

// variantEnum is the enum a variant expression of type t builds: t itself
// for a unit variant, the result for a constructor.
func variantEnum(t Type) *EnumType {
	switch t := resolveTypeVar(t).(type) {
	case *EnumType:
		return t
	case *FuncType:
		et, _ := resolveTypeVar(t.Return).(*EnumType)
		return et
	}
	return nil
}

// enumKey names an enum by its identity, (declaring file, name). Two keys of
// one collection share a type, so type arguments add nothing.
func enumKey(et *EnumType) string {
	return et.Origin + ":" + et.Name
}

func variantHasNoPayload(et *EnumType, name string) bool {
	for _, v := range et.Variants {
		if v.Name == name {
			return v.DataType == nil
		}
	}
	return false
}

// typeQualifier is the spelling of a type-name qualifier (`Direction`,
// `geo.Direction`): a chain whose last segment is capitalized, so it names a
// type and not a value, whose names are snake_case.
func typeQualifier(n ast.Node) (string, bool) {
	switch v := n.(type) {
	case *ast.TypeIdent:
		return v.Name, true
	case *ast.FieldAccess:
		if v.Field == nil || v.Field.Name == "" || !unicode.IsUpper(rune(v.Field.Name[0])) {
			return "", false
		}
		var prefix string
		switch o := v.Object.(type) {
		case *ast.Ident:
			prefix = o.Name
		case *ast.TypeIdent:
			prefix = o.Name
		default:
			return "", false
		}
		return prefix + "." + v.Field.Name, true
	}
	return "", false
}

// typedAs reports whether the checker gave n the primitive type want, so a
// literal standing for a distinct type (whose equality its own impl may
// decide) is not taken for a constant.
func (c *checker) typedAs(n ast.Node, want Type) bool {
	return resolveTypeVar(c.fa.ExprTypes[n]) == want
}

// floatCanon spells a Float by value. `-0.0 == 0.0`, so both are one key.
// A literal is never NaN.
func floatCanon(f float64) string {
	if f == 0 {
		f = 0
	}
	return "f:" + strconv.FormatFloat(f, 'g', -1, 64)
}

// decimalCanon spells a Decimal by value: `1.0d` and `1.00d` are equal.
func decimalCanon(d rt.Decimal) string {
	n := d.Normalize()
	return "d:" + n.MantissaOrZero().String() + "e" + strconv.Itoa(int(n.Scale))
}

func literalText(lexeme, fallback string) string {
	if lexeme != "" {
		return lexeme
	}
	return fallback
}

// stringText is a String key as a message shows it: quoted, with the
// escapes Nomi and Go share.
func stringText(s string) string {
	plain := true
	for _, r := range s {
		if r == '"' || r == '\\' || !unicode.IsPrint(r) {
			plain = false
			break
		}
	}
	if plain {
		return `"` + s + `"`
	}
	return strconv.Quote(s)
}
