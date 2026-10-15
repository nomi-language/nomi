package analysis

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// typeVarID matches a type variable's rendering, `?17`. Its number is a
// per-process counter, so a dump renders every unsolved variable as `?`.
var typeVarID = regexp.MustCompile(`\?[0-9]+`)

// DumpFileForTest renders what one file's analysis holds about itself as
// canonical text: the given diagnostics in order, then, each sorted, the type
// the checker gave every expression (by position and node kind), every
// definition's type, what every reference resolves to, the file's own impls
// and demands, and its imports. Two analyses of one file that dump alike hold
// the same facts. root is stripped from the paths the dump names, so dumps of
// one project in two directories compare.
func DumpFileForTest(fa *FileAnalysis, errs []TypeError, root string) string {
	path := func(p string) string { return strings.TrimPrefix(p, root) }
	typ := func(t Type) string {
		if t == nil || isNilType(t) {
			return "<nil>"
		}
		return typeVarID.ReplaceAllString(t.String(), "?")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "file %s origin %s\n", path(fa.FilePath), fa.Origin)
	for _, e := range errs {
		fmt.Fprintf(&b, "diag %d:%d %s\n", e.Line, e.Col, e.Message)
		for _, h := range e.Hints {
			fmt.Fprintf(&b, "  hint %s\n", h)
		}
		for _, r := range e.Related {
			fmt.Fprintf(&b, "  related %s:%d:%d %s\n", path(r.File), r.Line, r.Col, r.Message)
		}
	}
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
		lines = append(lines, fmt.Sprintf("ref %d:%d %s -> %s %d at %s:%d:%d", pos.Line, pos.Col, sym.Name, real.Name, real.Kind, path(real.SourceFile), real.Pos.Line, real.Pos.Col))
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
	return b.String()
}

// UnionDemandsForTest exposes unionDemands, the fold of files' own
// ImplManifests that MissingImpls reads, to external _test packages.
func UnionDemandsForTest(files []*FileAnalysis) map[string]map[string][]Recording {
	return unionDemands(files)
}

// ProgramDemandsForTest answers the union of every ImplManifest of fa's
// program, each file's own, bucket by bucket.
func ProgramDemandsForTest(fa *FileAnalysis) map[string]map[string][]Recording {
	out := map[string]map[string][]Recording{}
	files := fa.Program().Files()
	if len(files) == 0 {
		files = []*FileAnalysis{fa}
	}
	for _, file := range files {
		for iface, byType := range file.ImplManifest {
			if out[iface] == nil {
				out[iface] = map[string][]Recording{}
			}
			for typeName, recs := range byType {
				out[iface][typeName] = append(out[iface][typeName], recs...)
			}
		}
	}
	return out
}
