package format

import (
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/strlit"
)

// emitFile walks top-level AST nodes and renders them to a string.
// Adjacent top-level declarations are separated by a blank line, with compact
// runs for imports and simple bodyless declarations (for example consecutive
// once/type/typealias declarations). Author-written blank lines inside compact
// runs are preserved as grouping boundaries. The file ends with a trailing
// newline.
//
// Imports are rendered as one group: a single import stays `import std/io`,
// and two or more (whether written as separate statements or as blocks) merge
// into one sorted, newline-separated `import { ... }` block.
//
// fileEndTrivia is comments / blank-line trivia that appears after the
// last top-level declaration and before EOF. The parser captures it via
// ParseFile; without explicit handling here it would be silently dropped
// (formatter data-loss bug). Standalone comments emit each on their own
// line; blank-line trivia emits an extra HardLine to preserve the gap
// between the last decl and the trailing comment block.
func emitFile(nodes []ast.Node, fileEndTrivia []ast.Trivia) string {
	if len(nodes) == 0 {
		// Trivia-only file (e.g. just comments): emit each comment on its
		// own line and end with a trailing newline. Empty input still
		// renders empty.
		if len(fileEndTrivia) == 0 {
			return ""
		}
		return Render(Concat(emitFileEndTrivia(fileEndTrivia, false), HardLine()), defaultWidth)
	}
	nodes = collapseSingleEntryBlocks(nodes)
	parts := make([]Doc, 0, len(nodes)*3+1)
	for i, n := range nodes {
		if i > 0 {
			parts = append(parts, HardLine())
			if wantsBlankBetween(nodes[i-1], n) && !hasLeadingBlank(n) {
				parts = append(parts, HardLine())
			}
		}
		if i > 0 && dropsLeadingBlankInCompactRun(nodes[i-1], n) {
			parts = append(parts, emitWithTriviaNoLeadingBlank(n))
		} else {
			parts = append(parts, emitWithTrivia(n))
		}
	}
	if len(fileEndTrivia) > 0 {
		parts = append(parts, emitFileEndTrivia(fileEndTrivia, true))
	}
	parts = append(parts, HardLine())
	doc := Concat(parts...)
	return Render(doc, defaultWidth)
}

// emitFileEndTrivia renders end-of-file trivia (after the last top-level
// declaration). Each comment emits on its own line preceded by HardLine();
// blank-line trivia inside the run emits an extra HardLine, so author-written
// gaps between the last decl and the trailing-comment block are preserved.
//
// hasPrecedingNode signals that at least one top-level node came before the
// trivia: in that case the leading HardLine separates the last node from
// the first trivia entry. With no preceding node (a trivia-only file), the
// first comment lands on line 1 directly.
func emitFileEndTrivia(trivia []ast.Trivia, hasPrecedingNode bool) Doc {
	parts := make([]Doc, 0, len(trivia)*2)
	emittedComment := false
	for _, t := range trivia {
		switch t.Kind {
		case ast.TriviaComment:
			if !emittedComment && !hasPrecedingNode {
				// First comment in a trivia-only file: no leading HardLine.
				parts = append(parts, Text(t.Text))
			} else {
				parts = append(parts, HardLine(), Text(t.Text))
			}
			emittedComment = true
		case ast.TriviaBlankLine:
			parts = append(parts, HardLine())
		}
	}
	return Concat(parts...)
}

// collapseSingleEntryBlocks rewrites any single-entry brace block
// (`import { std/io }`) to the per-statement form (`import std/io`).
// A one-item block is just a heavier spelling of a one-line import, so the
// canonical form is the standalone statement.
//
// It does NOT merge separate imports or unwrap multi-entry blocks — the two
// surface forms (per-line statements and the newline-separated
// `import { ... }` block) are both first-class and preserved as written.
//
// When a wrapper block collapses, its leading trivia (a file/section header
// comment above `import { ... }`) transfers to the surviving inner statement,
// prepended so it stays above any trivia the inner entry carried itself.
func collapseSingleEntryBlocks(nodes []ast.Node) []ast.Node {
	out := make([]ast.Node, 0, len(nodes))
	for _, n := range nodes {
		blk, ok := n.(*ast.ImportBlock)
		if !ok || blk.Go || len(blk.Entries) != 1 {
			out = append(out, n)
			continue
		}
		survivor := blk.Entries[0]
		if len(blk.Leading) > 0 {
			survivor.Leading = append(append([]ast.Trivia(nil), blk.Leading...), survivor.Leading...)
		}
		out = append(out, survivor)
	}
	return out
}

// wantsBlankBetween reports whether the formatter should enforce a blank line
// between two adjacent top-level declarations.
func wantsBlankBetween(prev, next ast.Node) bool {
	if isImportNode(prev) && isImportNode(next) {
		return false
	}
	return !wantsCompactSeparator(prev, next)
}

// wantsCompactSeparator reports whether two adjacent top-level declarations
// may form a tight run. A whitespace-only source gap between compact
// declarations remains a grouping boundary; comments/doc-comments/decorators
// also keep the declarations visually separated.
func wantsCompactSeparator(prev, next ast.Node) bool {
	if hasLeadingComment(next) {
		return false
	}
	prevKind, ok := compactDeclKind(prev)
	if !ok {
		return false
	}
	nextKind, ok := compactDeclKind(next)
	return ok && prevKind == nextKind
}

func dropsLeadingBlankInCompactRun(prev, next ast.Node) bool {
	if hasLeadingComment(next) {
		return false
	}
	prevKind, ok := compactDeclKind(prev)
	if !ok || !strings.HasPrefix(prevKind, "derive:") {
		return false
	}
	nextKind, ok := compactDeclKind(next)
	return ok && nextKind == prevKind
}

func isImportNode(n ast.Node) bool {
	switch n.(type) {
	case *ast.ImportStmt, *ast.ImportBlock:
		return true
	}
	return false
}

func compactDeclKind(n ast.Node) (string, bool) {
	switch v := n.(type) {
	case *ast.OnceBinding:
		if v.Doc != "" {
			return "", false
		}
		return "once", true
	case *ast.TypeDef:
		if v.Doc != "" || len(v.Decorators) > 0 || v.HasBody {
			return "", false
		}
		return "type", true
	case *ast.TypeAlias:
		if v.Doc != "" {
			return "", false
		}
		return "typealias", true
	case *ast.ExternType:
		if v.Doc != "" || len(v.Decorators) > 0 || v.HasBody {
			return "", false
		}
		return "host type", true
	case *ast.ExternFunc:
		if v.Doc != "" || v.ImplIface != nil {
			return "", false
		}
		return "host fn", true
	case *ast.ImplConformance:
		if !v.Derive || v.Doc != "" || len(v.AttachedTests) > 0 || v.Receiver == nil {
			return "", false
		}
		return "derive:" + v.Receiver.TypeString(), true
	}
	return "", false
}

// hasLeadingBlank reports whether n's leading trivia already begins with a
// blank-line trivia, in which case emitFile skips its own separator so we
// don't end up with two blank lines.
func hasLeadingBlank(n ast.Node) bool {
	ht, ok := n.(ast.HasTrivia)
	if !ok {
		return false
	}
	return hasBlankBeforeComments(ht.GetLeading())
}

// hasBlankBeforeComments reports whether leading trivia begins with a blank
// line, before any comment.
func hasBlankBeforeComments(leading []ast.Trivia) bool {
	for _, t := range leading {
		if t.Kind == ast.TriviaBlankLine {
			return true
		}
		if t.Kind == ast.TriviaComment {
			return false
		}
	}
	return false
}

// withoutBlankBeforeComments drops the blank lines at the start of leading
// trivia, keeping any between its comments.
func withoutBlankBeforeComments(leading []ast.Trivia) []ast.Trivia {
	for i, t := range leading {
		if t.Kind != ast.TriviaBlankLine {
			return leading[i:]
		}
	}
	return nil
}

func hasLeadingComment(n ast.Node) bool {
	ht, ok := n.(ast.HasTrivia)
	if !ok {
		return false
	}
	for _, t := range ht.GetLeading() {
		if t.Kind == ast.TriviaComment {
			return true
		}
	}
	return false
}

// emitWithTrivia wraps emit(n) with the node's leading and trailing trivia.
//
// Leading trivia emits above the node: a comment becomes its own line, a
// blank-line trivia becomes an extra HardLine that combines with the outer
// separator to produce a visible blank line.
//
// Trailing trivia emits after the node on the same line: `<node> // comment`.
//
// Nodes that do not implement HasTrivia pass through unchanged.
func emitWithTrivia(n ast.Node) Doc {
	return emitWithTriviaDoc(n, emit(n))
}

func emitWithTriviaNoLeadingBlank(n ast.Node) Doc {
	ht, ok := n.(ast.HasTrivia)
	if !ok {
		return emit(n)
	}
	return emitWithTriviaParts(emit(n), withoutBlankTrivia(ht.GetLeading()), ht.GetTrailing())
}

func emitWithTrailingTrivia(n ast.Node) Doc {
	ht, ok := n.(ast.HasTrivia)
	if !ok {
		return emit(n)
	}
	trailing := ht.GetTrailing()
	if len(trailing) == 0 {
		return emit(n)
	}
	parts := []Doc{emit(n)}
	for _, t := range trailing {
		if t.Kind == ast.TriviaComment {
			parts = append(parts, Hidden(Concat(Text(" "), Text(t.Text))))
		}
	}
	return Concat(parts...)
}

func emitLeadingTriviaDocs(trivia []ast.Trivia) []Doc {
	parts := make([]Doc, 0, len(trivia))
	for _, t := range trivia {
		switch t.Kind {
		case ast.TriviaComment:
			parts = append(parts, Text(t.Text))
		case ast.TriviaBlankLine:
			parts = append(parts, HardLine())
		}
	}
	return parts
}

// emitWithTriviaDoc is the internal variant used when the body Doc is already
// rendered by a specialized path (e.g. case-arm emit). It wraps `body` with
// whatever trivia the node carries.
func emitWithTriviaDoc(n ast.Node, body Doc) Doc {
	ht, ok := n.(ast.HasTrivia)
	if !ok {
		return body
	}
	return emitWithTriviaParts(body, ht.GetLeading(), ht.GetTrailing())
}

func emitWithTriviaParts(body Doc, leading []ast.Trivia, trailing []ast.Trivia) Doc {
	if len(leading) == 0 && len(trailing) == 0 {
		return body
	}
	parts := make([]Doc, 0, len(leading)*2+2+len(trailing)*2)
	// A `//#` module comment does not belong to what follows it, so a blank
	// line always separates a run of them from the next comment or the node.
	inModuleComment := false
	for _, t := range leading {
		switch t.Kind {
		case ast.TriviaComment:
			if inModuleComment && !isModuleComment(t.Text) {
				parts = append(parts, HardLine())
			}
			inModuleComment = isModuleComment(t.Text)
			// A standalone leading comment occupies its own line above the
			// node. HardLine after the comment so the node starts on the
			// next line.
			parts = append(parts, Text(t.Text), HardLine())
		case ast.TriviaBlankLine:
			// A blank line before the node becomes an extra HardLine in
			// addition to whatever separator the outer emit already has.
			inModuleComment = false
			parts = append(parts, HardLine())
		}
	}
	if inModuleComment {
		parts = append(parts, HardLine())
	}
	parts = append(parts, body)
	for _, t := range trailing {
		if t.Kind == ast.TriviaComment {
			// Same-line trailing comment: " // text". Wrapped in Hidden so
			// the comment doesn't influence ancestor Group fits decisions —
			// otherwise the formatter would break structural code (anon
			// structs, struct literals, function types, …) just to make
			// room for an explanatory comment, which is uglier than letting
			// the comment spill past the budget.
			parts = append(parts, Hidden(Concat(Text(" "), Text(t.Text))))
		}
	}
	return Concat(parts...)
}

// isModuleComment reports whether a comment is a `//#` module comment.
func isModuleComment(text string) bool {
	return strings.HasPrefix(text, "//#")
}

// emitEndTrivia renders trivia that sits at the end of a block (after the
// last statement but before the closing delimiter). Each comment goes on its
// own line preceded by a HardLine; blank-line trivia emits an extra HardLine.
// The caller is responsible for placing the closing delimiter afterward.
func emitEndTrivia(trivia []ast.Trivia) Doc {
	// Drop blank-line trivia sitting immediately before the closing delimiter:
	// a trailing blank at the bottom of a block is always noise, symmetric with
	// the leading blank after the opener (which emitWithTriviaDoc already drops)
	// and with a trailing blank after a comment. Only the run right before `}`
	// goes — blank lines between items keep their own trivia and are untouched.
	for len(trivia) > 0 && trivia[len(trivia)-1].Kind == ast.TriviaBlankLine {
		trivia = trivia[:len(trivia)-1]
	}
	if len(trivia) == 0 {
		return Text("")
	}
	parts := make([]Doc, 0, len(trivia)*2)
	for _, t := range trivia {
		switch t.Kind {
		case ast.TriviaComment:
			parts = append(parts, HardLine(), Text(t.Text))
		case ast.TriviaBlankLine:
			parts = append(parts, HardLine())
		}
	}
	return Concat(parts...)
}

// hasTrailingLineComment reports whether the node carries a trailing line
// comment. Line comments (`// ...`) consume everything to the end of the
// line, so any construct whose flat form would place tokens after the
// comment (like a single-stmt block's closing `}`) must break.
func hasTrailingLineComment(n ast.Node) bool {
	ht, ok := n.(ast.HasTrivia)
	if !ok {
		return false
	}
	for _, t := range ht.GetTrailing() {
		if t.Kind == ast.TriviaComment {
			return true
		}
	}
	// ExprStmt may carry the comment on the wrapped expression instead of
	// on the ExprStmt itself, depending on parser placement.
	if es, ok := n.(*ast.ExprStmt); ok && es.Expr != nil {
		return hasTrailingLineComment(es.Expr)
	}
	return false
}

