package format

import (
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/strlit"
	"github.com/nomi-language/nomi/internal/token"
)

// emitFile walks top-level AST nodes and renders them to a string.
// Adjacent top-level declarations are separated by a blank line, with compact
// runs for imports and simple bodyless declarations (for example consecutive
// once/type/typealias declarations). Author-written blank lines inside compact
// runs are preserved as grouping boundaries. The file ends with a trailing
// newline.
//
// Imports are rendered as one group: a single import stays `import std::io`,
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
// (`import { std::io }`) to the per-statement form (`import std::io`).
// A one-item block is just a heavier spelling of a one-line import, so the
// canonical form is the standalone statement.
//
// It does NOT merge separate imports or unwrap multi-entry blocks — the two
// surface forms (per-line statements and the newline-separated
// `import { ... }` block) are both first-class and preserved as written.
//
// When a wrapper block collapses, its leading trivia (a file/section header
// comment above `import { ... }`) transfers to the surviving inner statement,
// prepended so it stays above any trivia the inner entry carried itself. A
// block with a comment before its `}` or after it stays a block: the one-line
// statement has no place for either.
func collapseSingleEntryBlocks(nodes []ast.Node) []ast.Node {
	out := make([]ast.Node, 0, len(nodes))
	for _, n := range nodes {
		blk, ok := n.(*ast.ImportBlock)
		if !ok || len(blk.Entries) != 1 || trailingHasComment(blk.EndTrivia) || trailingHasComment(blk.GetTrailing()) {
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
	return emitWithTriviaParts(emit(n), withoutBlankTrivia(ht.GetLeading()), outerTrailing(n, ht))
}

func emitWithTrailingTrivia(n ast.Node, more bool) Doc {
	ht, ok := n.(ast.HasTrivia)
	if !ok || keepsEndTriviaAsTrailing(n) {
		return emitStage(n, more)
	}
	trailing := ht.GetTrailing()
	if len(trailing) == 0 {
		return emitStage(n, more)
	}
	parts := []Doc{emitStage(n, more)}
	for _, t := range trailing {
		if t.Kind == ast.TriviaComment {
			parts = append(parts, Hidden(Concat(Text(" "), Text(t.Text))))
		}
	}
	return Concat(parts...)
}

// keepsEndTriviaAsTrailing reports whether n's Trailing trivia are the
// comments before its closing `}`, which its own emit writes inside the
// braces: a Case's after its last arm, a Block's after its last statement.
// Writing them again after the node would repeat them, and a line comment
// there would swallow whatever follows on the line.
func keepsEndTriviaAsTrailing(n ast.Node) bool {
	switch n.(type) {
	case *ast.Case, *ast.Block:
		return true
	}
	return false
}

// outerTrailing is the trailing trivia written after n: none for a node
// whose Trailing are its end trivia (keepsEndTriviaAsTrailing).
func outerTrailing(n ast.Node, ht ast.HasTrivia) []ast.Trivia {
	if keepsEndTriviaAsTrailing(n) {
		return nil
	}
	return ht.GetTrailing()
}

// emitStageWithTrivia is emitWithTrivia for a pipe stage.
func emitStageWithTrivia(n ast.Node, more bool) Doc {
	return emitWithTriviaDoc(n, emitStage(n, more))
}

// endsInOpenLambda reports whether n's text ends in a lambda's bare body,
// which reaches as far right as an expression can.
func endsInOpenLambda(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.Lambda:
		return true
	case *ast.Unary:
		return endsInOpenLambda(v.Right)
	case *ast.Binary:
		return endsInOpenLambda(v.Right)
	case *ast.TryOp:
		return false
	case *ast.Dbg:
		return v.Expr != nil && endsInOpenLambda(v.Expr)
	case *ast.Unwrap:
		return false
	case *ast.Assertion:
		return v.Expr != nil && endsInOpenLambda(v.Expr)
	}
	return false
}

// emitStage writes a pipe stage; more says another stage follows it. A
// parenthesized stage keeps its parentheses, which emitGroupedExpr would
// drop around a call: `x |> (f())` pipes x into the value f() returns, which
// the checker rejects, and `x |> f()` calls f with x.
//
// A stage that ends in a lambda's bare body is parenthesized when another
// stage follows, since the body would take that stage in: the parser builds
// `x |> dbg fn -> y` as the stages `fn -> y` and `dbg`, and
// `x |> fn -> y |> dbg` reads as one lambda whose body is `y |> dbg`.
//
// A stage unwrapped with `!` is the Unwrap, and the parentheses are under
// it: `x |> (f())!` keeps them as `x |> (f())` does.
func emitStage(n ast.Node, more bool) Doc {
	if u, ok := n.(*ast.Unwrap); ok {
		if g, ok := u.Expr.(*ast.GroupedExpr); ok && g.Expr != nil {
			return Concat(emitStage(u.Expr, false), Text("!"))
		}
	}
	g, ok := n.(*ast.GroupedExpr)
	if !ok || g.Expr == nil {
		if more && endsInOpenLambda(n) {
			return Concat(Text("("), emit(n), Text(")"))
		}
		return emit(n)
	}
	if groupedHasComments(g) {
		return emit(n)
	}
	inner := g.Expr
	for {
		next, ok := inner.(*ast.GroupedExpr)
		if !ok || next.Expr == nil {
			break
		}
		inner = next.Expr
	}
	return Concat(Text("("), emit(inner), Text(")"))
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
	return emitWithTriviaParts(body, ht.GetLeading(), outerTrailing(n, ht))
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
	if pipe, ok := n.(*ast.Binary); ok && pipe.Op == "|>" {
		return withPipeDangling(pipe, emitNode(n))
	}
	return withDangling(n, emitNode(n))
}

// withPipeDangling writes the dangling comments of the pipeline n around d,
// the document for it: n's own and those of each pipeline in its left spine,
// which collectPipeChain flattens into stages so no emit call writes them.
// Every place that writes a pipeline without emit calls it once.
func withPipeDangling(n *ast.Binary, d Doc) Doc {
	before, after := pipeDangling(n)
	parts := make([]Doc, 0, len(before)+1+len(after))
	for _, t := range before {
		parts = append(parts, CommentBefore(t.Text))
	}
	parts = append(parts, d)
	for _, t := range after {
		parts = append(parts, LineSuffix(t.Text))
	}
	return Concat(parts...)
}

// withPipeDanglingAfter is withPipeDangling for a pipeline after a
// binding's `=`, which may break onto the lines below (pipeAfterEquals). No
// comment may come between the `=` and the pipeline, so all of them end the
// pipeline's last line, as a comment before a one-line value ends its line.
func withPipeDanglingAfter(n *ast.Binary, d Doc) Doc {
	before, after := pipeDangling(n)
	parts := []Doc{d}
	for _, t := range append(before, after...) {
		parts = append(parts, LineSuffix(t.Text))
	}
	return Concat(parts...)
}

// pipeDangling is the dangling comments of the pipeline n and of each
// pipeline in its left spine, each list in source order.
func pipeDangling(n *ast.Binary) (before, after []ast.Trivia) {
	for cur := n; ; {
		before = append(before, cur.GetDanglingBefore()...)
		after = append(after, cur.GetDanglingAfter()...)
		inner, ok := cur.Left.(*ast.Binary)
		if !ok || inner.Op != "|>" {
			break
		}
		cur = inner
	}
	for _, ts := range [][]ast.Trivia{before, after} {
		sort.SliceStable(ts, func(i, j int) bool {
			return ts[i].Line < ts[j].Line || ts[i].Line == ts[j].Line && ts[i].Col < ts[j].Col
		})
	}
	return before, after
}

// withDangling writes n's dangling comments (ast.TriviaCarrier) around d,
// the document for n: those before n on the line above it when n starts a
// line and otherwise at the end of the line n starts on, those after it at
// the end of the line n ends on. Each dispatcher that
// writes a node itself, rather than handing it to another, calls it once.
func withDangling(n ast.Node, d Doc) Doc {
	h, ok := n.(ast.HasDangling)
	if !ok {
		return d
	}
	before, after := h.GetDanglingBefore(), h.GetDanglingAfter()
	if len(before) == 0 && len(after) == 0 {
		return d
	}
	parts := make([]Doc, 0, len(before)+1+len(after))
	for _, t := range before {
		parts = append(parts, CommentBefore(t.Text))
	}
	parts = append(parts, d)
	for _, t := range after {
		parts = append(parts, LineSuffix(t.Text))
	}
	return Concat(parts...)
}

func emitNode(n ast.Node) Doc {
	switch v := n.(type) {
	case *ast.CurrentValue:
		// A bare `..` spread: the spread's `..` is written by the literal,
		// and nothing follows it. An operand writes its own.
		if v.Operand {
			return Text("..")
		}
		return Nil()
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
	case *ast.FilePath:
		return Text(v.Spelling())
	case *ast.SelfExpr:
		return Text("self")
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
		if v.Op == "|>" {
			return emitPipeChain(v)
		}
		return emitBinary(v, followsOther)
	case *ast.GroupedExpr:
		return emitGroupedExpr(v)
	case *ast.Capture:
		return emitCapture(v)
	case *ast.Unary:
		return emitUnary(v, followsOther)
	case *ast.Is:
		return emitIs(v)
	case *ast.Ascription:
		return emitAscription(v)
	case *ast.Block:
		// A block expression: `begin`, its statements on the lines below,
		// and `end`; `begin end` when it is empty.
		return Concat(Text("begin"), emitBlockForceBroken(v))
	case *ast.Lambda:
		return emitLambda(v)
	case *ast.Then:
		return emitThen(v)
	case *ast.Tap:
		return emitTap(v)
	case *ast.FieldAccess:
		if comments := triviaComments(v.Object); len(comments) > 0 {
			// `self // note` then `.get(b)` on the next line: the comment
			// ends the receiver's line and the access continues below it.
			parts := []Doc{emit(v.Object)}
			for _, c := range comments {
				parts = append(parts, LineSuffix(c))
			}
			parts = append(parts, Nest(defaultIndent, Concat(HardLine(), Text("."), Text(v.Field.Name))))
			return Concat(parts...)
		}
		return Concat(emit(v.Object), Text("."), Text(v.Field.Name))
	case *ast.NamedArg:
		// NamedArg only legally appears inside a Call's Args. Emitting it
		// directly as an expression preserves "name: value" shape; the Call
		// emitter relies on this path.
		if argValueIsPun(v) {
			return Text(v.Name + ":")
		}
		return Concat(Text(v.Name), Text(": "), emit(v.Value))
	case *ast.Call:
		return emitCall(v)
	case *ast.Case:
		return emitCase(v)
	case *ast.StructLit:
		return emitStructLit(v)
	case *ast.ListLit:
		if len(v.Spreads) > 0 {
			return emitListSpreadLit(v)
		}
		open := "["
		if v.TypeName != nil {
			open = v.TypeName.TypeString() + "["
		}
		hasEndTrivia := trailingHasComment(v.EndTrivia)
		elems := make([]Doc, 0, len(v.Items))
		for _, it := range v.Items {
			elems = append(elems, emit(it))
		}
		// A comment forces the broken form: a `// comment` cannot share a
		// line with an element or the closing `]`. So does a literal three
		// compound levels deep (tooDeep), whose elements still lay out on
		// their own.
		if hasEndTrivia || nodesHaveComments(v.Items) || tooDeep(v, len(v.Items)) {
			return forceBrokenItems(open, "]", elems, v.Items, v.EndTrivia)
		}
		// A line break after the `[` keeps it broken (brokenAfterOpen).
		authored := itemsBrokenAfterOpen(v.Line, v.Items)
		if texts, ok := fillTexts(v.Items); ok {
			return filledList(open, "]", texts, authored)
		}
		if authored {
			return forceBrokenItems(open, "]", elems, v.Items, v.EndTrivia)
		}
		return emitBracedList(open, "]", elems)
	case *ast.MapLit:
		open := shapeOpen("", v.TypeName, "{")
		hasEndTrivia := trailingHasComment(v.EndTrivia)
		items, elems := mapLitParts(v, emit)
		lastComma := !endsInSpread(len(v.Entries), v.Spreads)
		first := 0
		if len(v.Spreads) > 0 && v.Spreads[0].At == 0 {
			first = v.Spreads[0].Line
		} else if len(v.Entries) > 0 {
			first = nodeStartLine(v.Entries[0].Key)
		}
		// A comment forces the broken form, and so do depth and a line
		// break after the `{` (brokenAfterOpen).
		if hasEndTrivia || nodesHaveComments(items) || tooDeep(v, len(v.Entries)) || brokenAfterOpen(v.Line, first) {
			cs, _ := nodesComments(items)
			return commentedList(open, "}", elems, cs, v.EndTrivia, lastComma)
		}
		return bracedList(open, "}", elems, lastComma)
	case *ast.TupleLit:
		elems := make([]Doc, 0, len(v.Items))
		for _, it := range v.Items {
			elems = append(elems, emit(it))
		}
		return emitTupleItems(elems, v)
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
	case *ast.Subtests:
		return emitSubtests(v)
	case *ast.Dbg:
		return emitDbg(v)
	case *ast.Unwrap:
		return Concat(emitMarked(v.Expr), Text("!"))
	case *ast.Todo:
		return emitTodo(v)
	case *ast.Unreachable:
		return Concat(Text("unreachable "), emit(v.Reason))
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
	case *ast.KeywordDecl:
		return emitKeywordDecl(v)
	case *ast.EmbedDecl:
		return emitEmbedDecl(v)
	case *ast.ExternType:
		return emitExternType(v)
	case *ast.ExternPackage:
		return emitExternPackage(v)
	case *ast.StringInterp:
		return emitStringInterp(v)
	case *ast.TaggedString:
		return emitTaggedString(v)
	case *ast.TryOp:
		if v.Expr == nil {
			return Text("?")
		}
		return Concat(emitMarked(v.Expr), Text("?"))
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
		return Text("&" + strconv.Itoa(v.N))
	case *ast.PlaceholderParam:
		return Text("&" + strconv.Itoa(v.N))
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
		if cs := tupleDestructureComments(v); anyComments(cs) {
			// The bindings have no trailing comma.
			docs := make([]Doc, len(v.Bindings))
			for i, b := range v.Bindings {
				docs[i] = Text("_")
				if b != nil {
					docs[i] = Text(b.Name)
				}
			}
			return emitBindingValue(emitCommentedListNoLastComma("(", ")", docs, cs), v.Value)
		}
		return emitBindingValue(Concat(parts...), v.Value)
	case *ast.StructDestructure:
		// {x:, y:} = expr  or  {x: a, y: b} = expr — anonymous only.
		// Reuse emitPattern by wrapping in a StructPattern with no TypeName.
		pat := &ast.StructPattern{Fields: v.Fields}
		return emitBindingValue(emitPattern(pat), v.Value)
	case *ast.PatternBinding:
		return emitPatternBinding(v)
	case *ast.PatternDestructure:
		if pipe, ok := v.Value.(*ast.Binary); ok && pipe.Op == "|>" {
			source, steps := collectPipeChain(pipe)
			prefix := Concat(Text("assert "), emitPattern(v.Pattern), Text(" = "), emitWithTrivia(source))
			return withPipeDangling(pipe, emitPrefixedPipeParts(prefix, source, steps, false))
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
		return Group(Concat(target, Text(" ="), withPipeDanglingAfter(b, pipeAfterEquals(emitPipeChain(b)))))
	}
	return Concat(target, Text(" = "), emit(value))
}

// pipeAfterEquals places a pipeline after a binding's `=`: on the `=` line
// when the enclosing Group is flat, or on the lines below one indent in. On
// the `=` line it gets no indent of its own, so an `if` or `case` stage's
// block (pipeStageDoc) is indented from the binding's line, as
// `y = case x {` is.
func pipeAfterEquals(chain Doc) Doc {
	return IfBroken(Concat(Text(" "), chain), Nest(defaultIndent, Concat(Line(), chain)))
}

// emitPatternBinding renders `Pattern = value [else { ... }]`.
//
// A plain else block of one statement stays on the binding's line when the
// whole statement fits the line at its indent (`Some(e) = email else { "none" }`,
// `Ok(x) = parse(s) else { continue }`), so a run of guards stays a run of
// one-line statements; otherwise the block breaks, its statements on the
// lines below. Else arms
// always break, laid out as a case's arms are.
//
// A pipe value that breaks after `=` puts the `else` on its own line at the
// statement's indent, below the last stage.
func emitPatternBinding(v *ast.PatternBinding) Doc {
	target := emitPattern(v.Pattern)
	if v.Else == nil {
		if bareStartsDestructure(v.Pattern) {
			target = Concat(Text("("), target, Text(")"))
		}
		return emitBindingValue(target, v.Value)
	}
	head := patternBindingHead(v, target)
	if block := v.Else.Block; block != nil {
		broken := Concat(head, emitBlockForceBroken(block))
		if !isInlineableBlockBody(block) || !isInlineBranch(block.Stmts[0]) {
			return broken
		}
		flat := Concat(head, emitWithTrivia(block.Stmts[0]))
		// fits rejects a flat form that holds a forced break; the Group below
		// decides by the line, from the statement's column.
		if blockKeepsBroken(block, flat) || !fits(flat, nil, defaultWidth) {
			return broken
		}
		return Group(IfBroken(flat, broken))
	}
	return Concat(head,
		Nest(defaultIndent, emitCaseArms(v.Else.Arms, v.Else.GetTrailing())),
		HardLine(),
		Text("end"),
	)
}

// patternBindingHead renders a binding with an else up to its `else`.
func patternBindingHead(v *ast.PatternBinding, target Doc) Doc {
	if pipe, ok := v.Value.(*ast.Binary); ok && pipe.Op == "|>" {
		return Group(Concat(target, Text(" ="),
			withPipeDanglingAfter(pipe, pipeAfterEquals(emitPipeChain(pipe))),
			IfBroken(Text(" "), HardLine()),
			Text("else ")))
	}
	return Concat(target, Text(" = "), emit(v.Value), Text(" else "))
}

// bareStartsDestructure reports whether pattern, written bare at the start
// of a statement `pattern = value` with no `else`, is read as one of the
// destructuring statements rather than as a pattern binding: a tuple of
// two or more names (TupleDestructure), a struct pattern without a type
// whose fields each bind a name (StructDestructure), or a variant named
// by a type, `T(n)`, `m.T(n)` or `T(_)` (DistinctDestructure). The author
// parenthesized such a pattern to get the pattern binding, so the
// formatter keeps one pair of parentheses around it. A name or `_` alone
// needs none: `(a) = v` is the binding `a = v`. A map pattern needs none
// either: it is always read as a pattern binding.
func bareStartsDestructure(pattern ast.Node) bool {
	isName := func(n ast.Node) bool {
		switch n.(type) {
		case *ast.IdentPattern, *ast.WildcardPattern:
			return true
		}
		return false
	}
	switch p := pattern.(type) {
	case *ast.TuplePattern:
		if len(p.Patterns) < 2 {
			return false
		}

		for _, e := range p.Patterns {
			if !isName(e) {
				return false
			}
		}
		return true
	case *ast.StructPattern:
		if p.TypeName != nil || len(p.Fields) == 0 {
			return false
		}
		for _, f := range p.Fields {
			if f.Pattern != nil {
				if _, ok := f.Pattern.(*ast.IdentPattern); !ok {
					return false
				}
			}
		}
		return true
	case *ast.EnumPattern:
		if p.Binding == "" && (p.Payload == nil || !isName(p.Payload)) {
			return false
		}
		switch t := p.Variant.(type) {
		case *ast.SimpleType:
			// `.Some(n)` starts with a dot, and a lower-case single name
			// is no type the destructure reads.
			return t.Name != "" && unicode.IsUpper([]rune(t.Name)[0])
		case *ast.QualifiedType:
			_, simple := t.Member.(*ast.SimpleType)
			return simple
		}
	}
	return false
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
// there keeps the line layout it was written with: a `then` stage stacks a
// pipeline elsewhere, but a line break would split a one-line string. An
// expression written on one line stays on it however long its line is,
// since the text around it cannot break: `${color(email)}` at the end of a
// long line of markup is not split into `${color(` and `email` and `)}`.
func emitInterpolatedExpr(n ast.Node) Doc {
	var d Doc
	if pipe, ok := n.(*ast.Binary); ok && pipe.Op == "|>" {
		d = withPipeDangling(pipe, emitPipeChainWithMode(pipe, pipeStackAsWritten))
	} else {
		d = emit(n)
	}
	if nodeStartLine(n) != nodeEndLine(n) {
		return d
	}
	if flat := Render(d, 1<<30); !strings.Contains(flat, "\n") {
		return Text(flat)
	}
	return d
}

// emitTripleStringLit renders a triple-quoted non-interpolated string.
//
// Canonical shape:
//
//	x = """
//	    line1
//	    line2
//	    """
//
// The body lines and the closing `"""` sit at one indent, which
// bodyFromOpenerLine measures from the line the opening `"""` is on. The
// opening `"""` sits on the current line; a HardLine immediately follows;
// each line of the decoded value renders as `Text(line)` followed by a
// HardLine; the closing `"""` sits on its own line. Every HardLine breaks
// to that indent, so the body needs no Nest of its own.
//
// The lexer strips the indent of the closing `"""` from every body line,
// so the closing-quote indent and the body indent must match for the
// round-trip to be lossless. Both are the same indent, so they always
// agree, and the body's own nesting is kept relative to it.
//
// Content encoding: triple-quoted source preserves real newlines, real
// `"`, real `\`, etc. The only re-encoding rule is `${` -> `\${`, which is
// what `escapeInterpolationOpeners` does. We do NOT use strconv.Quote here — that would
// turn newlines into `\n` and defeat the whole purpose of the triple form.
func emitTripleStringLit(v *ast.StringLit) Doc {
	// A one-line value that ends in a carriage return keeps the one-line
	// form, `"""0\r"""`: in the canonical shape the `\r` would end a body
	// line, and the lexer reads `\r\n` as a line break and drops it.
	if strings.HasSuffix(v.Value, "\r") && !strings.Contains(v.Value, "\n") {
		return Text(`"""` + encodeTripleStringContent(v.Value) + `"""`)
	}
	return tripleLiteral("", []ast.StringPart{ast.StringText{Value: v.Value}}, false, v.EndLine == v.Line)
}

// tripleStringFraming wraps a body Doc in `<prefix>"""\n<body>\n"""`,
// with the body and closing `"""` placed by bodyFromOpenerLine. `prefix`
// is a typed literal's tag, or empty; raw backtick strings use
// rawBacktickFraming.
func tripleStringFraming(prefix string, body Doc) Doc {
	return bodyFromOpenerLine(Concat(
		Text(prefix+`"""`),
		InString(Concat(
			HardLine(),
			body,
			HardLine(),
			Text(`"""`),
		)),
	))
}

// bodyFromOpenerLine renders a framed multi-line literal, whose body and
// closing delimiter sit at one indent, measured from the line its opening
// delimiter is on. A literal that starts its line (a statement, a
// function's result, an argument on a line of its own) puts them at that
// line's indent. One that follows code on its line (`page = Html"""`,
// `body: Html"""`, `Some(url) -> Html"""`, `return Html"""`) puts them one
// level in, as a broken pipeline or call continues after `=`. The decision
// is made from the rendered line, not from the literal's parent node.
func bodyFromOpenerLine(framed Doc) Doc {
	return AtLineIndent(func(startsLine bool) Doc {
		if startsLine {
			return framed
		}
		return Nest(defaultIndent, framed)
	})
}

// emitTripleStringInterp renders a triple-quoted interpolated string. Same
// shape as emitTripleStringLit; static text parts are split on `\n` so each
// real newline becomes a HardLine and gets re-indented through the Nest,
// and dynamic parts emit as `${ <expr> }`. Static text content is run
// through encodeTripleStringContent so a literal `${` round-trips.
func emitTripleStringInterp(v *ast.StringInterp) Doc {
	return tripleLiteral("", v.Parts, false, v.EndLine == v.Line)
}

// tripleLiteral renders a triple-quoted literal, or a multi-line raw one,
// with prefix (a typed literal's tag) before its opening delimiter and its
// body and closing delimiter placed by bodyFromOpenerLine.
//
// Two rules keep a template readable:
//
//   - A non-raw literal written on one line (oneLine) stays on one line,
//     `Html"""<span>${n}</span>"""`, when it fits in the line width from
//     the column it starts at. One that does not fit is framed.
//   - A slot whose expression spans lines is laid out from the column of
//     its `${` (tripleSlot), so its lines stay with the markup around it.
func tripleLiteral(prefix string, parts []ast.StringPart, raw, oneLine bool) Doc {
	var body []Doc
	for i, p := range parts {
		switch part := p.(type) {
		case ast.StringText:
			last := i == len(parts)-1
			if raw {
				body = append(body, tripleBodyDoc(part.Value, last))
			} else {
				body = append(body, tripleBodyDoc(encodeTripleStringContent(part.Value), last))
			}
		case ast.StringExpr:
			body = append(body, tripleSlot(part.Expr))
		}
	}
	if raw {
		return rawBacktickFraming(prefix, Concat(body...))
	}
	framed := tripleStringFraming(prefix, Concat(body...))
	if !oneLine {
		return framed
	}
	flat := []Doc{Text(prefix + `"""`)}
	for _, p := range parts {
		switch part := p.(type) {
		case ast.StringText:
			if strings.Contains(part.Value, "\n") || strings.HasSuffix(part.Value, "\r") {
				return framed
			}
			flat = append(flat, Verbatim(encodeTripleStringContent(part.Value)))
		case ast.StringExpr:
			flat = append(flat, Text("${"), emitInterpolatedExpr(part.Expr), Text("}"))
		}
	}
	one := InString(Concat(append(flat, Text(`"""`))...))
	return WithColumn(func(col, _, width int) Doc {
		text := Render(one, 1<<30)
		if strings.Contains(text, "\n") || col+textWidth(text) > width {
			return framed
		}
		return one
	})
}

// tripleSlot is a `${...}` in a multi-line literal's body. Its expression
// is nested to the column of its `${`, so a slot that spans lines
// (`${if ... }`) lays its later lines out from there rather than from the
// statement around the literal.
func tripleSlot(expr ast.Node) Doc {
	return WithColumn(func(col, indent, _ int) Doc {
		return Concat(Text("${"), Nest(col-indent, emitInterpolatedExpr(expr)), Text("}"))
	})
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
		// Triple-quoted, or a multi-line raw literal, whose one
		// StringText is its body with no escaping.
		return tripleLiteral(prefix, v.Parts, v.Raw, taggedOnOneLine(v))
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
// Same framing as the non-raw triple form (body at the current indent,
// HardLine boundaries promoted from `\n`s in the body), but the body
// content is emitted verbatim — no `${` escaping and no other
// transformation. A literal `${name}` in the source becomes `${name}`
// in the AST `Value` (the lexer is in raw mode, so it neither parsed
// the slot nor read an escape), and we re-emit it as `${name}` here.
// On re-lex the backtick opener again puts the lexer into raw mode, so
// the round-trip is lossless.
func emitRawTripleStringLit(v *ast.StringLit) Doc {
	body := tripleBodyDoc(v.Value, true)
	return rawBacktickFraming("", body)
}

// rawBacktickFraming is tripleStringFraming for a multi-line raw string:
// the body and closing backtick are placed by bodyFromOpenerLine.
func rawBacktickFraming(prefix string, body Doc) Doc {
	return bodyFromOpenerLine(Concat(
		Text(prefix+"`"),
		InString(Concat(
			HardLine(),
			body,
			HardLine(),
			Text("`"),
		)),
	))
}

// tripleBodyDoc converts an already-hash-escaped string into a Doc whose
// `\n` boundaries have been promoted to HardLines, so each body line
// breaks to the current indent. endsBody says s is the body's last text,
// so the closing delimiter's line break follows it.
//
// A line that ends in `\r` just before a line break is written with one
// more `\r`: the lexer reads the `\r` right before a line feed as part of
// a CRLF line end and drops it, and drops only that one.
func tripleBodyDoc(s string, endsBody bool) Doc {
	if s == "" {
		return Nil()
	}
	lines := strings.Split(s, "\n")
	parts := make([]Doc, 0, len(lines)*2-1)
	for i, line := range lines {
		if i > 0 {
			parts = append(parts, HardLine())
		}
		if strings.HasSuffix(line, "\r") && (i < len(lines)-1 || endsBody) {
			line += "\r"
		}
		if line != "" {
			parts = append(parts, Verbatim(line))
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
// Shape (inline): `if <cond> then <then> else <else>`
// Shape (block):
//
//	if <cond> then
//	  <then>
//	else
//	  <else>
//	end
//
// Layout rules:
//
//   - `else if` chains (any chain with at least one `else if` rung) ALWAYS
//     render in broken form, even when the flat form would fit. Multi-rung
//     conditionals read better with each rung on its own line.
//   - A plain `if`/`else` renders inline iff each branch is a single-statement
//     block with no comments AND the flat one-line form has no forced break
//     and fits; otherwise it breaks. A guard, an if with no else
//     (`if n < 0 then return 0`), fits when it fits the line at its column.
//     An if/else with two branches must also be at most
//     singleLineValueIfMaxWidth (50) columns flat.
//     This is width-based and canonical — branch *content* doesn't matter (a
//     call or binary op inlines when short), and how the source was typed is
//     ignored (a short multi-line if/else collapses to one line). Inherently
//     multi-line branches (case, multi-stmt block, multi-stage pipe) carry a
//     HardLine that the fits check rejects, so they never render inline.
func emitIf(v *ast.If) Doc {
	return emitIfParens(v, false)
}

// emitIfParens is emitIf, with the inline form in parentheses when parens is
// set: an `if` that something follows on its line, a binary operator's left
// operand or a pipe's head, whose inline `else` branch would take it in.
func emitIfParens(v *ast.If, parens bool) Doc {
	if hasElseIf(v) {
		return emitIfForceBroken(v)
	}
	inner := emitIfInner(v)
	if parens {
		inner = Concat(Text("("), inner, Text(")"))
	}
	if ifBranchesInlineable(v) && !ifKeepsBroken(v, inner) && fits(inner, nil, oneLineIfMaxWidth(v)) {
		// Inline-eligible: flatten when it also fits the current line, else
		// fall back to the force-broken layout. Routing the broken case
		// through emitIfForceBroken keeps it strictly all-or-nothing — no
		// partial layout where one branch breaks and the other stays inline
		// (which independent inner-block Groups would otherwise produce).
		return Group(IfBroken(inner, emitIfForceBroken(v)))
	}
	return emitIfForceBroken(v)
}

// oneLineIfMaxWidth is the widest flat form of v that may render on one
// line: the line width for a guard, an if with no else, and
// singleLineValueIfMaxWidth for an if/else with two branches. The Group
// that renders it still checks the line, from the if's column.
func oneLineIfMaxWidth(v *ast.If) int {
	if v.Else == nil {
		return defaultWidth
	}
	return singleLineValueIfMaxWidth
}

// emitWith renders the statement `with Type.field = value`. A struct
// literal value needs no parentheses, so any written around one are dropped.
func emitWith(v *ast.With) Doc {
	// A struct literal value needs no parentheses, however many it has.
	// Parentheses holding a comment stay.
	value := v.Value
	inner := value
	for g, ok := inner.(*ast.GroupedExpr); ok && g.Expr != nil && !groupedHasComments(g); g, ok = inner.(*ast.GroupedExpr) {
		inner = g.Expr
	}
	if lit, ok := inner.(*ast.StructLit); ok {
		value = lit
	}
	return Group(Concat(Text("with "), emit(v.Target), Text(" = "), emit(value)))
}

// emitSubtests renders `subtests rows, pattern { body }` and
// `subtests rows, pattern, name { body }`. The body is always broken across
// lines, as a test's is. A header too long for its line breaks its tuple
// pattern one element per line, as a call breaks its arguments:
//
//	subtests cases, (
//	    description,
//	    (white_row, white_column),
//	    expected
//	), description {
//
// The pipe-stage form puts the rows first and the stage on a line of its
// own, after the rows' pipeline written one stage per line:
//
//	[
//	    ("basic", "Portable Network Graphics", "PNG"),
//	]
//	|> subtests (description, phrase, expected), description {
func emitSubtests(v *ast.Subtests) Doc {
	if v.Piped {
		return emitPipedSubtests(v)
	}
	header := Concat(Text("subtests "), emit(v.Rows), Text(", "), emitSubtestsPattern(v.Pattern))
	if v.Name != nil {
		header = Concat(header, Text(", "), emit(v.Name))
	}
	return Concat(header, emitBlockForceBroken(v.Body))
}

func emitPipedSubtests(v *ast.Subtests) Doc {
	var rows Doc
	if pipe, ok := v.Rows.(*ast.Binary); ok && pipe.Op == "|>" {
		rows = emitWithTriviaDoc(pipe, emitPipeChainForced(pipe))
	} else {
		rows = emitWithTrivia(v.Rows)
	}
	parts := []Doc{rows}
	for _, leading := range emitLeadingTriviaDocs(v.StageLeading) {
		parts = append(parts, HardLine(), leading)
	}
	stage := Concat(Text("|> subtests "), emitSubtestsPattern(v.Pattern))
	if v.Name != nil {
		stage = Concat(stage, Text(", "), emit(v.Name))
	}
	parts = append(parts, HardLine(), stage, emitBlockForceBroken(v.Body))
	return Concat(parts...)
}

// emitSubtestsPattern is a `subtests` pattern: a tuple pattern without
// comments is a Group that breaks one element per line, with no comma after
// the last, which a tuple pattern's syntax has none of; any other pattern is
// laid out as anywhere.
func emitSubtestsPattern(n ast.Node) Doc {
	tp, ok := n.(*ast.TuplePattern)
	if !ok || len(tp.Patterns) < 2 {
		return emitPattern(n)
	}
	elems, _, hasComments := patternItems(tp.Patterns)
	if hasComments {
		return emitPattern(n)
	}
	return withDangling(n, bracedList("(", ")", elems, false))
}

// emitDefer renders `defer call(...)`.
func emitDefer(v *ast.Defer) Doc {
	if v.OnError {
		return Group(Concat(Text("defer on error "), emit(v.Call)))
	}
	return Group(Concat(Text("defer "), emit(v.Call)))
}

func emitTestDecl(v *ast.TestDecl) Doc {
	if !v.Group {
		header := Concat(Text("test "), Text(encodeNomiString(v.Name)))
		if v.ContextPattern != nil {
			header = Concat(header, Text(", "), emitPattern(v.ContextPattern))
		}
		return Concat(header, emitBlockForceBroken(v.Body))
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
	if st := v.Subtests; st != nil {
		// The group's items are the block's, which is the group's only
		// item.
		header := Concat(Text("subtests "), emit(st.Rows), Text(", "), emitSubtestsPattern(st.Pattern))
		if st.Name != nil {
			header = Concat(header, Text(", "), emit(st.Name))
		}
		block := Concat(header, emitDocsBlockForceBroken(body, st.EndTrivia))
		body = []Doc{emitWithTriviaParts(block, withoutBlankTrivia(st.Leading), st.Trailing)}
	}
	return Concat(Text("group "), Text(encodeNomiString(v.Name)), emitDocsBlockForceBroken(body, v.Body.GetTrailing()))
}

// emitTestBoot renders a group's `boot server.boot(startup)` line.
func emitTestBoot(expr ast.Node) Doc {
	return Group(Concat(Text("boot "), emit(expr)))
}

func emitTestSetup(expr ast.Node) Doc {
	header := Text("setup")
	if block, ok := expr.(*ast.Block); ok {
		if single := singlePlainExprStmt(block); single != nil && !isBlockExpr(single) {
			return Group(Concat(header, Text(" "), emit(single)))
		}
		return Concat(header, Text(" begin"), emitBlockForceBroken(block))
	}
	return Group(Concat(header, Text(" "), emit(expr)))
}

// isBlockExpr reports whether n is a block expression. A body whose one
// statement is a block (`setup { {} }`, `fn -> { {} }`) keeps its own braces
// around it: written bare after the header, the inner block's braces would
// read back as the body's, one level gone. flattenBodyBlocks has already
// merged every inner block that may take the body's place, so the ones left
// here are empty or hold a `defer`, `with`, nested `fn` or import.
func isBlockExpr(n ast.Node) bool {
	_, ok := n.(*ast.Block)
	return ok
}

// singlePlainExprStmt answers the expression of a block that holds one
// expression statement and no comment, which a `setup` writes after its
// keyword with no braces. A blank line inside the braces is no reason to
// keep them: formatting drops it either way, so a second pass would write
// the bare form.
func singlePlainExprStmt(block *ast.Block) ast.Node {
	if block == nil || len(block.Stmts) != 1 || hasNonBlankTrivia(block.GetLeading()) || hasNonBlankTrivia(block.GetTrailing()) {
		return nil
	}
	stmt := block.Stmts[0]
	if carrier, ok := stmt.(ast.HasTrivia); ok && (hasNonBlankTrivia(carrier.GetLeading()) || hasNonBlankTrivia(carrier.GetTrailing())) {
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
		return withPipeDangling(pipe, emitKeywordPrefixedPipe(kw, pipe))
	}
	return Group(Concat(Text(kw), Text(" "), emit(v.Expr)))
}

func emitGroupedExpr(v *ast.GroupedExpr) Doc {
	if v == nil || v.Expr == nil {
		return Text("()")
	}
	if c := itemComments(v.Expr); c != nil {
		// A comment inside the parentheses keeps them, the expression on
		// its own line between them with its comments around it.
		return emitCommentedListNoLastComma("(", ")", []Doc{emit(v.Expr)}, []*ast.ItemComments{c})
	}
	if inner, ok := v.Expr.(*ast.GroupedExpr); ok {
		// The inner parentheses go, and their comments stay.
		return withDangling(inner, emitGroupedExpr(inner))
	}
	if groupedExprCanDropParens(v.Expr) {
		return emit(v.Expr)
	}
	return Concat(Text("("), emit(v.Expr), Text(")"))
}

// emitMarked writes the expression a postfix `!` or `?` is written
// against. A parenthesized `_` keeps its parentheses: `(_)!` parses, and
// `_!` does not.
func emitMarked(n ast.Node) Doc {
	g, ok := n.(*ast.GroupedExpr)
	if !ok || itemComments(g.Expr) != nil {
		return emit(n)
	}
	inner := g.Expr
	for next, ok := inner.(*ast.GroupedExpr); ok && next.Expr != nil; next, ok = inner.(*ast.GroupedExpr) {
		inner = next.Expr
	}
	if id, ok := inner.(*ast.Ident); ok && id.Name == "_" {
		return withDangling(g, Concat(Text("("), emit(inner), Text(")")))
	}
	return emit(n)
}

func groupedExprCanDropParens(n ast.Node) bool {
	switch n.(type) {
	case *ast.Ident, *ast.SelfExpr, *ast.TypeIdent, *ast.IntLit, *ast.FloatLit, *ast.DecimalLit, *ast.CodepointLit,
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

// emitConcurrentBlock renders `concurrent { body }` — spec §16's
// structured-concurrency scope. An atomic body (one statement that fits on a
// line, like `concurrent { 42 }`) stays inline; anything multi-statement breaks
// the body onto its own lines.
func emitConcurrentBlock(v *ast.ConcurrentBlock) Doc {
	return Concat(Text("concurrent"), emitBlockForceBroken(v.Body))
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
	parts = append(parts, Text(" then "), emitInlineBranch(v.Then))
	if v.Else != nil {
		parts = append(parts, Text(" else "))
		// `else if` chain: recurse without adding a nested Group so the whole
		// chain shares one flat-vs-broken decision.
		if nested, ok := v.Else.(*ast.If); ok {
			parts = append(parts, emitIfInner(nested))
		} else if b, ok := v.Else.(*ast.Block); ok {
			parts = append(parts, emitInlineBranch(b))
		} else {
			parts = append(parts, emit(v.Else))
		}
	}
	return Concat(parts...)
}

// emitInlineBranch writes the one statement of an inline branch.
func emitInlineBranch(b *ast.Block) Doc {
	if b == nil || len(b.Stmts) != 1 {
		return Nil()
	}
	return emitWithTrivia(b.Stmts[0])
}

// isInlineBranch reports whether stmt may stand as an inline branch: an
// expression or a jump. An `if` there would take the outer `else` as its
// own, or read back as an `else if` chain, so it keeps the block form.
func isInlineBranch(stmt ast.Node) bool {
	if es, ok := stmt.(*ast.ExprStmt); ok {
		stmt = es.Expr
	}
	switch stmt.(type) {
	case *ast.If, *ast.Binding, *ast.TupleDestructure, *ast.StructDestructure, *ast.PatternDestructure,
		*ast.PatternBinding, *ast.DistinctDestructure, *ast.With, *ast.Defer, *ast.Assertion,
		*ast.FuncDef, *ast.StructDef, *ast.EnumDef, *ast.TypeDef, *ast.TypeAlias,
		*ast.InterfaceDef, *ast.ImplBlock, *ast.ImportStmt, *ast.ImportBlock, *ast.Subtests, *ast.Block:
		return false
	}
	return true
}

func emitIfCondition(v *ast.If) Doc {
	if v == nil || v.Cond == nil {
		return nil
	}
	if v.CondPattern != nil {
		return Concat(emitPattern(v.CondPattern), Text(" = "), emit(v.Cond))
	}
	if v.StagePrefix {
		// A pipe stage keeps its parentheses (emitStage).
		return emitStage(v.Cond, false)
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
// are no more visually complex than their payloads. It judges n as the
// formatter prints it, without the parentheses emitGroupedExpr drops:
// `Ok((x))` prints as `Ok(x)`, and judging the parentheses would inline the
// block only on a second format.
func isAtomicExpr(n ast.Node) bool {
	switch v := stripDroppedParens(n).(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit, *ast.CodepointLit, *ast.StringLit,
		*ast.Ident, *ast.SelfExpr, *ast.TypeIdent, *ast.Placeholder, *ast.PlaceholderParam:
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
		if !isVariantCallee(v.Func) || nodesHaveComments(v.Args) || len(v.EndTrivia) > 0 {
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
	switch v := stripDroppedParens(n).(type) {
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
// triviaComments is the text of each comment in n's trailing trivia, or nil
// for a node whose Trailing are its end trivia (keepsEndTriviaAsTrailing).
func triviaComments(n ast.Node) []string {
	ht, ok := n.(ast.HasTrivia)
	if !ok {
		return nil
	}
	var out []string
	for _, t := range outerTrailing(n, ht) {
		if t.Kind == ast.TriviaComment {
			out = append(out, t.Text)
		}
	}
	return out
}

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
	if trailingHasComment(b.GetTrailing()) || nodeHasLeadingComment(b.Stmts[0]) {
		return false
	}
	return !hasTrailingLineComment(b.Stmts[0]) && isInlineBranch(b.Stmts[0])
}

// nodeHasLeadingComment reports whether n carries a comment above it.
func nodeHasLeadingComment(n ast.Node) bool {
	ht, ok := n.(ast.HasTrivia)
	return ok && trailingHasComment(ht.GetLeading())
}

// ifKeepsBroken reports whether an `if` the short form could write on one
// line keeps its broken form: blockKeepsBroken for its first branch.
func ifKeepsBroken(v *ast.If, inner Doc) bool {
	return blockKeepsBroken(v.Then, inner)
}

// blockKeepsBroken reports whether a block the short form could write on
// one line (`if`, a pattern binding's `else`) keeps its broken form, as
// keepsBrokenLines says for a list: its first statement starts on a line
// below its header, and short, the short form, holds an element broken
// whatever the width.
func blockKeepsBroken(b *ast.Block, short Doc) bool {
	if b == nil || len(b.Stmts) == 0 {
		return false
	}
	return keepsBrokenLines(b.Line, nodeStartLine(b.Stmts[0]), []Doc{short})
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
	return Concat(emitIfBranchesBroken(v), HardLine(), Text("end"))
}

// emitIfBranchesBroken writes an if/else chain's branches in block form, each
// branch's statements below its `then` or `else`, without the `end` that the
// chain shares.
func emitIfBranchesBroken(v *ast.If) Doc {
	parts := []Doc{Text("if")}
	if cond := emitIfCondition(v); cond != nil {
		parts = append(parts, Text(" "), cond)
	}
	parts = append(parts, Text(" then"), emitBodyLines(v.Then))
	if v.Else != nil {
		parts = append(parts, HardLine())
		switch e := v.Else.(type) {
		case *ast.If:
			parts = append(parts, Text("else "), emitIfBranchesBroken(e))
		case *ast.Block:
			parts = append(parts, Text("else"), emitBodyLines(e))
		default:
			// Defensive: shouldn't happen per parser invariants, but don't
			// panic — fall back to the standard emit.
			parts = append(parts, Text("else"), Nest(defaultIndent, Concat(HardLine(), emit(v.Else))))
		}
	}
	return Concat(parts...)
}

// emitBlockForceBroken renders a body after its header in always-broken
// form: its statements on the lines below, separated by HardLine so the
// layout engine cannot flatten, end-of-block trivia (comments before `end`)
// after the last statement, and `end`.
func emitBlockForceBroken(b *ast.Block) Doc {
	body := emitStatementDocs(b.Stmts)
	return emitDocsBlockForceBroken(body, b.GetTrailing())
}

func emitStatementDocs(stmts []ast.Node) []Doc {
	return emitStatementDocsTop(stmts, true)
}

// emitStatementDocsTop is emitStatementDocs; dropTop drops the blank lines
// the source wrote above the first statement, among its comments too. A
// blank line below a header is no blank line between statements: a body has
// none at its top, as it had none after its `{`. The lines of a `//!` prompt
// keep theirs, which stand for blank prompt lines.
func emitStatementDocsTop(stmts []ast.Node, dropTop bool) []Doc {
	if len(stmts) == 0 {
		return nil
	}
	items := make([]statementItem, len(stmts))
	for i, s := range stmts {
		items[i] = newStatementItem(s, emit(s))
	}
	if ht, ok := stmts[0].(ast.HasTrivia); ok && dropTop && hasBlankTrivia(ht.GetLeading()) {
		items[0].full = emitWithTriviaParts(items[0].doc, withoutBlankTrivia(ht.GetLeading()), outerTrailing(stmts[0], ht))
		items[0].authorBlank = false
	}
	return []Doc{emitStatementSequence(items)}
}

// statementItem is one statement of a block for emitStatementSequence. doc is
// the statement alone, which is what is measured; full adds its comments.
// authorBlank records a blank line the source wrote above it, which full
// already renders. node is nil for an item that is not a statement node, and
// blankAfter forces a blank line after the item. guardHead is the head of a
// binding with an else block (`Some(x) = value else `), measured in place of
// doc.
type statementItem struct {
	node        ast.Node
	doc         Doc
	full        Doc
	guardHead   Doc
	authorBlank bool
	blankAfter  bool
}

func newStatementItem(s ast.Node, doc Doc) statementItem {
	it := statementItem{
		node:        s,
		doc:         doc,
		full:        emitWithTriviaDoc(s, doc),
		authorBlank: hasLeadingBlank(s),
	}
	if pb, ok := s.(*ast.PatternBinding); ok && pb.Else != nil && pb.Else.Block != nil {
		it.guardHead = patternBindingHead(pb, emitPattern(pb.Pattern))
	}
	return it
}

// emitStatementSequence lays out a block's statements, one per line, with one
// blank line between two of them when:
//   - either one spans more than one line, so a multi-line statement stands
//     apart from its neighbours (no blank line is added at the top or bottom
//     of the block). A pipe statement is no exception either way: a stacked
//     one, or one that breaks to fit the width, is set apart, and one that
//     fits on its line sits with its neighbours. A binding with an else
//     block counts by its head alone: a guard whose `Pattern = value else {`
//     is one line sits with its neighbours however its block breaks (an else
//     with arms is laid out as a case, and counts whole);
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
			if it.guardHead != nil {
				multiline[i] = spansLines(it.guardHead, indent, width)
			} else {
				multiline[i] = spansLines(it.doc, indent, width)
			}
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
	var out renderOut
	render(&out, d, indent, indent, noFlatGroup, nil, width, false)
	return strings.Contains(out.String(), "\n")
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
	case docInString:
		return containsHardLine(v.d)
	case docWithIndent:
		return containsHardLine(v.f(0, unboundedWidth))
	case docWithColumn:
		return containsHardLine(v.f(0, 0, unboundedWidth))
	case docAtLineIndent:
		return containsHardLine(v.f(true))
	}
	return false
}

func isPipeExpr(n ast.Node) bool {
	if assertion, ok := n.(*ast.Assertion); ok {
		return assertion.Expr != nil && isPipeExpr(assertion.Expr)
	}
	pipe, ok := n.(*ast.Binary)
	return ok && pipe.Op == "|>"
}

// emitDocsBlockForceBroken writes a body after its header: parts, the
// statements, indented on the lines below, and `end` at the header's
// indentation, with endTrivia, the comments before `end`, above it. An
// empty body is ` end` on the header's line.
func emitDocsBlockForceBroken(parts []Doc, endTrivia []ast.Trivia) Doc {
	if len(parts) == 0 && !trailingHasComment(endTrivia) {
		return Text(" end")
	}
	return Concat(emitBodyLinesDocs(parts, endTrivia), HardLine(), Text("end"))
}

// emitBodyLinesDocs writes a body's statements, parts, on the lines below
// its header, one indent in, with endTrivia after them: an `if` branch and an
// arm's body, which the next branch, arm or `end` closes.
func emitBodyLinesDocs(parts []Doc, endTrivia []ast.Trivia) Doc {
	if len(parts) == 0 {
		if len(endTrivia) == 0 {
			return Nil()
		}
		// Only comments: emitEndTrivia starts its own line.
		return Nest(defaultIndent, emitEndTrivia(endTrivia))
	}
	if len(endTrivia) > 0 {
		parts = append(parts, emitEndTrivia(endTrivia))
	}
	return Nest(defaultIndent, Concat(HardLine(), Concat(parts...)))
}

// emitBodyLines is emitBodyLinesDocs for a block.
func emitBodyLines(b *ast.Block) Doc {
	return emitBodyLinesDocs(emitStatementDocs(b.Stmts), b.GetTrailing())
}

// emitDeclBody writes a declaration's body, its members one per line below
// the header and `end`, or ` end` when it has none.
func emitDeclBody(elems []Doc, endTrivia []ast.Trivia) Doc {
	body := make([]Doc, 0, len(elems)*2)
	for _, e := range elems {
		body = append(body, HardLine(), e)
	}
	if len(endTrivia) > 0 {
		body = append(body, emitEndTrivia(endTrivia))
	}
	if len(elems) == 0 && !trailingHasComment(endTrivia) {
		return Text(" end")
	}
	return Concat(Nest(defaultIndent, Concat(body...)), HardLine(), Text("end"))
}

// maxCompoundDepth is the depth at which a Map/List/struct-shaped
// pattern or construction breaks vertically regardless of width. Three
// consecutive compound levels are unreadable single-line even when they
// happen to fit — the eye loses track of which `{...}` closes which
// opener. Below this threshold the Group's own width-based decision
// still applies: a depth-2 form that exceeds the width still breaks.
//
// Only the literal or pattern whose own depth reaches the threshold
// breaks. Its elements are laid out on their own merits, by width and by
// their own depth, so in `[R{p: P{x: 0, y: 0}, n: 1}]` the list breaks
// and the depth-2 `R{...}` stays on one line inside it.
const maxCompoundDepth = 3

// tooDeep reports whether n, a compound literal or pattern with count
// elements, breaks for its depth whatever its width.
func tooDeep(n ast.Node, count int) bool {
	return count > 0 && compoundDepth(n) >= maxCompoundDepth
}

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
	case *ast.GroupedExpr:
		// Parentheses add no level, and the formatter drops most of them:
		// the depth must be the same with them and without.
		return compoundDepth(v.Expr)
	case *ast.MapLit:
		d := 0
		for _, sp := range v.Spreads {
			if c := compoundDepth(sp.Head); c > d {
				d = c
			}
		}
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
		if len(v.Spreads) > 0 && v.TypeName == nil {
			// `[head, ..tail]` is cons-shaped and conventionally short, so it
			// is no compound level and passes no depth through: a struct
			// holding one stays on one line when it fits. It still breaks
			// for width, and stays broken when its author broke it after
			// `[`.
			return 0
		}
		d := 0
		for _, it := range v.Operands() {
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
		for _, t := range v.Tails {
			if c := compoundDepth(t); c > d {
				d = c
			}
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
	case *ast.AsPattern:
		// `Obj{...} as o` — the name adds no level.
		return compoundDepth(v.Pattern)
	case *ast.OrPattern:
		// `Obj{...} or Obj{...}` — the deepest alternative's level.
		d := 0
		for _, alt := range v.Alternatives {
			if c := compoundDepth(alt); c > d {
				d = c
			}
		}
		return d
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

// emitBracedList renders "<open>e1, e2, ...<close>" flat, or
// "<open>\n  e1,\n  e2,\n  ...\n<close>" broken, choosing via a Group so
// the layout engine picks flat when the content fits the line budget and
// broken otherwise. A trailing comma appears only in the broken form.
//
// Empty input renders as "<open><close>" with no space or Group — no layout
// decision is needed.
func emitBracedList(open, close string, elems []Doc) Doc {
	return bracedList(open, close, elems, true)
}

// bracedList is emitBracedList, with the broken form's trailing comma only
// when lastComma is set.
func bracedList(open, close string, elems []Doc, lastComma bool) Doc {
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
	comma := Nil()
	if lastComma {
		comma = IfBroken(Nil(), Text(","))
	}
	return Group(Concat(
		Text(open),
		Nest(defaultIndent, Concat(LineOrEmpty(), inner)),
		comma,
		LineOrEmpty(),
		Text(close),
	))
}

// emitStructLit renders a struct literal, nominal (`User{...}`) or with no
// type name (`{...}`). Field punning, where the parser synthesizes
// Value=Ident{Name:FieldName}, is re-emitted as `name:`.
//
// Width-based, except that a literal whose source breaks the line after
// its `{` stays broken even when it would fit flat (brokenAfterOpen).
func emitStructLit(v *ast.StructLit) Doc {
	open := structOpen(v.TypeName)
	hasEndTrivia := trailingHasComment(v.EndTrivia)
	hasInterFieldTrivia := structFieldValsHaveLeading(v.Fields) || structFieldValsHaveLeading(v.Positional) || v.Spread != nil && itemComments(v.Spread) != nil
	elems := make([]Doc, 0, len(v.Fields)+len(v.Positional)+1)
	// `..head` leads, always. The position rule is the parser's and the
	// formatter has no freedom here: moving it would be a different
	// program (a spread after a field does not parse).
	if v.Spread != nil {
		elems = append(elems, Concat(Text(".."), emit(v.Spread)))
	}
	for _, f := range v.Fields {
		elems = append(elems, emitStructFieldValWithLeading(f))
	}
	// A value written with no field name is an error the checker reports;
	// the formatter, which runs without it, keeps the value after the
	// named fields.
	for _, f := range v.Positional {
		elems = append(elems, Concat(append(emitLeadingComments(withoutBlankTrivia(f.LeadingComments)), emitPositionalValue(f.Value))...))
	}
	// EndTrivia (trailing in-body comment) or any inter-field leading
	// comment forces the broken form — a `// comment` can't share a
	// line with the surrounding fields / closing `}`. So do a literal
	// three compound levels deep (tooDeep) and a line break after the `{`
	// (brokenAfterOpen).
	if brokenAfterOpen(v.Line, structLitFirstLine(v)) || hasEndTrivia || hasInterFieldTrivia || tooDeep(v, len(v.Fields)) {
		return emitCommentedList(open, "}", elems, structLitComments(v), v.EndTrivia)
	}
	return emitBracedList(open, "}", elems)
}

// emitPositionalValue writes a struct literal's value with no field name. A
// parenthesized name, `User{(age)}`, keeps one pair of parentheses:
// emitGroupedExpr drops them around an identifier, and `User{age:}` puns
// the field `age`, a different program.
func emitPositionalValue(n ast.Node) Doc {
	g, ok := n.(*ast.GroupedExpr)
	if !ok || groupedHasComments(g) {
		return emit(n)
	}
	inner := g.Expr
	for next, ok := inner.(*ast.GroupedExpr); ok && next.Expr != nil && !nodeHasDangling(next); next, ok = inner.(*ast.GroupedExpr) {
		inner = next.Expr
	}
	if _, ok := inner.(*ast.Ident); !ok {
		return emit(n)
	}
	return withDangling(g, Concat(Text("("), emit(inner), Text(")")))
}

// structLitFirstLine is the line v's first element starts on, its spread's
// or its first field's, or 0 when it has none.
func structLitFirstLine(v *ast.StructLit) int {
	if v.Spread != nil {
		return v.SpreadLine
	}
	if len(v.Fields) > 0 {
		return v.Fields[0].Line
	}
	return 0
}

// nodeStartLine is the line n's first token is on.
func nodeStartLine(n ast.Node) int {
	if s, ok := n.(ast.HasSpan); ok && !s.GetSpan().IsZero() {
		return s.GetSpan().StartLine
	}
	return n.LineNum()
}

// structFieldValsHaveLeading reports whether any field carries inter-field
// leading comments or a same-line trailing one. Used by emitStructLit to
// force the broken form when at least one comment lives beside a field —
// otherwise the comment lines would collapse into the flat layout and emit
// syntax-invalid output (a `// comment` cannot sit mid-line).
func structFieldValsHaveLeading(fields []ast.StructFieldVal) bool {
	for _, f := range fields {
		if len(f.LeadingComments) > 0 || len(f.Trailing) > 0 {
			return true
		}
	}
	return false
}

// structLitComments is the comments emitCommentedList writes beside each
// of emitStructLit's elements: the spread's first, when there is one, then
// each field's same-line comment. A field's leading comments are part of
// its element's document.
func structLitComments(v *ast.StructLit) []*ast.ItemComments {
	var out []*ast.ItemComments
	if v.Spread != nil {
		out = append(out, itemComments(v.Spread))
	}
	for _, f := range v.Fields {
		out = append(out, &ast.ItemComments{Trailing: f.Trailing})
	}
	for _, f := range v.Positional {
		out = append(out, &ast.ItemComments{Trailing: f.Trailing})
	}
	return out
}

// fieldValueIsPun reports whether a field's value is exactly its own name, the
// shape both the parser's punning synthesis and an explicit `name: name` land
// on. Parentheses around the name count, `t: (t)`, unless a comment sits in
// or beside them: emitGroupedExpr drops them around an identifier, so
// writing `t: t` would leave a second format to pun it.
func fieldValueIsPun(f ast.StructFieldVal) bool {
	v := f.Value
	for g, ok := v.(*ast.GroupedExpr); ok; g, ok = v.(*ast.GroupedExpr) {
		if g.Expr == nil || groupedHasComments(g) || nodeHasDangling(g) {
			return false
		}
		v = g.Expr
	}
	id, ok := v.(*ast.Ident)
	return ok && id.Name == f.Name
}

// argValueIsPun reports whether a named argument's value is exactly its own
// name, `status: status`, which is written `status:`, as fieldValueIsPun
// decides for a struct field.
func argValueIsPun(a *ast.NamedArg) bool {
	return fieldValueIsPun(ast.StructFieldVal{Name: a.Name, Value: a.Value})
}

// patternFieldIsPun reports whether a struct pattern field's pattern is a
// name pattern for the field's own name with no comment, as `t: (t)` parses.
func patternFieldIsPun(f ast.StructPatternField) bool {
	id, ok := f.Pattern.(*ast.IdentPattern)
	return ok && id.Name == f.Name && itemComments(id) == nil && !nodeHasDangling(id)
}

// nodeHasDangling reports whether n carries a dangling comment before or
// after it.
func nodeHasDangling(n ast.Node) bool {
	h, ok := n.(ast.HasDangling)
	return ok && (len(h.GetDanglingBefore()) > 0 || len(h.GetDanglingAfter()) > 0)
}

// emitStructFieldValWithLeading renders one struct-literal field with
// its leading-comment block above. A value that is the field's own name is
// written punned, `name:`.
func emitStructFieldValWithLeading(f ast.StructFieldVal) Doc {
	parts := []Doc{}
	parts = append(parts, emitLeadingComments(withoutBlankTrivia(f.LeadingComments))...)
	if fieldValueIsPun(f) {
		parts = append(parts, Text(f.Name+":"))
		return Concat(parts...)
	}
	parts = append(parts, Text(f.Name), Text(": "), emit(f.Value))
	return Concat(parts...)
}

// emitCase renders a case expression. It always breaks, one arm per line,
// and lays the arms out together in one of two ways:
//
//   - Flat, when every arm fits on its line as `<pattern> -> <body>` and no
//     arm is a block: the arms sit on consecutive lines with no blank line
//     between them.
//
//   - Broken, otherwise: every arm puts its body on the lines below its
//     `->`, one indent in, and one blank line separates every two arms:
//
//     case <scrutinee>
//     <pattern> [when <guard>] ->
//     <body>
//
//     <pattern> ->
//     <statements>
//     end
//
// In both layouts the source's blank lines between arms are ignored, and a
// comment stays directly above its arm. The arms start at a column known only
// at render time, so the decision is made there (WithIndent), measuring each
// arm from that column.
func emitCase(v *ast.Case) Doc {
	parts := []Doc{Text("case")}
	if _, grouped := v.Value.(*ast.GroupedExpr); grouped && v.StagePrefix {
		// A pipe stage keeps its parentheses: `|> case (f()) {` pipes into
		// the value f() returns, and `|> case f() {` calls f.
		parts = append(parts, Text(" "), emitStage(v.Value, false))
	} else if v.Value != nil {
		parts = append(parts, Text(" "), emit(v.Value))
	}
	if len(v.Branches) == 0 && !trailingHasComment(v.GetTrailing()) {
		return Concat(append(parts, Text(" end"))...)
	}
	// Trivia sitting between the last arm and the `end` was attached to the
	// Case node by the parser; it is emitted above the `end`.
	parts = append(parts,
		Nest(defaultIndent, emitCaseArms(v.Branches, v.GetTrailing())),
		HardLine(),
		Text("end"),
	)
	return Concat(parts...)
}

// emitCaseArms lays out case arms, each on its own line after a HardLine,
// followed by endTrivia, the comments between the last arm and the `end`,
// at the last arm's body's indent when that body is below its `->`.
// When every arm fits on its line and none has a block or triple-quoted body,
// the arms sit on consecutive lines; otherwise each arm breaks and a blank
// line separates them.
func emitCaseArms(branches []ast.CaseBranch, endTrivia []ast.Trivia) Doc {
	return WithIndent(func(indent, width int) Doc {
		flat := true
		for i := range branches {
			b := &branches[i]
			// A block or triple-quoted body spans lines, as a block does:
			// every arm breaks, and blank lines separate them.
			if _, isBlock := b.Body.(*ast.Block); isBlock || isTripleLiteral(b.Body) || armBodyHasComments(b.Body) {
				flat = false
				break
			}
			rem := width - indent
			line := caseArmLine(b, false)
			if caseArmKeepsBroken(b, line) || !fitsWalk(line, &rem, false) {
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
			if n := len(branches); n > 0 && !flat && !isTripleLiteral(branches[n-1].Body) {
				// The last arm's body is on the lines below its `->`, and
				// the parser reads the lines up to `end` as that body's: a
				// comment there is the body's last line, at its indent.
				body = append(body, Nest(defaultIndent, emitEndTrivia(endTrivia)))
			} else {
				body = append(body, emitEndTrivia(endTrivia))
			}
		}
		return Concat(body...)
	})
}

// caseArmKeepsBroken reports whether arm b keeps its body on the line below
// its `->`, as keepsBrokenLines says for a list: the source writes it there,
// and line, the arm on one line, holds an element broken whatever the
// width.
func caseArmKeepsBroken(b *ast.CaseBranch, line Doc) bool {
	head := b.Pattern
	if b.Guard != nil {
		head = b.Guard
	}
	return keepsBrokenLines(nodeEndLine(head), nodeStartLine(b.Body), []Doc{line})
}

// caseArmLine renders one arm, `<pattern> [when <guard>] -> <body>`, without
// its comments. A block body goes below the `->`, its statements one indent
// in. A triple-quoted or multi-line
// raw literal follows `-> ` on the pattern's line too, its body one level in
// (bodyFromOpenerLine). Any other body follows on the same line, or with
// broken on the next line one indent in.
func caseArmLine(b *ast.CaseBranch, broken bool) Doc {
	prefix := emitGroupedPattern(b.Pattern)
	if b.Guard != nil {
		prefix = Concat(prefix, Text(" when "), emit(b.Guard))
	}
	if block, isBlock := b.Body.(*ast.Block); isBlock {
		// Every broken arm's body starts on the line after its pattern, one
		// indent in.
		return Concat(prefix, Text(" ->"), emitBodyLines(block))
	}
	if armBodyHasComments(b.Body) {
		// The body below the `->` carries its own comments (a jump the
		// parser took as the arm's body, parser.singleBody), which stay
		// on its lines.
		return Concat(prefix, Text(" ->"), Nest(defaultIndent, Concat(HardLine(), emitWithTriviaNoLeadingBlank(b.Body))))
	}
	if broken && !isTripleLiteral(b.Body) {
		return Concat(prefix, Text(" ->"), Nest(defaultIndent, Concat(HardLine(), emitLambdaSingleExprBody(b.Body))))
	}
	return Concat(prefix, Text(" -> "), emit(b.Body))
}

// armBodyHasComments reports whether an arm's body, other than a block,
// holds comments of its own: `-> continue // why` written below the `->`,
// which the parser makes the body itself with the comment on it.
func armBodyHasComments(body ast.Node) bool {
	if _, isBlock := body.(*ast.Block); isBlock {
		return false
	}
	ht, ok := body.(ast.HasTrivia)
	return ok && (trailingHasComment(ht.GetLeading()) || trailingHasComment(ht.GetTrailing()))
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
			// Hidden, as emitWithTriviaParts writes a trailing comment:
			// emitCaseArms measured the arm without it, so a group in
			// the body must not break to make room for it.
			parts = append(parts, Hidden(Concat(Text(" "), Text(t.Text))))
		}
	}
	return Concat(parts...)
}

// emitCall renders a function call with a paren-delimited arg list.
//
// Special case — last arg is a lambda: emit `func(leading_args, fn params -> <body>)`
// so the lambda's opener hugs the call's `(` and its `}` hugs the `)`. Prevents
// the ugly double-indent of a multi-stmt lambda inside a generic argList Group:
//
//	// without special case:              // with special case:
//	lists.map(                             lists.map(fn x -> {
//	  fn x -> {                             if x == 3 { return 300 }
//	    if x == 3 { return 300 }            x * 10
//	    x * 10                            })
//	  },
//	)
//
// For trivial single-expression lambdas the output is identical either way
// (`lists.map(fn x -> x * 2)` fits inline); the special-case only differs when the
// lambda body breaks.
func emitCall(v *ast.Call) Doc {
	callee := emit(v.Func)
	if len(v.TypeArgs) > 0 {
		callee = Concat(callee, emitTypeArgs(v.TypeArgs))
	}
	n := len(v.Args)
	if n == 0 {
		return Concat(callee, emitEmptyArgList(v))
	}
	if cs, ok := nodesComments(v.Args); ok {
		elems := make([]Doc, len(v.Args))
		for i, a := range v.Args {
			elems[i] = emitCallArg(a)
		}
		return Concat(callee, emitCommentedArgs(elems, cs))
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
					emitLambdaHeader(lam),
					Nest(defaultIndent, Concat(HardLine(), emitLambdaSingleExprBody(expr))),
					HardLine(),
					Text("end)"),
				)
				return Concat(callee, Concat(parts...))
			}
			if isStructLitExpr(expr) || isTripleLiteral(expr) {
				// A struct literal body stays on the parameters' line and
				// breaks itself, as in emitLambdaKeepingBraces, and so does
				// a triple-quoted or multi-line raw literal.
				parts := []Doc{Text("(")}
				for _, a := range leading {
					parts = append(parts, emitCallArg(a), Text(", "))
				}
				parts = append(parts, emitLambdaHeader(lam), Text(" "), emit(expr), Text(")"))
				return Concat(callee, Concat(parts...))
			}

			// Single-expression tail lambda: when the body does not fit on
			// the parameters' line it moves below them, and the call closes
			// `end)` as a block-bodied tail lambda's does. One Group governs
			// the whole thing so the body break and the `end` move together:
			// flat stays bare, broken is a block.
			parts := []Doc{Text("(")}
			for _, a := range leading {
				parts = append(parts, emitCallArg(a), Text(", "))
			}
			parts = append(parts,
				emitLambdaHeader(lam),
				Nest(defaultIndent, Concat(Line(), emit(expr))),
				endWhenBroken(),
				Text(")"),
			)
			return Concat(callee, Group(Concat(parts...)))
		}
		// Block-bodied tail lambda: hug the `)` — the `end` is the lambda's
		// own, so `end)` closes the body and then the call.
		parts := []Doc{Text("(")}
		for _, a := range leading {
			parts = append(parts, emitCallArg(a), Text(", "))
		}
		parts = append(parts, emit(v.Args[n-1]), Text(")"))
		return Concat(callee, Concat(parts...))
	}
	if elems := callArgDocs(v.Args); keepsBrokenList(nodeEndLine(v.Func), v.Args, elems) {
		return Concat(callee, brokenList("(", ")", elems, n > 1))
	}
	return Concat(callee, emitArgList(v.Args))
}

// callArgDocs is each of args as emitArgList writes it.
func callArgDocs(args []ast.Node) []Doc {
	out := make([]Doc, len(args))
	for i, a := range args {
		out[i] = emitCallArg(a)
	}
	return out
}

// lambdaSingleExpr returns the body expression and true when lam's body is a
// single ExprStmt — the bare-expression lambda shape (`fn x -> x + 1`, `fn x -> if … {…}`)
// that emitLambda renders without block braces. Block-bodied lambdas (multi-
// statement, a single break/return/continue the parser wrapped in `{}`, or a
// pipe written in braces, which keep them) return false; those keep the
// hugged `)`.
func lambdaSingleExpr(lam *ast.Lambda) (ast.Node, bool) {
	if lam.Body == nil || len(lam.Body.Stmts) != 1 {
		return nil, false
	}
	es, ok := lam.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || isBlockExpr(es.Expr) || (isPipeExpr(es.Expr) && authoredLambdaBlock(lam)) || lambdaKeepsBlock(lam) || statementOrBlockHasComment(lam.Body, es) {
		// A braced pipe body keeps its braces (see emitLambda), so it hugs
		// `})` like a block body, and so do a block written across lines
		// (lambdaKeepsBlock) and a body with a comment, which only the
		// block form writes.
		return nil, false
	}
	return es.Expr, true
}

// authoredLambdaBlock reports whether lam's body was written below its `->`,
// closed by `end`. The parser wraps a one-line body in a Block with no
// closing line.
func authoredLambdaBlock(lam *ast.Lambda) bool {
	return lam.Body != nil && lam.Body.EndLine != 0
}

// lambdaKeepsBlock reports whether lam's body was written as a block, below
// its `->` and closed by `end`. Such a body stays a block with its
// statements on their own lines, however short: the line break after the
// `->` is the author's, as a break after an object literal's `{` is in
// Prettier.
func lambdaKeepsBlock(lam *ast.Lambda) bool {
	return authoredLambdaBlock(lam) && len(lam.Body.Stmts) > 0 &&
		nodeStartLine(lam.Body.Stmts[0]) > lam.Body.Line
}

// emitTypeArgs renders a turbofish type-argument list, `<Int>` /
// `<String, Int>`. Type args are short, so they emit flat (no breaking).
func emitTypeArgs(args []ast.TypeExpr) Doc {
	if cs, ok := typesComments(args); ok {
		docs := make([]Doc, len(args))
		for i, a := range args {
			docs[i] = emitTypeItem(a)
		}
		return emitCommentedListNoLastComma("<", ">", docs, cs)
	}
	for _, a := range args {
		if typeHasComment(a) {
			// A comment inside an argument, which TypeString would drop.
			docs := make([]Doc, len(args))
			for i, a := range args {
				docs[i] = emitTypeExpr(a)
			}
			return Concat(Text("<"), Join(Text(", "), docs...), Text(">"))
		}
	}
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

// emitCallArg renders one argument inside a call's argument list. A pipe
// chain as an argument is always laid out stacked.
func emitCallArg(n ast.Node) Doc {
	if v, ok := n.(*ast.Binary); ok && v.Op == "|>" {
		return withPipeDangling(v, emitPipeChainForced(v))
	}
	return emit(n)
}

// emitLambda renders a lambda as "fn params -> body".
//
// Shapes:
//   - Zero params: "fn -> body".
//   - One or more params: "fn p1, p2, ... -> body".
//
// Body layout:
//   - Empty body: "fn params -> {}".
//   - A braced body whose first statement the source starts on a line
//     below the `{` (lambdaKeepsBlock): a block, however short.
//   - Exactly one ExprStmt: emit the expression inline ("fn params -> expr"),
//     flat-or-break via Group.
//   - Otherwise: emit a block ("fn params -> {\n  stmt1\n  stmt2\n}").
func emitLambda(v *ast.Lambda) Doc {
	return emitLambdaKeepingBraces(v, false)
}

// emitThen renders a `then` stage. Its lambda keeps the block it was
// written with: the body of a one-line `then` lambda ends at the next `|>`,
// so a block body that holds a pipe cannot become one line.
func emitThen(v *ast.Then) Doc {
	if v.Capture != nil {
		return Concat(Text("then "), emit(v.Capture))
	}
	return Concat(Text("then "), emitLambdaKeepingBraces(v.Lambda, true))
}

// emitThenQuestion renders a `then?` stage: a `then` stage and the `?`
// stage after it.
func emitThenQuestion(v *ast.Then) Doc {
	if v.Capture != nil {
		return Concat(Text("then? "), emit(v.Capture))
	}
	return Concat(Text("then? "), emitLambdaKeepingBraces(v.Lambda, true))
}

// isQuestionStage reports whether a pipe stage is a `?` stage.
func isQuestionStage(stage ast.Node) bool {
	try, ok := stage.(*ast.TryOp)
	return ok && try.Expr == nil
}

// emitTap renders a `tap` stage, whose lambda keeps its block as a `then`
// stage's does.
func emitTap(v *ast.Tap) Doc {
	if v.Capture != nil {
		return Concat(Text("tap "), emit(v.Capture))
	}
	if v.Name != nil {
		return Concat(Text("tap "), emit(v.Name))
	}
	return Concat(Text("tap "), emitLambdaKeepingBraces(v.Lambda, true))
}

// emitLambdaKeepingBraces is emitLambda. With keepBlock, a one-expression
// body written as a block below the `->` stays one; otherwise only a block
// body that holds a pipe does.
func emitLambdaKeepingBraces(v *ast.Lambda, keepBlock bool) Doc {
	header := emitLambdaHeader(v)

	body := v.Body
	if body == nil || len(body.Stmts) == 0 && !trailingHasComment(body.GetTrailing()) {
		return Concat(header, Text(" end"))
	}
	if len(body.Stmts) == 0 {
		// A body holding only comments keeps them, on their own lines.
		return emitLambdaBlock(header, body)
	}

	if es, ok := soleExprStmt(body); ok && !statementOrBlockHasComment(body, es) && lambdaExprBodyShouldBreak(es.Expr) {
		// An if that breaks or continues always goes below the
		// parameters, broken; whether the author wrote it there does not
		// change it.
		return Concat(
			header,
			Nest(defaultIndent, Concat(HardLine(), emitLambdaSingleExprBody(es.Expr))),
			HardLine(),
			Text("end"),
		)
	}
	if lambdaKeepsBlock(v) {
		return emitLambdaBlock(header, body)
	}
	if len(body.Stmts) == 1 {
		if es, ok := body.Stmts[0].(*ast.ExprStmt); ok {
			if statementOrBlockHasComment(body, es) || isBlockExpr(es.Expr) {
				return emitLambdaBlock(header, body)
			}
			if isStructLitExpr(es.Expr) || isTripleLiteral(es.Expr) {
				// A struct literal body stays on the parameters' line and
				// breaks itself when too wide. Read back, a broken literal
				// is stacked, which keeps it on that line, so moving it to
				// the next line would not survive a second format. A
				// triple-quoted or multi-line raw literal stays there too,
				// its body one level in (bodyFromOpenerLine).
				return Concat(header, Text(" "), emit(es.Expr))
			}
			// A body too long for the parameters' line moves below them,
			// closed by `end`, as rustfmt writes a closure in a block. Read
			// back, that body is a block (lambdaKeepsBlock), and stays one.
			return Group(Concat(
				header,
				Nest(defaultIndent, Concat(Line(), emit(es.Expr))),
				endWhenBroken(),
			))
		}
		if !isInlineBranch(body.Stmts[0]) || statementOrBlockHasComment(body, body.Stmts[0]) {
			return emitLambdaBlock(header, body)
		}
		return Group(Concat(
			header,
			Nest(defaultIndent, Concat(Line(), emitWithTrivia(body.Stmts[0]))),
			endWhenBroken(),
		))
	}

	// Multi-statement body: always below the parameters, closed by `end`.
	return emitLambdaBlock(header, body)
}

// soleExprStmt returns body's statement when it is its only one and an
// expression statement.
func soleExprStmt(body *ast.Block) (*ast.ExprStmt, bool) {
	if len(body.Stmts) != 1 {
		return nil, false
	}
	es, ok := body.Stmts[0].(*ast.ExprStmt)
	return es, ok && es.Expr != nil
}

// endWhenBroken closes a lambda's one-expression body with `end` on its own
// line at the lambda's indentation when the enclosing Group breaks and the
// body moves below the parameters, and writes nothing when the body stays
// on their line.
func endWhenBroken() Doc { return Concat(LineOrEmpty(), IfBroken(Nil(), Text("end"))) }

// isStructLitExpr reports whether n is a struct literal, in any number of
// parentheses.
func isStructLitExpr(n ast.Node) bool {
	for g, ok := n.(*ast.GroupedExpr); ok && g.Expr != nil; g, ok = n.(*ast.GroupedExpr) {
		n = g.Expr
	}
	_, ok := n.(*ast.StructLit)
	return ok
}

// isTripleLiteral reports whether n is a triple-quoted literal or a
// multi-line raw one, tagged or not: a literal that frames its body on the
// lines below its opening delimiter.
func isTripleLiteral(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.StringLit:
		return v.Triple
	case *ast.StringInterp:
		return v.Triple
	case *ast.TaggedString:
		return v.Triple
	}
	return false
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
	return Concat(header, emitBlockForceBroken(body))
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

// emitLambdaHeader renders the "fn p1, p2, ... ->" prefix. Zero params emit
// "fn ->".
//
// A header holding a comment is written as a list one parameter per line
// (lambdaParamsNeedList), except one whose only comments each end the line
// of a parameter other than the last, after its comma (`fn acc = 0, // why`
// then the next parameter on the next line), which keeps the first
// parameter on the `fn` line. A comment on the body's DanglingBefore
// (gatherLambdaHeaderComments) goes back below the parameter it followed.
func emitLambdaHeader(lam *ast.Lambda) Doc {
	params := lam.Params
	if len(params) == 0 {
		return Text("fn ->")
	}
	after := make([][]ast.Trivia, len(params))
	var unplaced []ast.Trivia
	if lam.Body != nil {
		for _, t := range lam.Body.GetDanglingBefore() {
			i := len(params) - 1
			for i >= 0 && !paramStartsBefore(params[i], t) {
				i--
			}
			if i < 0 || i == len(params)-1 {
				unplaced = append(unplaced, t)
				continue
			}
			after[i] = append(after[i], t)
		}
	}
	if lambdaParamsNeedList(params, after) {
		// The parameter list has no trailing comma.
		cs := make([]*ast.ItemComments, len(params))
		docs := make([]Doc, len(params))
		for i, p := range params {
			docs[i] = emitLambdaParam(p)
			c := p.Comments
			c.After = append(append([]ast.Trivia(nil), c.After...), after[i]...)
			cs[i] = &c
		}
		parts := make([]Doc, 0, len(unplaced)+1)
		for _, t := range unplaced {
			parts = append(parts, CommentBefore(t.Text))
		}
		return Concat(append(parts, emitCommentedListNoLastComma("fn", "->", docs, cs))...)
	}
	parts := make([]Doc, 0, len(params)*3+1)
	for _, t := range unplaced {
		parts = append(parts, CommentBefore(t.Text))
	}
	parts = append(parts, Text("fn "))
	for i, p := range params {
		parts = append(parts, emitLambdaParam(p))
		if i == len(params)-1 {
			break
		}
		parts = append(parts, Text(","))
		for _, t := range p.Comments.Trailing {
			parts = append(parts, LineSuffix(t.Text))
		}
		if len(p.Comments.Trailing) > 0 {
			parts = append(parts, Nest(defaultIndent, HardLine()))
		} else {
			parts = append(parts, Text(" "))
		}
	}
	parts = append(parts, Text(" ->"))
	return Concat(parts...)
}

// emitLambdaParam renders a single lambda param. Supports bare name, name
// with default (`acc = 0`), name with type annotation (`x: Int`),
// destructuring patterns (tuple/struct/map), and destructuring patterns
// with defaults (`(a, b) = (0, 1)`). Type annotations must round-trip
// because the checker rejects unannotated local-lambda bindings; a redundant
// self-typing one is already gone (stripSelfTypingAnnotations).
func emitLambdaParam(p ast.Param) Doc {
	var parts []Doc
	switch {
	case p.TypeOnly:
		// `fn Request -> ...`: the type alone.
		parts = []Doc{emitTypeExpr(p.TypeAnnotation)}
	case p.Destructure != nil:
		parts = []Doc{emitPattern(p.Destructure)}
	default:
		parts = []Doc{Text(p.Name)}
	}
	if p.TypeAnnotation != nil && !p.TypeOnly {
		parts = append(parts, Text(": "), emitTypeExpr(p.TypeAnnotation))
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
	switch n.(type) {
	case *ast.IdentPattern, *ast.WildcardPattern, *ast.TuplePattern, *ast.StructPattern,
		*ast.EnumPattern, *ast.ListPattern, *ast.MapPattern, *ast.AsPattern, *ast.OrPattern:
		return withDangling(n, emitPatternNode(n))
	}
	return emitPatternNode(n)
}

// emitOrPattern writes `A or B or C` on one line when it fits, and
// otherwise one alternative per line, each `or` ending the line before the
// next alternative (the lexer reads a line ending in `or` as continuing),
// the later alternatives indented one level. An alternative that is an `as`
// pattern is parenthesized, since `as` binds looser than `or`. A comment
// written after an `or` stays after it, which breaks the pattern.
func emitOrPattern(v *ast.OrPattern) Doc {
	comments := func(alt ast.Node) []ast.Trivia {
		var out []ast.Trivia
		if ht, ok := alt.(ast.HasTrivia); ok {
			for _, t := range ht.GetLeading() {
				if t.Kind == ast.TriviaComment {
					out = append(out, t)
				}
			}
		}
		return out
	}
	broken := false
	for _, alt := range v.Alternatives[1:] {
		broken = broken || len(comments(alt)) > 0
	}
	docs := make([]Doc, len(v.Alternatives))
	for i, alt := range v.Alternatives {
		docs[i] = emitPattern(alt)
		if _, isAs := alt.(*ast.AsPattern); isAs {
			docs[i] = Concat(Text("("), docs[i], Text(")"))
		}
	}
	// Written one alternative per line and holding an alternative broken
	// whatever the width (a pattern kept broken after its bracket), the
	// alternatives stay one per line, as keepsBrokenLines says for a list:
	// otherwise alternatives broken for width would hug that pattern on
	// the next format.
	if len(v.Alternatives) > 1 {
		broken = broken || keepsBrokenLines(nodeEndLine(v.Alternatives[0]), nodeStartLine(v.Alternatives[1]), docs)
	}
	parts := make([]Doc, 0, len(v.Alternatives))
	for i, alt := range v.Alternatives {
		doc := docs[i]
		if i == 0 {
			parts = append(parts, doc)
			continue
		}
		step := []Doc{Text(" or")}
		for _, t := range comments(alt) {
			step = append(step, LineSuffix(t.Text))
		}
		if broken {
			step = append(step, HardLine(), doc)
		} else {
			step = append(step, Line(), doc)
		}
		parts = append(parts, Nest(defaultIndent, Concat(step...)))
	}
	return Group(Concat(parts...))
}

func emitPatternNode(n ast.Node) Doc {
	switch v := n.(type) {
	case *ast.IdentPattern:
		return Text(v.Name)
	case *ast.WildcardPattern:
		return Text("_")
	case *ast.AsPattern:
		// `as` binds looser than every other pattern construct and every
		// pattern position is delimited, so the inner pattern never needs
		// parentheses. When the inner pattern breaks, the name follows its
		// closing bracket.
		return Concat(emitGroupedPattern(v.Pattern), Text(" as "+v.Name))
	case *ast.OrPattern:
		return emitOrPattern(v)
	case *ast.TuplePattern:
		open := "("
		elems, cs, ok := patternItems(v.Patterns)
		if ok {
			return emitCommentedListNoLastComma(open, ")", elems, cs)
		}
		parts := make([]Doc, 0, len(v.Patterns)*2+1)
		parts = append(parts, Text(open))
		for i, e := range elems {
			if i > 0 {
				parts = append(parts, Text(", "))
			}
			parts = append(parts, e)
		}
		parts = append(parts, Text(")"))
		return Concat(parts...)
	case *ast.StructPattern:
		open := structOpen(v.TypeName)
		elems := make([]Doc, 0, len(v.Fields))
		for _, f := range v.Fields {
			switch {
			case patternFieldIsPun(f):
				// `name: (name)`: the parser drops the parentheses and
				// keeps the name as a pattern, which `name: name` would
				// read back as a binding, so pun it now.
				elems = append(elems, Text(f.Name+":"))
			case f.Pattern != nil:
				// Value match: `name: <pattern>`.
				elems = append(elems, Concat(Text(f.Name), Text(": "), emitGroupedPattern(f.Pattern)))
			case f.Binding != "" && f.Binding != f.Name:
				// Rename: `name: binding`.
				elems = append(elems, Concat(Text(f.Name), Text(": "), Text(f.Binding)))
			default:
				// Field punning: the name and its colon, `name:`.
				elems = append(elems, Text(f.Name+":"))
			}
		}
		first := 0
		if len(v.Fields) > 0 {
			first = v.Fields[0].NameLine
		}
		// A comment forces the broken form, and so do depth and a line
		// break after the `{` (brokenAfterOpen).
		if cs := structPatternComments(v.Fields); anyComments(cs) || tooDeep(v, len(v.Fields)) || brokenAfterOpen(v.Line, first) {
			return emitCommentedList(open, "}", elems, cs, nil)
		}
		return emitBracedList(open, "}", elems)
	case *ast.EnumPattern:
		name := Text(v.Variant.TypeString())
		switch {
		case v.Binding != "":
			// Fast-path: Variant(binding).
			return Concat(name, Text("("), Text(v.Binding), Text(")"))
		case v.Payload != nil:
			// The flat payload of a tuple-payload variant, `.Pos(a, b)`,
			// keeps its comma list inside the variant's parentheses.
			if tp, ok := v.Payload.(*ast.TuplePattern); ok && tp.Flat {
				elems, cs, ok := patternItems(tp.Patterns)
				if ok {
					return Concat(name, emitCommentedListNoLastComma("(", ")", elems, cs))
				}
				parts := make([]Doc, 0, len(tp.Patterns)*2+3)
				parts = append(parts, name, Text("("))
				for i, e := range elems {
					if i > 0 {
						parts = append(parts, Text(", "))
					}
					parts = append(parts, e)
				}
				parts = append(parts, Text(")"))
				return Concat(parts...)
			}
			// Variant(<nested pattern>): the payload is a single pattern.
			// A distinct type over a tuple is taken apart by the tuple
			// pattern that names it, `Pair(a, b)`.
			elems, cs, ok := patternItems([]ast.Node{v.Payload})
			if ok {
				return Concat(name, emitCommentedListNoLastComma("(", ")", elems, cs))
			}
			return Concat(name, Text("("), elems[0], Text(")"))
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
		hasEndTrivia := trailingHasComment(v.EndTrivia)
		// Spread patterns (`[a, b, ..rest]`) skip the
		// emitBracedList/forceBrokenBraced path: the trailing `..rest`
		// piece is not a comma-separated entry, and the existing
		// formatter convention (mirroring ListSpreadLit) is to emit
		// spread patterns flat regardless.
		if len(v.Tails) > 0 {
			// `[first, ..mid, last]` is written as a vector pattern is.
			rest := v.TailSpread
			if v.BareRest {
				rest = nil
			}
			return emitSeqPattern(v, open, v.Heads, true, rest, v.Tails, v.EndTrivia)
		}
		if v.TailSpread != nil {
			// A comment is the exception, which no flat form can hold.
			items := append(append([]ast.Node{}, v.Heads...), v.TailSpread)
			// A bare rest (`[first, ..]`) is written as the `..` alone.
			rest := func(d Doc) Doc {
				if v.BareRest {
					return Text("..")
				}
				return Concat(Text(".."), d)
			}
			if elems, cs, ok := patternItems(items); ok {
				elems[len(elems)-1] = rest(elems[len(elems)-1])
				return emitCommentedListNoLastComma(open, "]", elems, cs)
			}
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
			parts = append(parts, rest(emitPattern(v.TailSpread)), Text("]"))
			return Concat(parts...)
		}
		elems, cs, ok := patternItems(v.Heads)
		// A comment forces the broken form, and so do depth and a line
		// break after the `[` (brokenAfterOpen).
		if hasEndTrivia || ok || tooDeep(v, len(v.Heads)) || itemsBrokenAfterOpen(v.Line, v.Heads) {
			return emitCommentedList(open, "]", elems, cs, v.EndTrivia)
		}
		return emitBracedList(open, "]", elems)
	case *ast.MapPattern:
		open := shapeOpen("", v.TypeName, "{")
		hasEndTrivia := trailingHasComment(v.EndTrivia)
		elems := make([]Doc, 0, len(v.Entries))
		for _, e := range v.Entries {
			elems = append(elems, Concat(emit(e.Key), Text(" => "), emitGroupedPattern(e.Pattern)))
		}
		// A comment forces the broken form, and so do depth and a line
		// break after the `{` (brokenAfterOpen).
		keys := mapPatternKeys(v.Entries)
		if hasEndTrivia || nodesHaveComments(keys) || tooDeep(v, len(v.Entries)) || itemsBrokenAfterOpen(v.Line, keys) {
			return forceBrokenItems(open, "}", elems, keys, v.EndTrivia)
		}
		return emitBracedList(open, "}", elems)
	}
	// A string prefix pattern, `"/users/" + id`: the parser builds a Binary
	// whose right operand is the bound name's IdentPattern.
	if b, ok := n.(*ast.Binary); ok {
		if name, ok := b.Right.(*ast.IdentPattern); ok {
			return Concat(emitPattern(b.Left), Text(" "+b.Op+" "), Text(name.Name))
		}
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
//	+ - ||| ^^^           = 8
//	* / % &&& <<< >>>     = 9
//	(unary)               = 10
func precedence(op string) int {
	switch op {
	case "|>":
		return 6
	case "or":
		return 2
	case "and":
		return 3
	case "==", "!=", "in":
		return 4
	case "<", ">", "<=", ">=":
		return 5
	case "+", "-", "|||", "^^^":
		return 8
	case "*", "/", "%", "&&&", "<<<", ">>>":
		return 9
	}
	// Unknown operator — treat as tight-binding; callers add parens
	// defensively when the child has strictly-lower precedence.
	return 100
}

// unaryPrecedence is the binding level of unary `-` and `~~~` (parser's
// prefixPrecedence). It's greater than any binary precedence, so any
// binary child of a unary needs parenthesizing.
const unaryPrecedence = 10

// notOperandPrecedence is the loosest operator `not`'s operand holds bare
// (the parser's notOperandPrecedence): `not a == b` negates the comparison,
// and an `and` or `or` under a `not` keeps its parentheses.
const notOperandPrecedence = 4

// isPrecedence is the binding level of `is` (the parser's, `==`'s). It
// does not chain with a comparison, so its value holds only what binds more
// tightly than a comparison bare: `xs |> List.head() is Some(_)`, `(a < b)
// is True`.
const isPrecedence = 4

// ascriptionPrecedence is the binding level of `as` (the parser's, `|>`'s).
const ascriptionPrecedence = 6

// emitAscription writes `value as Type`. The value holds what binds at
// least as tightly as `|>` bare, so `xs |> Iter.to_list() as List<Int>`
// keeps its shape, and a looser operator keeps its parentheses.
func emitAscription(v *ast.Ascription) Doc {
	return Concat(emitOperand(v.Value, ascriptionPrecedence, followsOther), Text(" as "), emitTypeExpr(v.Type))
}

// emitIs writes `value is Pattern` with one space on each side of `is`. A
// pattern that is an `or` (or names one with `as`) keeps its parentheses,
// since a bare `or` after the pattern is the Boolean `or`.
func emitIs(v *ast.Is) Doc {
	value := emitOperand(v.Value, precedence("<")+1, followsOther)
	if u, ok := v.Value.(*ast.Unary); ok && u.Op == "not" {
		value = Concat(Text("("), emit(v.Value), Text(")"))
	}
	pattern := emitPattern(v.Pattern)
	if isOrPattern(v.Pattern) {
		pattern = Concat(Text("("), pattern, Text(")"))
	}
	return Concat(value, Text(" is "), pattern)
}

// isOrPattern reports whether n is an `or` pattern, possibly named with
// `as`.
func isOrPattern(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.OrPattern:
		return true
	case *ast.AsPattern:
		return isOrPattern(v.Pattern)
	}
	return false
}

// follower is the token written straight after an operand whose
// parentheses the formatter may drop, when that token can join a bare
// name in front of it into something else (emitOperand).
type follower int

const (
	followsOther follower = iota
	// followsLess: a `<`, which makes a name a call with type arguments.
	followsLess
)

// emitBinary writes a binary operation other than a pipe. next is what
// follows it (emitOperand).
func emitBinary(v *ast.Binary, next follower) Doc {
	head, parts, breakable := binaryChain(v, next)
	// A comment after an operand, before the operator on the next line,
	// stays there: the operator then starts the next line whatever the
	// layout.
	comments := func(part binaryPart) Doc {
		var ds []Doc
		for _, c := range part.comments {
			ds = append(ds, Hidden(Concat(Text(" "), Text(c))))
		}
		return Concat(ds...)
	}
	inline := head
	for _, part := range parts {
		if len(part.comments) > 0 {
			inline = Concat(inline, comments(part), Nest(defaultIndent, Concat(HardLine(), Text(part.op), Text(" "), part.right)))
			continue
		}
		inline = Concat(inline, Text(" "), Text(part.op), Text(" "), part.right)
	}
	if !breakable {
		return inline
	}
	// A comparison or boolean operator may start a line (no expression
	// starts with one), so an operation too long for its line breaks
	// before the operator, the right operand one indent in, before it
	// breaks inside either operand, and a chain of `and`s (or `or`s) breaks
	// before each of them. That holds only when the first operand fits on
	// its line: one that breaks anyway (`}) == 3`, a call broken after its
	// `(`) keeps the operator after its last line. An `and` or `or` chain
	// breaks before each operator even when an operand spans lines (a
	// lambda with a block body).
	var rest Doc = Nil()
	for _, part := range parts {
		var sep Doc = Line()
		if len(part.comments) > 0 {
			sep = HardLine()
		}
		rest = Concat(rest, comments(part), sep, Text(part.op), Text(" "), part.right)
	}
	broken := Group(Concat(head, Nest(defaultIndent, rest)))
	single := !(v.Op == "and" || v.Op == "or")
	return WithColumn(func(col, indent, width int) Doc {
		// A first operand that keeps a line break whatever the layout (a
		// literal written across lines, which fits counts as one line)
		// breaks anyway, so it keeps the operator too.
		if !fits(head, nil, width-col) || containsHardLine(head) {
			return inline
		}
		// A comparison whose right operand spans lines on a line of its
		// own too (a triple-quoted string, a long literal) keeps the
		// operator after the first operand, and the right operand breaks
		// inside its brackets.
		last := parts[len(parts)-1]
		lastFits := fits(last.right, nil, width-indent-defaultIndent-len(last.op)-1)
		if single && !lastFits {
			return inline
		}
		// An `and` or `or` chain whose last operand ends in a bracketed
		// literal that breaks even on a line of its own stays on its line,
		// as far as the literal's opening bracket, when that fits, and the
		// literal breaks inside its brackets. Broken before the operator
		// instead, the literal would keep its line break after the bracket
		// (brokenAfterOpen), and a second format would join the chain.
		if !single && !lastFits && endsInBrokenLiteral(v.Right) && !anyPartComments(parts) &&
			fits(Concat(head, chainPrefix(parts[:len(parts)-1])), nil, width-col) && fits(Nil(), inline, width-col) {
			return inline
		}
		return broken
	})
}

// endsInBrokenLiteral reports whether n's right edge is a bracketed literal
// or pattern that keeps a line break after its opening bracket
// (brokenAfterOpen): the operand of a `not`, the right operand of an
// operator other than `|>`, or the pattern of an `is`.
func endsInBrokenLiteral(n ast.Node) bool {
	for {
		if hasDangling(n) || itemComments(n) != nil {
			return false
		}
		switch v := n.(type) {
		case *ast.Unary:
			n = v.Right
		case *ast.Binary:
			if v.Op == "|>" {
				return false
			}
			n = v.Right
		case *ast.Is:
			n = v.Pattern
		case *ast.StructLit, *ast.ListLit, *ast.MapLit, *ast.StructPattern:
			return true
		default:
			return false
		}
	}
}

// chainPrefix writes parts as emitBinary's flat layout does, each operator
// and right operand after a space.
func chainPrefix(parts []binaryPart) Doc {
	var d Doc = Nil()
	for _, part := range parts {
		d = Concat(d, Text(" "), Text(part.op), Text(" "), part.right)
	}
	return d
}

// anyPartComments reports whether a comment is written before any of
// parts' operators.
func anyPartComments(parts []binaryPart) bool {
	for _, part := range parts {
		if len(part.comments) > 0 {
			return true
		}
	}
	return false
}

// trailingBeforeOperator is the text of each line comment written after
// operand n, before the operator that follows it on the next line: the
// trailing trivia of n and of the operands along its right edge. A block or
// a case writes its trailing trivia inside its braces itself
// (keepsEndTriviaAsTrailing).
func trailingBeforeOperator(n ast.Node) []string {
	var out []string
	for n != nil {
		if ht, ok := n.(ast.HasTrivia); ok && !keepsEndTriviaAsTrailing(n) {
			for _, t := range ht.GetTrailing() {
				if t.Kind == ast.TriviaComment {
					out = append(out, t.Text)
				}
			}
		}
		switch v := n.(type) {
		case *ast.Binary:
			n = v.Right
		case *ast.Unary:
			n = v.Right
		default:
			n = nil
		}
	}
	return out
}

// binaryPart is an operator and its right operand, as written after the
// first operand of a chain.
type binaryPart struct {
	op    string
	right Doc
	// comments are the line comments written before op, after the operand
	// before it.
	comments []string
}

// binaryChain writes v as its first operand and the operators and right
// operands after it: a left operand that is the same `and` or `or` (`a and b
// and c`, nested to the left) joins the chain. breakable reports whether the
// chain may break before its operators: each is a comparison, `and` or `or`
// (continuesLine), no right operand is a bracketed literal, which breaks
// inside its brackets instead (`== [`), the first operand is no pipeline,
// whose last stage the operator follows, and a comparison has no right
// operand that spans lines.
func binaryChain(v *ast.Binary, next follower) (Doc, []binaryPart, bool) {
	p := precedence(v.Op)
	boolean := v.Op == "and" || v.Op == "or"
	// Left child: all Nomi binary operators are left-associative (parser
	// uses prec+1 for the right operand), so a left child at the same
	// precedence level needs no parens.
	leftNext := followsOther
	if v.Op == "<" {
		leftNext = followsLess
	}
	var head Doc
	var parts []binaryPart
	breakable := true
	if v.Op == "in" {
		// `in` does not chain with a comparison, so each operand holds
		// only what binds more tightly than one bare: `x in xs |>
		// Iter.to_set()`, `(a < b) in flags`.
		head = emitOperand(v.Left, precedence("<")+1, leftNext)
		right := emitOperand(v.Right, precedence("<")+1, next)
		return head, []binaryPart{{op: v.Op, right: right}}, false
	}
	if lb, ok := v.Left.(*ast.Binary); ok && boolean && lb.Op == v.Op && !hasDangling(lb) {
		head, parts, breakable = binaryChain(lb, leftNext)
	} else {
		head = emitOperand(v.Left, p, leftNext)
		breakable = !isPipeExpr(v.Left)
	}
	// Right child: for left-associative operators, a right child at the
	// same precedence level DOES need parens to preserve the tree shape
	// (e.g., `1 - (2 - 3)`).
	var right Doc
	if b, ok := v.Right.(*ast.Binary); ok && b.Op != "|>" && precedence(b.Op) <= p {
		right = Concat(Text("("), emit(v.Right), Text(")"))
	} else {
		right = emitOperand(v.Right, p, next)
	}
	breakable = breakable && continuesLine(v.Op) && !opensBracket(v.Right)
	var comments []string
	if continuesLine(v.Op) {
		// Only these operators can follow a line break, and so a comment.
		comments = trailingBeforeOperator(v.Left)
	}
	return head, append(parts, binaryPart{op: v.Op, right: right, comments: comments}), breakable
}

// hasDangling reports whether n carries comments of its own, which
// withDangling writes around it.
func hasDangling(n ast.Node) bool {
	h, ok := n.(ast.HasDangling)
	return ok && (len(h.GetDanglingBefore()) > 0 || len(h.GetDanglingAfter()) > 0)
}

// opensBracket reports whether n is a literal written in brackets or braces
// (a bracket, map, tuple or struct literal), which breaks inside
// them when it is too long.
func opensBracket(n ast.Node) bool {
	switch n.(type) {
	case *ast.ListLit, *ast.MapLit, *ast.TupleLit, *ast.StructLit:
		return true
	}
	return false
}

// continuesLine reports whether op may start a continuation line: a
// comparison or boolean operator, which no expression starts with.
func continuesLine(op string) bool {
	switch op {
	case "==", "!=", "<", "<=", ">", ">=", "and", "or":
		return true
	}
	return false
}

// emitUnary writes a unary operation. `-` and `~~~` bind tighter than any
// binary, so any binary operand is parenthesized; `not`, a keyword, is
// followed by a space and holds everything down to `==` bare.
func emitUnary(v *ast.Unary, next follower) Doc {
	if v.Op == "not" {
		return Concat(Text("not "), emitOperand(v.Right, notOperandPrecedence, next))
	}
	return Concat(Text(v.Op), emitOperand(v.Right, unaryPrecedence, next))
}

// emitOperand writes child as an operand of an operator that binds at
// minPrec, in parentheses when its own operator binds looser.
//
// next is what follows the operand, and some parentheses must stay because
// of it. The name they hold may end a longer operand, `-(f)` or `a + (f)`.
//
//   - followsLess: the parser reads a name, a field path or a type name
//     followed by `<T>(` as a call with type arguments, so dropping the
//     parentheses from `(f) < Dog > (x)`, two comparisons, writes the call
//     `f<Dog>(x)`.
func emitOperand(child ast.Node, minPrec int, next follower) Doc {
	if ifn, ok := child.(*ast.If); ok {
		// An inline `if`'s `else` branch extends as far right as it can, so
		// as an operand it goes in parentheses.
		return emitIfParens(ifn, true)
	}
	if b, ok := child.(*ast.Binary); ok && precedence(b.Op) < minPrec {
		return Concat(Text("("), emit(child), Text(")"))
	}
	if _, ok := child.(*ast.Is); ok && minPrec > isPrecedence {
		return Concat(Text("("), emit(child), Text(")"))
	}
	if _, ok := child.(*ast.Ascription); ok && minPrec > ascriptionPrecedence {
		return Concat(Text("("), emit(child), Text(")"))
	}
	if next == followsOther {
		return emit(child)
	}
	switch c := child.(type) {
	case *ast.GroupedExpr:
		if groupedHasComments(c) {
			return emit(child)
		}
		inner := ast.Node(c)
		for g, ok := inner.(*ast.GroupedExpr); ok && g.Expr != nil; g, ok = inner.(*ast.GroupedExpr) {
			inner = g.Expr
		}
		switch inner.(type) {
		case *ast.Ident, *ast.FieldAccess, *ast.TypeIdent:
			return withDangling(c, Concat(Text("("), emit(inner), Text(")")))
		}
	case *ast.Binary:
		if c.Op != "|>" {
			return withDangling(c, emitBinary(c, next))
		}
		// `x |> f()?` as an operand keeps its parentheses, as any pipe
		// does.
		if try, ok := c.Right.(*ast.TryOp); ok && try.Expr == nil {
			return Concat(Text("("), emit(child), Text(")"))
		}
	case *ast.Unary:
		return withDangling(c, emitUnary(c, next))
	}
	return emit(child)
}

// emitCapture writes a capture as `&` against its body. A parenthesized
// body keeps its parentheses, whatever it holds: `&(f(&1))` is a function
// of its body, where `&f(&1)` is a partial application, which takes the
// callee's parameter names and defaults.
func emitCapture(v *ast.Capture) Doc {
	return Concat(Text("&"), captureBody(v.Body))
}

// captureBody writes a capture's body, keeping the parentheses of a body
// that is a parenthesized expression, possibly under postfix `!` or `?`
// marks (`&("")!`): without them the body
// would not start with `(`, a call or a literal, which is what a capture's
// `&` is written against.
func captureBody(n ast.Node) Doc {
	switch v := n.(type) {
	case *ast.GroupedExpr:
		if v.Expr != nil {
			return withDangling(v, captureParens(v))
		}
	case *ast.Unwrap:
		if groupedHead(v.Expr) {
			return Concat(captureBody(v.Expr), Text("!"))
		}
	case *ast.TryOp:
		if v.Expr != nil && groupedHead(v.Expr) {
			return Concat(captureBody(v.Expr), Text("?"))
		}
	}
	return emit(n)
}

// groupedHead reports whether n is a parenthesized expression, possibly
// under postfix `!` and `?` marks.
func groupedHead(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.GroupedExpr:
		return v.Expr != nil
	case *ast.Unwrap:
		return groupedHead(v.Expr)
	case *ast.TryOp:
		return v.Expr != nil && groupedHead(v.Expr)
	}
	return false
}

// captureParens writes a capture's parenthesized body, keeping the
// parentheses emitGroupedExpr would drop. A comment inside them puts the
// expression on its own line, and doubled parentheses (`&((x))`) write one
// pair, their comments kept.
func captureParens(g *ast.GroupedExpr) Doc {
	if c := itemComments(g.Expr); c != nil {
		return emitCommentedListNoLastComma("(", ")", []Doc{emit(g.Expr)}, []*ast.ItemComments{c})
	}
	if inner, ok := g.Expr.(*ast.GroupedExpr); ok && inner.Expr != nil {
		return withDangling(inner, captureParens(inner))
	}
	return Concat(Text("("), emit(g.Expr), Text(")"))
}

// stripDroppedParens is n without the parentheses emitGroupedExpr drops
// around it.
func stripDroppedParens(n ast.Node) ast.Node {
	for {
		g, ok := n.(*ast.GroupedExpr)
		if !ok || g.Expr == nil || itemComments(g.Expr) != nil {
			return n
		}
		if _, nested := g.Expr.(*ast.GroupedExpr); !nested && !groupedExprCanDropParens(g.Expr) {
			return n
		}
		n = g.Expr
	}
}

// tryOperandPrecedence is the binding level of `?`'s operand (the
// parser's tryOperandPrecedence).
const tryOperandPrecedence = 10

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

	parts = append(parts, emitFuncDefHeader(v), emitBlockForceBroken(v.Body))
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
//	[pub |export ]fn name[<T, ...>](params)[: ReturnType][ where T: Bound]
func emitFuncDefHeader(v *ast.FuncDef) Doc {
	parts := make([]Doc, 0, 6)
	if v.Export {
		parts = append(parts, Text("export "))
	}
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
		docs := make([]Doc, len(v.TypeParams))
		for i, p := range v.TypeParams {
			d := []Doc{Text(p.Name)}
			for j, b := range p.Bounds {
				if j == 0 {
					d = append(d, Text(": "))
				} else {
					d = append(d, Text(" and "))
				}
				d = append(d, emitTypeExpr(b))
			}
			docs[i] = Concat(d...)
		}
		if cs := typeParamsComments(v.TypeParams); anyComments(cs) {
			parts = append(parts, emitCommentedListNoLastComma("<", ">", docs, cs))
		} else {
			parts = append(parts, Text("<"), Join(Text(", "), docs...), Text(">"))
		}
	}

	paramDocs := make([]Doc, 0, len(v.Params))
	for _, p := range v.Params {
		paramDocs = append(paramDocs, emitFuncDefParam(p))
	}
	parts = append(parts, emitParamList(v.Line, paramDocs, v.Params, v.ParamsEndTrivia))

	if v.ReturnTypeExpr != nil {
		parts = append(parts, Text(" -> "), emitTypeExpr(v.ReturnTypeExpr))
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
	switch {
	case p.TypeOnly:
		// `fn stats(Request)`: the type alone.
		head = emitTypeExpr(p.TypeAnnotation)
	case p.Destructure != nil:
		head = emitPattern(p.Destructure)
	default:
		head = Text(p.Name)
	}
	parts := []Doc{head}
	// A redundant self-typing annotation is already gone
	// (stripSelfTypingAnnotations).
	if p.TypeAnnotation != nil && !p.TypeOnly {
		parts = append(parts, Text(": "), emitTypeExpr(p.TypeAnnotation))
	}
	if p.Default != nil {
		parts = append(parts, Text(" = "), emit(p.Default))
	}
	return Concat(parts...)
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
//
// The parser reads a grouping `(T)` as T, so a comment written inside those
// parentheses is T's item comments. Outside a list, which writes its items'
// comments itself (emitTypeItem), the parentheses stay, T on its own line
// with its comments where they were.
func emitTypeExpr(t ast.TypeExpr) Doc {
	if t == nil {
		return Nil()
	}
	if c := itemComments(t); c != nil {
		return emitCommentedListNoLastComma("(", ")", []Doc{emitTypeItem(t)}, []*ast.ItemComments{c})
	}
	return emitTypeItem(t)
}

// emitTypeItem renders t as an element of a list that writes t's item
// comments around it.
func emitTypeItem(t ast.TypeExpr) Doc {
	if t == nil {
		return Nil()
	}
	return withDangling(t, emitTypeExprNode(t))
}

func emitTypeExprNode(t ast.TypeExpr) Doc {
	switch n := t.(type) {
	case *ast.SimpleType:
		return Text(n.Name)
	case *ast.SelfType:
		return Text("self")
	case *ast.QualifiedType:
		return Concat(Text(n.Module), Text("."), emitTypeExpr(n.Member))
	case *ast.DotVariantType:
		// Dot-leading variant shorthand used in TypeName slots of
		// literal-attach forms (`.Obj{"k" => v}`, `.Arr[...]`, `.Rect{w:, h:}`)
		// and patterns. Same round-trip-as-written semantics as DotVariant.
		return Concat(Text("."), Text(n.Name))
	case *ast.GenericType:
		if cs, ok := typesComments(n.Params); ok {
			docs := make([]Doc, len(n.Params))
			for i, p := range n.Params {
				docs[i] = emitTypeItem(p)
			}
			return Concat(Text(n.Name), emitCommentedListNoLastComma("<", ">", docs, cs))
		}
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
			paramDocs = append(paramDocs, emitTypeItem(p))
		}
		if cs, ok := typesComments(n.Params); ok {
			if n.Return == nil {
				return emitCommentedListNoLastComma("(", ")", paramDocs, cs)
			}
			list := emitCommentedListNoLastComma("(", ")", paramDocs, cs)
			return Concat(list, Text(" -> "), emitTypeExpr(n.Return))
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
	return emitBracedStructFieldsWithEndTrivia("{", t.Fields, t.EndTrivia)
}

// emitBracedStructFieldsWithEndTrivia renders a braced list of struct
// fields after open, `{` for an anonymous struct type and `{` for a
// struct-shaped enum variant. Width-based, with a preserve-user-multi-line escape:
//
//	flat:                .{a: Int, b: String}
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
// Used by both anonymous struct type expressions (`fn f(p: {a: Int}) -> R`)
// and struct-shaped enum variant payloads (`HttpError {status: Int, ...}`)
// so the brace-bounded-fields layout stays identical wherever it appears.
//
// endTrivia is rendered between the last field and the closing `}`.
// Captured by parseAnonStructType / parseEnumVariant (struct shape) when
// a comment / blank line sits before the closing brace. EndTrivia or any
// inter-field LeadingComments forces the broken form (a `// comment`
// cannot share a line with another field or with `}`).
func emitBracedStructFieldsWithEndTrivia(open string, fields []ast.StructField, endTrivia []ast.Trivia) Doc {
	if len(fields) == 0 {
		if len(endTrivia) == 0 {
			return Text(open + "}")
		}
		return forceBrokenBracedWithEndTrivia(open, "}", nil, endTrivia)
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
		return forceBrokenBracedWithTrailing(open, "}", elems, trailing, endTrivia)
	}
	body := []Doc{LineOrEmpty(), elems[0]}
	for _, e := range elems[1:] {
		body = append(body, Text(","), Line(), e)
	}
	return Group(Concat(
		Text(open),
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
//
// The end trivia's blank lines are dropped: every element is written with
// a comma, and the lexer emits no blank-line token after a comma or after a
// comment that follows one, so a blank line kept here would be gone on the
// next format.
func forceBrokenBracedWithTrailing(open, close string, elems []Doc, trailing [][]ast.Trivia, endTrivia []ast.Trivia) Doc {
	body := make([]Doc, 0, len(elems)*4+1)
	for i, e := range elems {
		body = append(body, HardLine(), e, Text(","))
		if i < len(trailing) {
			body = append(body, emitSameLineComments(trailing[i])...)
		}
	}
	if endTrivia = withoutBlankTrivia(endTrivia); len(endTrivia) > 0 {
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
	if cs := typeParamsComments(tp); anyComments(cs) {
		docs := make([]Doc, len(tp))
		for i, p := range tp {
			docs[i] = emitTypeParam(p)
		}
		return emitCommentedListNoLastComma("<", ">", docs, cs)
	}
	parts := make([]Doc, 0, len(tp)*2+1)
	parts = append(parts, Text("<"))
	for i, p := range tp {
		if i > 0 {
			parts = append(parts, Text(", "))
		}
		parts = append(parts, emitTypeParam(p))
	}
	parts = append(parts, Text(">"))
	return Concat(parts...)
}

// emitTypeParam is one type parameter, `P`, or `P = Unit` with a default.
func emitTypeParam(p ast.TypeParam) Doc {
	if p.Default == nil {
		return Text(p.Name)
	}
	return Concat(Text(p.Name), Text(" = "), emitTypeExpr(p.Default))
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
	// The fields are comma-separated, and the lexer emits no blank line
	// after a comma, so a blank line written before a field would be gone
	// the next time: none is written.
	parts = append(parts, emitLeadingComments(withoutBlankTrivia(f.LeadingComments))...)
	parts = append(parts, emitDocComment(f.Doc)...)
	parts = append(parts, emitFieldHead(f)...)
	if f.Default != nil {
		parts = append(parts, Text(" = "), emit(f.Default))
	}
	return Concat(parts...)
}

// emitFieldHead is a field's name and its type, `name: Type`, or its name
// alone for a field whose type comes from its default (`misses = 0`).
func emitFieldHead(f ast.StructField) []Doc {
	if f.TypeAnnotation == nil {
		return []Doc{Text(f.Name)}
	}
	return []Doc{Text(f.Name), Text(": "), emitTypeExpr(f.TypeAnnotation)}
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
// canonical form: `name: Type[ = default]` or `name = default` — one per line, no commas.
// Leading `//` trivia and the `///` doc comment stack above the item (doc
// closest to the field). The comma-separated form stays in emitStructField
// for anon-struct types and struct-shaped enum variant payloads.
func emitStructDeclField(f ast.StructField) Doc {
	parts := []Doc{}
	parts = append(parts, emitLeadingComments(f.LeadingComments)...)
	parts = append(parts, emitDocComment(f.Doc)...)
	parts = append(parts, emitFieldHead(f)...)
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
	if textWidth(flat) <= conformanceWrapWidth {
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
	widths := make([]int, len(entries))
	for i, entry := range entries {
		widths[i] = textWidth(entry)
	}
	var lines [][]string
	i := 0
	for _, line := range packGreedy(widths, textWidth(keyword)+1, 0, conformanceWrapWidth, 0) {
		lines = append(lines, entries[i:i+len(line)])
		i += len(line)
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
//	  fn method(s: Name) -> T { ... }
//	}
//
// The body is always multi-line when non-empty. A single space
// separates the type name (or type-params) from the opening `{`. `pub`
// precedes `opaque` when both are present; `opaque` is emitted before
// `struct` whenever the AST carries the flag (see spec §4, *Opaque distinct types*).
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

	members := make([]Doc, 0, len(v.Fields)+len(v.Items))
	for _, f := range v.Fields {
		members = append(members, emitStructDeclField(f))
	}
	members = appendTypeBodyItems(members, v.Items)
	parts = append(parts, emitDeclBody(members, v.EndTrivia))
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
		// the variant itself adds none. The parser reads `Name(T)` as
		// `Name T`; a comment inside those parentheses keeps them
		// (emitTypeExpr), written `Name (` as a tuple payload is.
		return Concat(Text(v.Name), Text(" "), emitTypeExpr(v.DataTypeExpr))
	case "struct":
		return Concat(Text(v.Name), Text(" "), emitBracedStructFieldsWithEndTrivia("{", v.Fields, v.EndTrivia))
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
// §4, *Opaque distinct types*).
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

	members := make([]Doc, 0, len(v.Variants)+len(v.Items))
	for _, va := range v.Variants {
		members = append(members, emitEnumDeclVariant(va))
	}
	members = appendTypeBodyItems(members, v.Items)
	parts = append(parts, emitDeclBody(members, v.EndTrivia))
	return Concat(parts...)
}

// emitInterfaceMethodSig renders an interface method signature (no body):
//
//	fn name(p1: T1, p2: T2) -> Ret
//
// Default methods (Body != nil) tagged with the `open` modifier render
// as `open fn name(...)`. The contextual `open` keyword only makes
// sense on a default method — the parser rejects `open` on bodyless
// methods — so emitting `open` here without checking Body is safe in
// practice; the AST flag is only ever set on default methods.
func emitInterfaceMethodSig(m ast.InterfaceMethod) Doc {
	paramDocs := make([]Doc, 0, len(m.Params))
	for _, p := range m.Params {
		// The parameter list is a function's, destructuring patterns
		// included: writing the name the parser gave a pattern, with no
		// type, does not parse.
		paramDocs = append(paramDocs, emitFuncDefParam(p))
	}
	parts := []Doc{}
	if m.Open {
		parts = append(parts, Text("open "))
	}
	if m.Extern {
		parts = append(parts, Text("host "))
	}
	parts = append(parts, Text("fn "), Text(m.Name), emitTypeParams(m.TypeParams), emitParamList(m.GetSpan().StartLine, paramDocs, m.Params, m.ParamsEndTrivia))
	if m.ReturnTypeExpr != nil {
		parts = append(parts, Text(" -> "), emitTypeExpr(m.ReturnTypeExpr))
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
		parts = append(parts, sig, emitBlockForceBroken(blk))
		return Concat(append(parts, trailing...)...)
	}
	parts = append(parts, sig, Text(" "), emit(m.Body))
	return Concat(append(parts, trailing...)...)
}

// emitInterfaceDef renders an interface declaration. Its methods are always
// multi-line and newline-separated (no trailing commas).
func emitInterfaceDef(v *ast.InterfaceDef) Doc {
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	parts = append(parts, Text("interface "), Text(v.Name), emitTypeParams(v.TypeParams))
	parts = append(parts, emitWhereClause(v.WhereClauses)...)

	members := make([]Doc, 0, len(v.Methods))
	for i := range v.Methods {
		m := v.Methods[i]
		member := emitInterfaceMethod(m)
		if len(members) > 0 && m.Body != nil && !hasLeadingBlank(&v.Methods[i]) {
			member = Concat(HardLine(), member)
		}
		members = append(members, member)
	}
	parts = append(parts, emitDeclBody(members, v.EndTrivia))
	return Concat(parts...)
}

// emitImplBlock renders an impl block. The top-level surface form is a single
// interface and a single implementing type:
//
//	impl Iface for Type                      // interface impl, body, `end`
//	impl Iter for Box<T> where T: Bound      // constrained generic impl
//
// The header reuses emitTypeParams for the `<...>` clause (no space before
// `<`, matching `fn name<T>`). Items are the block's FuncDef / ExternFunc
// declarations, each emitted with its own doc comments + decorators via
// emit(); the multi-line braced body mirrors emitInterfaceDef so blank-line
// separation and trailing trivia are handled identically. An empty impl is
// one line, `impl Iface for Type end`.
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
	parts = append(parts, emitDeclBody(members, v.EndTrivia))
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
	parts = append(parts, Text("type "), Text(v.Name), emitTypeParams(v.TypeParams))
	if v.InnerTypeExpr != nil {
		parts = append(parts, Text(" "), emitTypeExpr(v.InnerTypeExpr))
	}
	parts = emitOptionalTypeBody(parts, v.HasBody, v.Items, v.EndTrivia)
	return Concat(parts...)
}

// emitOptionalTypeBody appends an optional item body, closed by `end`, for
// internal ASTs. Source `type` and `host type` declarations do not parse with
// bodies anymore.
func emitOptionalTypeBody(parts []Doc, hasBody bool, items []ast.Node, endTrivia []ast.Trivia) []Doc {
	if !hasBody || (len(items) == 0 && len(endTrivia) == 0) {
		return parts
	}
	members := appendTypeBodyItems(nil, items)
	return append(parts, emitDeclBody(members, endTrivia))
}

// emitOnceBinding renders `[pub ]once name: T = expr`. The value is laid
// out as a binding's is (emitBindingValue), so a pipe breaks after `=` into
// the indented stacked form.
func emitOnceBinding(v *ast.OnceBinding) Doc {
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	target := []Doc{}
	if v.Public {
		target = append(target, Text("pub "))
	}
	target = append(target, Text("once "), Text(v.Name))
	if v.TypeAnnotation != nil {
		target = append(target, Text(": "), emitTypeExpr(v.TypeAnnotation))
	}
	parts = append(parts, emitBindingValue(Concat(target...), v.Value))
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

// emitExternFunc renders `[pub ]host fn name(params)[: Ret]` or a Go
// binding.
func emitExternFunc(v *ast.ExternFunc) Doc {
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	if v.ForeignAlias != "" {
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
	parts = append(parts, emitParamList(v.Line, paramDocs, v.Params, v.ParamsEndTrivia))

	if v.ReturnTypeExpr != nil {
		parts = append(parts, Text(" -> "), emitTypeExpr(v.ReturnTypeExpr))
	}
	parts = append(parts, emitWhereClause(v.WhereClauses)...)
	if v.ForeignAlias != "" {
		parts = append(parts, Text(" go "), Text(v.ForeignAlias), Text("."), Text(v.ForeignName))
	}
	return Concat(parts...)
}

// emitKeywordDecl renders a keyword declaration by the general rule
// `[pub] keyword name [(params)] [-> type] [[Tag]raw body]`,
// writing each part that was written.
func emitKeywordDecl(v *ast.KeywordDecl) Doc {
	parts := emitDocBeforeAttachedTests(v.Doc, nil)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	parts = append(parts, Text(v.Keyword+" "+v.Name))
	if v.HasParams {
		paramDocs := make([]Doc, 0, len(v.Params))
		for _, p := range v.Params {
			paramDocs = append(paramDocs, emitFuncDefParam(p))
		}
		parts = append(parts, emitParamList(v.Line, paramDocs, v.Params, v.ParamsEndTrivia))
	}
	if v.ReturnTypeExpr != nil {
		parts = append(parts, Text(" -> "), emitTypeExpr(v.ReturnTypeExpr))
	}
	if v.Body != nil {
		parts = append(parts, Text(" "+v.BodyTag), emit(v.Body))
	}
	return Concat(parts...)
}

// emitEmbedDecl renders `[pub] embed name `patterns“, its body as written.
func emitEmbedDecl(v *ast.EmbedDecl) Doc {
	parts := emitDocBeforeAttachedTests(v.Doc, nil)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	parts = append(parts, Text("embed "+v.Name+" "), emit(v.Body))
	return Concat(parts...)
}

// emitExternType renders `[pub ]host type Name[<T, ...>]` or a Go binding, optionally followed by a type-body item list.
func emitExternType(v *ast.ExternType) Doc {
	// Doc-comment first, then decorators — see emitStructDef for the parser
	// constraint that drives this order.
	parts := emitDocBeforeAttachedTests(v.Doc, v.AttachedTests)
	parts = append(parts, emitAttachedTests(v.AttachedTests)...)
	parts = append(parts, emitTypeDeclDecorators(v.Decorators)...)
	if v.Public {
		parts = append(parts, Text("pub "))
	}
	if v.ForeignAlias != "" {
		if v.Opaque {
			parts = append(parts, Text("opaque "))
		}
		parts = append(parts, Text("type "), Text(v.Name), emitTypeParams(v.TypeParams))
		parts = append(parts, Text(" go "), Text(v.ForeignAlias), Text("."), Text(v.ForeignName))
	} else {
		parts = append(parts, Text("host type "), Text(v.Name), emitTypeParams(v.TypeParams))
	}
	parts = append(parts, emitWhereClause(v.WhereClauses)...)
	parts = emitOptionalTypeBody(parts, v.HasBody, v.Items, v.EndTrivia)
	return Concat(parts...)
}

// emitImport renders an import statement in canonical form:
//
//	import <path>
//	import <path> as alias
//	import <path>.A
//	import <path>.{A, B, ...}
//	import <path>.Owner.{A, B as C, ...}
//	pub import <path>.{A, B, C}
//
// The selective list stays inline when it fits. When the full import exceeds
// the width budget, the formatter breaks after the module path and renders one
// top-level selector per continuation line. A selective-name alias (`Name as
// Alias`) stays on the same line as its original name — it's emitted as an
// atomic Text so the layout engine can't break between the name and `as`.
func emitImport(v *ast.ImportStmt) Doc {
	return Concat(Text(importKeyword(v.Pub)), emitImportBody(v))
}

// importKeyword is the statement's keyword with the space after it.
func importKeyword(pub bool) string {
	if pub {
		return "pub import "
	}
	return "import "
}

// emitImportBody renders an import without the leading `import ` keyword: the
// path, optional drill-through type and explicit selectors. Shared by
// emitImport (which prepends `import `) and emitImportBlock (which
// contributes the keyword once for the whole block, so each entry is just
// its body).
func emitImportBody(v *ast.ImportStmt) Doc {
	pathParts, owners := importPathAndOwners(v)
	path := strings.Join(pathParts, ast.PathSeparator)
	parts := []Doc{Text(path)}
	if cs, ok := nodesComments(v.Names); ok {
		// A comment in the list: the owners join the path, and the
		// items go one per line.
		items := importSelectorItemStrings(v)
		docs := make([]Doc, len(items))
		for i, it := range items {
			docs[i] = Text(it)
		}
		if v.IncludeParent {
			cs = append([]*ast.ItemComments{nil}, cs...)
		}
		head := path
		for _, o := range owners {
			head += "." + o
		}
		return Concat(Text(head+"."), emitCommentedList("{", "}", docs, cs, nil))
	}
	if len(v.Names) > 0 || v.IncludeParent {
		if v.IncludeParent && len(owners) == 0 && !importHasItemModifiers(v) {
			return Text(path + ".{" + strings.Join(importSelectorItemStrings(v), ", ") + "}")
		}
		selectors := importSelectorStrings(v)
		if importCanUseDottedSingleSelector(v, selectors) {
			return Text(path + "." + selectors[0])
		}
		return emitImportSelectorBody(path, selectors)
	} else if v.ModuleAlias != nil {
		parts = append(parts, Text(" as "+ast.ImportNodeName(v.ModuleAlias)))
	}
	return Concat(parts...)
}

func importCanUseDottedSingleSelector(v *ast.ImportStmt, selectors []string) bool {
	if len(selectors) != 1 || len(v.Names) != 1 || v.IncludeParent {
		return false
	}
	// A dotted name, `A.{B.C}`, stays in braces: without them its segments
	// read as more of the path, `A.B.C`.
	return !strings.Contains(ast.ImportNodeName(v.Names[0]), ".")
}

func emitImportSelectorBody(path string, selectors []string) Doc {
	if len(selectors) == 0 {
		return Text(path)
	}
	// A dotted name, `A.{B.C}`, stays in braces: without them its segments
	// read as more of the path, `A.B.C`. An owner's selector, `Maybe.{Some}`,
	// does not.
	if len(selectors) == 1 && (!strings.Contains(selectors[0], ".") || strings.Contains(selectors[0], "{")) {
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

// importPathAndOwners splits an import's path into the file path, written
// `::`-joined, and the owners after it. A parsed import records the split
// (FileSegments); one built without it takes its trailing PascalCase
// segments as owners, the rule the parser applies.
func importPathAndOwners(v *ast.ImportStmt) ([]string, []string) {
	modulePath := v.ModulePath
	var owners []string
	if v.FileSegments > 0 && v.FileSegments <= len(modulePath) {
		for _, p := range modulePath[v.FileSegments:] {
			owners = append(owners, ast.ImportNodeName(p))
		}
		modulePath = modulePath[:v.FileSegments]
	} else if len(v.Names) > 0 || v.IncludeParent {
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
		items = append(items, n)
	}
	return items
}

func importSelectorStrings(v *ast.ImportStmt) []string {
	_, owners := importPathAndOwners(v)
	items := importSelectorItemStrings(v)
	ownerPrefix := strings.Join(owners, ".")
	if ownerPrefix == "" {
		if v.IncludeParent {
			return []string{"{" + strings.Join(items, ", ") + "}"}
		}
		return items
	}
	if len(items) == 1 && !v.IncludeParent && !importHasItemModifiers(v) {
		return []string{ownerPrefix + "." + items[0]}
	}
	return []string{ownerPrefix + ".{" + strings.Join(items, ", ") + "}"}
}

func importHasItemModifiers(v *ast.ImportStmt) bool {
	for _, alias := range v.Aliases {
		if alias != nil {
			return true
		}
	}
	return false
}

// emitImportBlock renders the brace block form:
//
//	import {
//	  std::io
//	  std::lists: List
//	}
//
// or the same after `pub` for a block of re-exports. Always multiline, one entry per line, newline-separated — no commas and no
// trailing comma. This is the analog of Go's `import ( ... )`: a block of 2+
// entries never collapses onto a single line. (Single-entry blocks are
// collapsed to the bare per-statement form before we get here, by
// collapseSingleEntryBlocks.) Each entry is rendered by emitImportBody and
// Nest-indented, so a selective entry that itself overflows width still
// breaks correctly under the block indent.
func emitImportBlock(v *ast.ImportBlock) Doc {
	blockHead := strings.TrimSpace(importKeyword(v.Pub))
	if len(v.Entries) == 0 {
		if trailingHasComment(v.EndTrivia) {
			// Only comments: they stay inside the block.
			return Concat(Text(blockHead), Nest(defaultIndent, emitEndTrivia(v.EndTrivia)), HardLine(), Text("end"))
		}
		return Text(blockHead + " end")
	}
	parts := []Doc{Text(blockHead)}
	for i, e := range v.Entries {
		body := emitImportBody(e)
		// emitWithTriviaDoc renders the entry's leading comments (each on its own
		// line above it) and trailing same-line comment around the keyword-less
		// body. emit(e) is NOT usable here — it would prepend `import `.
		entry := emitWithTriviaDoc(e, body)
		if i > 0 && hasBlankTrivia(e.Leading) && !importEntryEndsLine(v.Entries[i-1]) {
			// The entry above ends in a word after which the lexer reads a
			// line break as a continuation (a path may end in `opaque` or
			// `struct`), so a blank line here would not read back. Drop it
			// as the next format would.
			entry = emitWithTriviaParts(body, commentTrivia(e.Leading), outerTrailing(e, e))
		}
		parts = append(parts, Nest(defaultIndent, Concat(HardLine(), entry)))
	}
	if len(v.EndTrivia) > 0 {
		parts = append(parts, Nest(defaultIndent, emitEndTrivia(v.EndTrivia)))
	}
	parts = append(parts, HardLine(), Text("end"))
	return Concat(parts...)
}

// importEntryEndsLine reports whether the lexer ends a statement after e's
// line, so that a blank line written after it reads back as one. It does not
// after a keyword, which an import path may end in.
func importEntryEndsLine(e *ast.ImportStmt) bool {
	for _, t := range lexer.Lex(Render(emitImportBody(e), 1<<20) + "\n\nx") {
		if t.Type == token.BLANK_LINE {
			return true
		}
	}
	return false
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
	// one with a `then` stage (pipeChainStacked).
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
	// Stack when the source wrote the chain across multiple lines (any step
	// on a different line than the source), or when it has a `then` stage.
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
	if ifn, ok := source.(*ast.If); ok {
		// An inline `if` at a pipe's head goes in parentheses: its `else`
		// branch would take the pipe in.
		parts = append(parts, emitWithTriviaDoc(source, emitIfParens(ifn, true)))
	} else {
		parts = append(parts, emitWithTrivia(source))
	}
	for i := 0; i < len(steps); i++ {
		step := steps[i]
		if ifn, ok := step.(*ast.If); ok && i+1 < len(steps) && !isQuestionStage(steps[i+1]) {
			// An `if` stage that a stage follows is written in block form:
			// inline, its `else` branch would take the next stage in.
			parts = append(parts, Concat(sep, Text("|> "), emitWithTriviaDoc(ifn, emitIfForceBroken(ifn))))
			continue
		}
		if isQuestionStage(step) {
			// A `?` stage is written against the stage before it.
			parts = append(parts, Text("?"))
			continue
		}
		if then, ok := step.(*ast.Then); ok && i+1 < len(steps) && isQuestionStage(steps[i+1]) {
			// `|> then? fn v -> ...`.
			parts = append(parts, Concat(sep, Text("|> "), emitWithTriviaDoc(then, emitThenQuestion(then))))
			i++
			continue
		}
		if i+1 < len(steps) {
			if decorated, ok := emitDecoratedPipeStage(step, steps[i+1], i+2 < len(steps)); ok {
				decorated = pipeDecoratedStageDoc(step, steps[i+1], decorated)
				if authoredMultiline {
					if leading := decoratedStageLeading(step, steps[i+1]); len(leading) > 0 {
						for _, leadingDoc := range emitLeadingTriviaDocs(leading) {
							parts = append(parts, sep, leadingDoc)
						}
						parts = append(parts, Concat(sep, Text("|> "), decorated))
						i++
						continue
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
					parts = append(parts, Concat(sep, Text("|> "), emitWithTrailingTrivia(step, i+1 < len(steps))))
					continue
				}
			}
		}
		parts = append(parts, Concat(sep, Text("|> "), pipeStageDoc(step, emitStageWithTrivia(step, i+1 < len(steps)))))
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
// line: when the author wrote it across lines, or when it has a `then` or
// `tap` stage with a lambda. Such a lambda's bare body ends at the next `|>`,
// which one line hides (`5 |> then fn n -> n + 1 |> then fn n -> n * 2` reads
// as one lambda); a line per stage shows where each body ends. A capture
// ends with its body, so a stage applying one stays on the line.
func pipeChainStacked(source ast.Node, steps []ast.Node) bool {
	return pipeChainAuthoredMultiline(source, steps) || pipeChainHasThenStage(steps)
}

func pipeChainHasThenStage(steps []ast.Node) bool {
	for _, step := range steps {
		switch s := step.(type) {
		case *ast.Then:
			if s.Lambda != nil {
				return true
			}
		case *ast.Tap:
			if s.Lambda != nil {
				return true
			}
		}
	}
	return false
}

// pipeChainAuthoredMultiline reports whether the author wrote the pipeline
// across lines: some stage starts on a line after the one the source or the
// stage before it ends on. A stage written across lines does not count, so
// `x |> f(P{` ... `}) |> dbg`, which keeps the pipe on its first line, reads
// back as it was written.
func pipeChainAuthoredMultiline(source ast.Node, steps []ast.Node) bool {
	prevEnd := nodeEndLine(source)
	for _, step := range steps {
		if nodeStartLine(step) > prevEnd {
			return true
		}
		prevEnd = max(prevEnd, nodeEndLine(step))
	}
	return false
}

// nodeEndLine is the line n's last token is on, or its LineNum when the
// parser recorded no extent.
func nodeEndLine(n ast.Node) int {
	if s, ok := n.(ast.HasSpan); ok && !s.GetSpan().IsZero() {
		return s.GetSpan().EndLine
	}
	return n.LineNum()
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
		if ifn, ok := step.(*ast.If); ok && i+1 < len(steps) && !isQuestionStage(steps[i+1]) {
			// An `if` stage that a stage follows is written in block form.
			parts = append(parts, Nest(defaultIndent, Concat(sep, Text("|> "), emitWithTriviaDoc(ifn, emitIfForceBroken(ifn)))))
			continue
		}
		if isQuestionStage(step) {
			// A `?` stage is written against the stage before it.
			parts = append(parts, Text("?"))
			continue
		}
		if then, ok := step.(*ast.Then); ok && i+1 < len(steps) && isQuestionStage(steps[i+1]) {
			// `|> then? fn v -> ...`.
			parts = append(parts, Nest(defaultIndent, Concat(sep, Text("|> "), emitWithTriviaDoc(then, emitThenQuestion(then)))))
			i++
			continue
		}
		if i+1 < len(steps) {
			if decorated, ok := emitDecoratedPipeStage(step, steps[i+1], i+2 < len(steps)); ok {
				decorated = pipeDecoratedStageDoc(step, steps[i+1], decorated)
				if authoredMultiline {
					if leading := decoratedStageLeading(step, steps[i+1]); len(leading) > 0 {
						for _, leadingDoc := range emitLeadingTriviaDocs(leading) {
							parts = append(parts, Nest(defaultIndent, Concat(sep, leadingDoc)))
						}
						parts = append(parts, Nest(defaultIndent, Concat(sep, Text("|> "), decorated)))
						i++
						continue
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
					parts = append(parts, Nest(defaultIndent, Concat(sep, Text("|> "), emitWithTrailingTrivia(step, i+1 < len(steps)))))
					continue
				}
			}
		}
		parts = append(parts, Nest(defaultIndent, Concat(sep, Text("|> "), pipeStageDoc(step, emitStageWithTrivia(step, i+1 < len(steps))))))
	}
	chain := Concat(parts...)
	if authoredMultiline {
		return chain
	}
	return Group(chain)
}

// decoratedStageLeading is the comments above a stage written with its
// keyword before it, `|> f()?` or `|> case f() {`: the parser gives them
// to the stage or, for `case` and `if`, to the keyword.
func decoratedStageLeading(stage, keyword ast.Node) []ast.Trivia {
	var leading []ast.Trivia
	for _, n := range []ast.Node{stage, keyword} {
		if ht, ok := n.(ast.HasTrivia); ok {
			leading = append(leading, ht.GetLeading()...)
		}
	}
	return leading
}

// pipeStageDoc lays out an `if` or `case` stage as a block in the line the
// `|>` is on: its arms or branches one indent in from that line and its `}`
// back at the line's indent, as a block lambda stage is. Only its first line
// counts against an inline pipeline's width, so `x |> case {` stays one line
// with the `|>` rather than stacking the pipeline, and a `|> case f() {`
// too wide for the line stacks it.
func pipeStageDoc(stage ast.Node, doc Doc) Doc {
	if pipeStageIsBlock(stage) {
		return LocalBrokenBlock(doc)
	}
	return doc
}

func pipeDecoratedStageDoc(stage ast.Node, keyword ast.Node, doc Doc) Doc {
	if pipeStageIsBlock(stage) || pipeStageIsBlock(keyword) {
		return LocalBrokenBlock(doc)
	}
	return doc
}

func pipeStageIsBlock(stage ast.Node) bool {
	switch stage.(type) {
	case *ast.If, *ast.Case:
		return true
	default:
		return false
	}
}

// isKeywordStage reports whether a pipe stage is a keyword the piped value
// feeds: an `if` with no condition, a `case` with no subject, or a bare
// `?`, `dbg`, `assert` or `refute`. The parser builds `|> dbg f()?` as
// the stages `f()`, `?`, `dbg`, so a keyword never decorates another
// keyword.
func isKeywordStage(stage ast.Node) bool {
	switch s := stage.(type) {
	case *ast.If:
		return s.Cond == nil
	case *ast.Case:
		return s.Value == nil
	case *ast.TryOp:
		return s.Expr == nil
	case *ast.Dbg:
		return s.Expr == nil
	case *ast.Assertion:
		return s.Expr == nil
	}
	return false
}

// emitDecoratedPipeStage writes stage with the keyword stage after it as its
// prefix (`|> f()?`); more says another stage follows the keyword.
func emitDecoratedPipeStage(stage ast.Node, keyword ast.Node, more bool) (Doc, bool) {
	switch stage.(type) {
	case *ast.Then, *ast.Tap:
		// A keyword does not prefix a `then` or `tap` stage: `|> then fn v ->
		// f(v)` and a `?` stage stay two stages.
		return nil, false
	}
	if isKeywordStage(stage) {
		// A keyword stage is no condition or subject: `|> if then a else b`
		// then `|> case`, or `|> dbg` then a `?` stage, stay two stages, as
		// written.
		return nil, false
	}
	switch kw := keyword.(type) {
	case *ast.If:
		// `|> f() |> if then` and `|> if f() then` are the same stages;
		// each stays as it was written (StagePrefix). With a stage after
		// it, the `if` is written in block form: inline, its `else` branch
		// would take that stage in.
		if !kw.StagePrefix || kw.Cond != nil {
			return nil, false
		}
		joined := &ast.If{
			Cond:        stage,
			Then:        kw.Then,
			Else:        kw.Else,
			StagePrefix: true,
			Line:        kw.Line,
			Col:         kw.Col,
		}
		if more {
			return emitIfForceBroken(joined), true
		}
		return emitIf(joined), true
	case *ast.Case:
		// `|> f() |> case` and `|> case f()` are the same stages; each
		// stays as it was written (StagePrefix).
		if !kw.StagePrefix || kw.Value != nil {
			return nil, false
		}
		// The case's Trailing are the comments before its `end`, which
		// emitCase writes above it.
		return emitCase(&ast.Case{
			TriviaCarrier: ast.TriviaCarrier{Trailing: kw.GetTrailing()},
			Value:         stage,
			Branches:      kw.Branches,
			StagePrefix:   true,
			Line:          kw.Line,
			Col:           kw.Col,
		}), true
	default:
		return nil, false
	}
}

// shapeOpen is the opening of a literal or pattern of a built-in shape: its
// symbol, the name of the type built on the shape when there is one, and its
// bracket. `{`, `Point{`, `.Circle{`, `Kvs{`, `Path[`.
// structOpen is a struct literal's or pattern's opening: `{` for the
// unnamed form, and the name alone for a named one, `Point{`.
func structOpen(name ast.TypeExpr) string {
	if name == nil {
		return "{"
	}
	return name.TypeString() + "{"
}

func shapeOpen(sigil string, name ast.TypeExpr, bracket string) string {
	if name == nil {
		return sigil + bracket
	}
	return sigil + name.TypeString() + bracket
}

// spreadRestDoc is a vector or set pattern's `..` with what follows it: a
// name, `_`, or nothing.
func spreadRestDoc(rest ast.Node) Doc {
	if rest == nil {
		return Text("..")
	}
	return Concat(Text(".."), emitPattern(rest))
}

// emitListSpreadLit writes a bracket literal with spreads, `[head, ..tail]`,
// `[..xs, y, ..ys]`: on one line when it fits and was written so, else one
// operand per line. A broken literal ending in a `..` spread has no
// trailing comma.
func emitListSpreadLit(v *ast.ListLit) Doc {
	open := shapeOpen("", v.TypeName, "[")
	lastComma := !endsInSpread(len(v.Items), v.Spreads)
	hasEndTrivia := trailingHasComment(v.EndTrivia)
	items := v.Operands()
	elems := spreadOperandDocs(v.Items, v.Spreads, emit)
	authored := brokenAfterOpen(v.Line, firstOperandLine(v.Items, v.Spreads))
	if hasEndTrivia || nodesHaveComments(items) || tooDeep(v, len(v.Items)) || authored {
		cs, _ := nodesComments(items)
		return commentedList(open, "]", elems, cs, v.EndTrivia, lastComma)
	}
	return bracedList(open, "]", elems, lastComma)
}

// emitSeqPattern writes a vector pattern, or a list pattern with elements
// after its `..`: open, the heads, the `..` with its rest (nil for a bare
// `..`) when spread is set, and the tails.
func emitSeqPattern(v ast.Node, open string, heads []ast.Node, spread bool, rest ast.Node, tails []ast.Node, endTrivia []ast.Trivia) Doc {
	items := append([]ast.Node{}, heads...)
	if spread && rest != nil {
		items = append(items, rest)
	}
	items = append(items, tails...)
	elems, cs, commented := patternItems(items)
	if spread {
		// The `..` stands between the heads and the tails, with its rest
		// pattern's Doc when there is one.
		at := len(heads)
		spread := Text("..")
		if rest != nil {
			spread = Concat(Text(".."), elems[at])
			elems = append(elems[:at:at], append([]Doc{spread}, elems[at+1:]...)...)
		} else {
			elems = append(elems[:at:at], append([]Doc{spread}, elems[at:]...)...)
			if cs != nil {
				cs = append(cs[:at:at], append([]*ast.ItemComments{nil}, cs[at:]...)...)
			}
		}
		if commented || trailingHasComment(endTrivia) {
			return emitCommentedList(open, "]", elems, cs, endTrivia)
		}
		parts := make([]Doc, 0, len(elems)*2+2)
		parts = append(parts, Text(open))
		for i, e := range elems {
			if i > 0 {
				parts = append(parts, Text(", "))
			}
			parts = append(parts, e)
		}
		parts = append(parts, Text("]"))
		return Concat(parts...)
	}
	if trailingHasComment(endTrivia) || commented || tooDeep(v, len(heads)) || itemsBrokenAfterOpen(v.LineNum(), heads) {
		return emitCommentedList(open, "]", elems, cs, endTrivia)
	}
	return emitBracedList(open, "]", elems)
}

// taggedOnOneLine reports whether v, a triple-quoted typed literal, was
// written on one line: its span starts and ends on its line.
func taggedOnOneLine(v *ast.TaggedString) bool {
	sp := v.GetSpan()
	return !v.Raw && !sp.IsZero() && sp.StartLine == sp.EndLine
}

// hasBlankTrivia reports whether trivia holds a blank line.
func hasBlankTrivia(trivia []ast.Trivia) bool {
	for _, t := range trivia {
		if t.Kind == ast.TriviaBlankLine {
			return true
		}
	}
	return false
}
