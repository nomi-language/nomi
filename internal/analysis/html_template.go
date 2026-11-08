package analysis

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/htmltemplate"
)

// An `Html` literal (spec §18 *HTML templates*) is not handed to a
// `Literal` handler. Its tag is std/html's `Html`, which the compiler
// knows: the builder reads the literal's text as an HTML template
// (internal/htmltemplate), reports its errors at their place in the
// literal, and gives the literal the ordinary call it stands for, which the
// checker checks and the IR builder lowers in its place:
//
//	html$.assemble(["<p class=\"", html$.slot_attribute(c), "\">", html$.slot_text(x), "</p>"])
//
// Each slot is a call of the std/html function its context chose, whose
// parameter's interface is the slot's type rule, so a value with no
// rendering where it stands is the argument check's error at the slot.
// Element content takes a `ToHtml` or another `Display`, so the checker
// moves a `slot_text` call whose value is only `Display` to
// `slot_text_display` (chooseHtmlTextSlots).
// The slot expressions are the user's own nodes. `html$` is a hidden
// import of std/html that a file importing std/html gains
// (withHtmlImport); std/html's own literals call the functions by name.
//
// The added nodes are placed on characters of the literal itself that no
// node of the program is placed on: the call of `assemble` on the opening
// and closing quotes, and each slot's call on the `$` and `{` of its `${`.
// The identifiers' resolutions are keyed by position, so each of them has
// a place of its own.

// htmlModuleAlias is the alias of the hidden import of std/html.
const htmlModuleAlias = "html$"

// htmlImport is `import std::html as html$`, on the hidden imports' line,
// after the json import's columns.
func htmlImport(fromStd bool) *ast.ImportStmt {
	l := hiddenImportLine
	path := []ast.Node{&ast.Ident{Name: "std", Line: l, Col: 14}, &ast.Ident{Name: "html", Line: l, Col: 15}}
	if fromStd {
		path = path[1:]
	}
	return &ast.ImportStmt{
		ModulePath:   path,
		FileSegments: len(path),
		ModuleAlias:  &ast.Ident{Name: htmlModuleAlias, Line: l, Col: 16},
		Line:         l,
		Col:          13,
	}
}

// withHtmlImport adds htmlImport to a file that imports std/html, so its
// `Html` literals can name std/html's slot functions. The import is the
// module std/html already is, so it costs nothing; a file whose literals
// do not need it leaves it unused, which a hidden import may be.
func withHtmlImport(nodes []ast.Node) []ast.Node {
	importsHtml, fromStd := false, false
	for _, n := range nodes {
		var imps []*ast.ImportStmt
		switch imp := n.(type) {
		case *ast.ImportStmt:
			imps = []*ast.ImportStmt{imp}
		case *ast.ImportBlock:
			imps = imp.Entries
		}
		for _, imp := range imps {
			if imp.Line == hiddenImportLine && imp.ModuleAlias != nil && ast.ImportNodeName(imp.ModuleAlias) == htmlModuleAlias {
				return nodes
			}
			if segs, ok := importsStdHtml(imp); ok {
				importsHtml, fromStd = true, segs == 1
			}
		}
	}
	if !importsHtml {
		return nodes
	}
	at := 0
	for at < len(nodes) && isPreludeInjectLine(nodes[at].LineNum()) {
		at++
	}
	out := make([]ast.Node, 0, len(nodes)+1)
	out = append(out, nodes[:at]...)
	out = append(out, htmlImport(fromStd))
	return append(out, nodes[at:]...)
}

// importsStdHtml reports whether imp imports std/html, `std/html` from a
// program or `html` from another std file, and how many segments its path
// has.
func importsStdHtml(imp *ast.ImportStmt) (int, bool) {
	segs := imp.ModulePath
	if imp.FileSegments > 0 && imp.FileSegments <= len(segs) {
		segs = segs[:imp.FileSegments]
	}
	switch len(segs) {
	case 1:
		return 1, ast.ImportNodeName(segs[0]) == "html"
	case 2:
		return 2, ast.ImportNodeName(segs[0]) == "std" && ast.ImportNodeName(segs[1]) == "html"
	}
	return 0, false
}