// emit dispatches to a per-node-kind emit function. Unimplemented node kinds
// panic — later bundles fill in the remaining cases.
func emit(n ast.Node) Doc {
	switch v := n.(type) {
	case *ast.ExprStmt:
		return emit(v.Expr)
	case *ast.IntLit:
		// Round-trip the original lexeme so the user's choice of separators,
		// hex/binary/octal radix, etc. survives formatting. Synthetic IntLits
		// (no parser origin) fall back to plain base-10.
		if v.Lexeme != "" {
			return Text(v.Lexeme)
		}
		return Text(strconv.FormatInt(v.Value, 10))
	case *ast.RangeLit:
		return emitRangeLit(v)
	case *ast.FloatLit:
		if v.Lexeme != "" {
			return Text(v.Lexeme)
		}
		// strconv.FormatFloat with 'f'/-1 prints whole-number floats as "5"
		// (no decimal), which the Nomi lexer reads back as IntLit — a
		// semantic change. Always emit at least one fractional digit.
		s := strconv.FormatFloat(v.Value, 'f', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return Text(s)
	case *ast.DecimalLit:
		// Round-trip the source lexeme verbatim (incl. the trailing `d` and any
		// `_` separators) — scale is significant, so re-deriving the text would
		// risk dropping trailing zeros.
		return Text(v.Lexeme)
	case *ast.CodepointLit:
		// The body as written, escapes and all: `'\''` stays `'\''`.
		return Text("'" + v.Lexeme + "'")
	case *ast.StringLit:
		switch {
		case v.Raw && v.Triple:
			return emitRawTripleStringLit(v)
		case v.Raw:
			return emitRawStringLit(v)
		case v.Triple:
			return emitTripleStringLit(v)
		}
		// The lexer decodes escapes and strips quotes, so re-quote for output.
		// Use encodeNomiString so a literal `${` round-trips: the lexer reads
		// `${` as an interpolation opener, so the decoded value's `${` is
		// written `\${` on emission.
		return Text(encodeNomiString(v.Value))
	case *ast.Ident:
		return Text(v.Name)
	case *ast.TypeIdent:
		// Nomi's True/False are parsed as TypeIdent (uppercase identifiers).
		return Text(v.Name)
	case *ast.DotVariant:
		// Dot-leading variant shorthand (`.Red`, `.Circle(1.0)` via Call
		// wrapping). The leading dot is part of the surface syntax; round-
		// trips through the formatter verbatim.
		return Concat(Text("."), Text(v.Name))
	case *ast.FieldAccessor:
		// `.name`, `.address.city`: written as one token run, no spaces.
		return Text(v.Spelling())
	case *ast.Binding:
		if v.TypeAnnotation != nil {
			return emitBindingValue(Concat(Text(v.Name), Text(": "), emitTypeExpr(v.TypeAnnotation)), v.Value)
		}
		return emitBindingValue(Text(v.Name), v.Value)
	case *ast.Binary:
		op := v.Op
		if op == "|>" {
			return emitPipeChain(v)
		}
		p := precedence(op)
		// Left child: all Nomi binary operators are left-associative
		// (parser uses prec+1 for the right operand), so a left child at
		// the same precedence level doesn't need parens.
		left := parenIfLooser(v.Left, p)
		// Right child: for left-associative operators, a right child at
		// the same precedence level DOES need parens to preserve the
		// tree shape (e.g., `1 - (2 - 3)`).
		var right Doc
		if b, ok := v.Right.(*ast.Binary); ok && b.Op != "|>" && precedence(b.Op) <= p {
			right = Concat(Text("("), emit(v.Right), Text(")"))
		} else {
			right = parenIfLooser(v.Right, p)
		}
		return Concat(left, Text(" "), Text(op), Text(" "), right)
	case *ast.GroupedExpr:
		return emitGroupedExpr(v)
	case *ast.Unary:
		// Unary binds tighter than any binary; parenthesize any binary child.
		return Concat(Text(v.Op), parenIfLooser(v.Right, unaryPrecedence))
	case *ast.Block:
		// Flat form:   "{ expr }"
		// Broken form: "{\n  stmt1\n  stmt2\n  ...\n}"
		//
		// Nest wraps only the CONTENT, not the delimiters.
		endTrivia := v.GetTrailing()
		if len(v.Stmts) == 0 {
			// Blank-only end trivia is stripped (emitEndTrivia), so a block
			// holding only blank lines is empty — `{}`. Only a comment keeps
			// the broken form below.
			if !trailingHasComment(endTrivia) {
				return Text("{}")
			}
			// Block with only trivia (e.g. comments between `{` and `}`).
			body := emitEndTrivia(endTrivia)
			return Concat(
				Text("{"),
				Nest(defaultIndent, body),
				HardLine(),
				Text("}"),
			)
		}
		// A single statement with no end-of-block *comment* is flat-eligible —
		// trailing blank lines are stripped, so they don't force the broken
		// form (which would otherwise collapse only on a second pass).
		if len(v.Stmts) == 1 && !trailingHasComment(endTrivia) {
			inner := emitWithTrivia(v.Stmts[0])
			// A trailing line comment on the statement consumes everything
			// up to the next newline — flat form would swallow the closing
			// `}`. Force the broken form by using HardLines.
			if hasTrailingLineComment(v.Stmts[0]) {
				return Group(Concat(
					Text("{"),
					Nest(defaultIndent, Concat(HardLine(), inner)),
					HardLine(),
					Text("}"),
				))
			}
			return Group(Concat(
				Text("{"),
				Nest(defaultIndent, Concat(Line(), inner)),
				Line(),
				Text("}"),
			))
		}
		// Multi-stmt (or single-stmt with end-of-block trivia): HardLines between stmts
		// force broken layout.
		body := emitStatementDocs(v.Stmts)
		if len(endTrivia) > 0 {
			body = append(body, emitEndTrivia(endTrivia))
		}
		return Group(Concat(
			Text("{"),
			Nest(defaultIndent, Concat(HardLine(), Concat(body...))),
			HardLine(),
			Text("}"),
		))
	case *ast.Lambda:
		return emitLambda(v)
	case *ast.FieldAccess:
		return Concat(emit(v.Object), Text("."), Text(v.Field.Name))
	case *ast.NamedArg:
		// NamedArg only legally appears inside a Call's Args. Emitting it
		// directly as an expression preserves "name: value" shape; the Call
		// emitter relies on this path.
		return Concat(Text(v.Name), Text(": "), emit(v.Value))
	case *ast.Call:
		return emitCall(v)
	case *ast.Case:
		return emitCase(v)
	case *ast.StructLit:
		return emitStructLit(v)
	case *ast.ListLit:
		open := "["
		if v.TypeName != nil {
			open = v.TypeName.TypeString() + "["
		}
		hasEndTrivia := len(v.EndTrivia) > 0
		// Chain-aware break: when this compound's own depth crosses the
		// threshold, render in broken form AND propagate mustBreak to every
		// child so nested compounds in the same chain also break. See the
		// emitChildMustBreak comment for the pass-through rules (wrappers
		// like Ok(...) propagate but don't break themselves).
		if compoundDepth(v) >= maxCompoundDepth && len(v.Items) > 0 {
			elems := make([]Doc, 0, len(v.Items))
			for _, it := range v.Items {
				elems = append(elems, emitChildMustBreak(it))
			}
			return forceBrokenBracedWithEndTrivia(open, "]", elems, v.EndTrivia)
		}
		elems := make([]Doc, 0, len(v.Items))
		for _, it := range v.Items {
			elems = append(elems, emit(it))
		}
		// EndTrivia (trailing in-body comment) forces the broken form —
		// a `// comment` cannot share a line with the closing `]`.
		if hasEndTrivia {
			return forceBrokenBracedWithEndTrivia(open, "]", elems, v.EndTrivia)
		}
		return emitBracedList(open, "]", elems)
	case *ast.VectorLit:
		hasEndTrivia := len(v.EndTrivia) > 0
		if compoundDepth(v) >= maxCompoundDepth && len(v.Items) > 0 {
			elems := make([]Doc, 0, len(v.Items))
			for _, it := range v.Items {
				elems = append(elems, emitChildMustBreak(it))
			}
			return forceBrokenBracedWithEndTrivia("#[", "]", elems, v.EndTrivia)
		}
		elems := make([]Doc, 0, len(v.Items))
		for _, it := range v.Items {
			elems = append(elems, emit(it))
		}
		if hasEndTrivia {
			return forceBrokenBracedWithEndTrivia("#[", "]", elems, v.EndTrivia)
		}
		return emitBracedList("#[", "]", elems)
	case *ast.SetLit:
		hasEndTrivia := len(v.EndTrivia) > 0
		if compoundDepth(v) >= maxCompoundDepth && len(v.Items) > 0 {
			elems := make([]Doc, 0, len(v.Items))
			for _, it := range v.Items {
				elems = append(elems, emitChildMustBreak(it))
			}
			return forceBrokenBracedWithEndTrivia("#{", "}", elems, v.EndTrivia)
		}
		elems := make([]Doc, 0, len(v.Items))
		for _, it := range v.Items {
			elems = append(elems, emit(it))
		}
		if hasEndTrivia {
			return forceBrokenBracedWithEndTrivia("#{", "}", elems, v.EndTrivia)
		}
		return emitBracedList("#{", "}", elems)
	case *ast.ListSpreadLit:
		// [head1, head2, ..tail] — emit flat with literal delimiters. We
		// don't bother with the broken form: spread literals are
		// conventionally short and breaking around the `..` reads poorly.
		parts := make([]Doc, 0, len(v.Heads)*2+3)
		parts = append(parts, Text("["))
		for i, h := range v.Heads {
			if i > 0 {
				parts = append(parts, Text(", "))
			}
			parts = append(parts, emit(h))
		}
		if len(v.Heads) > 0 {
			parts = append(parts, Text(", "))
		}
		parts = append(parts, Text(".."), emit(v.TailSpread), Text("]"))
		return Concat(parts...)
	case *ast.MapLit:
		open := "{"
		if v.TypeName != nil {
			open = v.TypeName.TypeString() + "{"
		}
		hasEndTrivia := len(v.EndTrivia) > 0
		// Chain-aware break: see ListLit above.
		if compoundDepth(v) >= maxCompoundDepth && len(v.Entries) > 0 {
			elems := make([]Doc, 0, len(v.Entries))
			for _, e := range v.Entries {
				elems = append(elems, Concat(emit(e.Key), Text(" => "), emitChildMustBreak(e.Value)))
			}
			return forceBrokenBracedWithEndTrivia(open, "}", elems, v.EndTrivia)
		}
		elems := make([]Doc, 0, len(v.Entries))
		for _, e := range v.Entries {
			elems = append(elems, Concat(emit(e.Key), Text(" => "), emit(e.Value)))
		}
		// EndTrivia (trailing in-body comment) forces the broken form.
		if hasEndTrivia {
			return forceBrokenBracedWithEndTrivia(open, "}", elems, v.EndTrivia)
		}
		return emitBracedList(open, "}", elems)
	case *ast.TupleLit:
		elems := make([]Doc, 0, len(v.Items))
		for _, it := range v.Items {
			elems = append(elems, emit(it))
		}
		return emitBracedList("(", ")", elems)
	case *ast.FuncDef:
		return emitFuncDef(v)
	case *ast.If:
		return emitIf(v)
	case *ast.With:
		return emitWith(v)
	case *ast.Defer:
		return emitDefer(v)
	case *ast.TestDecl:
		return emitTestDecl(v)
	case *ast.Assertion:
		return emitAssertion(v)
	case *ast.Dbg:
		return emitDbg(v)
	case *ast.Todo:
		return emitTodo(v)
	case *ast.ConcurrentBlock:
		return emitConcurrentBlock(v)
	case *ast.StructDef:
		return emitStructDef(v)
	case *ast.EnumDef:
		return emitEnumDef(v)
	case *ast.InterfaceDef:
		return emitInterfaceDef(v)
	case *ast.ImplBlock:
		return emitImplBlock(v)
	case *ast.ImplConformance:
		return emitImplConformance(v)
	case *ast.TypeAlias:
		return emitTypeAlias(v)
	case *ast.TypeDef:
		return emitTypeDef(v)
	case *ast.ExternFunc:
		return emitExternFunc(v)
	case *ast.ExternType:
		return emitExternType(v)
	case *ast.ExternPackage:
		return emitExternPackage(v)
	case *ast.GoBlock:
		return emitGoBlock(v)
	case *ast.StringInterp:
		return emitStringInterp(v)
	case *ast.TaggedString:
		return emitTaggedString(v)
	case *ast.TryOp:
		if v.Expr == nil {
			return Text("try")
		}
		return Concat(Text("try "), emit(v.Expr))
	case *ast.Return:
		if v.Value == nil {
			return Text("return")
		}
		return Concat(Text("return "), emit(v.Value))
	case *ast.Break:
		if v.Value == nil {
			return Text("break")
		}
		return Concat(Text("break "), emit(v.Value))
	case *ast.Continue:
		if v.Value == nil {
			return Text("continue")
		}
		return Concat(Text("continue "), emit(v.Value))
	case *ast.Placeholder:
		return Text("_")
	case *ast.ImportStmt:
		return emitImport(v)
	case *ast.ImportBlock:
		return emitImportBlock(v)
	case *ast.OnceBinding:
		return emitOnceBinding(v)
	case *ast.TupleDestructure:
		// (a, b, _) = expr — bindings are *Ident or nil (wildcard).
		parts := make([]Doc, 0, len(v.Bindings)*2+3)
		parts = append(parts, Text("("))
		for i, b := range v.Bindings {
			if i > 0 {
				parts = append(parts, Text(", "))
			}
			if b == nil {
				parts = append(parts, Text("_"))
			} else {
				parts = append(parts, Text(b.Name))
			}
		}
		parts = append(parts, Text(")"))
		return emitBindingValue(Concat(parts...), v.Value)
	case *ast.StructDestructure:
		// {x, y} = expr  or  {x: a, y: b} = expr — anonymous only.
		// Reuse emitPattern by wrapping in a StructPattern with no TypeName.
		pat := &ast.StructPattern{Fields: v.Fields}
		return emitBindingValue(emitPattern(pat), v.Value)
	case *ast.MapDestructure:
		// {"k" => binding, ...} = expr.
		pat := &ast.MapPattern{Entries: v.Entries}
		return emitBindingValue(emitPattern(pat), v.Value)
	case *ast.PatternBinding:
		return emitPatternBinding(v)
	case *ast.PatternDestructure:
		if pipe, ok := v.Value.(*ast.Binary); ok && pipe.Op == "|>" {
			source, steps := collectPipeChain(pipe)
			prefix := Concat(Text("assert "), emitPattern(v.Pattern), Text(" = "), emitWithTrivia(source))
			return emitPrefixedPipeParts(prefix, source, steps, false)
		}
		return Concat(Text("assert "), emitPattern(v.Pattern), Text(" = "), emit(v.Value))
	case *ast.DistinctDestructure:
		// TypeName(binding) = expr, or TypeName(_) = expr for wildcard.
		inner := Text("_")
		if v.Binding != nil {
			inner = Text(v.Binding.Name)
		}
		typeName := Text(v.TypeName)
		if v.TypeNameExpr != nil {
			typeName = emitTypeExpr(v.TypeNameExpr)
		}
		return emitBindingValue(Concat(typeName, Text("("), inner, Text(")")), v.Value)
	}
	panic("unimplemented: " + n.NodeType())
}

// emitBindingValue renders `target = value` for a binding and every
// destructuring form. A pipe value breaks after `=` and indents the pipe
// block so the source and each `|>` align one level in (a multi-stage pipe
// always breaks; a single pipe that fits stays inline via the Group).
func emitBindingValue(target Doc, value ast.Node) Doc {
	if b, ok := value.(*ast.Binary); ok && b.Op == "|>" {
		return Group(Concat(target, Text(" ="), Nest(defaultIndent, Concat(Line(), emitPipeChain(b)))))
	}
	return Concat(target, Text(" = "), emit(value))
}

// emitPatternBinding renders `Pattern = value [else { ... }]`.
//
// A plain else block of one statement stays on the binding's line when the
// whole statement is at most singleLineBindingElseMaxWidth wide and fits the
// line (`Some(e) = email else { "none" }`, `Ok(x) = parse(s) else { continue }`);
// otherwise the block breaks, its statements on the lines below. Else arms
// always break, laid out as a case's arms are.
//
// A pipe value that breaks after `=` puts the `else` on its own line at the
// statement's indent, below the last stage.
func emitPatternBinding(v *ast.PatternBinding) Doc {
	target := emitPattern(v.Pattern)
	if v.Else == nil {
		return emitBindingValue(target, v.Value)
	}
	// head is everything up to the else's `{`, `else ` included.
	var head Doc
	if pipe, ok := v.Value.(*ast.Binary); ok && pipe.Op == "|>" {
		head = Group(Concat(target, Text(" ="),
			Nest(defaultIndent, Concat(Line(), emitPipeChain(pipe))),
			IfBroken(Text(" "), HardLine()),
			Text("else ")))
	} else {
		head = Concat(target, Text(" = "), emit(v.Value), Text(" else "))
	}
	if block := v.Else.Block; block != nil {
		broken := Concat(head, emitBlockForceBroken(block))
		if !isInlineableBlockBody(block) {
			return broken
		}
		flat := Concat(head, emit(block))
		if !fits(flat, nil, singleLineBindingElseMaxWidth) {
			return broken
		}
		return Group(IfBroken(flat, broken))
	}
	return Concat(head, Text("{"),
		Nest(defaultIndent, emitCaseArms(v.Else.Arms, v.Else.GetTrailing())),
		HardLine(),
		Text("}"),
	)
}

// emitStringInterp renders an interpolated string. The lexer decoded escape
// sequences in text parts during scanning, so we must re-encode them to
// produce valid Nomi source. Interpolation uses `${expr}`; a bare `$` or `{`
// in a text segment is literal (no escaping needed), and a literal `${` is
// written `\${` so the lexer doesn't mistake it for an interpolation opener.
//
// Implementation note: text parts go through strlit.EncodeStringBody (via
// encodeStringInterpText), which emits only escapes the Nomi lexer reads back.
//
// For triple-quoted (`v.Triple == true`), the structure is different —
// real newlines, no escape encoding, only `${` escaping. See
// emitTripleStringInterp.
func emitStringInterp(v *ast.StringInterp) Doc {
	if v.Triple {
		return emitTripleStringInterp(v)
	}
	parts := []Doc{Text("\"")}
	for _, p := range v.Parts {
		switch part := p.(type) {
		case ast.StringText:
			parts = append(parts, Text(encodeStringInterpText(part.Value)))
		case ast.StringExpr:
			parts = append(parts, Text("${"), emitInterpolatedExpr(part.Expr), Text("}"))
		}
	}
	parts = append(parts, Text("\""))
	return Concat(parts...)
}

// emitInterpolatedExpr renders the expression of a `${...}`. A pipeline
// there keeps the line layout it was written with: a lambda stage stacks a
// pipeline elsewhere, but a line break would split a one-line string.
func emitInterpolatedExpr(n ast.Node) Doc {
	if pipe, ok := n.(*ast.Binary); ok && pipe.Op == "|>" {
		return emitPipeChainWithMode(pipe, pipeStackAsWritten)
	}
	return emit(n)
}

// emitTripleStringLit renders a triple-quoted non-interpolated string.
//
// Canonical shape:
//
//	"""
//	  line1
//	  line2
//	  """
//
// where each `  ` is `defaultIndent` spaces of additional indent beyond the
// current pretty-printer indent. The opening `"""` sits on the current
// line; a HardLine immediately follows; each line of the decoded value
// renders as `Text(line)` followed by a HardLine; the closing `"""` sits
// on its own line at the body indent. Wrapping the body and closing in
// `Nest(defaultIndent, ...)` is what bumps every embedded HardLine to the
// body-indent column — this is the formatter's existing indent mechanism,
// not a hand-rolled string of spaces.
//
// The lexer strips the indent of the line preceding the closing `"""`
// from every body line, so the closing-quote indent and the body indent
// must match for the round-trip to be lossless. Both come from the same
// Nest, so they always agree.
//
// Content encoding: triple-quoted source preserves real newlines, real
// `"`, real `\`, etc. The only re-encoding rule is `${` -> `\${`, which is
// what `escapeInterpolationOpeners` does. We do NOT use strconv.Quote here — that would
// turn newlines into `\n` and defeat the whole purpose of the triple form.
func emitTripleStringLit(v *ast.StringLit) Doc {
	body := tripleBodyDoc(encodeTripleStringContent(v.Value))
	return tripleStringFraming("", body, defaultIndent)
}

// tripleStringFraming wraps a body Doc in `<prefix>"""\n  <body>\n  """`,
// using a Nest to thread the body indent through the pretty-printer
// rather than emitting hand-rolled spaces. `prefix` is empty for plain
// triple-quoted strings; raw backtick strings use rawBacktickFraming.
// framing. `bodyIndent` is how many spaces beyond the current pretty-
// printer indent the body and closing `"""` sit at — `defaultIndent` for
// the default case (binding RHS, struct field value, etc.), `0` for the
// call-arg case (content-at-opening's-column; see emitCallArg).
func tripleStringFraming(prefix string, body Doc, bodyIndent int) Doc {
	return Concat(
		Text(prefix+`"""`),
		Nest(bodyIndent, Concat(
			HardLine(),
			body,
			HardLine(),
			Text(`"""`),
		)),
	)
}

// emitTripleStringInterp renders a triple-quoted interpolated string. Same
// shape as emitTripleStringLit; static text parts are split on `\n` so each
// real newline becomes a HardLine and gets re-indented through the Nest,
// and dynamic parts emit as `${ <expr> }`. Static text content is run
// through encodeTripleStringContent so a literal `${` round-trips.
func emitTripleStringInterp(v *ast.StringInterp) Doc {
	var bodyParts []Doc
	for _, p := range v.Parts {
		switch part := p.(type) {
		case ast.StringText:
			bodyParts = append(bodyParts, tripleBodyDoc(encodeTripleStringContent(part.Value)))
		case ast.StringExpr:
			bodyParts = append(bodyParts, Text("${"), emitInterpolatedExpr(part.Expr), Text("}"))
		}
	}
	return tripleStringFraming("", Concat(bodyParts...), defaultIndent)
}

// emitTaggedString renders a typed literal `<tag>"..."` (or its triple
// and raw variants) by reusing the existing string emission machinery
// and prefixing the appropriate tag. The shape is fixed by source order:
//
//   - single-line, regular: `<tag>"<encoded body>"`
//   - single-line, raw:     `<tag>`<verbatim body>“
//   - triple-quoted, regular: `<tag>"""\n  <body>\n  """`
//   - multi-line, raw:     `<tag>`\n  <verbatim body>\n  “
//
// The emit logic mirrors emitStringInterp / emitTripleStringInterp /
// emitRawStringLit / emitRawTripleStringLit one-for-one — the only
// difference is the tag prefix and (for raw forms) the lack of hash
// escaping.
func emitTaggedString(v *ast.TaggedString) Doc {
	prefix := v.Tag
	if v.Triple {
		// Triple-quoted: walk Parts emitting body Doc(s) and frame
		// with `<prefix>"""\n  <body>\n  """`. Raw multi-line tagged
		// literals (Raw && Triple) carry exactly one StringText so
		// the loop produces a single body chunk with no hash escape.
		var bodyParts []Doc
		for _, p := range v.Parts {
			switch part := p.(type) {
			case ast.StringText:
				if v.Raw {
					bodyParts = append(bodyParts, tripleBodyDoc(part.Value))
				} else {
					bodyParts = append(bodyParts, tripleBodyDoc(encodeTripleStringContent(part.Value)))
				}
			case ast.StringExpr:
				bodyParts = append(bodyParts, Text("${"), emitInterpolatedExpr(part.Expr), Text("}"))
			}
		}
		if v.Raw {
			return rawBacktickFraming(prefix, Concat(bodyParts...), defaultIndent)
		}
		return tripleStringFraming(prefix, Concat(bodyParts...), defaultIndent)
	}
	// Single-line: walk Parts emitting `<prefix>"<encoded body>"`.
	// Raw single-line typed literals carry exactly one StringText
	// emitted verbatim — same invariant as emitRawStringLit.
	if v.Raw {
		var body string
		for _, p := range v.Parts {
			if part, ok := p.(ast.StringText); ok {
				body += part.Value
			}
		}
		if strings.Contains(body, "`") {
			panic("emitTaggedString: raw single-line typed literal body contains a literal backtick (lexer/AST invariant violation): " + body)
		}
		return Text(prefix + "`" + body + "`")
	}
	parts := []Doc{Text(prefix + "\"")}
	for _, p := range v.Parts {
		switch part := p.(type) {
		case ast.StringText:
			parts = append(parts, Text(encodeStringInterpText(part.Value)))
		case ast.StringExpr:
			parts = append(parts, Text("${"), emitInterpolatedExpr(part.Expr), Text("}"))
		}
	}
	parts = append(parts, Text("\""))
	return Concat(parts...)
}

// emitRawStringLit renders a single-line raw string “ `<body>` “.
//
// Raw single-line strings have no escape mechanism — the lexer reads
// bytes verbatim until the next backtick, so the body cannot contain a backtick.
// We therefore emit the body as-is, with no `${` escaping, no
// `strconv.Quote`. A `${` in the body re-lexes as the literal `${`
// rather than an interpolation opener.
//
// AST invariant: `v.Value` cannot contain a backtick (the lexer wouldn't have
// produced this StringLit otherwise). If somehow it does, we panic
// with a clear message — that indicates a lexer/AST bug, not a
// recoverable formatter situation.
func emitRawStringLit(v *ast.StringLit) Doc {
	if strings.Contains(v.Value, "`") {
		panic("emitRawStringLit: raw single-line StringLit body contains a literal backtick (lexer/AST invariant violation): " + v.Value)
	}
	return Text("`" + v.Value + "`")
}

// emitRawTripleStringLit renders a raw multi-line backtick string.
//
// Same framing as the non-raw triple form (Nest-based body indent,
// HardLine boundaries promoted from `\n`s in the body), but the body
// content is emitted verbatim — no `${` escaping and no other
// transformation. A literal `${name}` in the source becomes `${name}`
// in the AST `Value` (the lexer is in raw mode, so it neither parsed
// the slot nor read an escape), and we re-emit it as `${name}` here.
// On re-lex the backtick opener again puts the lexer into raw mode, so
// the round-trip is lossless.
func emitRawTripleStringLit(v *ast.StringLit) Doc {
	body := tripleBodyDoc(v.Value)
	return rawBacktickFraming("", body, defaultIndent)
}

func rawBacktickFraming(prefix string, body Doc, bodyIndent int) Doc {
	return Concat(
		Text(prefix+"`"),
		Nest(bodyIndent, Concat(
			HardLine(),
			body,
			HardLine(),
			Text("`"),
		)),
	)
}

// tripleBodyDoc converts an already-hash-escaped string into a Doc whose
// `\n` boundaries have been promoted to HardLines, so the Nest re-indents
// each body line to the chosen body-indent column.
func tripleBodyDoc(s string) Doc {
	if s == "" {
		return Nil()
	}
	lines := strings.Split(s, "\n")
	parts := make([]Doc, 0, len(lines)*2-1)
	for i, line := range lines {
		if i > 0 {
			parts = append(parts, HardLine())
		}
		if line != "" {
			parts = append(parts, Text(line))
		}
	}
	return Concat(parts...)
}

// encodeTripleStringContent re-encodes a decoded triple-quoted body
// segment so it round-trips through the lexer.
//
// Triple-quoted source has one re-encoding rule: a literal `${` is written
// `\${` so a re-lex reads it as text rather than the start of an
// interpolation slot. Newlines, quotes, and other backslashes are literal in
// triple-quoted form.
func encodeTripleStringContent(s string) string {
	return escapeInterpolationOpeners(s)
}

// encodeNomiString re-encodes a decoded string into Nomi source form: a quoted
// single-line literal whose body is escaped by strlit.EncodeStringBody, with
// every literal `${` written `\${` so the round-trip is lossless. A `$` or a
// `{` alone is ordinary text.
func encodeNomiString(s string) string {
	return `"` + encodeStringInterpText(s) + `"`
}

// encodeStringInterpText re-encodes a decoded text segment for embedding in
// an interpolated string literal. Same rules as encodeNomiString, but minus
// the outer quotes (the segment is concatenated between them by the caller).
func encodeStringInterpText(s string) string {
	return escapeInterpolationOpeners(strlit.EncodeStringBody(s))
}

// escapeInterpolationOpeners writes each literal `${` as `\${`, the one
// spelling the lexer reads back as text rather than an interpolation.
func escapeInterpolationOpeners(s string) string {
	return strings.ReplaceAll(s, "${", `\${`)
}

// emitIf renders an if/else expression.
//
// Shape (flat): `if <cond> { <then> } else { <else> }`
// Shape (broken):
//
//	if <cond> {
//	  <then>
//	} else {
//	  <else>
//	}
//
// Layout rules:
//
//   - `else if` chains (any chain with at least one `else if` rung) ALWAYS
//     render in broken form, even when the flat form would fit. Multi-rung
//     conditionals read better with each rung on its own line.
//   - A plain `if`/`else` renders inline iff each branch is a single-statement
//     block with no comments AND the flat one-line form has no forced break
//     and fits singleLineIfElseMaxWidth (50) columns; otherwise it breaks.
//     This is width-based and canonical — branch *content* doesn't matter (a
//     call or binary op inlines when short), and how the source was typed is
//     ignored (a short multi-line if/else collapses to one line). Inherently
//     multi-line branches (case, multi-stmt block, multi-stage pipe) carry a
//     HardLine that the fits check rejects, so they never render inline.
func emitIf(v *ast.If) Doc {
	if hasElseIf(v) {
		return emitIfForceBroken(v)
	}
	inner := emitIfInner(v)
	if ifBranchesInlineable(v) && fits(inner, nil, singleLineIfElseMaxWidth) {
		// Inline-eligible: flatten when it also fits the current line, else
		// fall back to the force-broken layout. Routing the broken case
		// through emitIfForceBroken keeps it strictly all-or-nothing — no
		// partial layout where one branch breaks and the other stays inline
		// (which independent inner-block Groups would otherwise produce).
		return Group(IfBroken(inner, emitIfForceBroken(v)))
	}
	return emitIfForceBroken(v)
}

// emitWith renders the statement `with Type.field = value`. A struct
// literal value needs no parentheses, so any written around one are dropped.
func emitWith(v *ast.With) Doc {
	value := v.Value
	if g, ok := value.(*ast.GroupedExpr); ok {
		if lit, ok := g.Expr.(*ast.StructLit); ok {
			value = lit
		}
	}
	return Group(Concat(Text("with "), emit(v.Target), Text(" = "), emit(value)))
}

// emitDefer renders `defer call(...)`.
func emitDefer(v *ast.Defer) Doc {
	return Group(Concat(Text("defer "), emit(v.Call)))
}

func emitTestDecl(v *ast.TestDecl) Doc {
	if !v.Group {
		header := Concat(Text("test "), Text(encodeNomiString(v.Name)))
		if v.ContextPattern != nil {
			header = Concat(header, Text(", "), emitPattern(v.ContextPattern))
		}
		return Concat(header, Text(" "), emitBlockForceBroken(v.Body))
	}

	// The clock, boot and setup lines lead the group's statements, in the
	// order they run, and are laid out with them; the tests follow in source
	// order. The source may write the lines anywhere in the group, so each
	// line's comments travel with it. A blank line always follows the clock.
	type groupLine struct {
		node       ast.Node // nil for the clock, which is not a node
		doc        Doc
		leading    []ast.Trivia
		trailing   []ast.Trivia
		srcLine    int
		blankAfter bool
	}
	lines := make([]groupLine, 0, len(v.Body.Stmts)+3)
	trivia := func(n ast.Node) ([]ast.Trivia, []ast.Trivia) {
		if ht, ok := n.(ast.HasTrivia); ok {
			return ht.GetLeading(), ht.GetTrailing()
		}
		return nil, nil
	}
	if v.Clock != nil {
		lines = append(lines, groupLine{
			doc:        Group(Concat(Text("clock "), emit(v.Clock))),
			leading:    v.ClockLeading,
			trailing:   v.ClockTrailing,
			srcLine:    v.ClockLine,
			blankAfter: true,
		})
	}
	if v.Boot != nil {
		leading, trailing := trivia(v.Boot)
		lines = append(lines, groupLine{node: v.Boot, doc: emitTestBoot(v.Boot), leading: leading, trailing: trailing, srcLine: v.BootLine})
	}
	if v.Setup != nil {
		if block, ok := v.Setup.(*ast.Block); ok {
			// The comments above the line are the line's. A written block
			// keeps its trailing trivia, which holds the comments before
			// its closing brace; a synthesized one (`setup expr`, no
			// closing brace) holds only the line's trailing comment.
			bare := *block
			line := &ast.Block{Line: block.Line, Col: block.Col}
			line.Leading = block.Leading
			bare.Leading = nil
			if block.EndLine == 0 {
				line.Trailing = block.Trailing
				bare.Trailing = nil
			}
			lines = append(lines, groupLine{node: line, doc: emitTestSetup(&bare), leading: line.Leading, trailing: line.Trailing, srcLine: v.SetupLine})
		} else {
			leading, trailing := trivia(v.Setup)
			lines = append(lines, groupLine{node: v.Setup, doc: emitTestSetup(v.Setup), leading: leading, trailing: trailing, srcLine: v.SetupLine})
		}
	}
	for _, stmt := range v.Body.Stmts {
		leading, trailing := trivia(stmt)
		lines = append(lines, groupLine{node: stmt, doc: emit(stmt), leading: leading, trailing: trailing, srcLine: stmt.LineNum()})
	}

	// A blank line the source wrote above a line separated it from the line
	// written before it. It is kept only when that line still comes before
	// it; a line that follows the clock never keeps one, since the clock's
	// blank line stands for it.
	sourcePrev := func(i int) int {
		prev := -1
		for j, other := range lines {
			if other.srcLine < lines[i].srcLine && (prev < 0 || other.srcLine > lines[prev].srcLine) {
				prev = j
			}
		}
		return prev
	}
	items := make([]statementItem, 0, len(lines))
	for i, line := range lines {
		item := statementItem{
			node:        line.node,
			doc:         line.doc,
			full:        emitWithTriviaParts(line.doc, line.leading, line.trailing),
			authorBlank: hasBlankBeforeComments(line.leading),
			blankAfter:  line.blankAfter,
		}
		switch {
		case i > 0 && lines[i-1].blankAfter:
			item.full = emitWithTriviaParts(line.doc, withoutBlankTrivia(line.leading), line.trailing)
			item.authorBlank = false
		case line.srcLine > 0 && sourcePrev(i) != i-1:
			item.full = emitWithTriviaParts(line.doc, withoutBlankBeforeComments(line.leading), line.trailing)
			item.authorBlank = false
		}
		items = append(items, item)
	}
	var body []Doc
	if len(items) > 0 {
		body = []Doc{emitStatementSequence(items)}
	}
	return Concat(Text("tests "), Text(encodeNomiString(v.Name)), Text(" "), emitDocsBlockForceBroken(body, v.Body.GetTrailing()))
}

// emitTestBoot renders a group's `boot server.boot(startup)` line.
func emitTestBoot(expr ast.Node) Doc {
	return Group(Concat(Text("boot "), emit(expr)))
}

func emitTestSetup(expr ast.Node) Doc {
	header := Text("setup")
	if block, ok := expr.(*ast.Block); ok {
		if single := singlePlainExprStmt(block); single != nil {
			return Group(Concat(header, Text(" "), emit(single)))
		}
		return Concat(header, Text(" "), emitBlockForceBroken(block))
	}
	return Group(Concat(header, Text(" "), emit(expr)))
}

func singlePlainExprStmt(block *ast.Block) ast.Node {
	if block == nil || len(block.Stmts) != 1 || hasNonBlankTrivia(block.GetLeading()) || len(block.GetTrailing()) > 0 {
		return nil
	}
	stmt := block.Stmts[0]
	if carrier, ok := stmt.(ast.HasTrivia); ok && (len(carrier.GetLeading()) > 0 || len(carrier.GetTrailing()) > 0) {
		return nil
	}
	if es, ok := stmt.(*ast.ExprStmt); ok {
		return es.Expr
	}
	return nil
}

func hasNonBlankTrivia(trivia []ast.Trivia) bool {
	for _, t := range trivia {
		if t.Kind != ast.TriviaBlankLine {
			return true
		}
	}
	return false
}

func emitAssertion(v *ast.Assertion) Doc {
	kw := "assert"
	if v.Refute {
		kw = "refute"
	}
	if v.Expr == nil {
		return Text(kw)
	}
	if pipe, ok := v.Expr.(*ast.Binary); ok && pipe.Op == "|>" {
		return emitKeywordPrefixedPipe(kw, pipe)
	}
	return Group(Concat(Text(kw), Text(" "), emit(v.Expr)))
}

func emitGroupedExpr(v *ast.GroupedExpr) Doc {
	if v == nil || v.Expr == nil {
		return Text("()")
	}
	if inner, ok := v.Expr.(*ast.GroupedExpr); ok {
		return emitGroupedExpr(inner)
	}
	if groupedExprCanDropParens(v.Expr) {
		return emit(v.Expr)
	}
	return Concat(Text("("), emit(v.Expr), Text(")"))
}

func groupedExprCanDropParens(n ast.Node) bool {
	switch n.(type) {
	case *ast.Ident, *ast.TypeIdent, *ast.IntLit, *ast.FloatLit, *ast.DecimalLit, *ast.CodepointLit,
		*ast.StringLit, *ast.StringInterp, *ast.TaggedString, *ast.DotVariant,
		*ast.Call, *ast.FieldAccess:
		return true
	default:
		return false
	}
}

func emitDbg(v *ast.Dbg) Doc {
	if v.Expr == nil {
		return Text("dbg")
	}
	return Group(Concat(Text("dbg "), emit(v.Expr)))
}

// emitTodo renders `todo` and `todo "reason"`, keeping the reason's source
// form (plain, triple-quoted or raw).
func emitTodo(v *ast.Todo) Doc {
	if v.Reason == nil {
		return Text("todo")
	}
	return Concat(Text("todo "), emit(v.Reason))
}

// emitConcurrentBlock renders `concurrent { body }` — spec §20 layer 1's
// structured-concurrency scope. An atomic body (one statement that fits on a
// line, like `concurrent { 42 }`) stays inline; anything multi-statement breaks
// the body onto its own lines.
func emitConcurrentBlock(v *ast.ConcurrentBlock) Doc {
	header := Text("concurrent ")
	if isAtomicBlockBody(v.Body) {
		return Group(Concat(header, emit(v.Body)))
	}
	return Concat(header, emitBlockForceBroken(v.Body))
}

// hasElseIf reports whether the chain rooted at v contains at least one
// `else if` rung — i.e. v.Else (or any descendant Else) is itself an *ast.If.
func hasElseIf(v *ast.If) bool {
	_, ok := v.Else.(*ast.If)
	return ok
}

// emitIfInner builds the Doc content of an if/else chain without wrapping it
// in a Group. Used recursively so `else if` rungs share the outermost Group
// (emitted by emitIf) and flatten/break together.
func emitIfInner(v *ast.If) Doc {
	parts := []Doc{Text("if")}
	if cond := emitIfCondition(v); cond != nil {
		parts = append(parts, Text(" "), cond)
	}
	parts = append(parts, Text(" "), emit(v.Then))
	if v.Else != nil {
		parts = append(parts, Text(" else "))
		// `else if` chain: recurse without adding a nested Group so the whole
		// chain shares one flat-vs-broken decision.
		if nested, ok := v.Else.(*ast.If); ok {
			parts = append(parts, emitIfInner(nested))
		} else {
			parts = append(parts, emit(v.Else))
		}
	}
	return Concat(parts...)
}

func emitIfCondition(v *ast.If) Doc {
	if v == nil || v.Cond == nil {
		return nil
	}
	if v.CondPattern != nil {
		return Concat(emitPattern(v.CondPattern), Text(" = "), emit(v.Cond))
	}
	return emit(v.Cond)
}

// isAtomicExpr reports whether n is an "atomic" expression per the Option A
// if/else inlining rule: a simple value with no sub-computation that would
// benefit from its own line. Literals, bare identifiers, the wildcard `_`,
// and `.`-chains rooted at another atomic are all atomic. Early-exit control
// flow (`break`, `continue`, `return`) also counts as atomic — with or
// without an atomic value — so idioms like `if x < 0 { continue }` and
// `if n == 1 { break count }` stay on one line. Variant constructors with
// atomic payloads are atomic too: `Some(x)`, `Ok(42)`, `Shape.Circle(5.0)`
// are no more visually complex than their payloads.
func isAtomicExpr(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit, *ast.CodepointLit, *ast.StringLit,
		*ast.Ident, *ast.TypeIdent, *ast.Placeholder:
		return true
	case *ast.FieldAccess:
		return isAtomicExpr(v.Object)
	case *ast.Continue:
		return v.Value == nil || isAtomicExpr(v.Value)
	case *ast.Break:
		return v.Value == nil || isAtomicExpr(v.Value)
	case *ast.Return:
		return v.Value == nil || isAtomicExpr(v.Value)
	case *ast.Call:
		if !isVariantCallee(v.Func) {
			return false
		}
		for _, a := range v.Args {
			if na, ok := a.(*ast.NamedArg); ok {
				if !isAtomicExpr(na.Value) {
					return false
				}
				continue
			}
			if !isAtomicExpr(a) {
				return false
			}
		}
		return true
	}
	return false
}

