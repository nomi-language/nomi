package analysis

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// The functions the compiler calls to run a macro.
//
// A macro's impls are on a type its file may keep private, so what
// calls them must be in that file. When the compiler builds a macro
// module to run it (frontend.Config.MacroDrivers), each `macro name:
// Type` declaration of the entry gains hidden functions, named `name$part`,
// which no source can write or call:
//
//   - name$declaration builds a Declaration from values the compiler
//     passes (a Maybe field as a flag and a value, the body's parts one by
//     one);
//   - name$request and name$expand call Type's `Macro` impl, with the
//     configuration's JSON text decoded, each with its one record
//     (RequestCall, ExpandCall) built from the arguments;
//   - name$check_all calls its `ProgramCheck` impl, and name$hover,
//     name$definition, name$complete and name$actions its `Navigation`
//     impl, when the file implements them, the same way.
//
// A call `Type.request(...)` reaches the impl's own function or, when the
// impl leaves it out, the interface's default (std/macro gives `request`
// and each `Navigation` function one), so a driver is the same whichever
// the impl writes.
//
// The driver reaches std/macro through a hidden import of its own.

// MacroDriverName is the hidden function that runs part of the
// macro name.
func MacroDriverName(name, part string) string { return name + "$" + part }

const macroDriverImports = `import std/macro as macro__
`

const macroDriverTemplate = `fn NAME__declaration(keyword: String, name: String, public: Bool, params: List<macro__.Param>, has_result: Bool, result: macro__.TypeText, has_body: Bool, body_text: String, body_span: macro__.Span, start_line: Int, start_col: Int, indent_col: Int, has_tag: Bool, tag: String, tag_span: macro__.Span, doc: String, file: String, span: macro__.Span, name_span: macro__.Span) -> macro__.Declaration {
    body = #macro__.Body{
        text: body_text,
        span: body_span,
        start_line,
        start_col,
        indent_col,
        tag: if has_tag { Some(tag) } else { None },
        tag_span: if has_tag { Some(tag_span) } else { None },
    }

    #macro__.Declaration{
        keyword,
        name,
        public,
        params,
        result: if has_result { Some(result) } else { None },
        body: if has_body { Some(body) } else { None },
        doc,
        file,
        span,
        name_span,
    }
}

fn NAME__request(decl: macro__.Declaration, config: String) -> macro__.Request {
    IMPL.request(#macro__.RequestCall{decl, config: macro__.config(config)})
}

fn NAME__expand(decl: macro__.Declaration, config: String, inputs: List<macro__.Input>, sites: List<macro__.Site>) -> macro__.Expansion {
    IMPL.expand(#macro__.ExpandCall{decl, config: macro__.config(config), inputs, sites})
}
`

const macroDriverCheckTemplate = `fn NAME__check_all(decls: List<macro__.Declaration>, config: String, inputs: List<macro__.Input>) -> List<macro__.Diagnostic> {
    IMPL.check_all(#macro__.CheckCall{decls, config: macro__.config(config), inputs})
}
`

const macroDriverNavigationTemplate = `fn NAME__hover(decl: macro__.Declaration, config: String, inputs: List<macro__.Input>, line: Int, col: Int) -> Maybe<String> {
    IMPL.hover(#macro__.NavigationCall{decl, config: macro__.config(config), inputs, line, col})
}

fn NAME__definition(decl: macro__.Declaration, config: String, inputs: List<macro__.Input>, line: Int, col: Int) -> Maybe<macro__.Span> {
    IMPL.definition(#macro__.NavigationCall{decl, config: macro__.config(config), inputs, line, col})
}

fn NAME__complete(decl: macro__.Declaration, config: String, inputs: List<macro__.Input>, line: Int, col: Int) -> List<macro__.Completion> {
    IMPL.complete(#macro__.NavigationCall{decl, config: macro__.config(config), inputs, line, col})
}

fn NAME__actions(decl: macro__.Declaration, config: String, inputs: List<macro__.Input>, diagnostic: macro__.Diagnostic) -> List<macro__.Action> {
    IMPL.actions(#macro__.ActionsCall{decl, config: macro__.config(config), inputs, diagnostic})
}
`

// macroDriverAlias is the hidden import's alias.
const macroDriverAlias = "macro$"

// MacroDrivers answers the driver functions of the macro
// declarations among nodes, an entry's nodes, and their hidden import; nil
// when nodes declare no macro.
func MacroDrivers(nodes []ast.Node) []ast.Node {
	implements := func(iface, impl string) bool {
		for _, n := range nodes {
			if b, ok := n.(*ast.ImplBlock); ok && b.Interface != nil && TypeExprBaseName(b.Interface) == iface && TypeExprBaseName(b.Receiver) == impl {
				return true
			}
		}
		return false
	}
	var src strings.Builder
	src.WriteString(macroDriverImports)
	var names []string
	for _, n := range nodes {
		d, ok := n.(*ast.MacroDecl)
		if !ok || d.Impl == nil {
			continue
		}
		impl := d.Impl.TypeString()
		text := macroDriverTemplate
		if implements("ProgramCheck", TypeExprBaseName(d.Impl)) {
			text += macroDriverCheckTemplate
		}
		if implements("Navigation", TypeExprBaseName(d.Impl)) {
			text += macroDriverNavigationTemplate
		}
		src.WriteString("\n" + strings.ReplaceAll(strings.ReplaceAll(text, "NAME__", d.Name+"__"), "IMPL", impl))
		names = append(names, d.Name)
	}
	if len(names) == 0 {
		return nil
	}
	driver, err := parser.Parse(lexer.Lex(src.String()))
	if err != nil {
		panic("analysis: a macro driver does not parse: " + err.Error())
	}
	for _, n := range driver {
		ast.Inspect(n, func(m ast.Node) bool {
			switch v := m.(type) {
			case *ast.ImportStmt:
				v.ModuleAlias = &ast.Ident{Name: macroDriverAlias, Line: v.Line, Col: v.Col}
			case *ast.FuncDef:
				for _, name := range names {
					if part, ok := strings.CutPrefix(v.Name, name+"__"); ok {
						v.Name = MacroDriverName(name, part)
					}
				}
			case *ast.FieldAccess:
				if id, ok := v.Object.(*ast.Ident); ok && id.Name == "macro__" {
					id.Name = macroDriverAlias
				}
			case *ast.QualifiedType:
				if v.Module == "macro__" {
					v.Module = macroDriverAlias
				}
			}
			return true
		})
	}
	// The drivers are positioned at the slot after the file's last source
	// node, which nothing else of the file takes.
	shiftLines(driver, synthSlot(synthOriginDecl, len(nodes), synthKindExpansion))
	return driver
}
