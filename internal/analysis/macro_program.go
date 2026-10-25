package analysis

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A macro's ProgramCheck and Navigation, over the declarations its
// expansions keep (macroExpansion.call).

// CheckMacroPrograms runs, for each macro the declarations of
// files (by module key) were expanded with, its ProgramCheck once over all
// of them, and answers the diagnostics by the module key of the file each
// is in. One in an input file, or in no file of files, is under "", for
// the entry to report.
func CheckMacroPrograms(files map[string][]ast.Node) map[string][]TypeError {
	type group struct {
		exps []*macroExpansion
		keys []string
	}
	groups := map[string]*group{}
	var order []string
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, n := range files[key] {
			kd, ok := n.(*ast.KeywordDecl)
			if !ok {
				continue
			}
			e := macroExpansionOf(kd)
			if e == nil || e.call == nil || e.host == nil {
				continue
			}
			id := e.call.File + "\x00" + e.call.Name + "\x00" + e.root
			g := groups[id]
			if g == nil {
				g = &group{}
				groups[id] = g
				order = append(order, id)
			}
			g.exps = append(g.exps, e)
			g.keys = append(g.keys, key)
		}
	}
	var out map[string][]TypeError
	add := func(key string, te TypeError) {
		if out == nil {
			out = map[string][]TypeError{}
		}
		out[key] = append(out[key], te)
	}
	for _, id := range order {
		g := groups[id]
		first := g.exps[0]
		call := &MacroCheckCall{File: first.call.File, Root: first.call.Root, Module: first.call.Module, Name: first.call.Name, Config: first.call.Config}
		seen := map[string]bool{}
		for _, e := range g.exps {
			call.Decls = append(call.Decls, e.call.Decl)
			for _, in := range e.requested {
				if !seen[in.Path] {
					seen[in.Path] = true
					call.Inputs = append(call.Inputs, in)
				}
			}
		}
		sort.Slice(call.Inputs, func(i, j int) bool { return call.Inputs[i].Path < call.Inputs[j].Path })
		diags, failure := first.host.CheckAll(call)
		if failure != "" {
			kd := first.decl
			add(g.keys[0], TypeError{Line: kd.Line, Col: kd.Col, EndLine: kd.Line, EndCol: kd.KeywordEndCol(),
				Message: fmt.Sprintf("the macro `%s` (module %s) could not check its declarations: %s", first.name, first.module, failure)})
			continue
		}
		for _, d := range diags {
			key, e := "", first
			for i, x := range g.exps {
				if filepath.ToSlash(filepath.Clean(d.Span.File)) == x.file {
					key, e = g.keys[i], x
					break
				}
			}
			te := e.diagnosticError(d)
			if key == "" && te.File == "" {
				// A span in the module's files that no declaration is in.
				te.File = filepath.Join(e.root, filepath.FromSlash(d.Span.File))
			}
			add(key, te)
		}
	}
	return out
}

// MacroDeclAt is the declaration of nodes a macro expanded whose
// body holds line and col, or nil.
func MacroDeclAt(nodes []ast.Node, line, col int) *ast.KeywordDecl {
	for _, n := range nodes {
		kd, ok := n.(*ast.KeywordDecl)
		if !ok || kd.Body == nil || macroExpansionOf(kd) == nil {
			continue
		}
		sl, sc := kd.Body.Line, kd.Body.Col
		el, ec := kd.BodyPosition(len(kd.Body.Value))
		if kd.Body.Triple {
			el, ec = kd.Body.EndLine, kd.Body.EndCol
		}
		after := line > sl || line == sl && col >= sc
		before := line < el || line == el && col <= ec
		if after && before {
			return kd
		}
	}
	return nil
}

// MacroNavigate asks the macro that expanded kd, through its
// Navigation impl, about the position line and col of its body: op is
// "hover", "definition", "complete" or "actions", and diag the diagnostic
// an action is for. A definition's span is answered as a file path, absolute,
// and a position there. Nil when the macro answers nothing.
func MacroNavigate(kd *ast.KeywordDecl, op string, line, col int, diag *TypeError) *MacroAnswer {
	e := macroExpansionOf(kd)
	if e == nil || e.call == nil || e.host == nil {
		return nil
	}
	nav := &MacroNavigation{
		File: e.call.File, Root: e.call.Root, Module: e.call.Module, Name: e.call.Name,
		Decl: e.call.Decl, Config: e.call.Config, Inputs: e.requested, Op: op, Line: line, Col: col,
	}
	if diag != nil {
		file := e.file
		if diag.File != "" {
			if rel, err := filepath.Rel(e.root, diag.File); err == nil {
				file = filepath.ToSlash(rel)
			}
		}
		nav.Diagnostic = MacroDiagnostic{
			Span:    MacroSpan{File: file, Line: diag.Line, Col: diag.Col, EndLine: diag.EndLine, EndCol: diag.EndCol},
			Message: diag.Message, Hints: diag.Hints,
		}
	}
	return e.host.Navigate(nav)
}