// stdHtmlOrigin is std/html's build key.
const stdHtmlOrigin = "std/html"

// isStdHtmlTag reports whether the tag of n names std/html's Html.
func (b *builder) isStdHtmlTag(n *ast.TaggedString, scope *Scope) bool {
	sym := scope.Lookup(n.Tag)
	if sym == nil {
		return false
	}
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	if b.file.Origin == stdHtmlOrigin {
		def, isStruct := real.Node.(*ast.StructDef)
		return isStruct && def.Name == "Html" && sym.Resolved == nil
	}
	for _, scope := range b.stdHtmlScopes() {
		if decl := scope.Lookup("Html"); decl != nil && decl.Node != nil && decl.Node == real.Node {
			return true
		}
	}
	return false
}

// stdHtmlScopes are the module scopes this build holds for std/html: the
// stdlib analysis a program build is handed, or, inside the stdlib's own
// build, the module loaded under its name.
func (b *builder) stdHtmlScopes() []*Scope {
	var out []*Scope
	if fa := b.stdlibFAs["html"]; fa != nil && fa.ModuleScope != nil {
		out = append(out, fa.ModuleScope)
	}
	if fa := b.cache[stdHtmlOrigin]; fa != nil && fa.ModuleScope != nil {
		out = append(out, fa.ModuleScope)
	}
	if s := b.modules["html"]; s != nil {
		out = append(out, s)
	}
	if s := b.stdlibScopes["html"]; s != nil {
		// A std file's own build, whose hidden `import html as html$` moved
		// b.modules' key to the alias.
		out = append(out, s)
	}
	return out
}

// htmlTemplate reads n, whose tag is std/html's Html, and gives it its
// expansion, or reports its errors.
func (b *builder) htmlTemplate(n *ast.TaggedString) {
	var statics []string
	var slots []ast.Node
	pending := ""
	for _, part := range n.Parts {
		switch p := part.(type) {
		case ast.StringText:
			pending += p.Value
		case ast.StringExpr:
			statics = append(statics, pending)
			slots = append(slots, p.Expr)
			pending = ""
		}
	}
	statics = append(statics, pending)

	pos := htmlPositions{n: n, statics: statics, slots: slots}
	tmpl, errs := htmltemplate.Parse(statics, htmltemplate.Options{Line: func(p htmltemplate.Pos) int {
		line, _ := pos.at(p)
		return line
	}})
	if len(errs) > 0 {
		for _, e := range errs {
			line, col := pos.at(e.Pos)
			b.file.TypeErrors = append(b.file.TypeErrors, TypeError{Line: line, Col: col, Message: e.Message})
		}
		n.SetTemplate(nil)
		return
	}
	n.SetTemplate(b.htmlExpansion(n, tmpl, slots))
}

// htmlExpansion is the call tmpl's literal n stands for.
func (b *builder) htmlExpansion(n *ast.TaggedString, tmpl *htmltemplate.Template, slots []ast.Node) ast.Node {
	inStdHtml := b.file.Origin == stdHtmlOrigin
	fn := func(name string, line, col int) ast.Node {
		b.file.recordStandIn(line, col)
		if inStdHtml {
			return identExpr(name, line, col)
		}
		// `html$` on the first place, the function's name on the second.
		b.file.recordStandIn(line, col+1)
		fa := fieldAccess(identExpr(htmlModuleAlias, line, col), name, line, col+1)
		return fa
	}
	var items []ast.Node
	for i, s := range tmpl.Statics {
		if s != "" {
			items = append(items, &ast.StringLit{Value: s, Line: n.Line, Col: n.Col})
		}
		if i == len(tmpl.Slots) {
			break
		}
		slot := tmpl.Slots[i]
		line, col := exprStartLineCol(slots[i])
		col = max(col-2, 1) // the `$` of `${`
		args := []ast.Node{slots[i]}
		if slot.Context == htmltemplate.ContextOptionalAttribute {
			args = []ast.Node{&ast.StringLit{Value: slot.Attr, Line: line, Col: col}, slots[i]}
		}
		items = append(items, &ast.Call{Func: fn(htmlSlotFunction(slot.Context), line, col), Args: args, Line: line, Col: col})
	}
	// `html$` on the opening quote, `assemble` on the closing one.
	line, col := n.Line, n.Col
	b.file.recordStandIn(line, col)
	var callee ast.Node
	if inStdHtml {
		callee = identExpr("assemble", line, col)
	} else {
		end := n.GetSpan()
		closeLine, closeCol := line, col+1
		if !end.IsZero() && end.EndCol > 1 {
			closeLine, closeCol = end.EndLine, end.EndCol-1
		}
		b.file.recordStandIn(closeLine, closeCol)
		callee = &ast.FieldAccess{
			Object: identExpr(htmlModuleAlias, line, col),
			Field:  &ast.Ident{Name: "assemble", Line: closeLine, Col: closeCol},
			Line:   line,
			Col:    col,
		}
	}
	return &ast.Call{Func: callee, Args: []ast.Node{&ast.ListLit{Items: items, Line: line, Col: col}}, Line: line, Col: col}
}

