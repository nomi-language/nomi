package lsp

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// structFieldCandidates offers the fields a struct literal has not written
// yet, `Point{x: 1, ` offering `y`, with their types. The literal's type is
// its named type, its spread's (`{..base, `), or the type its position
// expects, and a bare brace's (`{‸}`, completion_bare_brace.go) the type its
// position expects. Fields without a default rank first, in declaration
// order. A patch (a spread, or a bare brace at a field under one) needs no
// field, so its fields keep declaration order.
func (r *completionRequest) structFieldCandidates() []candidate {
	written := map[string]bool{}
	t, hop := r.braceType, r.braceHop
	if lit := r.ctx.structLit; lit != nil {
		for _, f := range lit.Fields {
			if !strings.Contains(f.Name, completionSentinel) {
				written[f.Name] = true
			}
		}
		t, hop = r.structLitType(r.ctx.hit, 1, lit), 1
	}
	patch := underPatch(r.ctx.hit, hop)
	var out []candidate
	for i, c := range fieldCandidates(fieldsOf(t), r.declOf(t)) {
		f, _ := fieldOf(t, c.label)
		if written[c.label] {
			continue
		}
		c.insert = c.label + ": "
		c.order = i
		if f.HasDefault && !patch {
			c.order += 1000
		}
		out = append(out, c)
	}
	return out
}

// caseArmCandidates offers the head of a case arm. Over an enum subject it
// offers one item that writes every arm the case is missing, laid out as
// `nomi fmt` lays arms out (one per line at the arm's indent), followed by
// each missing variant's pattern. Over any other subject it offers the
// variants and types in scope.
func (r *completionRequest) caseArmCandidates() []candidate {
	c := r.ctx.caseNode
	var et *analysis.EnumType
	if c != nil && c.Value != nil {
		et, _ = analysis.ResolveTypeVar(r.exprType(c.Value)).(*analysis.EnumType)
	}
	if et == nil {
		return r.scopeCandidates(func(sym *analysis.Symbol) bool {
			k := realSymbol(sym).Kind
			return k == analysis.SymbolEnumVariant || k == analysis.SymbolEnum || k == analysis.SymbolStruct
		})
	}
	covered, all := coveredVariants(c, r.ctx.branch)
	if all {
		return nil
	}
	var missing []analysis.VariantDef
	for _, v := range et.Variants {
		if !covered[v.Name] {
			missing = append(missing, v)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	indent := r.lineIndent()
	var out []candidate
	if len(missing) > 1 {
		var snippet, plain strings.Builder
		n := 1
		for i, v := range missing {
			if i > 0 {
				snippet.WriteString("\n" + indent)
				plain.WriteString("\n" + indent)
			}
			pat, next := variantPattern(v, n, true)
			snippet.WriteString(fmt.Sprintf("%s -> ${%d}", pat, next))
			n = next + 1
			plainPat, _ := variantPattern(v, 0, false)
			plain.WriteString(plainPat + " -> ")
		}
		out = append(out, candidate{
			label:    "all missing arms",
			kind:     protocol.CompletionItemKindSnippet,
			detail:   fmt.Sprintf("%d arms of %s", len(missing), et.Name),
			insert:   plain.String(),
			snippet:  snippet.String(),
			filter:   missing[0].Name,
			locality: -1,
			asIs:     true,
			typ:      et,
		})
	}
	members := map[string]*analysis.Symbol{}
	if sym := r.enumSymbol(et); sym != nil {
		members = sym.Members
	}
	for i, v := range missing {
		pat, next := variantPattern(v, 1, true)
		plainPat, _ := variantPattern(v, 0, false)
		out = append(out, candidate{
			label:   "." + v.Name,
			kind:    protocol.CompletionItemKindEnumMember,
			sym:     realSymbol(members[v.Name]),
			insert:  plainPat + " -> ",
			snippet: fmt.Sprintf("%s -> ${%d}", pat, next),
			filter:  v.Name,
			order:   i,
		})
	}
	return out
}

// coveredVariants lists the variants the case's other arms already match,
// and reports a catch-all arm, after which no arm is missing.
func coveredVariants(c *ast.Case, skip int) (map[string]bool, bool) {
	covered := map[string]bool{}
	for i, b := range c.Branches {
		if i == skip {
			continue
		}
		switch p := b.Pattern.(type) {
		case *ast.WildcardPattern:
			if b.Guard == nil {
				return covered, true
			}
		case *ast.IdentPattern:
			if b.Guard == nil && !ast.IsPublic(p.Name) {
				return covered, true
			}
			covered[p.Name] = true
		case *ast.DotVariant:
			covered[p.Name] = true
		case *ast.EnumPattern:
			covered[variantNameOf(p.Variant)] = true
		case *ast.StructPattern:
			covered[variantNameOf(p.TypeName)] = true
		}
	}
	return covered, false
}

// variantNameOf reads the variant a pattern head names: `.Red`, `Red`,
// `Color.Red`.
func variantNameOf(te ast.TypeExpr) string {
	switch v := te.(type) {
	case *ast.DotVariantType:
		return v.Name
	case *ast.SimpleType:
		if i := strings.LastIndexByte(v.Name, '.'); i >= 0 {
			return v.Name[i+1:]
		}
		return v.Name
	case *ast.QualifiedType:
		return variantNameOf(v.Member)
	}
	return ""
}

// variantPattern renders the dot-leading pattern that matches v and binds its
// payload: `.Point`, `.Some(value)`, `.Rect{w, h}`. As a snippet each binding
// is a placeholder numbered from n; it returns the next free number.
func variantPattern(v analysis.VariantDef, n int, snippet bool) (string, int) {
	hole := func(name string) string {
		if !snippet {
			return name
		}
		s := fmt.Sprintf("${%d:%s}", n, snippetEscape(name))
		n++
		return s
	}
	switch v.Kind {
	case analysis.VariantPositional:
		return "." + v.Name + "(" + hole("value") + ")", n
	case analysis.VariantStruct:
		return "." + v.Name + "{" + fieldNames(v.Fields, hole) + "}", n
	case analysis.VariantEmbedded:
		if st, ok := analysis.ResolveTypeVar(v.Embedded).(*analysis.StructType); ok {
			return "." + v.Name + "{" + fieldNames(st.Fields, hole) + "}", n
		}
		return "." + v.Name + "(" + hole("value") + ")", n
	}
	return "." + v.Name, n
}

func fieldNames(fields []analysis.FieldDef, hole func(string) string) string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = hole(f.Name)
	}
	return strings.Join(names, ", ")
}