// isVariantCallee reports whether the callee of a Call is a variant or
// type constructor — a TypeIdent (uppercase) or a dotted chain ending in a
// TypeIdent with TypeIdent roots (e.g. `Shape.Circle`).
func isVariantCallee(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.TypeIdent:
		return true
	case *ast.FieldAccess:
		return isVariantCallee(v.Object)
	}
	return false
}

// isAtomicBlockBody reports whether a block body qualifies as atomic for the
// if/else inlining rule. A block is atomic iff it contains exactly one
// statement (unwrapping ExprStmt) and that statement is an atomic expression.
// Empty blocks and multi-stmt blocks are never atomic.
func isAtomicBlockBody(b *ast.Block) bool {
	if b == nil || len(b.Stmts) != 1 {
		return false
	}
	// A trailing *comment* before `}` needs its own line, so such a block is
	// not atomic even with one stmt. Trailing *blank* lines don't count —
	// emitEndTrivia strips them — so they must not suppress inlining (else the
	// block would collapse only on a second format pass, breaking idempotency).
	if trailingHasComment(b.GetTrailing()) {
		return false
	}
	stmt := b.Stmts[0]
	if hasTrailingLineComment(stmt) {
		return false
	}
	if es, ok := stmt.(*ast.ExprStmt); ok {
		stmt = es.Expr
	}
	return isAtomicExpr(stmt)
}

// trailingHasComment reports whether end-of-block trivia contains a comment
// (which needs its own line before `}`). Trailing blank lines alone don't
// count — emitEndTrivia strips them — so a block whose only end-of-block trivia
// is blank lines stays eligible for single-line/inline rendering.
func trailingHasComment(trivia []ast.Trivia) bool {
	for _, t := range trivia {
		if t.Kind == ast.TriviaComment {
			return true
		}
	}
	return false
}

// isInlineableBlockBody reports whether a block body may render inline in a
// single-line if/else: exactly one statement, no end-of-block trivia, no
// trailing comment. Unlike isAtomicBlockBody it does NOT restrict the
// statement's *content* — width (via fits) decides whether the inline form is
// actually used, and an inherently multi-line statement (case, multi-stage
// pipe) introduces a HardLine that fits rejects.
func isInlineableBlockBody(b *ast.Block) bool {
	if b == nil || len(b.Stmts) != 1 {
		return false
	}
	if trailingHasComment(b.GetTrailing()) {
		return false
	}
	return !hasTrailingLineComment(b.Stmts[0])
}

// ifBranchesInlineable reports whether a plain if/else (no else-if — the caller
// rules that out first via hasElseIf) has branch bodies eligible for inline
// rendering: each branch is a single-statement block with no comments. Width
// and forced-breaks are checked separately by the caller's fits() probe.
func ifBranchesInlineable(v *ast.If) bool {
	if !isInlineableBlockBody(v.Then) {
		return false
	}
	if v.Else == nil {
		return true
	}
	elseBlock, ok := v.Else.(*ast.Block)
	if !ok {
		return false
	}
	return isInlineableBlockBody(elseBlock)
}

// emitIfForceBroken renders an if/else chain in always-broken form, never
// relying on Block's own Group to decide flat-vs-broken. Each branch body is
// emitted via emitBlockForceBroken so HardLines force the layout regardless
// of remaining width.
func emitIfForceBroken(v *ast.If) Doc {
	parts := []Doc{Text("if")}
	if cond := emitIfCondition(v); cond != nil {
		parts = append(parts, Text(" "), cond)
	}
	parts = append(parts, Text(" "), emitBlockForceBroken(v.Then))
	if v.Else != nil {
		parts = append(parts, Text(" else "))
		switch e := v.Else.(type) {
		case *ast.If:
			parts = append(parts, emitIfForceBroken(e))
		case *ast.Block:
			parts = append(parts, emitBlockForceBroken(e))
		default:
			// Defensive: shouldn't happen per parser invariants, but don't
			// panic — fall back to the standard emit.
			parts = append(parts, emit(v.Else))
		}
	}
	return Concat(parts...)
}

// emitBlockForceBroken renders a block body in always-broken form. Statements
// are separated by HardLine so the layout engine cannot flatten; end-of-block
// trivia (comments before `}`) is placed after the last stmt.
//
// This is deliberately separate from the Block case in `emit`: that path uses
// `Line()` for single-stmt bodies so they can flatten when short. For the
// broken if/else emit we want guaranteed multi-line layout regardless of
// branch content length.
func emitBlockForceBroken(b *ast.Block) Doc {
	body := emitStatementDocs(b.Stmts)
	return emitDocsBlockForceBroken(body, b.GetTrailing())
}

func emitStatementDocs(stmts []ast.Node) []Doc {
	if len(stmts) == 0 {
		return nil
	}
	items := make([]statementItem, len(stmts))
	for i, s := range stmts {
		items[i] = newStatementItem(s, emit(s))
	}
	return []Doc{emitStatementSequence(items)}
}

// statementItem is one statement of a block for emitStatementSequence. doc is
// the statement alone, which is what is measured; full adds its comments.
// authorBlank records a blank line the source wrote above it, which full
// already renders. node is nil for an item that is not a statement node, and
// blankAfter forces a blank line after the item.
type statementItem struct {
	node        ast.Node
	doc         Doc
	full        Doc
	authorBlank bool
	blankAfter  bool
}

func newStatementItem(s ast.Node, doc Doc) statementItem {
	return statementItem{
		node:        s,
		doc:         doc,
		full:        emitWithTriviaDoc(s, doc),
		authorBlank: hasLeadingBlank(s),
	}
}

// emitStatementSequence lays out a block's statements, one per line, with one
// blank line between two of them when:
//   - either one spans more than one line, so a multi-line statement stands
//     apart from its neighbours (no blank line is added at the top or bottom
//     of the block);
//   - the first is a pipe statement;
//   - the first is an import and the second is not;
//   - the source wrote one there.
//
// Whether a statement spans lines depends on the column the block starts at,
// known only at render time, so the decision is made there (WithIndent),
// measuring each statement from that column. The result is memoized per
// indent and width, since enclosing layout decisions build it again.
func emitStatementSequence(items []statementItem) Doc {
	type key struct{ indent, width int }
	built := map[key]Doc{}
	return WithIndent(func(indent, width int) Doc {
		k := key{indent, width}
		if d, ok := built[k]; ok {
			return d
		}
		multiline := make([]bool, len(items))
		for i, it := range items {
			multiline[i] = spansLines(it.doc, indent, width)
		}
		parts := make([]Doc, 0, len(items)*3)
		for i, it := range items {
			if i > 0 {
				parts = append(parts, HardLine())
				if !it.authorBlank && statementsWantBlank(items[i-1], it, multiline[i-1], multiline[i]) {
					parts = append(parts, HardLine())
				}
			}
			parts = append(parts, it.full)
		}
		d := Concat(parts...)
		built[k] = d
		return d
	})
}

func statementsWantBlank(prev, next statementItem, prevMultiline, nextMultiline bool) bool {
	if prevMultiline || nextMultiline || prev.blankAfter {
		return true
	}
	if prev.node == nil {
		return false
	}
	if isPipeStatement(prev.node) {
		return true
	}
	return isImportNode(prev.node) && (next.node == nil || !isImportNode(next.node))
}

// spansLines reports whether d renders on more than one line when it starts
// at column indent of a width-wide line. A HardLine that always renders (in a
// nested block, a case, a literal the author wrote across lines) settles it.
// Otherwise d is rendered: only the layout knows whether a group breaks, and
// an over-long line with nowhere to break stays one line.
func spansLines(d Doc, indent, width int) bool {
	if containsHardLine(d) {
		return true
	}
	var sb strings.Builder
	render(&sb, d, indent, indent, noFlatGroup, nil, width, false)
	return strings.Contains(sb.String(), "\n")
}

// containsHardLine reports whether d has a HardLine that renders whatever
// layout its groups take. One under IfBroken does not count: it renders only
// in one mode, which the short `if` form uses to choose between one line and
// a full block.
func containsHardLine(d Doc) bool {
	switch v := d.(type) {
	case docHard:
		return true
	case docConcat:
		return containsHardLine(v.a) || containsHardLine(v.b)
	case docNest:
		return containsHardLine(v.d)
	case docGroup:
		return containsHardLine(v.d)
	case docLocalBroken:
		return containsHardLine(v.d)
	case docHidden:
		return containsHardLine(v.d)
	case docWithIndent:
		return containsHardLine(v.f(0, unboundedWidth))
	}
	return false
}

func isPipeStatement(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.Binding:
		return isPipeExpr(v.Value)
	case *ast.TupleDestructure:
		return isPipeExpr(v.Value)
	case *ast.StructDestructure:
		return isPipeExpr(v.Value)
	case *ast.MapDestructure:
		return isPipeExpr(v.Value)
	case *ast.DistinctDestructure:
		return isPipeExpr(v.Value)
	case *ast.PatternBinding:
		return isPipeExpr(v.Value)
	}
	if stmt, ok := n.(*ast.ExprStmt); ok {
		return isPipeExpr(stmt.Expr)
	}
	return isPipeExpr(n)
}

func isPipeExpr(n ast.Node) bool {
	if assertion, ok := n.(*ast.Assertion); ok {
		return assertion.Expr != nil && isPipeExpr(assertion.Expr)
	}
	pipe, ok := n.(*ast.Binary)
	return ok && pipe.Op == "|>"
}

func emitDocsBlockForceBroken(parts []Doc, endTrivia []ast.Trivia) Doc {
	if len(parts) == 0 {
		if len(endTrivia) == 0 {
			return Text("{}")
		}
		return Concat(
			Text("{"),
			Nest(defaultIndent, emitEndTrivia(endTrivia)),
			HardLine(),
			Text("}"),
		)
	}
	if len(endTrivia) > 0 {
		parts = append(parts, emitEndTrivia(endTrivia))
	}
	return Concat(
		Text("{"),
		Nest(defaultIndent, Concat(HardLine(), Concat(parts...))),
		HardLine(),
		Text("}"),
	)
}

// maxCompoundDepth is the depth at which a Map/List/struct-shaped
// pattern or construction breaks vertically regardless of width. Three
// consecutive compound levels are unreadable single-line even when they
// happen to fit — the eye loses track of which `{...}` closes which
// opener. Below this threshold the Group's own width-based decision
// still applies: a depth-2 form that exceeds the width still breaks.
const maxCompoundDepth = 3

// compoundDepth counts consecutive nested compound-pattern levels
// starting from n. A "compound" node is a map/list literal-or-pattern
// or a struct-shaped literal-or-pattern. Wrapped variants like
// `Some(x)`, `Ok(...)`, `String(s)` (Call / EnumPattern-with-payload)
// are not compound: they recurse but do not contribute a level.
// Anything not in either category — Idents, literals, binary ops,
// field-access, etc. — returns 0.
//
// The depth grows by 1 for the receiving compound node, plus the max
// depth of any directly-nested compound payload. So:
//
//	Obj{"k" => v}                          -> 1
//	Obj{"k" => Obj{"k" => v}}              -> 2
//	Obj{"k" => Obj{"k" => Obj{"k" => v}}}  -> 3
//	Ok(Obj{"k" => Obj{"k" => Obj{"k" => v}}})  ->  inner Ok(...) is a Call,
//	  so the outer depth is whatever its payload's depth is: 3.
//	Some(Obj{"k" => v})                    ->  Some is a Call (depth 0),
//	  payload depth is 1, so the OUTER expression has no compound (returns 0).
//	  The inner Obj's own compoundDepth is 1 — measured independently
//	  at the recursive call site inside emitMap.
//
// The helper is called by each compound emit path on its OWN node
// (e.g. emitMap calls compoundDepth on its MapLit). Nested compounds
// re-make the decision at their own emit site.
func compoundDepth(n ast.Node) int {
	if n == nil {
		return 0
	}
	switch v := n.(type) {
	case *ast.MapLit:
		d := 0
		for _, e := range v.Entries {
			if c := compoundDepth(e.Value); c > d {
				d = c
			}
		}
		return 1 + d
	case *ast.MapPattern:
		d := 0
		for _, e := range v.Entries {
			if c := compoundDepth(e.Pattern); c > d {
				d = c
			}
		}
		return 1 + d
	case *ast.ListLit:
		d := 0
		for _, it := range v.Items {
			if c := compoundDepth(it); c > d {
				d = c
			}
		}
		return 1 + d
	case *ast.VectorLit:
		d := 0
		for _, it := range v.Items {
			if c := compoundDepth(it); c > d {
				d = c
			}
		}
		return 1 + d
	case *ast.SetLit:
		d := 0
		for _, it := range v.Items {
			if c := compoundDepth(it); c > d {
				d = c
			}
		}
		return 1 + d
	case *ast.ListPattern:
		d := 0
		for _, h := range v.Heads {
			if c := compoundDepth(h); c > d {
				d = c
			}
		}
		if c := compoundDepth(v.TailSpread); c > d {
			d = c
		}
		return 1 + d
	case *ast.StructLit:
		d := 0
		if c := compoundDepth(v.Spread); c > d {
			d = c
		}
		for _, f := range v.Fields {
			if c := compoundDepth(f.Value); c > d {
				d = c
			}
		}
		return 1 + d
	case *ast.StructPattern:
		d := 0
		for _, f := range v.Fields {
			if c := compoundDepth(f.Pattern); c > d {
				d = c
			}
		}
		return 1 + d
	// --- Wrapped variants (don't count, but recurse) ---------------------
	case *ast.Call:
		// `Some(x)`, `Ok(...)`, `String(s)`, generic calls — the call
		// itself doesn't count as compound, but the inner payload's
		// compound depth still drives the wrapping caller's decision
		// (e.g. `Ok(Obj{Obj{Obj{v}}})` should break the Obj chain).
		d := 0
		for _, a := range v.Args {
			if c := compoundDepth(a); c > d {
				d = c
			}
		}
		return d
	case *ast.EnumPattern:
		// `Some(x)` / `Ok(Obj{...})` etc. — payload pattern is one node.
		return compoundDepth(v.Payload)
	case *ast.TupleLit:
		// Tuples aren't in the compound set, but a tuple holding a
		// compound payload must propagate the depth so an outer
		// compound sees through it.
		d := 0
		for _, it := range v.Items {
			if c := compoundDepth(it); c > d {
				d = c
			}
		}
		return d
	case *ast.TuplePattern:
		d := 0
		for _, p := range v.Patterns {
			if c := compoundDepth(p); c > d {
				d = c
			}
		}
		return d
	}
	return 0
}

// emitChildMustBreak renders an expression-position node knowing that an
// ancestor compound already decided to break. The chain-aware break rule:
// once any compound in a chain decides to break (depth >= 3 or width), every
// downstream compound in that chain also breaks regardless of its own
// independent depth or width — otherwise a deeply-nested chain would leave
// inner compounds densely packed on broken lines and defeat the readability
// goal of breaking in the first place.
//
// Pass-through cases:
//   - Compound nodes (Map/List/struct literal) — force broken form AND
//     propagate mustBreak to their own children.
//   - Wrapper nodes (Call like `Ok(...)`, TupleLit) — recurse through the
//     payload propagating mustBreak; the wrapper itself does NOT break.
//     This matches compoundDepth: wrappers don't add a level, so they don't
//     break a level either.
//   - Everything else — fall through to the regular emit(), which is the
//     leaf of the chain.
//
// Width-driven breaks at lower depths still work unchanged: the regular
// emit() path's Group + emitBracedList machinery handles them.
func emitChildMustBreak(n ast.Node) Doc {
	switch v := n.(type) {
	case *ast.ListLit:
		open := "["
		if v.TypeName != nil {
			open = v.TypeName.TypeString() + "["
		}
		if len(v.Items) == 0 {
			return Text(open + "]")
		}
		elems := make([]Doc, 0, len(v.Items))
		for _, it := range v.Items {
			elems = append(elems, emitChildMustBreak(it))
		}
		return forceBrokenBraced(open, "]", elems)
	case *ast.VectorLit:
		if len(v.Items) == 0 {
			return Text("#[]")
		}
		elems := make([]Doc, 0, len(v.Items))
		for _, it := range v.Items {
			elems = append(elems, emitChildMustBreak(it))
		}
		return forceBrokenBraced("#[", "]", elems)
	case *ast.SetLit:
		if len(v.Items) == 0 {
			return Text("#{}")
		}
		elems := make([]Doc, 0, len(v.Items))
		for _, it := range v.Items {
			elems = append(elems, emitChildMustBreak(it))
		}
		return forceBrokenBraced("#{", "}", elems)
	case *ast.MapLit:
		open := "{"
		if v.TypeName != nil {
			open = v.TypeName.TypeString() + "{"
		}
		if len(v.Entries) == 0 {
			return Text(open + "}")
		}
		elems := make([]Doc, 0, len(v.Entries))
		for _, e := range v.Entries {
			elems = append(elems, Concat(emit(e.Key), Text(" => "), emitChildMustBreak(e.Value)))
		}
		return forceBrokenBraced(open, "}", elems)
	case *ast.StructLit:
		open := "{"
		if v.TypeName != nil {
			open = v.TypeName.TypeString() + "{"
		}
		if len(v.Fields) == 0 && v.Spread == nil {
			return Text(open + "}")
		}
		elems := make([]Doc, 0, len(v.Fields)+1)
		punnable := structLitFieldsPunnable(v)
		if v.Spread != nil {
			elems = append(elems, Concat(Text(".."), emitChildMustBreak(v.Spread)))
		}
		for _, f := range v.Fields {
			if punnable && fieldValueIsPun(f) {
				elems = append(elems, Text(f.Name))
				continue
			}
			elems = append(elems, Concat(Text(f.Name), Text(": "), emitChildMustBreak(f.Value)))
		}
		return forceBrokenBraced(open, "}", elems)
	case *ast.Call:
		// Wrapper: `Ok(...)`, `Some(...)`, `String(...)` — propagate
		// mustBreak through the payload args without breaking the call
		// itself. emit(v.Func) is the callee head (Ident/FieldAccess);
		// args use emitCallArg's exception logic, then we override to
		// mustBreak-propagation.
		callee := emit(v.Func)
		nargs := len(v.Args)
		if nargs == 0 {
			return Concat(callee, Text("()"))
		}
		// Tail-lambda special-case from emitCall: emit literally, no
		// mustBreak propagation through a lambda body (the lambda body
		// is a fresh scope, not a compound-chain continuation).
		if _, tailIsLambda := v.Args[nargs-1].(*ast.Lambda); tailIsLambda {
			leading := v.Args[:nargs-1]
			parts := []Doc{Text("(")}
			for _, a := range leading {
				parts = append(parts, emitCallArgMustBreak(a), Text(", "))
			}
			parts = append(parts, emit(v.Args[nargs-1]), Text(")"))
			return Concat(callee, Concat(parts...))
		}
		// Single-arg wrappers (Ok(x), Some(x), String(x), Int(x)) are
		// the common case — render flat so the wrapper visually hugs
		// its broken payload without an extra layer of breaks. The Doc
		// for the inner compound was rendered via emitChildMustBreak
		// (which uses forceBrokenBraced / LocalBroken), so the LocalBroken
		// keeps the wrapper's parens on one line each.
		if nargs == 1 {
			return Concat(callee, Text("("), emitCallArgMustBreak(v.Args[0]), Text(")"))
		}
		// Multi-arg call wrapping a compound: each arg gets mustBreak
		// propagation; the arg list itself still uses the Group so the
		// flat-or-broken decision for the parens is width-driven.
		parts := make([]Doc, 0, nargs*2-1)
		for i, a := range v.Args {
			if i > 0 {
				parts = append(parts, Text(","), Line())
			}
			parts = append(parts, emitCallArgMustBreak(a))
		}
		inner := Concat(parts...)
		return Concat(callee, Group(Concat(
			Text("("),
			Nest(defaultIndent, Concat(LineOrEmpty(), inner)),
			IfBroken(Nil(), Text(",")),
			LineOrEmpty(),
			Text(")"),
		)))
	case *ast.TupleLit:
		// Tuples don't add a compound level (see compoundDepth), so a
		// tuple in a chain is a pass-through: propagate mustBreak to
		// each item but keep the tuple's own paren-delimited shape.
		elems := make([]Doc, 0, len(v.Items))
		for _, it := range v.Items {
			elems = append(elems, emitChildMustBreak(it))
		}
		return emitBracedList("(", ")", elems)
	}
	return emit(n)
}

