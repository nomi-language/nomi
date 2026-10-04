package lsp

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// "Generate function" answers the checker's error for a call of a function
// that does not exist:
//
//   - `foo(a, b)`, "undefined variable 'foo'": `fn foo(...)` after the
//     top-level declaration that makes the call;
//   - `Type.foo(x)` on a struct or enum the file declares, "type 'Type'
//     has no member 'foo'": the function at the end of the type's inherent
//     `impl Type` block, or in a new block after the declaration that makes
//     the call when the type has none.
//
// A parameter is named for its argument when the argument is a plain name
// or a named argument, `argN` (its 1-based position) otherwise; its type is
// the argument's type. A piped value is the argument it fills. The return
// type is the type the checker expected of the call. A call whose value is
// discarded (a statement before the block's last) returns Unit and the
// signature names none. With no expected type, or an argument whose type
// the checker did not solve, no function is offered: Nomi does not infer a
// signature, so a guessed one would fail somewhere else. The body is the
// fill placeholder.
//
// The fix reads the error from the analysis rather than from the request's
// diagnostics, and is offered while the range is inside the call: the
// checker's error is a point at the callee's name, which a client that
// sends only the diagnostics under the cursor leaves out once the cursor is
// past the name's first character. The request's diagnostic at that point,
// when it sent one, is the one the fix names.

var (
	undefinedNameMessage = regexp.MustCompile(`^undefined variable '([a-z_][A-Za-z0-9_]*[?!]?)'`)
	noTypeMemberMessage  = regexp.MustCompile(`^type '([A-Z][A-Za-z0-9_]*)' has no member '([a-z_][A-Za-z0-9_]*[?!]?)'$`)
)

// generateFunctions returns a fix for each undefined call under the range,
// with the diagnostic of diags that reports it.
func (r *refactorRequest) generateFunctions(diags []protocol.Diagnostic) []offer {
	var out []offer
	for _, e := range r.fa.TypeErrors {
		title, edited, ok := r.generateFunction(e.Line, e.Col, e.Message)
		if !ok {
			continue
		}
		a := offer{title: title, edited: edited}
		for _, d := range diags {
			if diagnosticHeadline(d.Message) == e.Message && diagCovers(r.lines, d, e.Line, e.Col) {
				a.diags = append(a.diags, d)
			}
		}
		out = append(out, a)
	}
	return out
}

func (r *refactorRequest) generateFunction(line, col int, msg string) (string, string, bool) {
	if m := undefinedNameMessage.FindStringSubmatch(msg); m != nil {
		for _, n := range r.all {
			id, ok := n.(*ast.Ident)
			if !ok || id.Name != m[1] || id.Line != line || id.Col != col {
				continue
			}
			call, ok := r.parent[id].(*ast.Call)
			if !ok || call.Func != id || !r.contains(call) {
				return "", "", false
			}
			sig, ok := r.signature(m[1], call)
			if !ok {
				return "", "", false
			}
			at, ok := r.declEnd()
			if !ok {
				return "", "", false
			}
			text := "\n\n" + sig + " {\n" + strings.Repeat(" ", format.IndentWidth) + fillPlaceholder + "\n}"
			return fmt.Sprintf("Generate function '%s'", m[1]), r.replace(at, at, text), true
		}
		return "", "", false
	}
	if m := noTypeMemberMessage.FindStringSubmatch(msg); m != nil {
		for _, n := range r.all {
			fa, ok := n.(*ast.FieldAccess)
			if !ok || fa.Field == nil || fa.Field.Name != m[2] || fa.Field.Line != line || fa.Field.Col != col {
				continue
			}
			if calleeObject(fa.Object) != m[1] || !r.declaresPlainType(m[1]) {
				return "", "", false
			}
			call, ok := r.parent[fa].(*ast.Call)
			if !ok || call.Func != fa || !r.contains(call) {
				return "", "", false
			}
			sig, ok := r.signature(m[2], call)
			if !ok {
				return "", "", false
			}
			title := fmt.Sprintf("Generate function '%s.%s'", m[1], m[2])
			edited, ok := r.addToInherentImpl(m[1], sig)
			return title, edited, ok
		}
	}
	return "", "", false
}

