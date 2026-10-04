package stdcompiler

import (
	"fmt"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/virtualproject"
)

// ProjectSpec is `compiler.Project`'s three fields as plain Go, and it exists so
// the assembly below has ONE input shape rather than one per caller.
//
// Manifest is a pointer because `Maybe<Toml>` has three states and a `string`
// has two: nil is None, and a non-nil empty string is `Some(Toml"")`, which is a
// legal Nomi value and a manifest with no sections rather than no manifest at
// all. Collapsing them would make `Some(Toml"")` behave as None — a wrong
// ANSWER, not a lost distinction.
type ProjectSpec struct {
	EntryPoint string
	Files      map[string]string
	Manifest   *string
}

// ProjectSpecDiagnostics is `compiler.check_project`'s pipeline: assemble the
// virtual project, parse every file, and run the analysis pass over it.
//
// Every caller goes through this one assembly, which decides which file is the
// entry, whether the manifest is threaded, and whether sibling diagnostics
// count. Those three decisions are not visible in the answer's SHAPE — all
// three produce a `List<Diagnostic>` either way — so two copies could diverge
// silently. checkproject_test.go pins the answers absolutely, as (line, col,
// message) triples.
//
// # The three decisions, each of which differs from `compiler.check`
//
//   - The entry is chosen by NAME through virtualproject.FromSources.
//     virtualproject's other constructor, fromFiles, chooses by looking for a
//     top-level `fn main`, and a project whose entry declares no `fn main` is
//     ordinary here.
//   - The manifest is threaded. `compiler.check` passes nil; without it the
//     entry_points contract reports nothing at all.
//   - Sibling diagnostics are INCLUDED. `compiler.check` passes false. The two
//     calls into ProjectDiagnostics differ in that one bool and in nothing
//     else, so a shorter list rather than an error is how getting it wrong
//     shows up.
//
// Parse errors are reported ALONE, from EVERY file rather than from the entry
// only: a project whose sibling does not parse is not type-checked, so reporting
// the entry's clean parse and then analyzing against a half-parsed sibling would
// answer about a program nobody wrote.
func ProjectSpecDiagnostics(spec ProjectSpec, root string) ([]rt.Diagnostic, error) {
	var manifest *analysis.Manifest
	if spec.Manifest != nil {
		m, err := analysis.ParseManifestData([]byte(*spec.Manifest), "<compiler.Project manifest>")
		if err != nil {
			return nil, err
		}
		manifest = m
	}

	project, err := virtualproject.FromSources(spec.EntryPoint, spec.Files, manifest)
	if err != nil {
		return nil, fmt.Errorf("compiler.check_project %w", err)
	}

	entryNodes, parseErrs := ParseSource(project.EntrySource)
	for _, source := range project.VirtualFiles {
		_, errs := ParseSource(source)
		parseErrs = append(parseErrs, errs...)
	}
	if len(parseErrs) > 0 {
		return ParseDiagnostics(parseErrs), nil
	}

	return ProjectDiagnostics(entryNodes, root, Loader(project.VirtualFiles),
		project.Manifest, project.EntryName, true), nil
}