// emitCallArgMustBreak is the call-arg-position analog of
// emitChildMustBreak — it preserves the triple-quoted-string call-arg
// indent exception (matching emitCallArg) for any arg that isn't itself
// a compound or compound wrapper.
func emitCallArgMustBreak(n ast.Node) Doc {
	switch n.(type) {
	case *ast.ListLit, *ast.VectorLit, *ast.SetLit, *ast.MapLit, *ast.StructLit, *ast.Call, *ast.TupleLit:
		return emitChildMustBreak(n)
	}
	return emitCallArg(n)
}

// emitPatternChildMustBreak is the pattern-position analog of
// emitChildMustBreak. The same pass-through rules apply: compound
// patterns (MapPattern, ListPattern, StructPattern) force-break and
// propagate; wrapper patterns (EnumPattern with payload, TuplePattern)
// propagate without breaking themselves; everything else falls through
// to the regular emitPattern.
func emitPatternChildMustBreak(n ast.Node) Doc {
	switch v := n.(type) {
	case *ast.MapPattern:
		open := "{"
		if v.TypeName != nil {
			open = v.TypeName.TypeString() + "{"
		}
		if len(v.Entries) == 0 {
			return Text(open + "}")
		}
		elems := make([]Doc, 0, len(v.Entries))
		for _, e := range v.Entries {
			elems = append(elems, Concat(emit(e.Key), Text(" => "), emitPatternChildMustBreak(e.Pattern)))
		}
		return forceBrokenBraced(open, "}", elems)
	case *ast.ListPattern:
		open := "["
		if v.TypeName != nil {
			open = v.TypeName.TypeString() + "["
		}
		// Spread patterns retain their flat-only convention even under
		// mustBreak — matches the standard emitPattern path.
		if v.TailSpread != nil {
			return emitPattern(n)
		}
		if len(v.Heads) == 0 {
			return Text(open + "]")
		}
		elems := make([]Doc, 0, len(v.Heads))
		for _, h := range v.Heads {
			elems = append(elems, emitPatternChildMustBreak(h))
		}
		return forceBrokenBraced(open, "]", elems)
	case *ast.StructPattern:
		open := "{"
		if v.TypeName != nil {
			open = v.TypeName.TypeString() + "{"
		}
		if len(v.Fields) == 0 {
			return Text(open + "}")
		}
		elems := make([]Doc, 0, len(v.Fields))
		for _, f := range v.Fields {
			switch {
			case f.Pattern != nil:
				elems = append(elems, Concat(Text(f.Name), Text(": "), emitPatternChildMustBreak(f.Pattern)))
			case f.Binding != "" && f.Binding != f.Name:
				elems = append(elems, Concat(Text(f.Name), Text(": "), Text(f.Binding)))
			default:
				elems = append(elems, Text(f.Name))
			}
		}
		return forceBrokenBraced(open, "}", elems)
	case *ast.EnumPattern:
		// Wrapper: `Ok(<pattern>)`, `Some(<pattern>)`, `String(<pattern>)`.
		// Propagate mustBreak through the payload but don't break the
		// wrapper itself. The wrapper's other shapes (bare variant,
		// flat-tuple shorthand, fast-path binding) don't carry compound
		// payloads — fall through to emitPattern.
		name := Text(v.Variant.TypeString())
		switch {
		case v.Binding != "":
			return Concat(name, Text("("), Text(v.Binding), Text(")"))
		case v.Payload != nil:
			if tp, ok := v.Payload.(*ast.TuplePattern); ok && tp.Flat {
				return emitPattern(n)
			}
			return Concat(name, Text("("), emitPatternChildMustBreak(v.Payload), Text(")"))
		default:
			return name
		}
	case *ast.TuplePattern:
		// Tuples don't add a level (see compoundDepth) — pass-through.
		parts := make([]Doc, 0, len(v.Patterns)*2+1)
		parts = append(parts, Text("("))
		for i, pat := range v.Patterns {
			if i > 0 {
				parts = append(parts, Text(", "))
			}
			parts = append(parts, emitPatternChildMustBreak(pat))
		}
		parts = append(parts, Text(")"))
		return Concat(parts...)
	}
	return emitPattern(n)
}

// emitBracedList renders "<open>e1, e2, ...<close>" flat, or
// "<open>\n  e1,\n  e2,\n  ...\n<close>" broken, choosing via a Group so
// the layout engine picks flat when the content fits the line budget and
// broken otherwise. A trailing comma appears only in the broken form.
//
// Empty input renders as "<open><close>" with no space or Group — no layout
// decision is needed.
func emitBracedList(open, close string, elems []Doc) Doc {
	if len(elems) == 0 {
		return Text(open + close)
	}
	parts := make([]Doc, 0, len(elems)*2-1)
	for i, e := range elems {
		if i > 0 {
			parts = append(parts, Text(","), Line())
		}
		parts = append(parts, e)
	}
	inner := Concat(parts...)
	return Group(Concat(
		Text(open),
		Nest(defaultIndent, Concat(LineOrEmpty(), inner)),
		IfBroken(Nil(), Text(",")),
		LineOrEmpty(),
		Text(close),
	))
}

// emitStructLit renders a struct literal, nominal (`User{...}`) or
// anonymous (`{...}`). Field punning — where the parser synthesizes
// Value=Ident{Name:FieldName} — is re-emitted as just the field name.
//
// Width-based with the same preserve-user-multi-line escape as
// emitBracedStructFieldsWithEndTrivia: a literal the user wrote across multiple source
// lines stays multi-line even when it would fit flat.
func emitStructLit(v *ast.StructLit) Doc {
	open := "{"
	if v.TypeName != nil {
		open = v.TypeName.TypeString() + "{"
	}
	hasEndTrivia := len(v.EndTrivia) > 0
	punnable := structLitFieldsPunnable(v)
	hasInterFieldTrivia := structFieldValsHaveLeading(v.Fields)
	// Chain-aware compound-depth break: when this struct's own depth
	// crosses the threshold, render in broken form AND propagate
	// mustBreak to every field value so nested compounds in the same
	// chain also break. The user-multi-line escape uses the regular
	// emit() recursion (no cascade — the user's choice is the source of
	// truth, not a depth signal).
	if !structLitUserMultiLine(v) &&
		compoundDepth(v) >= maxCompoundDepth && len(v.Fields) > 0 {
		elems := make([]Doc, 0, len(v.Fields)+1)
		if v.Spread != nil {
			elems = append(elems, Concat(Text(".."), emitChildMustBreak(v.Spread)))
		}
		for _, f := range v.Fields {
			elems = append(elems, emitStructFieldValWithLeading(f, true, punnable))
		}
		return forceBrokenBracedWithEndTrivia(open, "}", elems, v.EndTrivia)
	}
	elems := make([]Doc, 0, len(v.Fields)+1)
	// `..head` leads, always. The position rule is the parser's and the
	// formatter has no freedom here: moving it would be a different
	// program (a spread after a field does not parse).
	if v.Spread != nil {
		elems = append(elems, Concat(Text(".."), emit(v.Spread)))
	}
	for _, f := range v.Fields {
		elems = append(elems, emitStructFieldValWithLeading(f, false, punnable))
	}
	// EndTrivia (trailing in-body comment) or any inter-field leading
	// comment forces the broken form — a `// comment` can't share a
	// line with the surrounding fields / closing `}`.
	if structLitUserMultiLine(v) || hasEndTrivia || hasInterFieldTrivia {
		return forceBrokenBracedWithEndTrivia(open, "}", elems, v.EndTrivia)
	}
	return emitBracedList(open, "}", elems)
}

// structLitUserMultiLine reports whether the source had this struct
// literal's elements spread across multiple lines.
//
// The SPREAD COUNTS AS AN ELEMENT, and it has to: `{..p\n  x: 11\n}` has one
// field, so a fields-only rule read it as single-line and flattened a
// deliberately stacked literal. The spread's line is the `..` token's.
func structLitUserMultiLine(v *ast.StructLit) bool {
	first, last := -1, -1
	if v.Spread != nil {
		first, last = v.SpreadLine, v.SpreadLine
	}
	for _, f := range v.Fields {
		if first < 0 {
			first = f.Line
		}
		last = f.Line
	}
	if first < 0 || last < 0 {
		return false
	}
	return first != last
}

// structFieldValsHaveLeading reports whether any field carries inter-field
// leading comments. Used by emitStructLit to force the broken form when
// at least one comment lives above a field — otherwise the leading-
// comment lines would collapse into the flat layout and emit syntax-
// invalid output (a `// comment` cannot sit mid-line).
func structFieldValsHaveLeading(fields []ast.StructFieldVal) bool {
	for _, f := range fields {
		if len(f.LeadingComments) > 0 {
			return true
		}
	}
	return false
}

// structLitFieldsPunnable reports whether this literal's punned fields may be
// re-emitted in punned form (`{name}` rather than `{name: name}`).
//
// A single-field ANONYMOUS literal may NOT. `{name}` re-parses as a BLOCK whose
// value is `name`, not as a one-field anonymous struct, so punning there is a
// round-trip violation: the formatter rewrites a working program into a
// different one: `Struct.update(request, {context: context})` became
// `Struct.update(request, {context})`, which hands `Struct.update` a `Context`
// where a `Partial<Request>` belongs — an analysis error, and a runtime
// "second argument must be a struct".
//
// Every other arity and shape is unambiguous: `{a, b}` cannot be a block (a
// block has no comma-separated statements), and a nominal literal such as
// `User{name}` is disambiguated by its type name at any arity.
func structLitFieldsPunnable(v *ast.StructLit) bool {
	// A SPREAD ALSO DISAMBIGUATES. `{..base, name}` cannot be a block —
	// a block's first token is never `..` — so the one-field anonymous
	// case that forced `{name: name}` does not apply here.
	return v.TypeName != nil || v.Spread != nil || len(v.Fields) > 1
}

// fieldValueIsPun reports whether a field's value is exactly its own name, the
// shape both the parser's punning synthesis and an explicit `name: name` land
// on.
func fieldValueIsPun(f ast.StructFieldVal) bool {
	id, ok := f.Value.(*ast.Ident)
	return ok && id.Name == f.Name
}

// emitStructFieldValWithLeading renders one struct-literal field with
// its leading-comment block above. `cascadeMustBreak` selects between
// the regular and chain-aware emit path for the field's Value (the
// outer compound-depth break propagates mustBreak to every nested
// compound).
//
// Punning is preserved (Value=Ident with same name as field → just the
// name) regardless of cascade, but only where the punned form re-parses as
// the same literal — see structLitFieldsPunnable.
func emitStructFieldValWithLeading(f ast.StructFieldVal, cascadeMustBreak, punnable bool) Doc {
	parts := []Doc{}
	parts = append(parts, emitLeadingComments(f.LeadingComments)...)
	if punnable && fieldValueIsPun(f) {
		parts = append(parts, Text(f.Name))
		return Concat(parts...)
	}
	parts = append(parts, Text(f.Name), Text(": "))
	if cascadeMustBreak {
		parts = append(parts, emitChildMustBreak(f.Value))
	} else {
		parts = append(parts, emit(f.Value))
	}
	return Concat(parts...)
}

// emitCase renders a case expression. It always breaks, one arm per line,
// and lays the arms out together in one of two ways:
//
//   - Flat, when every arm fits on its line as `<pattern> -> <body>` and no
//     arm is a block: the arms sit on consecutive lines with no blank line
//     between them.
//
//   - Broken, otherwise: an arm whose body is a block keeps `-> {` on the
//     pattern's line with the block always broken, even around one
//     statement; every other arm puts its body on the next line, one indent
//     in; and one blank line separates every two arms:
//
//     case <scrutinee> {
//     <pattern> [when <guard>] ->
//     <body>
//
//     <pattern> -> {
//     <statements>
//     }
//     }
//
// In both layouts the source's blank lines between arms are ignored, and a
// comment stays directly above its arm. The arms start at a column known only
// at render time, so the decision is made there (WithIndent), measuring each
// arm from that column.
func emitCase(v *ast.Case) Doc {
	parts := []Doc{Text("case")}
	if v.Value != nil {
		parts = append(parts, Text(" "), emit(v.Value))
	}
	parts = append(parts, Text(" {"))
	// Trivia sitting between the last arm and the closing `}` was attached
	// to the Case node by the parser; it is emitted inside the braces.
	parts = append(parts,
		Nest(defaultIndent, emitCaseArms(v.Branches, v.GetTrailing())),
		HardLine(),
		Text("}"),
	)
	return Concat(parts...)
}

// emitCaseArms lays out case arms, each on its own line after a HardLine,
// followed by endTrivia, the comments between the last arm and the `}`.
// When every arm fits on its line and none has a block body, the arms sit on
// consecutive lines; otherwise each arm breaks and a blank line separates them.
func emitCaseArms(branches []ast.CaseBranch, endTrivia []ast.Trivia) Doc {
	return WithIndent(func(indent, width int) Doc {
		flat := true
		for i := range branches {
			b := &branches[i]
			if _, isBlock := b.Body.(*ast.Block); isBlock {
				flat = false
				break
			}
			rem := width - indent
			if !fitsWalk(caseArmLine(b, false), &rem, false) {
				flat = false
				break
			}
		}
		body := make([]Doc, 0, len(branches)*3)
		for i := range branches {
			if i > 0 && !flat {
				body = append(body, HardLine())
			}
			b := &branches[i]
			body = append(body, HardLine(), emitCaseArmTrivia(b, caseArmLine(b, !flat)))
		}
		if len(endTrivia) > 0 {
			body = append(body, emitEndTrivia(endTrivia))
		}
		return Concat(body...)
	})
}

// caseArmLine renders one arm, `<pattern> [when <guard>] -> <body>`, without
// its comments. A block body follows `-> ` on the pattern's line and is always
// broken, its statements on the lines below. Any other
// body follows on the same line, or with broken on the next line one indent
// in.
func caseArmLine(b *ast.CaseBranch, broken bool) Doc {
	prefix := emitPattern(b.Pattern)
	if b.Guard != nil {
		prefix = Concat(prefix, Text(" when "), emit(b.Guard))
	}
	if block, isBlock := b.Body.(*ast.Block); isBlock {
		// Always a full block, even around one statement, so every broken
		// arm's body starts on the line after its pattern, one indent in.
		return Concat(prefix, Text(" -> "), emitBlockForceBroken(block))
	}
	if broken {
		return Concat(prefix, Text(" ->"), Nest(defaultIndent, Concat(HardLine(), emitLambdaSingleExprBody(b.Body))))
	}
	return Concat(prefix, Text(" -> "), emit(b.Body))
}

// emitCaseArmTrivia surrounds an arm with its own comments: leading comments
// on the lines above it, and a trailing comment after it. A blank line the
// source wrote before the arm adds nothing, since emitCase decides the spacing
// between arms.
func emitCaseArmTrivia(b *ast.CaseBranch, arm Doc) Doc {
	leading := b.GetLeading()
	trailing := b.GetTrailing()
	if len(leading) == 0 && len(trailing) == 0 {
		return arm
	}
	parts := make([]Doc, 0, len(leading)*2+1+len(trailing)*2)
	for _, t := range leading {
		if t.Kind == ast.TriviaComment {
			parts = append(parts, Text(t.Text), HardLine())
		}
	}
	parts = append(parts, arm)
	for _, t := range trailing {
		if t.Kind == ast.TriviaComment {
			parts = append(parts, Text(" "), Text(t.Text))
		}
	}
	return Concat(parts...)
}

// emitCall renders a function call with a paren-delimited arg list.
//
// Special case — last arg is a lambda: emit `func(leading_args, |params| <body>)`
// so the lambda's opener hugs the call's `(` and its `}` hugs the `)`. Prevents
// the ugly double-indent of a multi-stmt lambda inside a generic argList Group:
//
//	// without special case:              // with special case:
//	lists.map(                             lists.map(|x| {
//	  |x| {                                 if x == 3 { return 300 }
//	    if x == 3 { return 300 }            x * 10
//	    x * 10                            })
//	  },
//	)
//
// For trivial single-expression lambdas the output is identical either way
// (`lists.map(|x| x * 2)` fits inline); the special-case only differs when the
// lambda body breaks.
func emitCall(v *ast.Call) Doc {
	callee := emit(v.Func)
	if len(v.TypeArgs) > 0 {
		callee = Concat(callee, emitTypeArgs(v.TypeArgs))
	}
	n := len(v.Args)
	if n == 0 {
		return Concat(callee, Text("()"))
	}
	if lam, tailIsLambda := v.Args[n-1].(*ast.Lambda); tailIsLambda {
		leading := v.Args[:n-1]
		if expr, ok := lambdaSingleExpr(lam); ok {
			if lambdaExprBodyShouldBreak(expr) {
				parts := []Doc{Text("(")}
				for _, a := range leading {
					parts = append(parts, emitCallArg(a), Text(", "))
				}
				parts = append(parts,
					emitLambdaHeader(lam.Params),
					Nest(defaultIndent, Concat(HardLine(), emitLambdaSingleExprBody(expr))),
					HardLine(),
					Text(")"),
				)
				return Concat(callee, Concat(parts...))
			}

			// Single-expression tail lambda: dangle the `)` onto its own line
			// when the body breaks, rather than hugging it to the body's closing
			// brace (the `else { acc })` pile-up — there's no lambda block brace
			// here, so the hugged `)` collides with the body's own `}` from an
			// inline if/case). One Group governs the whole thing so the body
			// break and the dangle move together: flat hugs, broken dangles.
			parts := []Doc{Text("(")}
			for _, a := range leading {
				parts = append(parts, emitCallArg(a), Text(", "))
			}
			parts = append(parts,
				emitLambdaHeader(lam.Params),
				Nest(defaultIndent, Concat(Line(), emit(expr))),
				LineOrEmpty(),
				Text(")"),
			)
			return Concat(callee, Group(Concat(parts...)))
		}
		// Block-bodied tail lambda: hug the `)` — the `}` is the lambda's own
		// block close, so `})` is the standard close-then-paren (matches Rust,
		// Go, JS/Prettier).
		parts := []Doc{Text("(")}
		for _, a := range leading {
			parts = append(parts, emitCallArg(a), Text(", "))
		}
		parts = append(parts, emit(v.Args[n-1]), Text(")"))
		return Concat(callee, Concat(parts...))
	}
	return Concat(callee, emitArgList(v.Args))
}

// lambdaSingleExpr returns the body expression and true when lam's body is a
// single ExprStmt — the bare-expression lambda shape (`|x| x + 1`, `|x| if … {…}`)
// that emitLambda renders without block braces. Block-bodied lambdas (multi-
// statement, a single break/return/continue the parser wrapped in `{}`, or a
// single pipe, whose braces cannot go) return false; those keep the hugged `)`.
func lambdaSingleExpr(lam *ast.Lambda) (ast.Node, bool) {
	if lam.Body == nil || len(lam.Body.Stmts) != 1 {
		return nil, false
	}
	es, ok := lam.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || isPipeExpr(es.Expr) {
		// A pipe body keeps its braces (see emitLambda), so it hugs `})`
		// like a block body.
		return nil, false
	}
	return es.Expr, true
}

// emitTypeArgs renders a turbofish type-argument list, `<Int>` /
// `<String, Int>`. Type args are short, so they emit flat (no breaking).
func emitTypeArgs(args []ast.TypeExpr) Doc {
	parts := make([]Doc, 0, len(args)*2+1)
	parts = append(parts, Text("<"))
	for i, a := range args {
		if i > 0 {
			parts = append(parts, Text(", "))
		}
		parts = append(parts, Text(a.TypeString()))
	}
	parts = append(parts, Text(">"))
	return Concat(parts...)
}

// emitArgList produces the paren-delimited argument list portion of a call.
// Empty arg list renders as "()".
//
// Flat form:    "(a, b, c)"
// Broken form, multiple args: "(\n  a,\n  b,\n  c,\n)"
// Broken form, one arg:       "(\n  a\n)"
//
// For multiple args, the trailing comma on break is produced via IfBroken so
// it only appears when the Group actually breaks. A single visible arg omits
// that comma so wrappers around triple-quoted strings stay quiet.
func emitArgList(args []ast.Node) Doc {
	if len(args) == 0 {
		return Text("()")
	}
	parts := make([]Doc, 0, len(args)*2-1)
	for i, a := range args {
		if i > 0 {
			parts = append(parts, Text(","), Line())
		}
		parts = append(parts, emitCallArg(a))
	}
	inner := Concat(parts...)
	trailingComma := Nil()
	if len(args) > 1 {
		trailingComma = IfBroken(Nil(), Text(","))
	}
	return Group(Concat(
		Text("("),
		Nest(defaultIndent, Concat(LineOrEmpty(), inner)),
		trailingComma,
		LineOrEmpty(),
		Text(")"),
	))
}

