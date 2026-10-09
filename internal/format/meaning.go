package format

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/token"
)

// SameMeaning reports how formatted, the formatter's output for src, means
// something other than src, or nil when it does not. formatted must parse,
// and its syntax tree must equal src's once src's is put in the order Format
// writes it in (imports combined and sorted, type body items ordered, a body
// that is one block flattened, a tail `return` dropped). Positions, comment
// placement, blank lines and parentheses are ignored, and so is the other
// spelling of a parenthesized name in a pattern (bindGroupedNames). Comment
// text is not: both texts must hold the same comments and doc comments. A
// comment line with no text, `//` or `///`, is layout, as a blank line is:
// the formatter writes a doc comment's trailing blank lines as `//` and drops
// a doc comment that is only blank lines.
//
// It is the property `nomi fmt` is held to; vmhost's format_meaning_test.go
// checks it over the corpus, generated programs and their layout variants.
func SameMeaning(src, formatted string) error {
	orig, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		return fmt.Errorf("the source does not parse: %v", err)
	}
	orig = normalizeImportLayout(orig)
	normalizeBodies(orig)
	orig = canonicalTypeBodies(collapseSingleEntryBlocks(orig))
	out, err := parser.Parse(lexer.Lex(formatted))
	if err != nil {
		return fmt.Errorf("the formatted text does not parse: %v", err)
	}
	bindGroupedNames(reflect.ValueOf(orig))
	bindGroupedNames(reflect.ValueOf(out))
	splitImportPaths(orig)
	splitImportPaths(out)
	if path, ok := equalAST(reflect.ValueOf(orig), reflect.ValueOf(out), "file"); !ok {
		return fmt.Errorf("the syntax tree changed at %s", path)
	}
	if changed := changedComments(src, formatted); changed != "" {
		return fmt.Errorf("the comments changed: %s", changed)
	}
	return nil
}

// changedComments describes each comment or doc comment src has and
// formatted does not, with the code tokens around it, and each formatted
// has and src does not. Comments compare by text, trimmed of trailing
// space.
func changedComments(src, formatted string) string {
	have := map[string]int{}
	for _, c := range comments(formatted) {
		have[c.text]++
	}
	var out []string
	for _, c := range comments(src) {
		if have[c.text] > 0 {
			have[c.text]--
			continue
		}
		out = append(out, fmt.Sprintf("lost %q after `%s` and before `%s`", c.text, c.before, c.after))
	}
	var added []string
	for text, n := range have {
		for ; n > 0; n-- {
			added = append(added, fmt.Sprintf("added %q", text))
		}
	}
	sort.Strings(added)
	return strings.Join(append(out, added...), "; ")
}

type comment struct {
	text, before, after string
}

// comments are src's comments and doc comments that hold text, in order,
// each with the lexemes of the code tokens before and after it.
func comments(src string) []comment {
	toks := lexer.Lex(src)
	code := func(i, step int) string {
		for ; i >= 0 && i < len(toks); i += step {
			switch toks[i].Type {
			case token.COMMENT, token.DOC_COMMENT, token.NEWLINE, token.BLANK_LINE:
				continue
			case token.EOF:
				return "end of file"
			}
			return toks[i].Lexeme
		}
		return "start of file"
	}
	var out []comment
	for i, tok := range toks {
		var text string
		switch tok.Type {
		case token.COMMENT:
			text = strings.TrimRight(tok.Lexeme, " \t\r")
			if text == "//" {
				continue
			}
		case token.DOC_COMMENT:
			if strings.TrimSpace(tok.Lexeme) == "" {
				continue
			}
			// A doc comment's text is what follows `///` and the one
			// space after it, if any (collectDocComments): `///0` and
			// `/// 0` document the same text, and the formatter writes
			// the second.
			text = "///" + strings.TrimPrefix(strings.TrimRight(tok.Lexeme, " \t\r"), " ")
		default:
			continue
		}
		out = append(out, comment{text: text, before: code(i-1, -1), after: code(i+1, 1)})
	}
	return out
}

// canonicalTypeBodies orders each top-level type's body items as the
// formatter writes them.
func canonicalTypeBodies(nodes []ast.Node) []ast.Node {
	for _, n := range nodes {
		canonicalTypeBody(n)
	}
	return nodes
}

func canonicalTypeBody(n ast.Node) {
	order := func(items []ast.Node) []ast.Node {
		items = orderTypeBodyItems(items)
		for _, item := range items {
			canonicalTypeBody(item)
		}
		return items
	}
	switch v := n.(type) {
	case *ast.StructDef:
		v.Items = order(v.Items)
	case *ast.EnumDef:
		v.Items = order(v.Items)
	case *ast.TypeDef:
		v.Items = order(v.Items)
	case *ast.ExternType:
		v.Items = order(v.Items)
	}
}

