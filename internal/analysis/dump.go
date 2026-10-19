package analysis

import (
	"cmp"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/parser"
)

// This file renders analyses as canonical text for tests that compare two
// analyses of the same sources. Only tests call it, in this package and
// outside it (an incremental check is compared with a full one), so the
// linker leaves it out of every binary.

// typeVarID matches a type variable's rendering, `?17`. Its number is a
// per-process counter, so a dump renders every unsolved variable as `?`.
var typeVarID = regexp.MustCompile(`\?[0-9]+`)

// dumpType renders t for a dump.
func dumpType(t Type) string {
	if t == nil || isNilType(t) {
		return "<nil>"
	}
	return typeVarID.ReplaceAllString(t.String(), "?")
}

// dumpErrors renders errs in order, prefix starting each.
func dumpErrors(b *strings.Builder, prefix string, errs []TypeError, path func(string) string) {
	for _, e := range errs {
		fmt.Fprintf(b, "%s%d:%d-%d:%d %s", prefix, e.Line, e.Col, e.EndLine, e.EndCol, e.Message)
		if e.Code != "" {
			fmt.Fprintf(b, " [%s]", e.Code)
		}
		b.WriteByte('\n')
		for _, h := range e.Hints {
			fmt.Fprintf(b, "  hint %s\n", h)
		}
		for _, r := range e.Related {
			fmt.Fprintf(b, "  related %s:%d:%d %s\n", path(r.File), r.Line, r.Col, r.Message)
		}
	}
}