// MacroSpanPath is the absolute path of the file a span a macro
// answered names, under the declaring module's root of kd, and whether it
// stays under that root.
func MacroSpanPath(kd *ast.KeywordDecl, span MacroSpan) (string, bool) {
	e := macroExpansionOf(kd)
	if e == nil {
		return "", false
	}
	file := filepath.ToSlash(filepath.Clean(span.File))
	if file == ".." || strings.HasPrefix(file, "../") || filepath.IsAbs(span.File) {
		return "", false
	}
	return filepath.Join(e.root, filepath.FromSlash(file)), true
}

// MacroRoot is the root of the module that declares kd, where a tool
// an action of its macro names runs; ok is false when no macro
// expanded kd.
func MacroRoot(kd *ast.KeywordDecl) (root string, ok bool) {
	e := macroExpansionOf(kd)
	if e == nil {
		return "", false
	}
	return e.root, true
}

// MacroFailed reports whether a macro declined a declaration among
// the files' nodes: it ran, reported an error and declared nothing. What
// that declaration would have declared is missing from its file, so the
// program's other errors follow from the failure (a name another file
// imports, a type a signature names, a call's result) and say nothing the
// macro's own error does not. Like a syntax error in an imported file,
// the failure is reported alone (MacroErrorsAlone), and so is a run the
// compiler refused or stopped (MacroResult.Refused) and a declaration
// whose macro's module did not build (MacroResult.Unbuilt), whose files
// keep their own errors (InUnbuiltMacroModule). A macro that wrote
// ill-formed code is not a declined declaration: the program's errors
// say why.
func MacroFailed(files map[string][]ast.Node) bool {
	for key, nodes := range files {
		if IsStdlibKey(key) {
			continue
		}
		for _, n := range nodes {
			kd, ok := n.(*ast.KeywordDecl)
			if !ok {
				continue
			}
			if e := macroExpansionOf(kd); e != nil && (e.declined || len(e.contractErrors()) > 0) {
				return true
			}
		}
	}
	return false
}

// InUnbuiltMacroModule reports whether the file at path is in the module
// of a macro that did not build for a declaration among the files' nodes
// (MacroResult.Unbuilt). When the program reports its macro declarations'
// errors alone (MacroFailed), such a file keeps its own: they are why the
// macro could not run.
func InUnbuiltMacroModule(files map[string][]ast.Node, path string) bool {
	if path == "" {
		return false
	}
	for key, nodes := range files {
		if IsStdlibKey(key) {
			continue
		}
		for _, n := range nodes {
			kd, ok := n.(*ast.KeywordDecl)
			if !ok {
				continue
			}
			e := macroExpansionOf(kd)
			if e == nil || !e.unbuilt || e.call == nil {
				continue
			}
			if rel, err := filepath.Rel(e.call.Root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// MacroErrorsAlone is what a file reports when a macro declaration
// of its program failed (MacroFailed): the errors its own macro
// declarations report, then checks, its macros' ProgramCheck
// diagnostics.
func MacroErrorsAlone(nodes []ast.Node, checks []TypeError) []TypeError {
	var out []TypeError
	for _, n := range nodes {
		if kd, ok := n.(*ast.KeywordDecl); ok {
			out = append(out, MacroErrors(kd)...)
		}
	}
	return append(out, checks...)
}

// MacroErrors are the errors kd's expansion reports about the
// declaration and its inputs, before its code is checked.
func MacroErrors(kd *ast.KeywordDecl) []TypeError {
	if e := macroExpansionOf(kd); e != nil {
		if contract := e.contractErrors(); len(contract) > 0 {
			return append(slices.Clone(e.errs), contract...)
		}
		return e.errs
	}
	return nil
}

// MacroOf names the macro that expanded kd, as `name` (module m),
// and whether one did.
func MacroOf(kd *ast.KeywordDecl) (name, module string, ok bool) {
	e := macroExpansionOf(kd)
	if e == nil {
		return "", "", false
	}
	return e.name, e.module, true
}