// emitCallArg renders one argument inside a call's argument list. It is a
// thin wrapper over emit() that applies the call-arg layout exception for
// multi-line triple-quoted strings: content aligns with the opening `"""`'s
// column (`bodyIndent = 0`) rather than the default `+defaultIndent` rule
// used for bindings, struct field values, etc.
//
// The exception only fires when the AST shape is exactly "Call's Args slot
// directly contains a triple-quoted StringLit/StringInterp/TaggedString" —
// no NamedArg wrapper, no nested expression. Bindings (where the opening
// `"""` sits on a different line from the binding name) keep the `+defaultIndent`
// rule; it reads naturally there and the existing
// tests/triple_quoted_strings.nomi convention lives at that
// layout.
//
// Rationale: every major prescriptive formatter that strips indentation
// in multi-line strings (Python's Black, Swift's swift-format, Elixir's
// mix format) settled on content-at-opening. The closing `"""` at the
// same column serves as the strip baseline; no extra indent for free.
func emitCallArg(n ast.Node) Doc {
	switch v := n.(type) {
	case *ast.Binary:
		if v.Op == "|>" {
			return emitPipeChainForced(v)
		}
	case *ast.StringLit:
		switch {
		case v.Raw && v.Triple:
			body := tripleBodyDoc(v.Value)
			return rawBacktickFraming("", body, 0)
		case v.Triple:
			body := tripleBodyDoc(encodeTripleStringContent(v.Value))
			return tripleStringFraming("", body, 0)
		}
	case *ast.StringInterp:
		if v.Triple {
			var bodyParts []Doc
			for _, p := range v.Parts {
				switch part := p.(type) {
				case ast.StringText:
					bodyParts = append(bodyParts, tripleBodyDoc(encodeTripleStringContent(part.Value)))
				case ast.StringExpr:
					bodyParts = append(bodyParts, Text("${"), emitInterpolatedExpr(part.Expr), Text("}"))
				}
			}
			return tripleStringFraming("", Concat(bodyParts...), 0)
		}
	case *ast.TaggedString:
		if v.Triple {
			prefix := v.Tag
			var bodyParts []Doc
			for _, p := range v.Parts {
				switch part := p.(type) {
				case ast.StringText:
					if v.Raw {
						bodyParts = append(bodyParts, tripleBodyDoc(part.Value))
					} else {
						bodyParts = append(bodyParts, tripleBodyDoc(encodeTripleStringContent(part.Value)))
					}
				case ast.StringExpr:
					bodyParts = append(bodyParts, Text("${"), emitInterpolatedExpr(part.Expr), Text("}"))
				}
			}
			if v.Raw {
				return rawBacktickFraming(prefix, Concat(bodyParts...), 0)
			}
			return tripleStringFraming(prefix, Concat(bodyParts...), 0)
		}
	}
	return emit(n)
}

// emitLambda renders a lambda as "|params| body".
//
// Shapes:
//   - Zero params: "|| body".
//   - One or more params: "|p1, p2, ...| body".
//
// Body layout:
//   - Empty body: "|params| {}".
//   - Exactly one ExprStmt: emit the expression inline ("|params| expr"),
//     flat-or-break via Group.
//   - Otherwise: emit a block ("|params| {\n  stmt1\n  stmt2\n}").
func emitLambda(v *ast.Lambda) Doc {
	header := emitLambdaHeader(v.Params)

	body := v.Body
	if body == nil || len(body.Stmts) == 0 {
		return Concat(header, Text(" {}"))
	}

	// Single-statement body:
	//   * ExprStmt — emit bare expression `|params| expr`, flat-or-break.
	//   * Other stmt (break/return/continue) — the parser required a block
	//     wrapper, so emit as `|params| { stmt }` flat-or-break.
	if len(body.Stmts) == 1 {
		if es, ok := body.Stmts[0].(*ast.ExprStmt); ok {
			if statementOrBlockHasComment(body, es) {
				return emitLambdaBlock(header, body)
			}
			if lambdaExprBodyShouldBreak(es.Expr) {
				return Concat(
					header,
					Nest(defaultIndent, Concat(HardLine(), emitLambdaSingleExprBody(es.Expr))),
				)
			}
			if isPipeExpr(es.Expr) {
				// A lambda body never extends over a pipe: `|y| xs |> f()` is
				// `(|y| xs) |> f()`. So a pipe body keeps its braces, flat or
				// broken; dropping them changes what the program means.
				return Group(Concat(
					header,
					Text(" {"),
					Nest(defaultIndent, Concat(Line(), emit(es.Expr))),
					Line(),
					Text("}"),
				))
			}
			return Group(Concat(
				header,
				Nest(defaultIndent, Concat(Line(), emit(es.Expr))),
			))
		}
		if statementOrBlockHasComment(body, body.Stmts[0]) {
			return emitLambdaBlock(header, body)
		}
		return Group(Concat(
			header,
			Text(" {"),
			Nest(defaultIndent, Concat(Line(), emitWithTrivia(body.Stmts[0]))),
			Line(),
			Text("}"),
		))
	}

	// Multi-statement body: always break into `|params| {\n stmts \n}`.
	return emitLambdaBlock(header, body)
}

func lambdaExprBodyShouldBreak(expr ast.Node) bool {
	if v, ok := expr.(*ast.If); ok {
		return ifContainsControlFlow(v)
	}
	return false
}

func emitLambdaSingleExprBody(expr ast.Node) Doc {
	if v, ok := expr.(*ast.If); ok && ifContainsControlFlow(v) {
		return emitIfForceBroken(v)
	}
	return emit(expr)
}

func ifContainsControlFlow(v *ast.If) bool {
	if v == nil {
		return false
	}
	return nodeContainsControlFlow(v.Then) || nodeContainsControlFlow(v.Else)
}

func nodeContainsControlFlow(n ast.Node) bool {
	switch v := n.(type) {
	case nil:
		return false
	case *ast.Break, *ast.Continue, *ast.Return:
		return true
	case *ast.Block:
		for _, stmt := range v.Stmts {
			if nodeContainsControlFlow(stmt) {
				return true
			}
		}
	case *ast.ExprStmt:
		return nodeContainsControlFlow(v.Expr)
	case *ast.If:
		return ifContainsControlFlow(v)
	case *ast.Case:
		for _, branch := range v.Branches {
			if nodeContainsControlFlow(branch.Body) {
				return true
			}
		}
	}
	return false
}

func emitLambdaBlock(header Doc, body *ast.Block) Doc {
	bodyStmts := emitStatementDocs(body.Stmts)
	if len(body.GetTrailing()) > 0 {
		bodyStmts = append(bodyStmts, emitEndTrivia(body.GetTrailing()))
	}
	return Concat(
		header,
		Text(" {"),
		Nest(defaultIndent, Concat(HardLine(), Concat(bodyStmts...))),
		HardLine(),
		Text("}"),
	)
}

func statementOrBlockHasComment(body *ast.Block, stmt ast.Node) bool {
	if trailingHasComment(body.GetTrailing()) {
		return true
	}
	if nodeHasComment(stmt) {
		return true
	}
	if es, ok := stmt.(*ast.ExprStmt); ok && es.Expr != nil {
		return nodeHasComment(es.Expr)
	}
	return false
}

func nodeHasComment(n ast.Node) bool {
	ht, ok := n.(ast.HasTrivia)
	if !ok {
		return false
	}
	for _, t := range ht.GetLeading() {
		if t.Kind == ast.TriviaComment {
			return true
		}
	}
	for _, t := range ht.GetTrailing() {
		if t.Kind == ast.TriviaComment {
			return true
		}
	}
	return false
}

// emitLambdaHeader renders the "|p1, p2, ...|" prefix. Zero-params emit "||".
func emitLambdaHeader(params []ast.Param) Doc {
	if len(params) == 0 {
		return Text("||")
	}
	parts := make([]Doc, 0, len(params)*2+1)
	parts = append(parts, Text("|"))
	for i, p := range params {
		if i > 0 {
			parts = append(parts, Text(", "))
		}
		parts = append(parts, emitLambdaParam(p))
	}
	parts = append(parts, Text("|"))
	return Concat(parts...)
}

// emitLambdaParam renders a single lambda param. Supports bare name, name
// with default (`acc = 0`), name with type annotation (`x: Int`),
// destructuring patterns (tuple/struct/map), and destructuring patterns
// with defaults (`(a, b) = (0, 1)`). Type annotations must round-trip
// because the checker rejects unannotated local-lambda bindings.
func emitLambdaParam(p ast.Param) Doc {
	var parts []Doc
	if p.Destructure != nil {
		parts = []Doc{emitPattern(p.Destructure)}
		// Keep a load-bearing annotation (tuple/anon-struct, or a head naming
		// a different type); drop a redundant self-typing one. Same rule as
		// emitFuncDefParam.
		if p.TypeAnnotation != nil && !redundantSelfTypingAnnotation(p) {
			parts = append(parts, Text(": "), emitTypeExpr(p.TypeAnnotation))
		}
	} else {
		parts = []Doc{Text(p.Name)}
		if p.TypeAnnotation != nil {
			parts = append(parts, Text(": "), emitTypeExpr(p.TypeAnnotation))
		}
	}
	if p.Default != nil {
		parts = append(parts, Text(" = "), emit(p.Default))
	}
	return Concat(parts...)
}

// emitPattern renders a destructuring pattern. Used both for lambda params
// (tuple/struct/map destructures) and for case-expression arm patterns
// (which also include enum variants, list cons, wildcards, and literals).
// Literal patterns fall through to the general expression emit.
func emitPattern(n ast.Node) Doc {
	switch v := n.(type) {
	case *ast.IdentPattern:
		return Text(v.Name)
	case *ast.WildcardPattern:
		return Text("_")
	case *ast.TuplePattern:
		parts := make([]Doc, 0, len(v.Patterns)*2+1)
		parts = append(parts, Text("("))
		for i, pat := range v.Patterns {
			if i > 0 {
				parts = append(parts, Text(", "))
			}
			parts = append(parts, emitPattern(pat))
		}
		parts = append(parts, Text(")"))
		return Concat(parts...)
	case *ast.StructPattern:
		open := "{"
		if v.TypeName != nil {
			open = v.TypeName.TypeString() + "{"
		}
		// Chain-aware break: when this pattern's own depth crosses the
		// threshold, render in broken form AND propagate mustBreak to
		// every child so nested compound patterns in the same chain
		// also break.
		if compoundDepth(v) >= maxCompoundDepth && len(v.Fields) > 0 {
			elems := make([]Doc, 0, len(v.Fields))
			for _, f := range v.Fields {
				switch {
				case f.Pattern != nil:
					elems = append(elems, Concat(Text(f.Name), Text(": "), emitPatternChildMustBreak(f.Pattern)))
				case f.Binding != "" && f.Binding != f.Name:
					elems = append(elems, Concat(Text(f.Name), Text(": "), Text(f.Binding)))
				default:
					elems = append(elems, Text(f.Name))
				}
			}
			return forceBrokenBraced(open, "}", elems)
		}
		elems := make([]Doc, 0, len(v.Fields))
		for _, f := range v.Fields {
			switch {
			case f.Pattern != nil:
				// Value match: `name: <pattern>`.
				elems = append(elems, Concat(Text(f.Name), Text(": "), emitPattern(f.Pattern)))
			case f.Binding != "" && f.Binding != f.Name:
				// Rename: `name: binding`.
				elems = append(elems, Concat(Text(f.Name), Text(": "), Text(f.Binding)))
			default:
				// Field punning: just the name.
				elems = append(elems, Text(f.Name))
			}
		}
		return emitBracedList(open, "}", elems)
	case *ast.EnumPattern:
		name := Text(v.Variant.TypeString())
		switch {
		case v.Binding != "":
			// Fast-path: Variant(binding).
			return Concat(name, Text("("), Text(v.Binding), Text(")"))
		case v.Payload != nil:
			// Flat-destructure shorthand `Variant(a, b, ...)` — preserved as
			// authored. The parser sets Flat=true only when the user wrote
			// the comma list directly inside the variant's parens (no inner
			// parens around the tuple). Emit the comma list without
			// re-parenthesizing so `Pair(a, b)` round-trips intact.
			if tp, ok := v.Payload.(*ast.TuplePattern); ok && tp.Flat {
				parts := make([]Doc, 0, len(tp.Patterns)*2+3)
				parts = append(parts, name, Text("("))
				for i, pat := range tp.Patterns {
					if i > 0 {
						parts = append(parts, Text(", "))
					}
					parts = append(parts, emitPattern(pat))
				}
				parts = append(parts, Text(")"))
				return Concat(parts...)
			}
			// General: Variant(<nested pattern>) — the payload is a single
			// pattern. Tuple-payload variants spell their tuple destructuring
			// explicitly (`Some((n, s))`), preserving the 1-arg-constructor
			// invariant. The flat shorthand above only applies to tuple-
			// distinct constructors.
			return Concat(name, Text("("), emitPattern(v.Payload), Text(")"))
		default:
			// Bare variant — only valid for variants with no data
			// (None, True, embedded zero-sized types).
			return name
		}
	case *ast.ListPattern:
		open := "["
		if v.TypeName != nil {
			open = v.TypeName.TypeString() + "["
		}
		hasEndTrivia := len(v.EndTrivia) > 0
		// Spread patterns (`[a, b, ..rest]`) skip the
		// emitBracedList/forceBrokenBraced path: the trailing `..rest`
		// piece is not a comma-separated entry, and the existing
		// formatter convention (mirroring ListSpreadLit) is to emit
		// spread patterns flat regardless.
		if v.TailSpread != nil {
			parts := make([]Doc, 0, len(v.Heads)*2+4)
			parts = append(parts, Text(open))
			for i, h := range v.Heads {
				if i > 0 {
					parts = append(parts, Text(", "))
				}
				parts = append(parts, emitPattern(h))
			}
			if len(v.Heads) > 0 {
				parts = append(parts, Text(", "))
			}
			parts = append(parts, Text(".."), emitPattern(v.TailSpread), Text("]"))
			return Concat(parts...)
		}
		// Chain-aware break: see StructPattern above.
		if compoundDepth(v) >= maxCompoundDepth && len(v.Heads) > 0 {
			elems := make([]Doc, 0, len(v.Heads))
			for _, h := range v.Heads {
				elems = append(elems, emitPatternChildMustBreak(h))
			}
			return forceBrokenBracedWithEndTrivia(open, "]", elems, v.EndTrivia)
		}
		elems := make([]Doc, 0, len(v.Heads))
		for _, h := range v.Heads {
			elems = append(elems, emitPattern(h))
		}
		// EndTrivia forces the broken form.
		if hasEndTrivia {
			return forceBrokenBracedWithEndTrivia(open, "]", elems, v.EndTrivia)
		}
		return emitBracedList(open, "]", elems)
	case *ast.MapPattern:
		open := "{"
		if v.TypeName != nil {
			open = v.TypeName.TypeString() + "{"
		}
		hasEndTrivia := len(v.EndTrivia) > 0
		// Chain-aware break: see StructPattern above.
		if compoundDepth(v) >= maxCompoundDepth && len(v.Entries) > 0 {
			elems := make([]Doc, 0, len(v.Entries))
			for _, e := range v.Entries {
				elems = append(elems, Concat(emit(e.Key), Text(" => "), emitPatternChildMustBreak(e.Pattern)))
			}
			return forceBrokenBracedWithEndTrivia(open, "}", elems, v.EndTrivia)
		}
		elems := make([]Doc, 0, len(v.Entries))
		for _, e := range v.Entries {
			elems = append(elems, Concat(emit(e.Key), Text(" => "), emitPattern(e.Pattern)))
		}
		// EndTrivia forces the broken form.
		if hasEndTrivia {
			return forceBrokenBracedWithEndTrivia(open, "}", elems, v.EndTrivia)
		}
		return emitBracedList(open, "}", elems)
	}
	// Literal patterns (IntLit, StringLit, FloatLit, etc.) and any other
	// expression-like node fall through to the general expression emit.
	return emit(n)
}

// precedence returns a numeric precedence level for binary operators,
// matching the parser's infixPrecedence table in parser/parser.go.
// Higher number = tighter binding.
//
//	or           = 2
//	and          = 3
//	== !=        = 4
//	< > <= >=    = 5
//	|>           = 6
//	.. ..=       = 7
//	+ -          = 8
//	* / %        = 9
//	(unary)      = 10
func precedence(op string) int {
	switch op {
	case "|>":
		return 6
	case "or":
		return 2
	case "and":
		return 3
	case "==", "!=":
		return 4
	case "<", ">", "<=", ">=":
		return 5
	case "+", "-":
		return 8
	case "*", "/", "%":
		return 9
	}
	// Unknown operator — treat as tight-binding; callers add parens
	// defensively when the child has strictly-lower precedence.
	return 100
}

// unaryPrecedence is the binding level of unary `-` and `!` (parser's
// prefixPrecedence). It's greater than any binary precedence, so any
// binary child of a unary needs parenthesizing.
const unaryPrecedence = 10

// parenIfLooser wraps child in parens when its operator binds looser than
// `minPrec`. Only Binary children can bind loose enough to matter; all
// other expression forms are self-delimiting.
func parenIfLooser(child ast.Node, minPrec int) Doc {
	if b, ok := child.(*ast.Binary); ok {
		if precedence(b.Op) < minPrec {
			return Concat(Text("("), emit(child), Text(")"))
		}
	}
	return emit(child)
}

// emitFuncDef renders a function definition:
//
//	[pub ]fn name[<T, ...>](params)[: ReturnType] {
//	  body
//	}
//
// Doc comments attached via the parser's attachDoc mechanism are re-emitted
// as consecutive `/// <line>` lines above the declaration. Params use the
// same Group+LineOrEmpty machinery as call-site arg lists, so they break to
// one-per-line with a trailing comma when the flat signature doesn't fit.
// The body always breaks across lines (emitBlockForceBroken) — `fn`
// declarations follow the dominant convention from gofmt, prettier, dart,
// zig, swift-format, and rustfmt's default. An empty body stays flat as
// `{}` (emitBlockForceBroken short-circuits the zero-statement case). This
// rule applies to top-level `fn`s and to impl-block methods (which delegate
// to emitFuncDef); inline expression-position blocks like `if cond { x }`
// keep their flat form.
func emitFuncDef(v *ast.FuncDef) Doc {
	parts := make([]Doc, 0, 8)

	// Doc comment: stored as lines joined by "\n" with the `///` prefix
	// stripped by the lexer/parser. Re-prepend `/// ` per line here.
	parts = append(parts, emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)...)

	parts = append(parts, emitAttachedTests(v.AttachedTests)...)

	parts = append(parts, emitFuncDefHeader(v), Text(" "), emitBlockForceBroken(v.Body))
	return Concat(parts...)
}

// emitDecorator renders a single `@name arg1, arg2` decorator. Args are
// AST nodes — TypeExpr (e.g. `@derive Formatted`) or *Ident; each renders
// with its existing emit helper.
func emitDecorator(d *ast.Decorator) Doc {
	parts := make([]Doc, 0, 1+2*len(d.Args))
	parts = append(parts, Text("@"+d.Name))
	for i, arg := range d.Args {
		if i == 0 {
			parts = append(parts, Text(" "))
		} else {
			parts = append(parts, Text(", "))
		}
		switch a := arg.(type) {
		case ast.TypeExpr:
			parts = append(parts, emitTypeExpr(a))
		case *ast.Ident:
			parts = append(parts, Text(a.Name))
		default:
			// Defensive — the parser only produces TypeExpr or *Ident here
			// today, but keep formatter resilient to future arg shapes.
			parts = append(parts, emit(a))
		}
	}
	return Concat(parts...)
}

// emitFuncDefHeader renders the function signature up to but not including
// the body:
//
//	[pub ]fn name[<T, ...>](params)[: ReturnType][ where T: Bound]
func emitFuncDefHeader(v *ast.FuncDef) Doc {
	parts := make([]Doc, 0, 6)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	if v.ImplFunction {
		parts = append(parts, Text("impl "))
	}
	parts = append(parts, Text("fn "))
	if v.ImplIface != nil && v.ImplIfaceSourceQualified {
		parts = append(parts, emitTypeExpr(v.ImplIface), Text("."))
	}
	parts = append(parts, Text(v.Name))

	if len(v.TypeParams) > 0 {
		tp := make([]Doc, 0, len(v.TypeParams)*2-1)
		for i, p := range v.TypeParams {
			if i > 0 {
				tp = append(tp, Text(", "))
			}
			tp = append(tp, Text(p.Name))
			for j, b := range p.Bounds {
				if j == 0 {
					tp = append(tp, Text(": "))
				} else {
					tp = append(tp, Text(" and "))
				}
				tp = append(tp, emitTypeExpr(b))
			}
		}
		parts = append(parts, Text("<"), Concat(tp...), Text(">"))
	}

	paramDocs := make([]Doc, 0, len(v.Params))
	for _, p := range v.Params {
		paramDocs = append(paramDocs, emitFuncDefParam(p))
	}
	parts = append(parts, emitBracedList("(", ")", paramDocs))

	if v.ReturnTypeExpr != nil {
		parts = append(parts, Text(": "), emitTypeExpr(v.ReturnTypeExpr))
	}
	parts = append(parts, emitWhereClause(v.WhereClauses)...)

	return Concat(parts...)
}

// emitFuncDefParam renders one function-definition parameter:
//
//	name[: Type][ = default]
//
// Destructuring params (tuple/struct/map) render the pattern in place of the
// name; this is rare in top-level function definitions but kept for parity
// with the lambda emitter.
func emitFuncDefParam(p ast.Param) Doc {
	var head Doc
	if p.Destructure != nil {
		head = emitPattern(p.Destructure)
	} else {
		head = Text(p.Name)
	}
	parts := []Doc{head}
	// Canonicalize self-typing params: drop a redundant annotation whose type
	// the pattern head already names (`Dur(x): Dur` → `Dur(x)`). Non-self-typing
	// annotations (tuple/anon-struct, or a head naming a *different* type) are
	// load-bearing and kept.
	if p.TypeAnnotation != nil && !redundantSelfTypingAnnotation(p) {
		parts = append(parts, Text(": "), emitTypeExpr(p.TypeAnnotation))
	}
	if p.Default != nil {
		parts = append(parts, Text(" = "), emit(p.Default))
	}
	return Concat(parts...)
}

// patternHeadTypeString returns the type name a self-typing pattern head names,
// or "" if the pattern is type-less (tuple, anon struct, dot-variant, plain).
// Used by the fmt canonicalizer to decide whether a `: T` annotation is
// redundant. Mirrors the analyzer's paramPatternHeadType, on the AST.
func patternHeadTypeString(pattern ast.Node) string {
	switch p := pattern.(type) {
	case *ast.EnumPattern:
		switch v := p.Variant.(type) {
		case *ast.SimpleType:
			return v.Name // `Dur(x)` → Dur
		case *ast.QualifiedType:
			return v.Module // `E.V(x)` → E (the qualifier is the type)
		}
	case *ast.StructPattern:
		if p.TypeName != nil {
			return p.TypeName.TypeString() // `Point{x, y}` → Point
		}
	}
	return ""
}

// redundantSelfTypingAnnotation reports whether a destructuring param's
// `: T` annotation merely restates the type its pattern head already names.
func redundantSelfTypingAnnotation(p ast.Param) bool {
	if p.Destructure == nil || p.TypeAnnotation == nil {
		return false
	}
	head := patternHeadTypeString(p.Destructure)
	return head != "" && head == p.TypeAnnotation.TypeString()
}