// DumpFileForTest renders what one file's analysis holds about itself as
// canonical text: the given diagnostics in order, then, each sorted, the type
// the checker gave every expression (by position and node kind), every
// definition's type, what every reference resolves to and the type, call and
// dispatch it carries, every other fact the checker records about a body
// node (expected types, target structs, field accessors, lambda patterns,
// subtests rows, application-field reads, punned labels, inferred bounds,
// type-method modules, block imports), the file's own impls and demands, its
// imports, and its scopes from the module scope down. Two analyses of one file
// that dump alike hold
// the same facts. root is stripped from the paths the dump names, so dumps of
// one project in two directories compare.
func DumpFileForTest(fa *FileAnalysis, errs []TypeError, root string) string {
	path := func(p string) string { return strings.TrimPrefix(p, root) }
	typ := dumpType
	var b strings.Builder
	fmt.Fprintf(&b, "file %s origin %s\n", path(fa.FilePath), fa.Origin)
	dumpErrors(&b, "diag ", errs, path)
	section := func(lines []string) {
		sort.Strings(lines)
		for _, l := range lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	var lines []string
	for n, t := range fa.ExprTypes {
		line, col := nodeLineCol(n)
		lines = append(lines, fmt.Sprintf("expr %d:%d %s = %s", line, col, n.NodeType(), typ(t)))
	}
	section(lines)
	lines = lines[:0]
	for pos, sym := range fa.Definitions {
		if sym == nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("def %d:%d %s %d = %s in %s", pos.Line, pos.Col, sym.Name, sym.Kind, typ(sym.Type), path(sym.SourceFile)))
	}
	section(lines)
	lines = lines[:0]
	for pos, sym := range fa.References {
		if sym == nil {
			continue
		}
		real := sym
		for real.Resolved != nil {
			real = real.Resolved
		}
		line := fmt.Sprintf("ref %d:%d %s -> %s %d at %s:%d:%d : %s", pos.Line, pos.Col, sym.Name, real.Name, real.Kind, path(real.SourceFile), real.Pos.Line, real.Pos.Col, typ(sym.Type))
		if sym.CallType != nil {
			line += " call " + typ(sym.CallType)
		}
		if sym.VariantType != nil {
			line += " variant " + typ(sym.VariantType)
		}
		if sym.DispatchImpl != nil {
			l, c := nodeLineCol(sym.DispatchImpl)
			line += fmt.Sprintf(" dispatch %d:%d %s", l, c, typ(sym.DispatchReceiver))
		}
		lines = append(lines, line)
	}
	section(lines)
	lines = lines[:0]
	at := func(n ast.Node) string {
		l, c := nodeLineCol(n)
		return fmt.Sprintf("%d:%d %s", l, c, n.NodeType())
	}
	for n, t := range fa.ExpectedTypes {
		lines = append(lines, fmt.Sprintf("expected %s = %s", at(n), typ(t)))
	}
	for n, st := range fa.TargetStructs {
		lines = append(lines, fmt.Sprintf("target %s = %s", at(n), typ(st)))
	}
	for n, t := range fa.FieldAccessors {
		lines = append(lines, fmt.Sprintf("accessor %s = %s", at(n), typ(t)))
	}
	for p, length := range fa.Placeholders {
		lines = append(lines, fmt.Sprintf("placeholder %d:%d length %d", p.Line, p.Col, length))
	}
	for n, t := range fa.PatternTypes {
		lines = append(lines, fmt.Sprintf("pattern %s = %s", at(n), typ(t)))
	}
	for n, t := range fa.LambdaPatternTypes {
		lines = append(lines, fmt.Sprintf("lambda pattern %s = %s", at(n), typ(t)))
	}
	for n, t := range fa.SubtestsRowTypes {
		lines = append(lines, fmt.Sprintf("subtests %s = %s", at(n), typ(t)))
	}
	for n := range fa.CoveringOrPatterns {
		lines = append(lines, fmt.Sprintf("covering or %s", at(n)))
	}
	for n, r := range fa.AppReads {
		lines = append(lines, fmt.Sprintf("app read %s = %s.%s %s", at(n), typ(r.App), r.Field.Name, r.ContextField))
	}
	for pos, r := range fa.AppReadsByPos {
		lines = append(lines, fmt.Sprintf("app read %d:%d = %s.%s %s", pos.Line, pos.Col, typ(r.App), r.Field.Name, r.ContextField))
	}
	for pos, sym := range fa.PunnedFieldLabels {
		if sym != nil {
			lines = append(lines, fmt.Sprintf("punned %d:%d -> %s at %d:%d", pos.Line, pos.Col, sym.Name, sym.Pos.Line, sym.Pos.Col))
		}
	}
	for fn, byParam := range fa.InferredTypeParamBounds {
		for param, ifaces := range byParam {
			lines = append(lines, fmt.Sprintf("inferred bound %s %s: %s", at(fn), param, strings.Join(ifaces, " ")))
		}
	}
	for key := range fa.TypeMethodModules {
		lines = append(lines, "type method module "+key)
	}
	for imp, scope := range fa.BlockImportScopes {
		lines = append(lines, fmt.Sprintf("block import %d:%d scope %d:%d-%d:%d", imp.Line, imp.Col, scope.Start.Line, scope.Start.Col, scope.End.Line, scope.End.Col))
	}
	section(lines)
	lines = lines[:0]
	for typeName, ifaces := range fa.Impls {
		for iface := range ifaces {
			lines = append(lines, fmt.Sprintf("impl %s for %s", iface, typeName))
		}
	}
	for iface, byType := range fa.ImplManifest {
		for typeName, recs := range byType {
			for _, rec := range recs {
				lines = append(lines, fmt.Sprintf("demand %s for %s at %s:%d:%d kind %d", iface, typeName, path(rec.Pos.File), rec.Pos.Line, rec.Pos.Col, rec.Kind))
			}
		}
	}
	for typeName, byName := range fa.TypeMethods {
		for name, sym := range byName {
			if sym == nil {
				continue
			}
			lines = append(lines, fmt.Sprintf("method %s.%s = %s", typeName, name, typ(sym.Type)))
		}
	}
	section(lines)
	fmt.Fprintf(&b, "imports %s\n", strings.Join(fa.ImportedModules, " "))
	for _, ii := range fa.ImplImports {
		fmt.Fprintf(&b, "impl import %d:%d unused %v\n", ii.Pos.Line, ii.Pos.Col, fa.Program().ImplImportUnused(ii))
	}
	if fa.ModuleScope != nil {
		b.WriteString("scopes\n")
		dumpScope(&b, fa.ModuleScope, 1)
	}
	return b.String()
}