// htmlSlotFunction is the std/html function a slot in context ctx calls.
func htmlSlotFunction(ctx htmltemplate.Context) string {
	switch ctx {
	case htmltemplate.ContextRCDATA:
		return "slot_rcdata"
	case htmltemplate.ContextAttr:
		return "slot_attribute"
	case htmltemplate.ContextURLStart:
		return "slot_url_start"
	case htmltemplate.ContextURLPath:
		return "slot_url_path"
	case htmltemplate.ContextURLQuery:
		return "slot_url_query"
	case htmltemplate.ContextURLFragment:
		return "slot_url_fragment"
	case htmltemplate.ContextCSS:
		return "slot_css"
	case htmltemplate.ContextOptionalAttribute:
		return "slot_optional_attribute"
	}
	return "slot_text"
}

// htmlPositions places a position in an `Html` literal's text at its
// source line and column.
type htmlPositions struct {
	n       *ast.TaggedString
	statics []string
	slots   []ast.Node
}

// at is the source line and column of p. A slot's position is its
// expression's.
func (h htmlPositions) at(p htmltemplate.Pos) (int, int) {
	if p.Part < len(h.slots) && p.Offset >= len(h.statics[p.Part]) {
		return exprStartLineCol(h.slots[p.Part])
	}
	line, col := h.start(p.Part)
	text := h.statics[p.Part]
	if p.Offset > len(text) {
		p.Offset = len(text)
	}
	before := text[:p.Offset]
	nl := strings.Count(before, "\n")
	if nl == 0 {
		return line, col + p.Offset
	}
	bodyCol := 1
	if h.n.BodyCol > 0 {
		bodyCol = h.n.BodyCol
	}
	return line + nl, bodyCol + len(before) - strings.LastIndexByte(before, '\n') - 1
}

// start is where static part i begins: after the opening quote, or after
// the `}` that ends slot i-1.
func (h htmlPositions) start(i int) (int, int) {
	n := h.n
	if i == 0 {
		switch {
		case n.Triple && n.BodyCol > 0 && n.GetSpan().EndLine > n.Line:
			// A triple-quoted body begins on the line after `"""`.
			return n.Line + 1, n.BodyCol
		case n.Triple && !n.Raw:
			return n.Line, n.Col + 3
		}
		return n.Line, n.Col + 1
	}
	if sp, ok := spanOf(h.slots[i-1]); ok {
		return sp.EndLine, sp.EndCol + 1
	}
	return exprStartLineCol(h.slots[i-1])
}

// checkHtmlLiteral is the type of an `Html` literal: its expansion's, or
// std/html's Html when its template has errors, which the builder
// reported.
func (c *checker) checkHtmlLiteral(n *ast.TaggedString, expansion ast.Node) Type {
	if expansion != nil {
		before := len(c.errors)
		c.chooseHtmlTextSlots(expansion)
		t := c.checkNode(expansion)
		c.htmlSlotHelp(expansion, before)
		return t
	}
	for _, part := range n.Parts {
		if se, ok := part.(ast.StringExpr); ok {
			c.checkNode(se.Expr)
		}
	}
	return c.reg.Lookup(n.Tag)
}