// lineIndent is the leading whitespace of the cursor's line.
func (r *completionRequest) lineIndent() string {
	offs := r.lines.starts
	start := offs[lineIndexOf(offs, r.ctx.start)]
	end := start
	for end < len(r.doc.Content) && (r.doc.Content[end] == ' ' || r.doc.Content[end] == '\t') {
		end++
	}
	return r.doc.Content[start:end]
}

// bootCandidates offers, after a group's `boot`, the entry boots the
// document can call: an imported entry file's (`server.boot`) and its own,
// ahead of every other name in scope.
func (r *completionRequest) bootCandidates() []candidate {
	var out []candidate
	isBoot := func(sym *analysis.Symbol) bool {
		fn, ok := realSymbol(sym).Node.(*ast.FuncDef)
		return ok && r.fa.BootFunctions[fn]
	}
	for name, sym := range r.fa.ModuleScope.Symbols {
		real := realSymbol(sym)
		switch {
		case real.Kind == analysis.SymbolModule && real.ModuleScope != nil:
			if boot := real.ModuleScope.LookupLocal("boot"); boot != nil && isBoot(boot) {
				out = append(out, candidate{label: name + ".boot", kind: protocol.CompletionItemKindFunction, sym: boot, filter: name, locality: -1})
			}
		case name == "boot" && isBoot(sym):
			out = append(out, candidate{label: "boot", kind: protocol.CompletionItemKindFunction, sym: sym, locality: -1})
		}
	}
	return append(out, r.scopeCandidates(valueSymbol)...)
}

// testGroupLine renders a `tests` group line for the keyword kw, its body
// indented one formatter level below the line.
func testGroupLine(kw, indent string) (snippet, plain string) {
	inner := indent + strings.Repeat(" ", format.IndentWidth)
	switch kw {
	case "clock":
		return "clock .${1|Virtual,System|}", "clock .Virtual"
	case "boot":
		return "boot ${1:entry}.boot(${2:startup})", "boot "
	case "setup":
		return "setup {\n" + inner + "$0\n" + indent + "}", "setup {\n" + inner + "\n" + indent + "}"
	case "test":
		return "test \"${1:name}\" {\n" + inner + "assert $0\n" + indent + "}", "test \"\" {\n" + inner + "\n" + indent + "}"
	}
	return kw, kw
}
