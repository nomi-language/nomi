package analysis

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// memberProgramOf is the EntryProgram of the file with module key key, a
// file of p other than its entry, or nil when it is not offered. It is the
// program `nomi check` builds with that file as its entry: the file and
// every project file it imports, directly or through others, with the
// analyses p's check made of them.
//
// A file's analysis in p is what the front end makes of it in its own
// program when nothing in p outside that program changes what the front
// end decides about it. So it is offered only when:
//
//   - p may be lowered for its own entry (programClean): no file has a
//     syntax error or a diagnostic, and the build read no file of another
//     module;
//   - no file of the program imports a whole project file that no name in
//     it uses, which the front end may judge by the impls the rest of p
//     uses (CheckImplImports);
//   - no file of the program reads application fields, which an entry's
//     boot answers;
//   - no file of p outside the program brings an impl the program could
//     use (bringsForeignImpls): one of an interface for a type the file
//     does not declare itself. An impl for one of its own types serves
//     only code that names the type, and no file of the program can.
//
// The entry's other checks (iteration, concurrency, boot scope, task
// lifetime) are the file's own diagnostics in p (documentBuild), and a
// file with one is not lowered (vmhost's analyzedProgram).
func memberProgramOf(p *CheckedProgram, key, manifest string) *EntryProgram {
	if len(p.Files) == 1 || !programClean(p) {
		return nil
	}
	members := map[string]bool{key: true}
	queue := []string{key}
	reachesEntry := false
	for len(queue) > 0 {
		fa := p.Files[queue[0]]
		queue = queue[1:]
		if len(fa.ImplImports) > 0 || len(fa.AppReads) > 0 {
			return nil
		}
		for _, imported := range fa.ImportedModules {
			if imported == key {
				reachesEntry = true
			}
			if members[imported] {
				continue
			}
			if p.Files[imported] == nil {
				// A file p does not hold: one of another module, which
				// programClean already refused, or an import p's check
				// did not answer.
				return nil
			}
			members[imported] = true
			queue = append(queue, imported)
		}
	}
	for other := range p.Files {
		if !members[other] && bringsForeignImpls(p.Nodes[other]) {
			return nil
		}
	}
	if members[p.Entry] && !filepath.IsAbs(p.Files[p.Entry].FilePath) {
		// Only p's entry may have no absolute path (programClean).
		return nil
	}
	ep := &EntryProgram{Nodes: p.Nodes[key], Root: p.Root, Manifest: manifest, ReachesEntry: reachesEntry, member: true}
	for other := range members {
		if other == key {
			continue
		}
		ep.Files = append(ep.Files, EntryProgramFile{Key: other, Path: p.Files[other].FilePath, Nodes: p.Nodes[other], FA: p.Files[other], Text: p.Texts[other]})
	}
	slices.SortFunc(ep.Files, func(a, b EntryProgramFile) int { return strings.Compare(a.Key, b.Key) })
	return ep
}

// bringsForeignImpls reports whether a file whose nodes are nodes, as a
// build prepared them, brings an interface impl for a type it does not
// declare: an impl block, derive or conformance whose receiver is not one
// of its own top-level types. An inherent block serves only files that
// import its file.
func bringsForeignImpls(nodes []ast.Node) bool {
	own := map[string]bool{}
	for _, n := range nodes {
		switch d := n.(type) {
		case *ast.StructDef:
			own[d.Name] = true
		case *ast.EnumDef:
			own[d.Name] = true
		case *ast.TypeDef:
			own[d.Name] = true
		case *ast.ExternType:
			own[d.Name] = true
		}
	}
	for _, n := range nodes {
		switch d := n.(type) {
		case *ast.ImplBlock:
			if d.Interface != nil && !own[TypeExprBaseName(d.Receiver)] {
				return true
			}
		case *ast.ImplConformance:
			if !own[TypeExprBaseName(d.Receiver)] {
				return true
			}
		}
	}
	return false
}
