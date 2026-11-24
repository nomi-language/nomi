package vmhost

import (
	"sort"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/irbuild"
)

// readersToo answers the paths of prog's files a lowering from the
// document's analysis lowers again after the changed files' edits
// (relowerAnalyzed): the changed ones, and every file whose check reads a
// file whose interface changed (signature) or sees an impl of moved, the
// impls that came, went or changed (analysis.ProgramReads.Readers), since
// it was lowered against the old ones; each comes after the files among
// them it imports. headers answers the impl headers of prog's files by
// path. ok is false when they are more than maxRelowered and a quarter of
// the program, whose lowerings one by one cost more than one whole
// lowering.
func readersToo(prog *irbuild.Program, changed, signature map[string]bool, moved []analysis.ImplHeader, headers func() map[string][]analysis.ImplHeader) (paths []string, ok bool) {
	pathOf, nameOf := map[string]string{}, map[string]string{}
	imports := map[string][]string{}
	var keys []string
	for _, m := range prog.Modules {
		pathOf[m.Name], nameOf[m.Path] = m.Path, m.Name
		keys = append(keys, m.Name)
		if m.FA != nil {
			imports[m.Name] = m.FA.ImportedModules
		}
	}
	lower := map[string]bool{}
	for path := range changed {
		lower[path] = true
	}
	changedKeys := map[string]bool{}
	for path := range signature {
		changedKeys[nameOf[path]] = true
	}
	reads := analysis.ProgramReads{
		Keys:    keys,
		Imports: func(key string) []string { return imports[key] },
		Headers: func(key string) []analysis.ImplHeader { return headers()[pathOf[key]] },
	}
	for key := range reads.Readers(changedKeys, moved) {
		lower[pathOf[key]] = true
	}
	if len(lower) > maxRelowered && len(lower)*4 > len(prog.Modules) {
		return nil, false
	}
	sorted := make([]string, 0, len(lower))
	for p := range lower {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	done := map[string]bool{}
	var visit func(p string)
	visit = func(p string) {
		if done[p] {
			return
		}
		done[p] = true
		for _, k := range imports[nameOf[p]] {
			if q := pathOf[k]; lower[q] {
				visit(q)
			}
		}
		paths = append(paths, p)
	}
	for _, p := range sorted {
		visit(p)
	}
	return paths, true
}