// signature is `fn name(params): Ret` for call.
func (r *refactorRequest) signature(name string, call *ast.Call) (string, bool) {
	args := append([]ast.Node(nil), call.Args...)
	site := ast.Node(call)
	if pipe, ok := r.parent[call].(*ast.Binary); ok && pipe.Op == "|>" && pipe.Right == call {
		site = pipe
		at := -1
		for i, a := range args {
			if hasPlaceholderArg(a) {
				at = i
			}
		}
		if at < 0 {
			args = append([]ast.Node{pipe.Left}, args...)
		} else if named, ok := args[at].(*ast.NamedArg); ok {
			na := *named
			na.Value = pipe.Left
			args[at] = &na
		} else {
			args[at] = pipe.Left
		}
	}
	used := map[string]bool{}
	params := make([]string, len(args))
	for i, a := range args {
		pname := ""
		value := a
		switch v := a.(type) {
		case *ast.Placeholder:
			return "", false
		case *ast.NamedArg:
			if _, ok := v.Value.(*ast.Placeholder); ok {
				return "", false
			}
			pname, value = v.Name, v.Value
		case *ast.Ident:
			pname = strings.TrimLeft(v.Name, "_")
		}
		if pname == "" || used[pname] || !isPlainIdent(pname) {
			pname = fmt.Sprintf("arg%d", i+1)
		}
		used[pname] = true
		t := typeText(r.fa.ExprTypes[value])
		if t == "" {
			// The checker leaves a piped call's arguments unchecked when
			// the callee is undefined; a literal says its own type.
			t = literalType(value)
		}
		if t == "" {
			return "", false
		}
		params[i] = pname + ": " + t
	}
	sig := "fn " + name + "(" + strings.Join(params, ", ") + ")"
	if t := typeText(r.fa.ExpectedTypes[site]); t != "" {
		if t != "Unit" {
			sig += ": " + t
		}
		return sig, true
	}
	if !r.discarded(site) {
		return "", false
	}
	return sig, true
}

// literalType is the type a scalar literal has wherever it stands, or "".
func literalType(n ast.Node) string {
	switch v := n.(type) {
	case *ast.StringLit, *ast.StringInterp:
		return "String"
	case *ast.IntLit:
		return "Int"
	case *ast.FloatLit:
		return "Float"
	case *ast.TypeIdent:
		if v.Name == "True" || v.Name == "False" {
			return "Bool"
		}
	}
	return ""
}

// discarded reports whether n is a statement whose value nothing reads: an
// expression statement before its block's last.
func (r *refactorRequest) discarded(n ast.Node) bool {
	stmt, block := r.statementOf(n)
	if stmt == nil {
		return false
	}
	if es, ok := stmt.(*ast.ExprStmt); ok {
		if es.Expr != n {
			return false
		}
	} else if stmt != n {
		return false
	}
	return block.Stmts[len(block.Stmts)-1] != stmt
}

// declEnd is the offset just past the closing `}` of the top-level
// declaration under the range.
func (r *refactorRequest) declEnd() (int, bool) {
	line, col := 0, 0
	switch d := r.decl.(type) {
	case *ast.FuncDef:
		line, col = d.Body.EndLine, d.Body.EndCol
	case *ast.ImplBlock:
		line, col = d.EndLine, d.EndCol
	case *ast.TestDecl:
		line, col = d.Body.EndLine, d.Body.EndCol
	}
	if line <= 0 {
		return 0, false
	}
	off := posToOffset(r.offs, line, col)
	if off >= len(r.content) || r.content[off] != '}' {
		return 0, false
	}
	return off + 1, true
}

// declaresPlainType reports whether the file declares a struct or enum
// named name with no type parameters.
func (r *refactorRequest) declaresPlainType(name string) bool {
	for _, n := range r.nodes {
		switch d := n.(type) {
		case *ast.StructDef:
			if d.Name == name {
				return len(d.TypeParams) == 0
			}
		case *ast.EnumDef:
			if d.Name == name {
				return len(d.TypeParams) == 0
			}
		}
	}
	return false
}

// addToInherentImpl writes sig with a placeholder body at the end of the
// file's `impl typeName { ... }` block, or in a new block after the
// declaration under the range.
func (r *refactorRequest) addToInherentImpl(typeName, sig string) (string, bool) {
	indent := strings.Repeat(" ", format.IndentWidth)
	fn := indent + sig + " {\n" + indent + indent + fillPlaceholder + "\n" + indent + "}"
	for _, n := range r.nodes {
		impl, ok := n.(*ast.ImplBlock)
		if !ok || impl.Interface != nil || receiverBaseName(impl.Receiver) != typeName || impl.EndLine <= 0 {
			continue
		}
		closeOff := posToOffset(r.offs, impl.EndLine, impl.EndCol)
		if closeOff >= len(r.content) || r.content[closeOff] != '}' {
			return "", false
		}
		lineStart := r.lineStart(closeOff)
		if strings.TrimSpace(r.content[lineStart:closeOff]) != "" {
			// `impl T {}` on one line.
			open := strings.LastIndexByte(r.content[:closeOff], '{')
			if open < 0 || strings.TrimSpace(r.content[open+1:closeOff]) != "" {
				return "", false
			}
			return r.replace(open+1, closeOff, "\n"+fn+"\n"), true
		}
		sep := ""
		if len(impl.Items) > 0 {
			sep = "\n"
		}
		return r.replace(lineStart, lineStart, sep+fn+"\n"), true
	}
	at, ok := r.declEnd()
	if !ok {
		return "", false
	}
	return r.replace(at, at, "\n\nimpl "+typeName+" {\n"+fn+"\n}"), true
}

// typeText is t as source, or "" when t is unknown or not fully solved.
func typeText(t analysis.Type) string {
	t = analysis.ResolveTypeVar(t)
	if t == nil {
		return ""
	}
	s := t.String()
	if s == "" || strings.ContainsAny(s, "?'") || strings.Contains(s, "unknown") || strings.Contains(s, "<invalid>") {
		return ""
	}
	return s
}