// dumpScope renders s and the scopes under it, one a line indented by depth:
// its span, the receiver it displays, and its symbols by name with their
// kind, position and type.
func dumpScope(b *strings.Builder, s *Scope, depth int) {
	b.WriteString(strings.Repeat("  ", depth))
	fmt.Fprintf(b, "scope %d:%d-%d:%d", s.Start.Line, s.Start.Col, s.End.Line, s.End.Col)
	if s.ReceiverDisplay != "" {
		fmt.Fprintf(b, " receiver %s", s.ReceiverDisplay)
	}
	names := make([]string, 0, len(s.Symbols))
	for name := range s.Symbols {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sym := s.Symbols[name]
		fmt.Fprintf(b, " %s=%d@%d:%d:%s", name, sym.Kind, sym.Pos.Line, sym.Pos.Col, dumpType(sym.Type))
	}
	b.WriteByte('\n')
	for _, c := range s.Children {
		dumpScope(b, c, depth+1)
	}
}

// DumpProgramForTest renders a checked program as canonical text, every
// position by file, line and column and every declaration by name and
// position, never by pointer, so two checks of the same texts dump alike
// byte for byte. In order:
//
//   - each file, entry first: its syntax errors, its diagnostics in the
//     order the front end reports them, its analysis (DumpFileForTest), and
//     its syntax tree as the checks left it (the derives and Debug impls
//     synthesized, tail calls marked);
//   - the program's own errors (Program.Errors);
//   - every `once` of the program, with its type and whether it was
//     inferred, and the cycle an unannotated one's value reads itself
//     through;
//   - the diagnostics the front end prints, in its order: the entry's syntax
//     errors alone when it has any, then any other file's alone, and
//     otherwise the entry's diagnostics and then each other file's.
func DumpProgramForTest(p *CheckedProgram) string {
	root := p.Root
	path := func(s string) string { return strings.TrimPrefix(s, root) }
	var b strings.Builder
	fmt.Fprintf(&b, "program entry %s\n", p.Entry)
	keys := p.Keys()
	for _, key := range keys {
		fa := p.Files[key]
		fmt.Fprintf(&b, "\n== file %s\n", key)
		if fa == nil {
			b.WriteString("(not reached)\n")
			continue
		}
		dumpErrors(&b, "syntax ", syntaxErrors(p.Syntax[key]), path)
		b.WriteString(DumpFileForTest(fa, p.Errors[key], root))
		b.WriteString("syntax tree\n")
		for _, n := range p.Nodes[key] {
			dumpSyntax(&b, n, 1)
		}
	}
	var prog *Program
	if fa := p.Files[p.Entry]; fa != nil {
		prog = fa.Program()
	}
	b.WriteString("\n== program\n")
	if prog != nil {
		dumpErrors(&b, "error ", prog.Errors, path)
		dumpOnces(&b, prog.Onces, path)
	}
	b.WriteString("\n== printed\n")
	for _, d := range printedDiagnostics(p) {
		dumpErrors(&b, d.file+":", []TypeError{d.err}, path)
	}
	return b.String()
}

// syntaxErrors are a parse's errors as type errors, for a dump.
func syntaxErrors(errs []parser.ParseError) []TypeError {
	out := make([]TypeError, len(errs))
	for i, e := range errs {
		out[i] = TypeError{Line: e.Line, Col: e.Col, Message: e.Message, Hints: e.Hints}
	}
	return out
}

// printedDiagnostic is one diagnostic the front end prints, with the module
// key of its file.
type printedDiagnostic struct {
	file string
	err  TypeError
}