// chooseHtmlTextSlots checks the value of each slot of expansion whose
// function depends on the value's type, and picks the function that writes
// it. In element content that is `slot_text` for a value that renders
// itself (`ToHtml`), else `slot_text_display`, which escapes any `Display`
// value as text; a value that is neither keeps `slot_text`, whose argument
// check reports it. In a context that writes a `Display` value's text (a
// quoted attribute value, `<title>`'s text, a URL after its start), a
// `Maybe` moves to the context's `_maybe` function, which writes its value
// or nothing, so a `Maybe`'s `Some(...)` is never written; a `Maybe` of a
// `Maybe` is an error there. The value's type is kept for the slot's call,
// which does not check it again.
func (c *checker) chooseHtmlTextSlots(expansion ast.Node) {
	for _, slot := range htmlSlotCalls(expansion) {
		name := htmlSlotCallee(slot)
		if name == nil || len(slot.Args) != 1 || (*name != "slot_text" && !htmlDisplaySlots[*name]) {
			continue
		}
		arg := slot.Args[0]
		ty := c.checkNode(arg)
		if c.checkedSlots == nil {
			c.checkedSlots = map[ast.Node]Type{}
		}
		c.checkedSlots[arg] = ty
		if *name != "slot_text" {
			line, col := exprStartLineCol(arg)
			if inner := maybePayload(ty); inner != nil && c.htmlSlotWritesInner(inner, c.recPos(line, col)) {
				if maybePayload(inner) != nil {
					e := errAt(arg, fmt.Sprintf("this slot does not show a `%s`", ty))
					e.Hints = []string{htmlNestedMaybeHelp}
					c.report(e)
				}
				c.retargetHtmlSlot(slot, *name+"_maybe")
			}
			continue
		}
		line, col := exprStartLineCol(arg)
		if c.htmlTextTakesDisplay(ty, c.recPos(line, col)) {
			c.retargetHtmlSlot(slot, "slot_text_display")
		}
	}
}

// htmlSlotWritesInner reports whether a `Maybe<inner>` in a slot that
// writes a `Display` value's text moves to the slot's `_maybe` function:
// inner is `Display`, or not yet known. Another keeps the slot's own
// function, whose argument check says what the slot takes.
func (c *checker) htmlSlotWritesInner(inner Type, at Pos) bool {
	if _, ok := resolveTypeVar(inner).(*TypeVar); ok {
		return true
	}
	return c.typeImplementsInterface(inner, "Display", at, RecordingKindCallSite)
}

// htmlDisplaySlots is the slot functions that write a `Display` value's
// text, each with a `_maybe` twin in std/html for a `Maybe`.
var htmlDisplaySlots = map[string]bool{
	"slot_rcdata":       true,
	"slot_attribute":    true,
	"slot_url_path":     true,
	"slot_url_query":    true,
	"slot_url_fragment": true,
}

// htmlTextTakesDisplay reports whether an element-content slot writes a
// value of type t through `Display` rather than `ToHtml`: t does not
// implement `ToHtml` and does implement `Display`, and is not a `Maybe`, a
// `Result` or a collection, whose text is not the value's. A value of
// interface type goes by its interface; a type parameter keeps `ToHtml`.
func (c *checker) htmlTextTakesDisplay(t Type, at Pos) bool {
	switch r := resolveTypeVar(t).(type) {
	case nil, *TypeVar:
		return false
	case *InterfaceType:
		return r.Name == "Display"
	case *TypeParam_:
		return false
	case *PrimitiveType:
		if r == TypeAny || r == TypeInfallible {
			return false
		}
	}
	if c.typeImplementsInterface(t, "ToHtml", at, RecordingKindCallSite) || htmlTextShape(t) != htmlTextScalar {
		return false
	}
	return c.typeImplementsInterface(t, "Display", at, RecordingKindCallSite)
}