// emitTypeExpr renders a TypeExpr as a Doc. Each type-expression node has
// its own case so nested anon structs participate in the width budget no
// matter how deeply they sit (`List<{...}>`, `(String, {...})`,
// `(Int, {...}) -> R`, `mod.Foo<{...}>`).
//
// Containers (GenericType, FuncType, QualifiedType) are intentionally
// *not* wrapped in their own Group — they render as flat text glue with
// recursive emitTypeExpr calls. This keeps the "outer container stays
// inline, inner anon struct flows multi-line" shape — same approach
// Prettier takes for `Promise<{...}>` in TypeScript. Long containers of
// only-simple types (`(VeryLongTypeA, VeryLongTypeB, ...)`) therefore
// don't auto-break at the container; they spill, same as before this
// change. The escape hatch when that bites is a `typealias` for the
// inner names.
func emitTypeExpr(t ast.TypeExpr) Doc {
	if t == nil {
		return Nil()
	}
	switch n := t.(type) {
	case *ast.SimpleType:
		return Text(n.Name)
	case *ast.SelfType:
		return Text("self")
	case *ast.QualifiedType:
		return Concat(Text(n.Module), Text("."), emitTypeExpr(n.Member))
	case *ast.DotVariantType:
		// Dot-leading variant shorthand used in TypeName slots of
		// literal-attach forms (`.Obj{"k" => v}`, `.Arr[...]`, `.Rect{w, h}`)
		// and patterns. Same round-trip-as-written semantics as DotVariant.
		return Concat(Text("."), Text(n.Name))
	case *ast.GenericType:
		parts := []Doc{Text(n.Name), Text("<")}
		for i, p := range n.Params {
			if i > 0 {
				parts = append(parts, Text(", "))
			}
			parts = append(parts, emitTypeExpr(p))
		}
		parts = append(parts, Text(">"))
		return Concat(parts...)
	case *ast.FuncType:
		// FuncType doubles as tuple type when Return is nil. Tuples render
		// transparently — long tuples don't auto-break themselves; the inner
		// anon-struct Group is the only break trigger. Function types (with
		// Return) wrap the param list in a Group so the whole signature
		// (including `-> R`) fits-checks together. Function-type param
		// syntax does NOT permit a trailing comma inside `(...)`, so the
		// Group emit here is a hand-rolled variant of emitBracedList without
		// the IfBroken trailing-comma — using emitBracedList directly would
		// produce unparseable output when the param list breaks.
		paramDocs := make([]Doc, 0, len(n.Params))
		for _, p := range n.Params {
			paramDocs = append(paramDocs, emitTypeExpr(p))
		}
		if n.Return == nil {
			parts := []Doc{Text("(")}
			for i, p := range paramDocs {
				if i > 0 {
					parts = append(parts, Text(", "))
				}
				parts = append(parts, p)
			}
			parts = append(parts, Text(")"))
			return Concat(parts...)
		}
		// max(0, len*2-1) — len=0 (zero-arg `() -> T` lambda types,
		// the layer-1 `spawn`/`await` extern signatures) would otherwise
		// underflow to a negative cap and panic in make.
		innerCap := len(paramDocs)*2 - 1
		if innerCap < 0 {
			innerCap = 0
		}
		inner := make([]Doc, 0, innerCap)
		for i, p := range paramDocs {
			if i > 0 {
				inner = append(inner, Text(","), Line())
			}
			inner = append(inner, p)
		}
		paramListGroup := Group(Concat(
			Text("("),
			Nest(defaultIndent, Concat(LineOrEmpty(), Concat(inner...))),
			LineOrEmpty(),
			Text(")"),
		))
		return Concat(paramListGroup, Text(" -> "), emitTypeExpr(n.Return))
	case *ast.AnonStructType:
		return emitAnonStructType(n)
	}
	// Fallback for any TypeExpr node not enumerated above: render via the
	// AST's TypeString(). Keeps the formatter robust if a new type-expr
	// node lands without a matching emit case.
	return Text(t.TypeString())
}

// emitAnonStructType renders an anonymous struct type. Delegates to the
// shared brace-list helper since the layout is identical to a struct-
// shaped enum variant payload.
func emitAnonStructType(t *ast.AnonStructType) Doc {
	return emitBracedStructFieldsWithEndTrivia(t.Fields, t.EndTrivia)
}

// emitBracedStructFieldsWithEndTrivia renders a `{field1, field2, ...}`
// braced list of struct fields. Width-based, with a preserve-user-multi-line escape:
//
//	flat:                {a: Int, b: String}
//	broken (auto):       {
//	                       a: Int,
//	                       b: String,
//	                     }
//	broken (preserved):  same as auto, but forced regardless of width
//
// If the source had fields on more than one line the multi-line shape is
// preserved unconditionally (HardLine, no Group). Mirrors the enum and
// fn-body in-progress-edit fix: typing `{` then a field then saving used
// to collapse the layout the user just made room in. Otherwise width
// decides.
//
// Trailing comma appears in any broken form (matches named struct
// declarations). Empty body renders as `{}` for resilience.
//
// Used by both anonymous struct type expressions (`fn f(p: {a: Int}): R`)
// and struct-shaped enum variant payloads (`HttpError {status: Int, ...}`)
// so the brace-bounded-fields layout stays identical wherever it appears.
//
// endTrivia is rendered between the last field and the closing `}`.
// Captured by parseAnonStructType / parseEnumVariant (struct shape) when
// a comment / blank line sits before the closing brace. EndTrivia or any
// inter-field LeadingComments forces the broken form (a `// comment`
// cannot share a line with another field or with `}`).
func emitBracedStructFieldsWithEndTrivia(fields []ast.StructField, endTrivia []ast.Trivia) Doc {
	if len(fields) == 0 {
		if len(endTrivia) == 0 {
			return Text("{}")
		}
		return forceBrokenBracedWithEndTrivia("{", "}", nil, endTrivia)
	}
	elems := make([]Doc, 0, len(fields))
	for _, f := range fields {
		elems = append(elems, emitStructField(f))
	}
	if structFieldsUserMultiLine(fields) || len(endTrivia) > 0 || structFieldsHaveComments(fields) {
		trailing := make([][]ast.Trivia, len(fields))
		for i, f := range fields {
			trailing[i] = f.Trailing
		}
		return forceBrokenBracedWithTrailing("{", "}", elems, trailing, endTrivia)
	}
	body := []Doc{LineOrEmpty(), elems[0]}
	for _, e := range elems[1:] {
		body = append(body, Text(","), Line(), e)
	}
	return Group(Concat(
		Text("{"),
		Nest(defaultIndent, Concat(body...)),
		IfBroken(Nil(), Text(",")),
		LineOrEmpty(),
		Text("}"),
	))
}

// structFieldsUserMultiLine reports whether the source had this field
// list spread across multiple lines. The first and last field's Line
// bracket the whole list — any gap means the user split it.
func structFieldsUserMultiLine(fields []ast.StructField) bool {
	if len(fields) < 2 {
		return false
	}
	return fields[0].Line != fields[len(fields)-1].Line
}

// structFieldsHaveComments reports whether any field carries inter-field
// LeadingComments or a same-line Trailing comment. Symmetric with
// structFieldValsHaveLeading on the literal side: the broken (multi-line)
// form is the only layout where a `// comment` beside a field is
// syntactically valid.
func structFieldsHaveComments(fields []ast.StructField) bool {
	for _, f := range fields {
		if len(f.LeadingComments) > 0 || len(f.Trailing) > 0 {
			return true
		}
	}
	return false
}

// forceBrokenBraced renders `{e1, e2, ...}` always-broken: each elem on
// its own indented line, trailing comma on the last. Used by the preserve-
// user-multi-line paths so the layout doesn't depend on width.
//
// Wrapped in LocalBroken so the inner HardLines don't propagate up to
// ancestor Groups (the param-list Group, the call-arg-list Group, etc.).
// Without LocalBroken, a multi-line struct literal inside a single param
// would force the param list to break too — see LocalBroken doc for the
// underlying Wadler limitation.
func forceBrokenBraced(open, close string, elems []Doc) Doc {
	return forceBrokenBracedWithEndTrivia(open, close, elems, nil)
}

// forceBrokenBracedWithEndTrivia is forceBrokenBraced plus an optional
// trailing EndTrivia block rendered between the last element (including
// its trailing comma) and the closing delimiter. Used by struct / anon
// struct emit paths to preserve trailing in-body comments.
//
// When elems is empty and endTrivia is set, the trivia renders by itself
// inside the braces.
func forceBrokenBracedWithEndTrivia(open, close string, elems []Doc, endTrivia []ast.Trivia) Doc {
	return forceBrokenBracedWithTrailing(open, close, elems, nil, endTrivia)
}

// forceBrokenBracedWithTrailing is forceBrokenBracedWithEndTrivia plus
// each element's same-line comments, rendered after its comma
// (`x: Int, // note`). trailing is indexed like elems and may be nil.
func forceBrokenBracedWithTrailing(open, close string, elems []Doc, trailing [][]ast.Trivia, endTrivia []ast.Trivia) Doc {
	body := make([]Doc, 0, len(elems)*4+1)
	for i, e := range elems {
		body = append(body, HardLine(), e, Text(","))
		if i < len(trailing) {
			body = append(body, emitSameLineComments(trailing[i])...)
		}
	}
	if len(endTrivia) > 0 {
		body = append(body, emitEndTrivia(endTrivia))
	}
	return LocalBroken(Concat(
		Text(open),
		Nest(defaultIndent, Concat(body...)),
		HardLine(),
		Text(close),
	))
}

// emitDocComment renders doc comment lines as `/// ` prefixed HardLines.
// Empty lines inside the docs round-trip as bare `///`.
func emitDocComment(doc string) []Doc {
	if doc == "" {
		return nil
	}
	var parts []Doc
	for _, line := range strings.Split(doc, "\n") {
		if line == "" {
			parts = append(parts, Text("///"), HardLine())
		} else {
			parts = append(parts, Text("/// "+line), HardLine())
		}
	}
	return parts
}

func emitDocCommentWithTrailingSeparators(doc string) []Doc {
	if doc == "" {
		return nil
	}
	lines := strings.Split(doc, "\n")
	trailingBlanks := 0
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		trailingBlanks++
		lines = lines[:len(lines)-1]
	}
	parts := emitDocComment(strings.Join(lines, "\n"))
	for i := 0; i < trailingBlanks; i++ {
		parts = append(parts, Text("//"), HardLine())
	}
	return parts
}

func emitDocBeforeAttachedTests(doc string, tests []ast.AttachedTest) []Doc {
	if len(tests) == 0 {
		return emitDocCommentWithTrailingSeparators(doc)
	}
	doc = docBeforeAttachedTests(doc, tests)
	return emitDocCommentWithTrailingSeparators(doc)
}

func docBeforeAttachedTests(doc string, tests []ast.AttachedTest) string {
	after := attachedTestsDocAfter(tests)
	if doc == "" || after == "" {
		return doc
	}
	if doc == after {
		return ""
	}
	suffix := "\n" + after
	if strings.HasSuffix(doc, suffix) {
		return strings.TrimSuffix(doc, suffix)
	}
	return doc
}

func attachedTestsDocAfter(tests []ast.AttachedTest) string {
	var docs []string
	for i := range tests {
		if tests[i].DocAfter != "" {
			docs = append(docs, tests[i].DocAfter)
		}
	}
	return strings.Join(docs, "\n")
}

func emitAttachedTestAfter(test ast.AttachedTest, betweenAttachedTests bool) []Doc {
	if len(test.After) == 0 {
		parts := emitDocComment(test.DocAfter)
		parts = append(parts, emitLeadingComments(test.GetLeading())...)
		return parts
	}
	parts := make([]Doc, 0, len(test.After)*2)
	for i, item := range test.After {
		if item.IsDoc {
			if item.Doc == "" && (betweenAttachedTests || attachedTestAfterItemIsTrailingDocBlank(test.After, i)) {
				parts = append(parts, Text("//"), HardLine())
			} else if item.Doc == "" {
				parts = append(parts, Text("///"), HardLine())
			} else {
				parts = append(parts, emitDocComment(item.Doc)...)
			}
			continue
		}
		if item.Trivia.Kind == ast.TriviaComment || item.Trivia.Kind == ast.TriviaBlankLine {
			parts = append(parts, emitLeadingComments([]ast.Trivia{item.Trivia})...)
		}
	}
	return parts
}

func attachedTestAfterItemIsTrailingDocBlank(after []ast.AttachedTestAfter, index int) bool {
	for i := index + 1; i < len(after); i++ {
		if after[i].IsDoc && after[i].Doc != "" {
			return false
		}
	}
	return true
}

func emitAttachedTests(tests []ast.AttachedTest) []Doc {
	if len(tests) == 0 {
		return nil
	}
	parts := make([]Doc, 0, len(tests)*2)
	for i := range tests {
		if tests[i].Body == nil {
			continue
		}
		appendDocAfter := func() {
			parts = append(parts, emitAttachedTestAfter(tests[i], i < len(tests)-1)...)
		}
		parts = append(parts, emitAttachedDocTest(tests[i]), HardLine())
		appendDocAfter()
	}
	return parts
}

func emitAttachedDocTest(test ast.AttachedTest) Doc {
	body := attachedTestCanonicalBody(test)
	bodyText := renderBlockBody(body)
	lines := strings.Split(bodyText, "\n")
	if bodyText == "" {
		lines = nil
	}
	parts := make([]Doc, 0, len(lines)*2)
	for len(lines) > 0 && lines[0] == "" {
		parts = append(parts, Text("//"), HardLine())
		lines = lines[1:]
	}
	if bodyText != "" {
		for _, line := range lines {
			if line == "" {
				parts = append(parts, Text("//!"), HardLine())
			} else {
				parts = append(parts, Text("//! "+line), HardLine())
			}
		}
	}
	for i := 0; i < test.TrailingPromptBlanks; i++ {
		parts = append(parts, Text("//"), HardLine())
	}
	if len(parts) > 0 {
		parts = parts[:len(parts)-1]
	}
	return Concat(parts...)
}

func attachedTestCanonicalBody(test ast.AttachedTest) *ast.Block {
	if test.Body == nil {
		return &ast.Block{}
	}
	if test.Kind == "test" || !test.Inline {
		return test.Body
	}
	stmt := singleInlineAttachedTestStmt(test.Body)
	if stmt == nil {
		return test.Body
	}
	return &ast.Block{
		Stmts: []ast.Node{stmt},
		Line:  test.Body.Line,
		Col:   test.Body.Col,
	}
}

func singleInlineAttachedTestStmt(body *ast.Block) ast.Node {
	if body == nil || len(body.Stmts) != 1 || len(body.GetLeading()) > 0 || len(body.GetTrailing()) > 0 {
		return nil
	}
	switch stmt := body.Stmts[0].(type) {
	case *ast.Assertion:
		if stmt.Expr == nil {
			return nil
		}
		return stmt
	case *ast.PatternDestructure:
		return stmt
	default:
		return nil
	}
}

// emitTypeDeclDecorators renders the `@derive ...` decorators (and any other
// future type-decl decorators) one per line above the declaration, in source
// order. Currently the parser only allows `@derive` on type decls (struct /
// enum / typedef), but the emitter is decorator-name agnostic — it just
// renders whatever the AST carries. Used by emitStructDef / emitEnumDef /
// emitTypeDef. Without this, `nomi fmt -w` silently strips decorators on
// type decls — same hazard as FuncDef decorators flagged in §emitFuncDef.
func emitTypeDeclDecorators(decorators []ast.Decorator) []Doc {
	if len(decorators) == 0 {
		return nil
	}
	parts := make([]Doc, 0, len(decorators)*2)
	for i := range decorators {
		parts = append(parts, emitDecorator(&decorators[i]), HardLine())
	}
	return parts
}

// emitTypeParams renders `<T, U>` (comma-separated) or Nil when
// there are none. Bounds are rendered as `where` clauses on the declaration.
func emitTypeParams(tp []ast.TypeParam) Doc {
	if len(tp) == 0 {
		return Nil()
	}
	parts := make([]Doc, 0, len(tp)*2+1)
	parts = append(parts, Text("<"))
	for i, p := range tp {
		if i > 0 {
			parts = append(parts, Text(", "))
		}
		parts = append(parts, Text(p.Name))
	}
	parts = append(parts, Text(">"))
	return Concat(parts...)
}

// emitMultilineBracedListWithEndTrivia renders a declaration body
// (struct/enum/interface/impl/extend members) as an always-broken braced
// block:
//
//	<open>{
//	  elem1,
//	  elem2,
//	  ...
//	<close>}
//
// Unlike emitBracedList, this never flattens: declaration bodies are
// conventionally multi-line even when short. If `comma` is true each element
// is followed by `,`; when false the elements stand alone (used for method
// declarations where trailing comma would be syntactically invalid). Empty
// body renders as `<open>{}<close>`.
//
// endTrivia is rendered between the last element (including the trailing
// comma) and the closing delimiter. Used by struct / enum / interface
// declarations to preserve trailing in-body comments captured by the
// parser: the trivia renders on its own indented line via emitEndTrivia,
// then the closing delimiter sits flush at the body indent.
//
// An empty element list with EndTrivia still renders the trivia block —
// `pub interface Foo { // only trivia }` is well-formed and the formatter
// must preserve it.
func emitMultilineBracedListWithEndTrivia(open, close string, elems []Doc, comma bool, endTrivia []ast.Trivia) Doc {
	if len(elems) == 0 {
		if len(endTrivia) == 0 {
			return Text(open + close)
		}
		return Concat(
			Text(open),
			Nest(defaultIndent, emitEndTrivia(endTrivia)),
			HardLine(),
			Text(close),
		)
	}
	body := make([]Doc, 0, len(elems)*2+2)
	for i, e := range elems {
		body = append(body, HardLine())
		body = append(body, e)
		if comma && i < len(elems)-1 {
			body = append(body, Text(","))
		}
	}
	// Trailing comma on last element when requested.
	if comma {
		body = append(body, Text(","))
	}
	if len(endTrivia) > 0 {
		body = append(body, emitEndTrivia(endTrivia))
	}
	return Concat(
		Text(open),
		Nest(defaultIndent, Concat(body...)),
		HardLine(),
		Text(close),
	)
}

// emitStructField renders one struct field: `name: Type[ = default]`.
// LeadingComments captured by the parser (inter-field comments) emit
// above the field on their own line(s) so they round-trip cleanly,
// followed by the `///` doc comment (closest to the field). The
// enclosing braced-list helper places each emitStructField result on a
// fresh HardLine, so a leading-comment block stacks naturally above the
// field with the same indent.
func emitStructField(f ast.StructField) Doc {
	parts := []Doc{}
	parts = append(parts, emitLeadingComments(f.LeadingComments)...)
	parts = append(parts, emitDocComment(f.Doc)...)
	parts = append(parts, Text(f.Name), Text(": "), emitTypeExpr(f.TypeAnnotation))
	if f.Default != nil {
		parts = append(parts, Text(" = "), emit(f.Default))
	}
	return Concat(parts...)
}

// emitLeadingComments renders trivia captured on a body-member's
// LeadingComments slot. Each comment lands on its own line above the
// member (the caller's HardLine separator places the member itself);
// blank-line trivia emits an extra HardLine to preserve gaps.
//
// Returns the segments to splice before the member's own content — the
// caller decides whether to wrap them in a Concat (or splice in-place).
// Empty input returns an empty slice; the caller can append unchanged.
func emitLeadingComments(trivia []ast.Trivia) []Doc {
	if len(trivia) == 0 {
		return nil
	}
	parts := make([]Doc, 0, len(trivia)*2)
	for _, t := range trivia {
		switch t.Kind {
		case ast.TriviaComment:
			parts = append(parts, Text(t.Text), HardLine())
		case ast.TriviaBlankLine:
			parts = append(parts, HardLine())
		}
	}
	return parts
}