// astSkipFields are the struct fields SameMeaning does not compare, beside
// every []ast.Trivia field: source positions and doc text (whose text
// changedComments compares instead).
// Any field named ...Line, ...Col or ...Span is skipped as well.
var astSkipFields = map[string]bool{
	// Embedded trivia carrier (leading/trailing comments and blank lines).
	"TriviaCarrier": true,
	// `clock` keeps its trivia directly on TestDecl because it is not a
	// standalone AST node.
	"ClockLeading":  true,
	"ClockTrailing": true,
	// Source-shape flag for import selector braces. `path: A, B` and
	// `path.{A, B}` parse with different Braced values but bind the same names.
	"Braced": true,
	// Doc-comment text attached to declarations.
	"Doc": true,
	// An empty `{}` body on an ExternType or TypeDef is no body; the
	// formatter drops the braces. A body with items compares by its Items.
	"HasBody": true,
}

func astSkipField(name string) bool {
	return astSkipFields[name] ||
		strings.HasSuffix(name, "Line") ||
		strings.HasSuffix(name, "Col") ||
		strings.HasSuffix(name, "Span")
}

// bindGroupedNames rewrites, in place, a variant or struct field pattern
// whose payload is a parenthesized name, `Some((n))` or `{x: (n)}`, to the
// binding the parser builds for `Some(n)` and `{x: n}`. The two bind the
// same name to the same value; the formatter writes the second.
func bindGroupedNames(v reflect.Value) {
	switch v.Kind() {
	case reflect.Interface, reflect.Ptr:
		if !v.IsNil() {
			bindGroupedName(v.Interface())
			bindGroupedNames(v.Elem())
		}
	case reflect.Struct:
		if v.CanAddr() {
			bindGroupedName(v.Addr().Interface())
		}
		for i := 0; i < v.NumField(); i++ {
			bindGroupedNames(v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if e := v.Index(i); e.Kind() == reflect.Interface && e.CanSet() && !e.IsNil() {
				if pb, ok := e.Interface().(*ast.PatternBinding); ok && pb.Else == nil {
					// `(a) = v` is the binding `a = v`.
					if id, ok := pb.Pattern.(*ast.IdentPattern); ok {
						e.Set(reflect.ValueOf(&ast.Binding{Name: id.Name, Value: pb.Value}))
					}
				}
			}
			bindGroupedNames(v.Index(i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			bindGroupedNames(v.MapIndex(k))
		}
	}
}

// splitImportPaths sets each import's FileSegments where the formatter
// splits its path (importPathAndOwners): the trailing PascalCase segments
// of an import that names things are owners, and the others the file path.
// `api.http.header.{self}` and `api/http/header.{self}` import the same
// thing: an owner is a type, whose name is PascalCase, so the analyzer
// never resolves a lowercase segment as one. Each segment then compares by
// its name alone.
func splitImportPaths(nodes []ast.Node) {
	split := func(n *ast.ImportStmt) {
		if n.FileSegments == 0 {
			return
		}
		path, _ := importPathAndOwners(n)
		n.FileSegments = len(path)
		// A segment is its name: `import "Mod"` and `import Mod` name the
		// same file, though the quoted one parses as an Ident.
		for i, seg := range n.ModulePath {
			n.ModulePath[i] = &ast.Ident{Name: ast.ImportNodeName(seg)}
		}
	}
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.ImportStmt:
			split(v)
		case *ast.ImportBlock:
			for _, e := range v.Entries {
				split(e)
			}
		}
	}
}

func bindGroupedName(n any) {
	switch n := n.(type) {
	case *ast.EnumPattern:
		if id, ok := n.Payload.(*ast.IdentPattern); ok && n.Binding == "" {
			n.Binding, n.Payload = id.Name, nil
		}
	case *ast.StructPatternField:
		if id, ok := n.Pattern.(*ast.IdentPattern); ok && n.Binding == "" {
			n.Binding, n.Pattern = id.Name, nil
		}
	}
}

var groupedType = reflect.TypeOf(&ast.GroupedExpr{})

// triviaType is the type of every field that holds comments and blank
// lines (Leading, Trailing, EndTrivia, LeadingComments, ...), whose
// placement SameMeaning ignores.
var triviaType = reflect.TypeOf([]ast.Trivia(nil))

func isGrouped(v reflect.Value) bool {
	if v.Kind() == reflect.Interface && !v.IsNil() {
		v = v.Elem()
	}
	return v.IsValid() && v.Type() == groupedType
}

// stageEndsInOpenLambda reports whether the pipe stage v, without its
// parentheses, ends in a lambda's bare body (endsInOpenLambda).
func stageEndsInOpenLambda(v reflect.Value) bool {
	v = ungrouped(v)
	if !v.IsValid() || (v.Kind() == reflect.Interface && v.IsNil()) {
		return false
	}
	n, ok := v.Interface().(ast.Node)
	return ok && endsInOpenLambda(n)
}

// ungrouped is v without the GroupedExprs around it.
func ungrouped(v reflect.Value) reflect.Value {
	for {
		e := v
		if e.Kind() == reflect.Interface && !e.IsNil() {
			e = e.Elem()
		}
		if e.Type() != groupedType || e.IsNil() {
			return v
		}
		v = reflect.ValueOf(e.Interface().(*ast.GroupedExpr).Expr)
		if !v.IsValid() {
			return v
		}
	}
}

// equalAST compares two syntax trees, skipping astSkipFields. When they
// differ, path names the first difference.
func equalAST(a, b reflect.Value, path string) (string, bool) {
	if !a.IsValid() || !b.IsValid() {
		return path, a.IsValid() == b.IsValid()
	}
	// Parentheses are transparent to the checker and to evaluation; the
	// formatter drops them around atoms and adds them where they show
	// structure. The tree inside them is what is compared.
	a, b = ungrouped(a), ungrouped(b)
	if a.IsValid() && a.Kind() == reflect.Interface && !a.IsNil() {
		a = a.Elem()
	}
	if b.IsValid() && b.Kind() == reflect.Interface && !b.IsNil() {
		b = b.Elem()
	}
	if !a.IsValid() || !b.IsValid() {
		return path, a.IsValid() == b.IsValid()
	}
	if a.Type() != b.Type() {
		return fmt.Sprintf("%s: %s became %s", path, a.Type(), b.Type()), false
	}
	switch a.Kind() {
	case reflect.Interface, reflect.Ptr:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() == b.IsNil() {
				return "", true
			}
			return fmt.Sprintf("%s: nil on one side only", path), false
		}
		return equalAST(a.Elem(), b.Elem(), path)
	case reflect.Struct:
		t := a.Type()
		for i := 0; i < a.NumField(); i++ {
			name := t.Field(i).Name
			if astSkipField(name) || t.Field(i).Type == triviaType {
				continue
			}
			// A destructured parameter's synthesized name, `__destr_N`,
			// carries the column of its opening delimiter. Its Destructure
			// field holds the pattern.
			if t == reflect.TypeOf(ast.Param{}) && name == "Name" &&
				strings.HasPrefix(a.Field(i).String(), "__destr_") && strings.HasPrefix(b.Field(i).String(), "__destr_") {
				continue
			}
			// Parentheses around a pipe stage are not transparent:
			// `x |> (f())` pipes x into the value f() returns. Around a
			// stage that ends in a lambda's bare body they are, since such
			// a stage is a value either way; the formatter adds them when
			// another stage follows (emitStage).
			if t == reflect.TypeOf(ast.Binary{}) && name == "Right" && a.FieldByName("Op").String() == "|>" &&
				isGrouped(a.Field(i)) != isGrouped(b.Field(i)) && !stageEndsInOpenLambda(a.Field(i)) {
				return path + ".Binary.Right: a pipe stage's parentheses changed", false
			}
			if p, ok := equalAST(a.Field(i), b.Field(i), path+"."+t.Name()+"."+name); !ok {
				return p, false
			}
		}
		return "", true
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return fmt.Sprintf("%s: %d elements became %d", path, a.Len(), b.Len()), false
		}
		for i := 0; i < a.Len(); i++ {
			if p, ok := equalAST(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i)); !ok {
				return p, false
			}
		}
		return "", true
	case reflect.Map:
		if a.Len() != b.Len() {
			return fmt.Sprintf("%s: %d entries became %d", path, a.Len(), b.Len()), false
		}
		for _, k := range a.MapKeys() {
			if p, ok := equalAST(a.MapIndex(k), b.MapIndex(k), fmt.Sprintf("%s[%v]", path, k)); !ok {
				return p, false
			}
		}
		return "", true
	default:
		if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			return fmt.Sprintf("%s: %#v became %#v", path, a.Interface(), b.Interface()), false
		}
		return "", true
	}
}