// retargetHtmlSlot makes slot call std/html's function named name. The
// builder resolved the callee to the function its context chose, by the
// callee's position, so the reference there moves with the name.
func (c *checker) retargetHtmlSlot(slot *ast.Call, name string) {
	var scope *Scope
	var at Pos
	switch f := slot.Func.(type) {
	case *ast.FieldAccess:
		obj, ok := ast.QualifierIdent(f.Object)
		if !ok {
			return
		}
		mod := c.fa.References[Pos{Line: obj.Line, Col: obj.Col}]
		for mod != nil && mod.ModuleScope == nil && mod.Resolved != nil {
			mod = mod.Resolved
		}
		if mod == nil {
			return
		}
		scope, at = mod.ModuleScope, Pos{Line: f.Field.Line, Col: f.Field.Col}
	case *ast.Ident:
		scope, at = c.fa.ModuleScope, Pos{Line: f.Line, Col: f.Col}
	}
	if scope == nil {
		return
	}
	sym := scope.Lookup(name)
	if sym == nil {
		return
	}
	*htmlSlotCallee(slot) = name
	c.fa.References[at] = sym
}

// takeCheckedSlot is the type chooseHtmlTextSlots gave node, which it
// checked, once.
func (c *checker) takeCheckedSlot(node ast.Node) (Type, bool) {
	if c.checkedSlots == nil {
		return nil, false
	}
	ty, ok := c.checkedSlots[node]
	if ok {
		delete(c.checkedSlots, node)
	}
	return ty, ok
}

// htmlSlotCalls is the slot calls of an `Html` literal's expansion.
func htmlSlotCalls(expansion ast.Node) []*ast.Call {
	call, ok := expansion.(*ast.Call)
	if !ok || len(call.Args) != 1 {
		return nil
	}
	list, ok := call.Args[0].(*ast.ListLit)
	if !ok {
		return nil
	}
	var out []*ast.Call
	for _, item := range list.Items {
		if slot, ok := item.(*ast.Call); ok && len(slot.Args) > 0 {
			out = append(out, slot)
		}
	}
	return out
}

// htmlSlotCallee is the name of the std/html function a slot call calls,
// to read or to change.
func htmlSlotCallee(slot *ast.Call) *string {
	switch f := slot.Func.(type) {
	case *ast.FieldAccess:
		return &f.Field.Name
	case *ast.Ident:
		return &f.Name
	}
	return nil
}

// htmlSlotHelps is the help for a slot whose value has no rendering where
// it stands, by the function its context calls.
var htmlSlotHelps = map[string]string{
	"slot_text":               "an element's content takes a value that renders itself (`ToHtml`: a `String`, a number, an `Html`, a `List<Html>`, a `Maybe` of one), or any other `Display` value, escaped as text",
	"slot_rcdata":             "`<title>` and `<textarea>` hold text, not markup: pass a `String`",
	"slot_attribute":          "an attribute's value is text: pass a `String` or another `Display` value, or a `Maybe` of one, which writes nothing for `None`",
	"slot_url_path":           "a slot after the start of a URL is text, escaped for its part of the URL: pass a `String` or another `Display` value, or a `Maybe` of one",
	"slot_url_query":          "a slot after the start of a URL is text, escaped for its part of the URL: pass a `String` or another `Display` value, or a `Maybe` of one",
	"slot_url_fragment":       "a slot after the start of a URL is text, escaped for its part of the URL: pass a `String` or another `Display` value, or a `Maybe` of one",
	"slot_url_start":          "a URL attribute's value starts with a `Url` (`Url.parse(text)`) or a `SafeUrl` the program vouches for, so data cannot make a `javascript:` link",
	"slot_css":                "a `style` value takes a number, or a `Css` the program vouches for",
	"slot_optional_attribute": "`name=${...}` writes the attribute or leaves it out: pass a `Bool` or a `Maybe<String>`; to write text, quote the value, `name=\"${...}\"`",
}

// The help for an element-content slot whose value is a wrapper or a
// collection that is `Display` but not `ToHtml`.
const (
	htmlTextWrapperHelp    = "a `Maybe` or a `Result` would show as `Some(...)` or `Ok(...)`: unwrap it; a `Maybe` of a `ToHtml` value shows its value, and `None` nothing"
	htmlTextCollectionHelp = "a collection would show as its `Display` form, `[a, b]`: render its elements with `Html.each`, or join them into a `String`"
	htmlNestedMaybeHelp    = "a `Maybe` writes its value or nothing, and the inner `Maybe` would show as `Some(...)`: flatten it with `Maybe.flatten`"
	htmlTextTupleHelp      = "a tuple would show as `(a, b)`: put each part in a slot of its own"
)