func withoutBlankTrivia(trivia []ast.Trivia) []ast.Trivia {
	if len(trivia) == 0 {
		return nil
	}
	filtered := make([]ast.Trivia, 0, len(trivia))
	for _, t := range trivia {
		if t.Kind == ast.TriviaBlankLine {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// emitStructDeclField renders one struct-declaration body field in the
// canonical form: `name: Type[ = default]` — one per line, no commas.
// Leading `//` trivia and the `///` doc comment stack above the item (doc
// closest to the field). The comma-separated form stays in emitStructField
// for anon-struct types and struct-shaped enum variant payloads.
func emitStructDeclField(f ast.StructField) Doc {
	parts := []Doc{}
	parts = append(parts, emitLeadingComments(f.LeadingComments)...)
	parts = append(parts, emitDocComment(f.Doc)...)
	parts = append(parts, Text(f.Name), Text(": "), emitTypeExpr(f.TypeAnnotation))
	if f.Default != nil {
		parts = append(parts, Text(" = "), emit(f.Default))
	}
	parts = append(parts, emitSameLineComments(f.Trailing)...)
	return Concat(parts...)
}

// emitSameLineComments renders a body member's Trailing comments after the
// member on its own line: ` // note`. Hidden, as in emitWithTriviaParts, so
// the comment never decides whether an enclosing group breaks.
func emitSameLineComments(trivia []ast.Trivia) []Doc {
	var parts []Doc
	for _, t := range trivia {
		if t.Kind == ast.TriviaComment {
			parts = append(parts, Hidden(Concat(Text(" "), Text(t.Text))))
		}
	}
	return parts
}

// appendTypeBodyItems renders a type body's legacy/internal non-shape items
// and appends them to members.
// A blank line is forced between executable/declarative items — and before
// the first such item when field/variant/contract members precede it —
// unless the item already carries a leading blank (avoids doubling).
// Mirrors emitImplBlock's adjacent-item rule and emitFile's top-level rule.
func appendTypeBodyItems(members []Doc, items []ast.Node) []Doc {
	items = orderTypeBodyItems(items)
	hasPrior := len(members) > 0
	for i := 0; i < len(items); i++ {
		item := items[i]
		if isImplConformance(item) {
			start := i
			for i+1 < len(items) && isImplConformance(items[i+1]) {
				i++
			}
			d := emitConformanceLines(items[start : i+1])
			if hasPrior || start > 0 {
				d = Concat(HardLine(), d)
			}
			members = append(members, d)
			continue
		}
		tightConformance := i > 0 && isImplConformance(items[i-1]) && isImplConformance(item)
		d := emitWithTrivia(item)
		if (!hasPrior && i == 0) || tightConformance {
			d = emitWithTriviaNoLeadingBlank(item)
		}
		if (hasPrior || i > 0) && !hasLeadingBlank(item) {
			if !tightConformance {
				d = Concat(HardLine(), d)
			}
		}
		members = append(members, d)
	}
	return members
}

func emitConformanceLines(items []ast.Node) Doc {
	var lines []Doc
	var prevDerive bool
	for i := 0; i < len(items); {
		conf, ok := items[i].(*ast.ImplConformance)
		if !ok {
			i++
			continue
		}
		derive := conf.Derive
		start := i
		originalLine := conf.Line
		hasOwnTrivia := conformanceHasOwnTrivia(conf)
		i++
		for i < len(items) {
			next, ok := items[i].(*ast.ImplConformance)
			if !ok || next.Derive != derive {
				break
			}
			if next.Line != originalLine && hasLeadingBlank(next) {
				break
			}
			if hasOwnTrivia && next.Line != originalLine {
				break
			}
			if !hasOwnTrivia && conformanceHasOwnTrivia(next) {
				break
			}
			i++
		}
		var line Doc
		if hasOwnTrivia {
			line = emitConformanceListWithTrivia(items[start:i], derive)
		} else {
			line = emitConformanceList(items[start:i], derive)
		}
		if len(lines) > 0 && (hasLeadingBlank(conf) || prevDerive != derive) {
			line = Concat(HardLine(), line)
		}
		lines = append(lines, line)
		prevDerive = derive
	}
	if len(lines) == 0 {
		return Nil()
	}
	return Join(HardLine(), lines...)
}

func conformanceHasOwnTrivia(conf *ast.ImplConformance) bool {
	if conf.Doc != "" || len(conf.AttachedTests) > 0 {
		return true
	}
	for _, tr := range conf.GetLeading() {
		if tr.Kind == ast.TriviaComment {
			return true
		}
	}
	return false
}

func emitConformanceList(items []ast.Node, derive bool) Doc {
	keyword := "impl"
	if derive {
		keyword = "derive"
	}
	entries := make([]string, 0, len(items))
	for _, item := range items {
		conf, ok := item.(*ast.ImplConformance)
		if !ok {
			continue
		}
		entries = append(entries, Render(emitTypeExpr(conf.Interface), defaultWidth))
	}
	return emitPackedConformanceList(keyword, entries)
}

func emitDeriveInterfaceList(conf *ast.ImplConformance) string {
	var entries []string
	if len(conf.Interfaces) > 0 {
		entries = make([]string, 0, len(conf.Interfaces))
		for _, iface := range conf.Interfaces {
			entries = append(entries, Render(emitTypeExpr(iface), defaultWidth))
		}
	} else if conf.Interface != nil {
		entries = append(entries, Render(emitTypeExpr(conf.Interface), defaultWidth))
	}
	return strings.Join(entries, ", ")
}

func emitPackedConformanceList(keyword string, entries []string) Doc {
	if len(entries) == 0 {
		return Text(keyword)
	}
	flat := keyword + " " + strings.Join(entries, ", ")
	if len(flat) <= conformanceWrapWidth {
		return Text(flat)
	}

	lines := packConformanceEntries(keyword, entries)
	parts := []Doc{Text(keyword + " " + strings.Join(lines[0], ", "))}
	for i := 1; i < len(lines); i++ {
		parts = append(parts, Text(","))
		parts = append(parts, Nest(defaultIndent, Concat(
			HardLine(),
			Text(strings.Join(lines[i], ", ")),
		)))
	}
	return Concat(parts...)
}

func packConformanceEntries(keyword string, entries []string) [][]string {
	var lines [][]string
	line := []string{}
	lineLen := len(keyword) + 1
	limit := conformanceWrapWidth
	for _, entry := range entries {
		addLen := len(entry)
		if len(line) > 0 {
			addLen += 2
		}
		if len(line) > 0 && lineLen+addLen > limit {
			lines = append(lines, line)
			line = []string{entry}
			lineLen = len(entry)
			continue
		}
		line = append(line, entry)
		lineLen += addLen
	}
	if len(line) > 0 {
		lines = append(lines, line)
	}
	return lines
}

func emitConformanceListWithTrivia(items []ast.Node, derive bool) Doc {
	if len(items) == 0 {
		return Nil()
	}
	conf, ok := items[0].(*ast.ImplConformance)
	if !ok {
		return emitConformanceList(items, derive)
	}
	parts := emitLeadingComments(withoutBlankTrivia(conf.GetLeading()))
	parts = append(parts, emitDocBeforeAttachedTests(conf.Doc, conf.AttachedTests)...)
	parts = append(parts, emitAttachedTests(conf.AttachedTests)...)
	parts = append(parts, emitConformanceList(items, derive))
	return Concat(parts...)
}

func flattenNestedImplBlocks(items []ast.Node) []ast.Node {
	counts := map[string]int{}
	for _, item := range items {
		ib, ok := item.(*ast.ImplBlock)
		if !ok || ib.Interface == nil || ib.Receiver != nil {
			continue
		}
		for _, child := range ib.Items {
			if name := implItemName(child); name != "" {
				counts[name]++
			}
		}
	}
	var out []ast.Node
	changed := false
	for _, item := range items {
		ib, ok := item.(*ast.ImplBlock)
		if !ok || ib.Interface == nil || ib.Receiver != nil {
			out = append(out, item)
			continue
		}
		changed = true
		out = append(out, &ast.ImplConformance{
			AttachedTests: ib.AttachedTests,
			Interface:     ib.Interface,
			Doc:           ib.Doc,
			Line:          ib.Line,
			Col:           ib.Col,
		})
		for _, child := range ib.Items {
			switch it := child.(type) {
			case *ast.FuncDef:
				it.ImplFunction = true
			case *ast.ExternFunc:
				it.ImplFunction = true
			}
			if counts[implItemName(child)] > 1 {
				switch it := child.(type) {
				case *ast.FuncDef:
					it.ImplIface = ib.Interface
					it.ImplIfaceInferred = false
					it.ImplIfaceSourceQualified = true
				case *ast.ExternFunc:
					it.ImplIface = ib.Interface
					it.ImplIfaceInferred = false
					it.ImplIfaceSourceQualified = true
				}
			}
			out = append(out, child)
		}
	}
	if !changed {
		return items
	}
	return out
}

func implItemName(item ast.Node) string {
	switch it := item.(type) {
	case *ast.FuncDef:
		return it.Name
	case *ast.ExternFunc:
		return it.Name
	default:
		return ""
	}
}

// orderTypeBodyItems reorders a type body's items into the reader-facing
// house order: ordinary value members in stable source order.
//
// Blank-line subgrouping authored BETWEEN ADJACENT conformances (e.g. a derived
// group kept apart from a hand-written group) is preserved; a blank that merely
// separated a conformance from a method is dropped (it was method-separation,
// not a conformance split). Leading comments on each entry are kept.
//
// Fast path: a body that is already ordered is returned untouched, so existing
// source that already follows the house style never churns. The reorder path
// mutates the leading trivia of conformance nodes — safe because the formatter
// re-parses a throwaway AST per Format call.
func orderTypeBodyItems(items []ast.Node) []ast.Node {
	items = flattenNestedImplBlocks(items)
	if typeBodyItemsAlreadyOrdered(items) {
		return items
	}
	var confs, values, impls []ast.Node
	for i, it := range items {
		if isMethodImplBlock(it) {
			impls = append(impls, it)
			continue
		}
		if !isImplConformance(it) {
			values = append(values, it)
			continue
		}
		if c, ok := it.(*ast.ImplConformance); ok {
			// A new block boundary only when this entry was blank-separated from the
			// immediately-preceding conformance in the source (author intent); a
			// blank inherited from a preceding method is method-separation, dropped.
			split := len(confs) > 0 && i > 0 && isImplConformance(items[i-1]) && hasLeadingBlank(it)
			var lead []ast.Trivia
			if split {
				lead = append(lead, ast.Trivia{Kind: ast.TriviaBlankLine})
			}
			for _, tr := range c.GetLeading() {
				if tr.Kind == ast.TriviaComment {
					lead = append(lead, tr)
				}
			}
			c.Leading = lead
		}
		confs = append(confs, it)
	}
	ordered := append(confs, values...)
	return append(ordered, impls...)
}

// typeBodyItemsAlreadyOrdered reports whether a parsed body follows the
// canonical category order. The non-value categories exist for recovery and
// migration paths; source type bodies emit only fields/variants, inherent
// functions, and once bindings.
func typeBodyItemsAlreadyOrdered(items []ast.Node) bool {
	phase := 0
	for _, it := range items {
		category := 1
		if isImplConformance(it) {
			category = 0
		} else if isMethodImplBlock(it) {
			category = 2
		}
		if category < phase {
			return false
		}
		if category > phase {
			phase = category
		}
	}
	return true
}

func isImplConformance(n ast.Node) bool {
	_, ok := n.(*ast.ImplConformance)
	return ok
}

func isMethodImplBlock(n ast.Node) bool {
	ib, ok := n.(*ast.ImplBlock)
	return ok && ib.Interface != nil
}

// emitStructDef renders a struct declaration in the canonical form — fields
// one per line (no commas), then derive entries and value items, blank-line
// separated for internally produced or legacy ASTs:
//
//	[pub ][opaque ]struct Name[<T, ...>] {
//	  one: Type
//	  two: Type = default
//
//	  fn method(s: Name): T { ... }
//	}
//
// The body is always multi-line when non-empty. A single space
// separates the type name (or type-params) from the opening `{`. `pub`
// precedes `opaque` when both are present; `opaque` is emitted before
// `struct` whenever the AST carries the flag (see spec §15.3).
func emitStructDef(v *ast.StructDef) Doc {
	// Doc-comment first, then any decorators the derive lowering attached.
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	parts = append(parts, emitTypeDeclDecorators(v.Decorators)...)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	if v.Opaque {
		parts = append(parts, Text("opaque "))
	}
	parts = append(parts, Text("struct "), Text(v.Name), emitTypeParams(v.TypeParams))
	parts = append(parts, emitWhereClause(v.WhereClauses)...)
	parts = append(parts, Text(" "))

	members := make([]Doc, 0, len(v.Fields)+len(v.Items))
	for _, f := range v.Fields {
		members = append(members, emitStructDeclField(f))
	}
	members = appendTypeBodyItems(members, v.Items)
	parts = append(parts, emitMultilineBracedListWithEndTrivia("{", "}", members, false, v.EndTrivia))
	return Concat(parts...)
}

// emitEnumVariant renders one enum variant: bare (`North`), positional
// (`Circle Float`, `Position (Int, Int)`), struct (`Rectangle {width: Float,
// height: Float}`), or embedded (`embeds Circle`).
//
// Variant payload syntax mirrors distinct-type declarations: the payload
// is a type expression following the variant name with a single space.
// Struct-shaped payloads keep their `{...}` block (also separated by a
// space).
func emitEnumVariant(v ast.EnumVariant) Doc {
	switch v.Kind {
	case "embedded":
		return Concat(Text("embeds "), emitTypeExpr(v.EmbeddedTypeExpr))
	case "positional":
		// Type-expression syntax: `Name <typeexpr>`. Tuple/function/
		// generic types render with their own parens or angle brackets;
		// the variant itself adds none.
		return Concat(Text(v.Name), Text(" "), emitTypeExpr(v.DataTypeExpr))
	case "struct":
		return Concat(Text(v.Name), Text(" "), emitBracedStructFieldsWithEndTrivia(v.Fields, v.EndTrivia))
	default:
		// bare
		return Text(v.Name)
	}
}

// emitEnumDeclVariant renders one enum-declaration body variant in the
// canonical form: `Name [payload]` or `embeds T`. Leading `//` trivia and the
// `///` doc comment stack above the item.
func emitEnumDeclVariant(v ast.EnumVariant) Doc {
	parts := []Doc{}
	parts = append(parts, emitLeadingComments(v.LeadingComments)...)
	parts = append(parts, emitDocComment(v.Doc)...)
	parts = append(parts, emitEnumVariant(v))
	parts = append(parts, emitSameLineComments(v.Trailing)...)
	return Concat(parts...)
}

// emitEnumDef renders an enum declaration in the canonical form — variants
// one per line (no `|` separators; old pipe-alternation input migrates), then
// value items, blank-line separated for internally produced or legacy ASTs:
//
//	[pub ][opaque ]enum Maybe<T> {
//	  None
//	  Some T
//	}
//
// The body is ALWAYS multi-line — a one-liner pipe enum stacks. An empty
// enum is unreachable for well-formed input but renders as `enum Name {}`
// for resilience. `pub` precedes `opaque` when both are present; `opaque`
// is emitted before `enum` whenever the AST carries the flag (see spec
// §15.3).
func emitEnumDef(v *ast.EnumDef) Doc {
	// Doc-comment first, then decorators — see emitStructDef for the parser
	// constraint that drives this order.
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	parts = append(parts, emitTypeDeclDecorators(v.Decorators)...)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	if v.Opaque {
		parts = append(parts, Text("opaque "))
	}
	parts = append(parts, Text("enum "), Text(v.Name), emitTypeParams(v.TypeParams))
	parts = append(parts, emitWhereClause(v.WhereClauses)...)
	parts = append(parts, Text(" "))

	members := make([]Doc, 0, len(v.Variants)+len(v.Items))
	for _, va := range v.Variants {
		members = append(members, emitEnumDeclVariant(va))
	}
	members = appendTypeBodyItems(members, v.Items)
	parts = append(parts, emitMultilineBracedListWithEndTrivia("{", "}", members, false, v.EndTrivia))
	return Concat(parts...)
}

// emitInterfaceMethodSig renders an interface method signature (no body):
//
//	fn name(p1: T1, p2: T2): Ret
//
// Default methods (Body != nil) tagged with the `open` modifier render
// as `open fn name(...)`. The contextual `open` keyword only makes
// sense on a default method — the parser rejects `open` on bodyless
// methods — so emitting `open` here without checking Body is safe in
// practice; the AST flag is only ever set on default methods.
func emitInterfaceMethodSig(m ast.InterfaceMethod) Doc {
	paramDocs := make([]Doc, 0, len(m.Params))
	for _, p := range m.Params {
		parts := []Doc{Text(p.Name), Text(": "), emitTypeExpr(p.TypeAnnotation)}
		if p.Default != nil {
			parts = append(parts, Text(" = "), emit(p.Default))
		}
		paramDocs = append(paramDocs, Concat(parts...))
	}
	parts := []Doc{}
	if m.Open {
		parts = append(parts, Text("open "))
	}
	if m.Extern {
		parts = append(parts, Text("host "))
	}
	parts = append(parts, Text("fn "), Text(m.Name), emitTypeParams(m.TypeParams), emitBracedList("(", ")", paramDocs))
	if m.ReturnTypeExpr != nil {
		parts = append(parts, Text(": "), emitTypeExpr(m.ReturnTypeExpr))
	}
	parts = append(parts, emitWhereClause(m.WhereClauses)...)
	return Concat(parts...)
}

// emitWhereClause renders a function- or impl-level `where` clause inline after
// the return type or impl receiver: ` where T: Comparable, K: Hashable and Equatable`.
// Returns
// no parts when there are no constraints.
func emitWhereClause(clauses []ast.WhereConstraint) []Doc {
	if len(clauses) == 0 {
		return nil
	}
	parts := []Doc{Text(" where ")}
	for i, wc := range clauses {
		if i > 0 {
			parts = append(parts, Text(", "))
		}
		parts = append(parts, Text(wc.Name))
		for j, b := range wc.Bounds {
			if j == 0 {
				parts = append(parts, Text(": "))
			} else {
				parts = append(parts, Text(" and "))
			}
			parts = append(parts, emitTypeExpr(b))
		}
	}
	return parts
}

// emitInterfaceField renders a `field name: Type` requirement inside
// an interface body. Symmetric with method emission — the parser
// admits the same Type-annotation grammar in both places.
// LeadingComments emit above the field on their own line(s), then the
// `///` doc comment (doc sits closest to the declaration, mirroring
// top-level emission order).
func emitInterfaceField(f ast.InterfaceField) Doc {
	parts := []Doc{}
	parts = append(parts, emitLeadingComments(f.LeadingComments)...)
	parts = append(parts, emitDocComment(f.Doc)...)
	parts = append(parts, Text("field "), Text(f.Name), Text(": "), emitTypeExpr(f.TypeAnnotation))
	parts = append(parts, emitSameLineComments(f.Trailing)...)
	return Concat(parts...)
}

// emitInterfaceMethod renders an interface method — signature only for
// abstract methods, signature plus body for default methods. Default-method
// bodies follow the same always-multi-line rule as `fn` declarations
// (see emitFuncDef). The Body field is typed `ast.Node` per the AST but
// the parser only ever puts a `*ast.Block` there for default methods; the
// type assertion is safe and falls through to the generic emit on the
// (unreachable) miss path so a malformed AST doesn't panic.
//
// Leading-comment trivia on the method (set by parseInterfaceDef when
// the user put a `// note` above the `fn ...` line) emits above the
// signature, then the `///` doc comment (closest to the declaration). A
// same-line comment after the signature or the body's `}` follows it.
func emitInterfaceMethod(m ast.InterfaceMethod) Doc {
	parts := []Doc{}
	leading := m.GetLeading()
	if m.Body == nil {
		leading = withoutBlankTrivia(leading)
	}
	parts = append(parts, emitLeadingComments(leading)...)
	parts = append(parts, emitDocComment(m.Doc)...)
	sig := emitInterfaceMethodSig(m)
	trailing := emitSameLineComments(m.GetTrailing())
	if m.Body == nil {
		parts = append(parts, sig)
		return Concat(append(parts, trailing...)...)
	}
	if blk, ok := m.Body.(*ast.Block); ok {
		parts = append(parts, sig, Text(" "), emitBlockForceBroken(blk))
		return Concat(append(parts, trailing...)...)
	}
	parts = append(parts, sig, Text(" "), emit(m.Body))
	return Concat(append(parts, trailing...)...)
}

// emitInterfaceDef renders an interface declaration. Contract members (field
// requirements, then methods) are always multi-line and newline-separated (no
// trailing commas); field requirements come before method declarations to
// match the conventional order in the spec examples (data shape first, then
// operations on it).
func emitInterfaceDef(v *ast.InterfaceDef) Doc {
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	parts = append(parts, Text("interface "), Text(v.Name), emitTypeParams(v.TypeParams))
	parts = append(parts, emitWhereClause(v.WhereClauses)...)
	parts = append(parts, Text(" "))

	members := make([]Doc, 0, len(v.Fields)+len(v.Methods))
	for _, f := range v.Fields {
		members = append(members, emitInterfaceField(f))
	}
	for i := range v.Methods {
		m := v.Methods[i]
		member := emitInterfaceMethod(m)
		if len(members) > 0 && m.Body != nil && !hasLeadingBlank(&v.Methods[i]) {
			member = Concat(HardLine(), member)
		}
		members = append(members, member)
	}
	parts = append(parts, emitMultilineBracedListWithEndTrivia("{", "}", members, false, v.EndTrivia))
	return Concat(parts...)
}

// emitImplBlock renders an impl block. The top-level surface form is a single
// interface and a single implementing type:
//
//	impl Iface for Type { ... }              // interface impl
//	impl Iter for Box<T> where T: Bound { ... }   // constrained generic impl
//
// The header reuses emitTypeParams for the `<...>` clause (no space before
// `<`, matching `fn name<T>`). Items are the block's FuncDef / ExternFunc
// declarations, each emitted with its own doc comments + decorators via
// emit(); the multi-line braced body mirrors emitInterfaceDef so blank-line
// separation and trailing trivia are handled identically. A truly empty
// interface impl renders bodyless (`impl Iface for Type`); inherent impls and
// comment-bearing empty blocks keep braces.
func emitImplBlock(v *ast.ImplBlock) Doc {
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	if v.Interface != nil && v.Receiver != nil {
		// Top-level impl: `impl Iface for Type`.
		parts = append(parts, Text("impl"), emitTypeParams(v.Generics), Text(" "), emitTypeExpr(v.Interface), Text(" for "), emitTypeExpr(v.Receiver))
	} else if v.Interface != nil {
		// Internal body-nested interface impl: `impl Iface`.
		parts = append(parts, Text("impl"), emitTypeParams(v.Generics), Text(" "), emitTypeExpr(v.Interface))
	} else {
		parts = append(parts, Text("impl"), emitTypeParams(v.Generics), Text(" "), emitTypeExpr(v.Receiver))
	}
	parts = append(parts, emitWhereClause(v.WhereClauses)...)
	parts = append(parts, Text(" "))

	if v.Interface != nil && v.Receiver != nil && len(v.Items) == 0 && len(v.EndTrivia) == 0 {
		return Concat(parts[:len(parts)-1]...)
	}

	members := make([]Doc, 0, len(v.Items))
	for i, item := range v.Items {
		// emitWithTrivia (not emit) so each item's leading `//` comments and
		// blank-line separation round-trip — mirroring how emitFile renders
		// top-level declarations. emitFuncDef itself only emits the `///` doc
		// comment; the inter-item trivia lives on the item's HasTrivia leading.
		d := emitWithTrivia(item)
		// Force a blank line between adjacent items so every function —
		// including one-line `host fn`s — is separated, regardless of how
		// the author spaced them. Mirrors emitFile's top-level rule; the
		// braced list emits the first HardLine, this adds the blank. Skipped
		// when the item already carries a leading blank (avoids doubling).
		if i > 0 && wantsBlankBetween(v.Items[i-1], item) && !hasLeadingBlank(item) {
			d = Concat(HardLine(), d)
		}
		members = append(members, d)
	}
	parts = append(parts, emitMultilineBracedListWithEndTrivia("{", "}", members, false, v.EndTrivia))
	return Concat(parts...)
}

// emitImplConformance renders one derive declaration line:
//
//	derive Equatable for Point
//
// The interface and receiver reuse emitTypeExpr so both bare names
// (`derive Iface for Point`) and qualified names round-trip.
func emitImplConformance(v *ast.ImplConformance) Doc {
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	if v.Derive {
		parts = append(parts, Text("derive"), emitTypeParams(v.Generics), Text(" "), Text(emitDeriveInterfaceList(v)))
	} else {
		parts = append(parts, Text("impl "), emitTypeExpr(v.Interface))
	}
	if v.Derive && v.Receiver != nil {
		parts = append(parts, Text(" for "), emitTypeExpr(v.Receiver))
	}
	if v.Derive && v.Options != nil {
		parts = append(parts, Text(" with "), emit(v.Options))
	}
	parts = append(parts, emitWhereClause(v.WhereClauses)...)
	return Concat(parts...)
}

// emitRangeLit renders a `..` / `..=` literal: `1..5`, `1..=5`, `..5`,
// `..=5`, `1..`, `..`. No spaces around the operator (Rust convention).
func emitRangeLit(v *ast.RangeLit) Doc {
	op := ".."
	if v.Inclusive {
		op = "..="
	}
	parts := []Doc{}
	if v.Start != nil {
		parts = append(parts, emit(v.Start))
	}
	parts = append(parts, Text(op))
	if v.End != nil {
		parts = append(parts, emit(v.End))
	}
	return Concat(parts...)
}

// emitTypeAlias renders `[pub ]typealias Name Target` for a single-type
// alias or `[pub ]typealias Name A and B and C` for a bound alias.
func emitTypeAlias(v *ast.TypeAlias) Doc {
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	parts = append(parts, Text("typealias "), Text(v.Name), Text(" "))
	if len(v.Bounds) > 0 {
		for i, b := range v.Bounds {
			if i > 0 {
				parts = append(parts, Text(" and "))
			}
			parts = append(parts, emitTypeExpr(b))
		}
	} else {
		parts = append(parts, emitTypeExpr(v.TargetTypeExpr))
	}
	return Concat(parts...)
}

// emitTypeDef renders a distinct type: `[pub ][opaque ]type Name [InnerType]`.
// A zero-sized type (`type Expired`) has no inner type. The `opaque`
// modifier (only valid on distinct types) renders after `pub` when both
// are present. Source type declarations are bodiless; the optional body path
// below exists for internally produced or legacy ASTs.
func emitTypeDef(v *ast.TypeDef) Doc {
	// Doc-comment first, then decorators — see emitStructDef for the parser
	// constraint that drives this order.
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	parts = append(parts, emitTypeDeclDecorators(v.Decorators)...)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	if v.Opaque {
		parts = append(parts, Text("opaque "))
	}
	parts = append(parts, Text("type "), Text(v.Name))
	if v.InnerTypeExpr != nil {
		parts = append(parts, Text(" "), emitTypeExpr(v.InnerTypeExpr))
	}
	parts = emitOptionalTypeBody(parts, v.HasBody, v.Items, v.EndTrivia)
	return Concat(parts...)
}

// emitOptionalTypeBody appends an optional `{ ... }` item body for internal or
// legacy ASTs. Source `type` and `host type` declarations do not parse with
// bodies anymore.
func emitOptionalTypeBody(parts []Doc, hasBody bool, items []ast.Node, endTrivia []ast.Trivia) []Doc {
	if !hasBody || (len(items) == 0 && len(endTrivia) == 0) {
		return parts
	}
	members := appendTypeBodyItems(nil, items)
	return append(parts, Text(" "), emitMultilineBracedListWithEndTrivia("{", "}", members, false, endTrivia))
}

// emitOnceBinding renders `[pub ]once name: T = expr`.
func emitOnceBinding(v *ast.OnceBinding) Doc {
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	parts = append(parts, Text("once "), Text(v.Name))
	if v.TypeAnnotation != nil {
		parts = append(parts, Text(": "), emitTypeExpr(v.TypeAnnotation))
	}
	parts = append(parts, Text(" = "), emit(v.Value))
	return Concat(parts...)
}

func emitExternPackage(v *ast.ExternPackage) Doc {
	parts := []Doc{Text("gopkg ")}
	parts = append(parts, Text(encodeNomiString(v.ImportPath)))
	if v.Alias != "" && v.Alias != inferredGoPackageAlias(v.ImportPath) {
		parts = append(parts, Text(" as "), Text(v.Alias))
	}
	return Concat(parts...)
}

func inferredGoPackageAlias(importPath string) string {
	importPath = strings.TrimSuffix(importPath, "/")
	if importPath == "" {
		return ""
	}
	if idx := strings.LastIndex(importPath, "/"); idx >= 0 {
		return importPath[idx+1:]
	}
	return importPath
}

func emitGoBlock(v *ast.GoBlock) Doc {
	body := dedentRawBlock(v.Body)
	if body == "" {
		return Text("go {}")
	}
	lines := strings.Split(body, "\n")
	parts := []Doc{Text("go {")}
	for _, line := range lines {
		parts = append(parts, Nest(defaultIndent, Concat(HardLine(), Text(strings.TrimRight(line, " \t")))))
	}
	parts = append(parts, HardLine(), Text("}"))
	return Concat(parts...)
}

func dedentRawBlock(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	minIndent := -1
	for i, line := range lines {
		if i == 0 {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent <= 0 {
		return body
	}
	for i, line := range lines {
		if i == 0 {
			continue
		}
		if strings.TrimSpace(line) == "" {
			lines[i] = ""
			continue
		}
		if len(line) >= minIndent {
			lines[i] = line[minIndent:]
		}
	}
	return strings.Join(lines, "\n")
}

// emitExternFunc renders `[pub ]host fn name(params)[: Ret]` or an inline Go
// binding.
func emitExternFunc(v *ast.ExternFunc) Doc {
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	if v.ForeignAlias != "" || v.GoBody != "" {
		parts = append(parts, Text("fn "), Text(v.Name), emitTypeParams(v.TypeParams))
	} else {
		if v.ImplFunction {
			parts = append(parts, Text("impl "))
		}
		parts = append(parts, Text("host fn "))
		if v.ImplIface != nil && v.ImplIfaceSourceQualified {
			parts = append(parts, emitTypeExpr(v.ImplIface), Text("."))
		}
		parts = append(parts, Text(v.Name), emitTypeParams(v.TypeParams))
	}

	paramDocs := make([]Doc, 0, len(v.Params))
	for _, p := range v.Params {
		paramDocs = append(paramDocs, emitFuncDefParam(p))
	}
	parts = append(parts, emitBracedList("(", ")", paramDocs))

	if v.ReturnTypeExpr != nil {
		parts = append(parts, Text(": "), emitTypeExpr(v.ReturnTypeExpr))
	}
	parts = append(parts, emitWhereClause(v.WhereClauses)...)
	if v.ForeignAlias != "" {
		parts = append(parts, Text(" go "), Text(v.ForeignAlias), Text("."), Text(v.ForeignName))
	}
	if v.GoBody != "" {
		parts = append(parts, Text(" "), emitInlineGoBlock(v.GoBody))
	}
	return Concat(parts...)
}

// emitExternType renders `[pub ]host type Name[<T, ...>]` or an inline Go
// binding, optionally followed by a type-body item list.
func emitExternType(v *ast.ExternType) Doc {
	// Doc-comment first, then decorators — see emitStructDef for the parser
	// constraint that drives this order.
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	parts = append(parts, emitTypeDeclDecorators(v.Decorators)...)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	if v.ForeignAlias != "" || v.GoBody != "" {
		if v.Opaque {
			parts = append(parts, Text("opaque "))
		}
		parts = append(parts, Text("type "), Text(v.Name), emitTypeParams(v.TypeParams))
		if v.ForeignAlias != "" {
			parts = append(parts, Text(" go "), Text(v.ForeignAlias), Text("."), Text(v.ForeignName))
		}
	} else {
		parts = append(parts, Text("host type "), Text(v.Name), emitTypeParams(v.TypeParams))
	}
	parts = append(parts, emitWhereClause(v.WhereClauses)...)
	if v.GoBody != "" {
		parts = append(parts, Text(" "), emitInlineGoBlock(v.GoBody))
		return Concat(parts...)
	}
	parts = emitOptionalTypeBody(parts, v.HasBody, v.Items, v.EndTrivia)
	return Concat(parts...)
}

func emitInlineGoBlock(body string) Doc {
	body = dedentRawBlock(body)
	if body == "" {
		return Text("go {}")
	}
	lines := strings.Split(body, "\n")
	parts := []Doc{Text("go {"), HardLine()}
	for i, line := range lines {
		if i > 0 {
			parts = append(parts, HardLine())
		}
		parts = append(parts, Text(strings.TrimRight(line, " \t")))
	}
	parts = append(parts, HardLine(), Text("}"))
	return Concat(Text("go {"), Nest(defaultIndent, Concat(HardLine(), Concat(parts[2:len(parts)-2]...))), HardLine(), Text("}"))
}

// emitImport renders an import statement in canonical form:
//
//	import <path>
//	import <path> as alias
//	import <path>.A
//	import <path>.{A, B, ...}
//	import <path>.Owner.{A, B as C, ...}
//	import <path>.{A, B export, C export as Cp}
//	import <path>.{A, B, C} export
//
// The selective list stays inline when it fits. When the full import exceeds
// the width budget, the formatter breaks after the module path and renders one
// top-level selector per continuation line. A selective-name alias (`Name as
// Alias`) stays on the same line as its original name — it's emitted as an
// atomic Text so the layout engine can't break between the name and `as`. The
// per-item `export [as <P>]` suffix is rendered into the same atomic Text for
// the same reason.
//
// ExportAlias is retained on the AST for recovery/internal compatibility, but
// valid source syntax does not produce it.
//
// Re-export modifiers come from the AST (B.1):
//   - Per-item: ExportFlags[i] / ExportAliases[i] are emitted alongside each
//     selected name.
//   - Line-level export renders after the full selector list.
func emitImport(v *ast.ImportStmt) Doc {
	if v.Extern {
		return emitExternPackage(&ast.ExternPackage{
			ImportPath: v.ExternPath,
			Alias:      v.ExternAlias,
		})
	}
	return Concat(Text("import "), emitImportBody(v))
}

// emitImportBody renders an import without the leading `import ` keyword: the
// path, optional drill-through type, explicit selectors, and re-export
// modifiers. Shared by emitImport (which prepends `import `) and
// emitImportBlock (which contributes the keyword once for the whole block, so
// each entry is just its body).
func emitImportBody(v *ast.ImportStmt) Doc {
	if v.Extern {
		return emitGoImportBody(v, true)
	}
	pathParts, owners := importPathAndOwners(v)
	path := strings.Join(pathParts, "/")
	parts := []Doc{Text(path)}
	if len(v.Names) > 0 || v.IncludeParent {
		if v.IncludeParent && len(owners) == 0 && !importHasItemModifiers(v) {
			body := path + ".{" + strings.Join(importSelectorItemStrings(v), ", ") + "}"
			if v.ExportAll {
				body += " export"
			}
			return Text(body)
		}
		selectors := importSelectorStrings(v)
		if !v.ExportAll {
			if importCanUseDottedSingleSelector(v, selectors) {
				return Text(path + "." + selectors[0])
			}
			return emitImportSelectorBody(path, selectors)
		}
		// Line-level `export` shorthand — preserved after the selector body.
		// This may be redundant when every item also carries a per-item flag
		// (per the plan, B.4 silently allows that combination); the formatter
		// preserves what was parsed.
		return Concat(emitImportSelectorBody(path, selectors), Text(" export"))
	} else if v.ModuleAlias != nil {
		parts = append(parts, Text(" as "+ast.ImportNodeName(v.ModuleAlias)))
	} else if v.ExportAll {
		// Legacy/internal empty-selector re-export.
		if v.ExportAlias != nil {
			parts = append(parts, Text(" export as "+ast.ImportNodeName(v.ExportAlias)))
		} else {
			parts = append(parts, Text(" export"))
		}
	}
	return Concat(parts...)
}

func emitGoImportBody(v *ast.ImportStmt, includeKeyword bool) Doc {
	parts := make([]string, 0, 3)
	if includeKeyword {
		parts = append(parts, "import")
	}
	if v.ExternAliasExplicit {
		parts = append(parts, v.ExternAlias)
	}
	parts = append(parts, encodeNomiString(v.ExternPath))
	return Text(strings.Join(parts, " "))
}

func importCanUseDottedSingleSelector(v *ast.ImportStmt, selectors []string) bool {
	if len(selectors) != 1 || len(v.Names) != 1 || v.IncludeParent || importNeedsFlatBraceList(v) {
		return false
	}
	return true
}

func emitImportSelectorBody(path string, selectors []string) Doc {
	if len(selectors) == 0 {
		return Text(path)
	}
	if len(selectors) == 1 {
		return Text(path + "." + selectors[0])
	}
	selectorDocs := make([]Doc, 0, len(selectors)*3-2)
	for i, selector := range selectors {
		if i > 0 {
			selectorDocs = append(selectorDocs, Text(","), Line())
		}
		selectorDocs = append(selectorDocs, Text(selector))
	}
	return Group(Concat(
		Text(path),
		Text(".{"),
		Nest(defaultIndent, Concat(LineOrEmpty(), Concat(selectorDocs...))),
		LineOrEmpty(),
		Text("}"),
	))
}

func importPathAndOwners(v *ast.ImportStmt) ([]string, []string) {
	modulePath := v.ModulePath
	var owners []string
	if len(v.Names) > 0 || v.IncludeParent {
		for len(modulePath) > 1 {
			if _, isType := modulePath[len(modulePath)-1].(*ast.TypeIdent); isType {
				owners = append([]string{ast.ImportNodeName(modulePath[len(modulePath)-1])}, owners...)
				modulePath = modulePath[:len(modulePath)-1]
				continue
			}
			break
		}
	}
	pathParts := make([]string, 0, len(modulePath))
	for _, p := range modulePath {
		pathParts = append(pathParts, ast.ImportNodeName(p))
	}
	return pathParts, owners
}

func importSelectorItemStrings(v *ast.ImportStmt) []string {
	items := make([]string, 0, len(v.Names)+1)
	if v.IncludeParent {
		items = append(items, "self")
	}
	for i, name := range v.Names {
		n := ast.ImportNodeName(name)
		if i < len(v.Aliases) && v.Aliases[i] != nil {
			n += " as " + ast.ImportNodeName(v.Aliases[i])
		}
		if i < len(v.ExportFlags) && v.ExportFlags[i] {
			n += " export"
			if i < len(v.ExportAliases) && v.ExportAliases[i] != nil {
				n += " as " + ast.ImportNodeName(v.ExportAliases[i])
			}
		}
		items = append(items, n)
	}
	return items
}

func importSelectorStrings(v *ast.ImportStmt) []string {
	_, owners := importPathAndOwners(v)
	items := importSelectorItemStrings(v)
	ownerPrefix := strings.Join(owners, ".")
	if ownerPrefix == "" {
		if v.IncludeParent || importNeedsFlatBraceList(v) {
			return []string{"{" + strings.Join(items, ", ") + "}"}
		}
		return items
	}
	if len(items) == 1 && !v.IncludeParent && !importHasItemModifiers(v) {
		return []string{ownerPrefix + "." + items[0]}
	}
	return []string{ownerPrefix + ".{" + strings.Join(items, ", ") + "}"}
}

func importNeedsFlatBraceList(v *ast.ImportStmt) bool {
	for _, flag := range v.ExportFlags {
		if flag {
			return true
		}
	}
	for _, alias := range v.ExportAliases {
		if alias != nil {
			return true
		}
	}
	return false
}

func importHasItemModifiers(v *ast.ImportStmt) bool {
	for _, alias := range v.Aliases {
		if alias != nil {
			return true
		}
	}
	for _, flag := range v.ExportFlags {
		if flag {
			return true
		}
	}
	for _, alias := range v.ExportAliases {
		if alias != nil {
			return true
		}
	}
	return false
}

// emitImportBlock renders the brace block form:
//
//	import {
//	  std/io
//	  std/lists: List
//	}
//
// Always multiline, one entry per line, newline-separated — no commas and no
// trailing comma. This is the analog of Go's `import ( ... )`: a block of 2+
// entries never collapses onto a single line. (Single-entry blocks are
// collapsed to the bare per-statement form before we get here, by
// collapseSingleEntryBlocks.) Each entry is rendered by emitImportBody and
// Nest-indented, so a selective entry that itself overflows width still
// breaks correctly under the block indent.
func emitImportBlock(v *ast.ImportBlock) Doc {
	if len(v.Entries) == 0 {
		if v.Go {
			return Text("go {}")
		}
		return Text("import {}")
	}
	if v.Go {
		parts := []Doc{Text("go {"), Nest(defaultIndent, Concat(HardLine(), Text("import (")))}
		for _, e := range v.Entries {
			parts = append(parts, Nest(defaultIndent*2, Concat(HardLine(), emitWithTriviaDoc(e, emitGoImportBody(e, false)))))
		}
		parts = append(parts, Nest(defaultIndent, Concat(HardLine(), Text(")"))), HardLine(), Text("}"))
		return Concat(parts...)
	}
	blockHead := "import {"
	parts := []Doc{Text(blockHead)}
	for i := 0; i < len(v.Entries); {
		e := v.Entries[i]
		group := []*ast.ImportStmt{e}
		if !v.Go && canGroupImportSelectorLine(e) {
			pathParts, _ := importPathAndOwners(e)
			for j := i + 1; j < len(v.Entries); j++ {
				next := v.Entries[j]
				nextPathParts, _ := importPathAndOwners(next)
				if !canGroupImportSelectorLine(next) || strings.Join(nextPathParts, "/") != strings.Join(pathParts, "/") {
					break
				}
				group = append(group, next)
			}
		}
		// emitWithTriviaDoc renders the entry's leading comments (each on its own
		// line above it) and trailing same-line comment around the keyword-less
		// body. emit(e) is NOT usable here — it would prepend `import `.
		var entry Doc
		if len(group) > 1 {
			entry = groupedImportSelectorDoc(group)
		} else {
			entry = emitWithTriviaDoc(e, emitImportBody(e))
		}
		parts = append(parts, Nest(defaultIndent, Concat(HardLine(), entry)))
		i += len(group)
	}
	if len(v.EndTrivia) > 0 {
		parts = append(parts, Nest(defaultIndent, emitEndTrivia(v.EndTrivia)))
	}
	parts = append(parts, HardLine(), Text("}"))
	return Concat(parts...)
}

func canGroupImportSelectorLine(v *ast.ImportStmt) bool {
	if v.Extern {
		return false
	}
	if importCanUseDottedSingleSelector(v, importSelectorStrings(v)) {
		return false
	}
	if _, owners := importPathAndOwners(v); len(owners) > 0 {
		return false
	}
	return (len(v.Names) > 0 || v.IncludeParent) &&
		v.ModuleAlias == nil &&
		!v.ExportAll &&
		len(v.Leading) == 0 &&
		len(v.Trailing) == 0
}

func groupedImportSelectorDoc(group []*ast.ImportStmt) Doc {
	if len(group) == 0 {
		return Nil()
	}
	pathParts, _ := importPathAndOwners(group[0])
	path := strings.Join(pathParts, "/")
	type ownerSelector struct {
		owner string
		items []string
	}
	ownerIndexes := map[string]int{}
	var ownerSelectors []ownerSelector
	flatSelectors := make([]string, 0, len(group))
	for _, entry := range group {
		_, owners := importPathAndOwners(entry)
		ownerPrefix := strings.Join(owners, ".")
		if ownerPrefix == "" {
			flatSelectors = append(flatSelectors, importSelectorStrings(entry)...)
			continue
		}
		items := importSelectorItemStrings(entry)
		if idx, ok := ownerIndexes[ownerPrefix]; ok {
			ownerSelectors[idx].items = append(ownerSelectors[idx].items, items...)
		} else {
			ownerIndexes[ownerPrefix] = len(ownerSelectors)
			ownerSelectors = append(ownerSelectors, ownerSelector{owner: ownerPrefix, items: items})
		}
	}
	for i := 0; i < len(ownerSelectors); i++ {
		owner := &ownerSelectors[i]
		for j := 0; j < len(flatSelectors); j++ {
			if flatSelectors[j] != owner.owner {
				continue
			}
			flatSelectors = append(flatSelectors[:j], flatSelectors[j+1:]...)
			owner.items = append([]string{"self"}, owner.items...)
			break
		}
	}
	selectors := make([]string, 0, len(flatSelectors)+len(ownerSelectors))
	selectors = append(selectors, flatSelectors...)
	for _, owner := range ownerSelectors {
		if len(owner.items) == 1 && owner.items[0] != "self" {
			selectors = append(selectors, owner.owner+"."+owner.items[0])
		} else {
			selectors = append(selectors, owner.owner+".{"+strings.Join(owner.items, ", ")+"}")
		}
	}
	return emitImportSelectorBody(path, selectors)
}

// emitPipeChain collects a left-associative chain of `|>` operations and emits
// it **as authored**, matching Elixir's mix format:
//
//   - Authored inline (source and every step on one source line) → a Group with
//     soft Line() separators: stays on one line when it fits, breaks (stacked,
//     aligned) only if it overflows the width budget.
//   - Authored multi-line (any step on a different source line than the source)
//     → HardLine separators, no Group: stays stacked, even when it would fit on
//     one line — mix format never collapses a multi-line pipe.
//
// Inline-vs-multi-line is thus the author's choice (it is NOT dictated by stage
// count); the formatter only forces a break on overflow. Each `|>` aligns with
// the source — no extra indent. In a prefix context (a binding RHS) the binding
// breaks after `=` and indents the whole block, so source and `|>` align there;
// see the `*ast.Binding` case. (A step whose own content is multi-line — e.g. a
// block lambda — also forces a stack: its HardLine fails the Group's fits check.)
func emitPipeChain(n *ast.Binary) Doc {
	return emitPipeChainWithMode(n, pipeStackRules)
}

func emitPipeChainForced(n *ast.Binary) Doc {
	return emitPipeChainWithMode(n, pipeStackAlways)
}

// pipeStackMode says when a pipeline puts each stage on its own line.
type pipeStackMode int

const (
	// pipeStackRules stacks a pipeline the author wrote across lines or
	// one with a lambda stage (pipeChainStacked).
	pipeStackRules pipeStackMode = iota
	// pipeStackAlways stacks every pipeline (a call argument).
	pipeStackAlways
	// pipeStackAsWritten stacks only a pipeline written across lines: the
	// expression of a `${...}`, where a line break would split a one-line
	// string.
	pipeStackAsWritten
)

func emitPipeChainWithMode(n *ast.Binary, mode pipeStackMode) Doc {
	// Walk the LEFT spine to collect steps (right-hand sides) in order.
	// Parsing is left-associative: `a |> b |> c` = Binary(|>, Binary(|>, a, b), c).
	// Leftmost non-pipe node is the source.
	source, steps := collectPipeChain(n)
	if len(steps) > 0 {
		if assertion, ok := steps[len(steps)-1].(*ast.Assertion); ok && assertion.Expr == nil {
			kw := "assert"
			if assertion.Refute {
				kw = "refute"
			}
			return emitKeywordPrefixedPipeParts(kw, source, steps[:len(steps)-1], mode == pipeStackAlways)
		}
	}
	// Stack when the source wrote the chain across multiple lines (any step
	// on a different line than the source; mirrors structLitUserMultiLine's
	// `.Line` comparison), or when it has a lambda stage.
	var authoredMultiline bool
	switch mode {
	case pipeStackAlways:
		authoredMultiline = true
	case pipeStackAsWritten:
		authoredMultiline = pipeChainAuthoredMultiline(source, steps)
	default:
		authoredMultiline = pipeChainStacked(source, steps)
	}
	sep := Line() // space when flat, newline when broken
	if authoredMultiline {
		sep = HardLine()
	}
	parts := make([]Doc, 0, len(steps)+1)
	parts = append(parts, emitWithTrivia(source))
	for i := 0; i < len(steps); i++ {
		step := steps[i]
		if i+1 < len(steps) {
			if decorated, ok := emitDecoratedPipeStage(step, steps[i+1]); ok {
				decorated = pipeDecoratedStageDoc(step, steps[i+1], decorated)
				if authoredMultiline {
					if ht, ok := step.(ast.HasTrivia); ok {
						leading := ht.GetLeading()
						if len(leading) > 0 {
							for _, leadingDoc := range emitLeadingTriviaDocs(leading) {
								parts = append(parts, sep, leadingDoc)
							}
							parts = append(parts, Concat(sep, Text("|> "), decorated))
							i++
							continue
						}
					}
				}
				parts = append(parts, Concat(sep, Text("|> "), decorated))
				i++
				continue
			}
		}
		if authoredMultiline {
			if ht, ok := step.(ast.HasTrivia); ok {
				leading := ht.GetLeading()
				if len(leading) > 0 {
					for _, leadingDoc := range emitLeadingTriviaDocs(leading) {
						parts = append(parts, sep, leadingDoc)
					}
					parts = append(parts, Concat(sep, Text("|> "), emitWithTrailingTrivia(step)))
					continue
				}
			}
		}
		parts = append(parts, Concat(sep, Text("|> "), pipeStageDoc(step, emitWithTrivia(step))))
	}
	chain := Concat(parts...)
	if authoredMultiline {
		return chain
	}
	return Group(chain)
}

func collectPipeChain(n *ast.Binary) (ast.Node, []ast.Node) {
	var steps []ast.Node
	cur := n
	for {
		steps = append([]ast.Node{cur.Right}, steps...)
		inner, ok := cur.Left.(*ast.Binary)
		if !ok || inner.Op != "|>" {
			return cur.Left, steps
		}
		cur = inner
	}
}

// pipeChainStacked reports whether a pipeline puts each stage on its own
// line: when the author wrote it across lines, or when it has a lambda stage.
// A bare lambda body ends at the next `|>`, which one line hides
// (`5 |> |n| n + 1 |> |n| n * 2` reads as one lambda); a line per stage shows
// where each body ends.
func pipeChainStacked(source ast.Node, steps []ast.Node) bool {
	return pipeChainAuthoredMultiline(source, steps) || pipeChainHasLambdaStage(steps)
}

func pipeChainHasLambdaStage(steps []ast.Node) bool {
	for _, step := range steps {
		if _, ok := step.(*ast.Lambda); ok {
			return true
		}
	}
	return false
}

func pipeChainAuthoredMultiline(source ast.Node, steps []ast.Node) bool {
	srcLine := source.LineNum()
	for _, step := range steps {
		if step.LineNum() != srcLine {
			return true
		}
	}
	return false
}

func emitKeywordPrefixedPipe(keyword string, n *ast.Binary) Doc {
	source, steps := collectPipeChain(n)
	return emitKeywordPrefixedPipeParts(keyword, source, steps, false)
}

func emitKeywordPrefixedPipeParts(keyword string, source ast.Node, steps []ast.Node, forceStacked bool) Doc {
	prefix := Concat(Text(keyword), Text(" "), emitWithTrivia(source))
	return emitPrefixedPipeParts(prefix, source, steps, forceStacked)
}

func emitPrefixedPipeParts(prefix Doc, source ast.Node, steps []ast.Node, forceStacked bool) Doc {
	authoredMultiline := forceStacked || pipeChainStacked(source, steps)
	sep := Line()
	if authoredMultiline {
		sep = HardLine()
	}
	parts := make([]Doc, 0, len(steps)+1)
	parts = append(parts, prefix)
	for i := 0; i < len(steps); i++ {
		step := steps[i]
		if i+1 < len(steps) {
			if decorated, ok := emitDecoratedPipeStage(step, steps[i+1]); ok {
				decorated = pipeDecoratedStageDoc(step, steps[i+1], decorated)
				if authoredMultiline {
					if ht, ok := step.(ast.HasTrivia); ok {
						leading := ht.GetLeading()
						if len(leading) > 0 {
							for _, leadingDoc := range emitLeadingTriviaDocs(leading) {
								parts = append(parts, Nest(defaultIndent, Concat(sep, leadingDoc)))
							}
							parts = append(parts, Nest(defaultIndent, Concat(sep, Text("|> "), decorated)))
							i++
							continue
						}
					}
				}
				parts = append(parts, Nest(defaultIndent, Concat(sep, Text("|> "), decorated)))
				i++
				continue
			}
		}
		if authoredMultiline {
			if ht, ok := step.(ast.HasTrivia); ok {
				leading := ht.GetLeading()
				if len(leading) > 0 {
					for _, leadingDoc := range emitLeadingTriviaDocs(leading) {
						parts = append(parts, Nest(defaultIndent, Concat(sep, leadingDoc)))
					}
					parts = append(parts, Nest(defaultIndent, Concat(sep, Text("|> "), emitWithTrailingTrivia(step))))
					continue
				}
			}
		}
		parts = append(parts, Nest(defaultIndent, Concat(sep, Text("|> "), pipeStageDoc(step, emitWithTrivia(step)))))
	}
	chain := Concat(parts...)
	if authoredMultiline {
		return chain
	}
	return Group(chain)
}

func pipeStageDoc(stage ast.Node, doc Doc) Doc {
	if pipeStageWantsNestedContinuation(stage) {
		return LocalBroken(Nest(defaultIndent, doc))
	}
	return doc
}

func pipeDecoratedStageDoc(stage ast.Node, keyword ast.Node, doc Doc) Doc {
	if pipeStageWantsNestedContinuation(stage) || pipeStageWantsNestedContinuation(keyword) {
		return LocalBroken(Nest(defaultIndent, doc))
	}
	return doc
}

func pipeStageWantsNestedContinuation(stage ast.Node) bool {
	switch stage.(type) {
	case *ast.If, *ast.Case:
		return true
	default:
		return false
	}
}

func emitDecoratedPipeStage(stage ast.Node, keyword ast.Node) (Doc, bool) {
	switch kw := keyword.(type) {
	case *ast.TryOp:
		if kw.Expr != nil {
			return nil, false
		}
		return Concat(Text("try "), emit(stage)), true
	case *ast.If:
		if kw.Cond != nil {
			return nil, false
		}
		return emitIf(&ast.If{
			Cond: stage,
			Then: kw.Then,
			Else: kw.Else,
			Line: kw.Line,
			Col:  kw.Col,
		}), true
	case *ast.Case:
		if kw.Value != nil {
			return nil, false
		}
		return emitCase(&ast.Case{
			Value:    stage,
			Branches: kw.Branches,
			Line:     kw.Line,
			Col:      kw.Col,
		}), true
	default:
		return nil, false
	}
}