// printedDiagnostics are the diagnostics the front end prints for p, in its
// order (frontend.Checker.check and Analyze): a syntax error in the entry
// stops it before analysis, a syntax error in another file is reported alone,
// and otherwise the entry's diagnostics come first and then each other
// file's, in key order.
func printedDiagnostics(p *CheckedProgram) []printedDiagnostic {
	var out []printedDiagnostic
	keys := p.Keys()
	keyOf := map[string]string{}
	for key, fa := range p.Files {
		keyOf[fa.FilePath] = key
	}
	add := func(key string, errs []TypeError) {
		for _, e := range errs {
			// The front end prints an error with the file its position
			// is in (TypeError.File).
			if k, ok := keyOf[e.File]; ok && e.File != "" {
				out = append(out, printedDiagnostic{k, e})
				continue
			}
			out = append(out, printedDiagnostic{key, e})
		}
	}
	if errs := p.Syntax[p.Entry]; len(errs) > 0 {
		add(p.Entry, syntaxErrors(errs))
		return out
	}
	for _, key := range keys[1:] {
		add(key, syntaxErrors(p.Syntax[key]))
	}
	if len(out) > 0 {
		return out
	}
	for _, key := range keys {
		add(key, p.Errors[key])
	}
	return out
}

// dumpOnces renders every `once` of t in file and position order.
func dumpOnces(b *strings.Builder, t *OnceTable, path func(string) string) {
	if t == nil {
		return
	}
	type once struct {
		file      string
		n         *ast.OnceBinding
		fa        *FileAnalysis
		owner     string
		annotated bool
	}
	var all []once
	for n, site := range t.sites {
		if site.fa == nil {
			continue
		}
		all = append(all, once{path(site.fa.FilePath), n, site.fa, site.owner, n.TypeAnnotation != nil})
	}
	slices.SortFunc(all, func(a, b once) int {
		return cmp.Or(cmp.Compare(a.file, b.file), cmp.Compare(a.n.Line, b.n.Line), cmp.Compare(a.n.Col, b.n.Col))
	})
	for _, o := range all {
		sym := o.fa.Definitions[Pos{Line: o.n.Line, Col: o.n.Col}]
		if sym == nil && o.fa.ModuleScope != nil {
			sym = o.fa.ModuleScope.Lookup(o.n.Name)
		}
		var ty Type
		if sym != nil {
			ty = sym.Type
		}
		how := "inferred"
		if o.annotated {
			how = "annotated"
		}
		owner := ""
		if o.owner != "" {
			owner = o.owner + "."
		}
		fmt.Fprintf(b, "once %s:%d:%d %s%s %s = %s", o.file, o.n.Line, o.n.Col, owner, o.n.Name, how, dumpType(ty))
		if cycle := t.cycles[o.n]; cycle != nil {
			fmt.Fprintf(b, " cycle %s", strings.Join(cycle, " -> "))
		}
		b.WriteByte('\n')
	}
}

// dumpSyntax renders the tree under n, one node a line indented by depth:
// its kind and the scalar fields it holds (names, positions, literal values,
// the flags the checks set), so two trees that dump alike are the same
// program.
func dumpSyntax(b *strings.Builder, n ast.Node, depth int) {
	b.WriteString(strings.Repeat("  ", depth))
	b.WriteString(n.NodeType())
	if v := reflect.ValueOf(n); v.Kind() == reflect.Pointer && !v.IsNil() {
		dumpScalars(b, "", v.Elem())
	}
	b.WriteByte('\n')
	ast.Children(n, func(c ast.Node) { dumpSyntax(b, c, depth+1) })
}

// dumpScalars renders v's exported scalar fields, and those of the plain
// structs it embeds or holds, as ` Name=value`.
func dumpScalars(b *strings.Builder, prefix string, v reflect.Value) {
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		fv := v.Field(i)
		name := prefix + f.Name
		switch fv.Kind() {
		case reflect.String, reflect.Bool,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			if !fv.IsZero() {
				fmt.Fprintf(b, " %s=%q", name, fmt.Sprint(fv.Interface()))
			}
		case reflect.Struct:
			if _, isNode := fv.Addr().Interface().(ast.Node); !isNode || f.Anonymous {
				dumpScalars(b, name+".", fv)
			}
		case reflect.Slice:
			if fv.Len() > 0 && fv.Type().Elem().Kind() == reflect.String {
				fmt.Fprintf(b, " %s=%q", name, fmt.Sprint(fv.Interface()))
			}
		}
	}
}
