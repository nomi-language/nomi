package vmhost

import (
	"sort"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/irbuild"
)

// importersToo answers the paths of prog's files a lowering from the
// document's analysis lowers again after the changed files' edits
// (relowerAnalyzed): the changed ones, and every file that imports one
// whose interface changed (signature), directly or through others, since
// it was lowered against the old interface; each comes after the files
// among them it imports. ok is false when they are more than
// maxRelowered and a quarter of the program, whose lowerings one
// by one cost more than one whole lowering.
func importersToo(prog *irbuild.Program, changed, signature, seers map[string]bool) (paths []string, ok bool) {
	pathOf := map[string]string{}
	imports := map[string][]string{}
	for _, m := range prog.Modules {
		pathOf[m.Name] = m.Path
		if m.FA != nil {
			imports[m.Path] = m.FA.ImportedModules
		}
	}
	importers := map[string][]string{}
	for path, keys := range imports {
		for _, k := range keys {
			if p := pathOf[k]; p != "" {
				importers[p] = append(importers[p], path)
			}
		}
	}
	lower := map[string]bool{}
	for path := range changed {
		lower[path] = true
	}
	for path := range seers {
		lower[path] = true
	}
	queue := make([]string, 0, len(signature))
	for path := range signature {
		queue = append(queue, path)
	}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, from := range importers[p] {
			if !lower[from] {
				lower[from] = true
				queue = append(queue, from)
			}
		}
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
		for _, k := range imports[p] {
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

// seersOf answers the paths of prog's files that see an impl whose header
// names the files of one of headers: each of those files is the file or one
// it imports, directly or through others.
func seersOf(prog *irbuild.Program, headers [][]string) map[string]bool {
	if len(headers) == 0 {
		return nil
	}
	imports := map[string][]string{}
	for _, m := range prog.Modules {
		if m.FA != nil {
			imports[m.Name] = m.FA.ImportedModules
		}
	}
	out := map[string]bool{}
	for _, m := range prog.Modules {
		visible := importClosureOf(m.Name, imports)
		for _, h := range headers {
			if seesHeader(analysis.ImplHeader{Files: h}, visible) {
				out[m.Path] = true
				break
			}
		}
	}
	return out
}
