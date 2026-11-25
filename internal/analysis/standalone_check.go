package analysis

import (
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
)

// StandaloneFile is one parsed file to check on its own, against the std
// and no program (CheckStandaloneFile): a std module's source, a std file
// open in an editor, a source std/compiler's `hover` is asked about.
type StandaloneFile struct {
	// Nodes are the file's declarations as parsed, its derives lowered
	// (LowerDerives).
	Nodes []ast.Node
	// Path is the file's path, "" for a file with none. Root is the
	// directory its imports resolve against.
	Path, Root string
	// Prelude is the scope of the names every user file reads. It is nil
	// for a std module, which imports what it names.
	Prelude *Scope
	// Modules are the std module scopes the file is built against.
	Modules map[string]*Scope
	// Loader answers the loader of the files this one imports, nil for
	// none. It is given the file's completed declarations, which a loader
	// answers a request for the file's own module with.
	Loader func(completed []ast.Node) FileLoader
	// Built runs once the file is built and before its types are. It gives
	// the analysis the std's impls (AttachStdlibProjectImpls,
	// AttachEditedStdlibImpls) and answers what it reports.
	Built func(fa *FileAnalysis, completed []ast.Node) []TypeError
	// Typed, when set, runs once the file's types are built and before its
	// bodies are checked.
	Typed func(fa *FileAnalysis, completed []ast.Node)
}

// StandaloneCheck is a file checked on its own.
type StandaloneCheck struct {
	// Nodes are the file's declarations completed with the impl blocks the
	// front end writes for them, which the build and every pass ran over.
	// A caller's later pass over the file (MarkTailCalls, a lowering) takes
	// these.
	Nodes []ast.Node
	FA    *FileAnalysis
	// BuildErrors are what the build reported: the analysis's TypeErrors
	// once built, before its types were.
	BuildErrors []TypeError
	// CheckErrors are what Built and each pass after the build reported,
	// in the order they ran.
	CheckErrors []TypeError
}

// CheckStandaloneFile builds, types and checks one file on its own. It is
// the one place a single file is built for a caller to check, since the
// order has a trap. A build completes the declarations it is given with
// the impl blocks the front end writes for them (SynthesizeUniversalDebug:
// an opaque type's Debug, a subtype's `try_from` and `unchecked`) in a copy
// it keeps. A caller that then typed and checked its own slice ran those
// passes over declarations that lack the blocks: the written functions had
// symbols and no types, a call of one did not resolve, and their bodies, a
// subtype's rule among them, went unchecked. Here the declarations are
// completed first, the build takes that slice, and BuildTypes, CheckTypes,
// CheckAppRoots, AnalyzeIterSensitivity and the four structural checks
// run over the same one, which the caller gets back.
// TestOnlyCheckStandaloneFileBuildsAFileOnItsOwn fails for shipped code
// that builds a single file any other way.
//
// What differs between callers is theirs: the std impls a file sees
// (Built, Typed), FinalizeCoherence, and where the errors go.
func CheckStandaloneFile(f StandaloneFile) StandaloneCheck {
	nodes := SynthesizeUniversalDebug(f.Nodes)
	var loader FileLoader
	if f.Loader != nil {
		loader = f.Loader(nodes)
	}
	fa := buildFileWithStdlibAtPath(nodes, f.Prelude, f.Modules, f.Root, loader, f.Path)
	var errs []TypeError
	if f.Built != nil {
		errs = f.Built(fa, nodes)
	}
	built := slices.Clone(fa.TypeErrors)
	errs = append(errs, BuildTypes(fa, nodes)...)
	if f.Typed != nil {
		f.Typed(fa, nodes)
	}
	errs = append(errs, CheckTypes(fa, nodes)...)
	errs = append(errs, CheckAppRoots(fa, nodes)...)
	errs = append(errs, AnalyzeIterSensitivity(fa, nodes)...)
	errs = append(errs, CheckConcurrentScope(fa, nodes)...)
	errs = append(errs, CheckBootScope(fa, nodes)...)
	errs = append(errs, CheckOnceCycles(fa, nodes)...)
	errs = append(errs, CheckTaskLifetime(fa, nodes)...)
	return StandaloneCheck{Nodes: nodes, FA: fa, BuildErrors: built, CheckErrors: errs}
}