// htmlArticle is articleFor, with `Html` read as "an HTML", and `Url` and
// `Uuid` as "a URL" and "a UUID".
func htmlArticle(name string) string {
	switch {
	case strings.HasPrefix(name, "Html"):
		return "an"
	case strings.HasPrefix(name, "Url"), strings.HasPrefix(name, "Uuid"):
		return "a"
	}
	return articleFor(name)
}

// htmlArgumentMismatch is the argument check's error for a slot value
// whose type does not implement the slot's interface.
var htmlArgumentMismatch = regexp.MustCompile("^argument [0-9]+: expected (.+), got (.+)$")

// htmlSlotHelp rewrites each error the checker reported since before that a
// slot's value has no rendering where it stands. The argument check names
// an argument the template does not show, so the error says what the slot
// takes, with the help for the slot's context.
func (c *checker) htmlSlotHelp(expansion ast.Node, before int) {
	type slotRule struct {
		name string
		arg  ast.Node
	}
	rules := map[Pos]slotRule{}
	for _, slot := range htmlSlotCalls(expansion) {
		name := htmlSlotCallee(slot)
		if name == nil {
			continue
		}
		if _, ok := htmlSlotHelps[*name]; ok {
			arg := slot.Args[len(slot.Args)-1]
			line, col := exprStartLineCol(arg)
			rules[Pos{Line: line, Col: col}] = slotRule{name: *name, arg: arg}
		}
	}
	for i := before; i < len(c.errors); i++ {
		e := &c.errors[i]
		rule, ok := rules[Pos{Line: e.Line, Col: e.Col}]
		if !ok || e.File != "" {
			continue
		}
		m := htmlArgumentMismatch.FindStringSubmatch(e.Message)
		if m == nil {
			continue
		}
		got := fmt.Sprintf("%s `%s`", htmlArticle(m[2]), m[2])
		e.Message = fmt.Sprintf("this slot takes %s `%s`, and %s is not one", htmlArticle(m[1]), m[1], got)
		e.Hints = []string{htmlSlotHelps[rule.name]}
		if rule.name != "slot_text" {
			continue
		}
		switch htmlTextShape(c.fa.ExprTypes[rule.arg]) {
		case htmlTextWrapper:
			e.Message = fmt.Sprintf("element content does not show %s", got)
			e.Hints = []string{htmlTextWrapperHelp}
		case htmlTextCollection:
			e.Message = fmt.Sprintf("element content does not show %s", got)
			e.Hints = []string{htmlTextCollectionHelp}
		case htmlTextTuple:
			e.Message = fmt.Sprintf("element content does not show %s", got)
			e.Hints = []string{htmlTextTupleHelp}
		default:
			e.Message = fmt.Sprintf("this slot takes a `ToHtml` or a `Display`, and %s is neither", got)
		}
	}
}

// The kinds of `Display` value that element content does not write as
// text: a wrapper, whose text names its variant, a collection and a tuple.
const (
	htmlTextScalar = iota
	htmlTextWrapper
	htmlTextCollection
	htmlTextTuple
)

// htmlTextShape is whether t is std's `Maybe` or `Result`, one of its
// collections, a tuple, or none of these.
func htmlTextShape(t Type) int {
	t = resolveTypeVar(t)
	switch t.(type) {
	case *ListType, *MapType:
		return htmlTextCollection
	case *TupleType:
		return htmlTextTuple
	}
	origin, _ := nominalOrigin(t)
	if origin != "" && !strings.HasPrefix(origin, "std/") && origin != "std" {
		return htmlTextScalar
	}
	switch concreteTypeName(t) {
	case "Maybe", "Result":
		return htmlTextWrapper
	case "Set", "Vector":
		return htmlTextCollection
	}
	return htmlTextScalar
}
