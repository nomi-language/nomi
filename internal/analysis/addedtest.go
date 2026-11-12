package analysis

import (
	"maps"
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
)

// CheckAddedTest checks test, a test declaration placed after the last line
// of the file fa analyzed, in fa's module scope, as a check of the file
// with test appended checks it, and leaves fa as it was. fa is a file whose
// bodies were walked from its kept declarations (a std module after
// StdLib.Bodies). The answer is a copy of fa whose body part also holds
// test's: its references, definitions and types, for a lowering of test
// against the file's declarations. The file's own bodies are not walked or
// checked again, so the cost is test's.
//
// The walk adds test's scope to fa's module scope, where ScopeAt finds it
// while the copy is checked and lowered; restore takes it out again, and
// the caller runs it once it is done with the copy. Until then nothing else
// may read or build fa. The errors are test's: one positioned before test
// is about the file, which is checked already, and is not answered.
//
// ok is false when fa was not kept, and nothing is changed.
func (fa *FileAnalysis) CheckAddedTest(test *ast.TestDecl) (copied *FileAnalysis, errs []TypeError, restore func(), ok bool) {
	k := fa.keptDecls()
	if k == nil || k.builder == nil || fa.ModuleScope == nil {
		return nil, nil, nil, false
	}
	cp := *fa
	cp.FileBodies = fa.FileBodies.clone()
	cp.generation = bodyGenerations.Add(1)
	scope := fa.ModuleScope
	children := len(scope.Children)
	restore = func() {
		clear(scope.Children[children:])
		scope.Children = scope.Children[:children]
	}
	b := *k.builder
	b.file = &cp
	b.fileScope = scope
	b.blockImports = maps.Clone(k.builder.blockImports)
	nodes := []ast.Node{test}
	from := len(cp.TypeErrors)
	b.buildModuleBodies(nodes, scope)
	cp.TypeErrors = append(cp.TypeErrors, checkUnused(&cp, nodes)...)
	if k.types != nil {
		cp.TypeErrors = append(cp.TypeErrors, buildBodyTypes(&cp, k.types, nodes)...)
	}
	all := slices.Clone(cp.TypeErrors[from:])
	all = append(all, CheckTypes(&cp, nodes)...)
	all = append(all, CheckAppRoots(&cp, nodes)...)
	all = append(all, AnalyzeIterSensitivity(&cp, nodes)...)
	all = append(all, CheckConcurrentScope(&cp, nodes)...)
	all = append(all, CheckBootScope(&cp, nodes)...)
	all = append(all, CheckTaskLifetime(&cp, nodes)...)
	for _, e := range all {
		if e.Line >= test.Line {
			errs = append(errs, e)
		}
	}
	return &cp, errs, restore, true
}

// clone copies a body part down to its tables, so a walk that adds to the
// copy leaves the original as it was.
func (fb FileBodies) clone() FileBodies {
	out := fb
	out.References = maps.Clone(fb.References)
	out.PunnedFieldLabels = maps.Clone(fb.PunnedFieldLabels)
	out.TodoPlaceholders = maps.Clone(fb.TodoPlaceholders)
	out.GroupRowTypes = maps.Clone(fb.GroupRowTypes)
	out.Definitions = maps.Clone(fb.Definitions)
	out.BlockImportScopes = maps.Clone(fb.BlockImportScopes)
	out.receiverMembers = maps.Clone(fb.receiverMembers)
	out.LambdaPatternTypes = maps.Clone(fb.LambdaPatternTypes)
	out.SubtestsRowTypes = maps.Clone(fb.SubtestsRowTypes)
	out.CoveringOrPatterns = maps.Clone(fb.CoveringOrPatterns)
	out.ElselessIfs = maps.Clone(fb.ElselessIfs)
	out.ExprTypes = maps.Clone(fb.ExprTypes)
	out.ExpectedTypes = maps.Clone(fb.ExpectedTypes)
	out.RedundantAscriptions = maps.Clone(fb.RedundantAscriptions)
	out.CheckedLiterals = maps.Clone(fb.CheckedLiterals)
	out.TargetLiterals = maps.Clone(fb.TargetLiterals)
	out.TargetLiteralBuilds = maps.Clone(fb.TargetLiteralBuilds)
	out.PatternTypes = maps.Clone(fb.PatternTypes)
	out.OperatorImpls = maps.Clone(fb.OperatorImpls)
	out.TargetStructs = maps.Clone(fb.TargetStructs)
	out.Memberships = maps.Clone(fb.Memberships)
	out.CallDefaults = maps.Clone(fb.CallDefaults)
	out.HoleDisplays = maps.Clone(fb.HoleDisplays)
	out.Operators = maps.Clone(fb.Operators)
	out.Coercions = maps.Clone(fb.Coercions)
	out.IterViews = maps.Clone(fb.IterViews)
	out.DefaultedRefs = maps.Clone(fb.DefaultedRefs)
	out.StandIns = maps.Clone(fb.StandIns)
	out.FieldAccessors = maps.Clone(fb.FieldAccessors)
	out.TupleItems = maps.Clone(fb.TupleItems)
	out.Placeholders = maps.Clone(fb.Placeholders)
	out.ImplManifest = cloneManifest(fb.ImplManifest)
	out.InferredTypeParamBounds = maps.Clone(fb.InferredTypeParamBounds)
	out.TypeMethodModules = maps.Clone(fb.TypeMethodModules)
	out.AppReads = maps.Clone(fb.AppReads)
	out.AppReadsByPos = maps.Clone(fb.AppReadsByPos)
	out.ImportedModules = slices.Clone(fb.ImportedModules)
	out.ImplImports = slices.Clone(fb.ImplImports)
	out.TypeErrors = slices.Clone(fb.TypeErrors)
	return out
}
