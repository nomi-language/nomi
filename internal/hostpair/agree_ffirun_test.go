package hostpair

import (
	"fmt"
	"sort"

	"github.com/nomi-language/nomi/internal/ffirun"
)

// ffirunPairing is one pairing as internal/ffirun/discovery.go derives it,
// flattened out of the DiscoveredPackage tree.
//
// Deliberately NOT hostpair.Pairing: the agreement check must compare two
// independently-shaped answers, and reusing the type under test on both sides
// is how a check stops being able to disagree.
type ffirunPairing struct {
	Kind       string
	Key        string
	EntryKey   string
	ImportPath string
	Symbol     string
	SourceFile string
	SourceLine int
	SourceCol  int
}

func (f ffirunPairing) String() string {
	symbol := f.ImportPath + "." + f.Symbol
	return fmt.Sprintf("%s %s -> %s", f.Kind, f.Key, symbol)
}

// flattenFfirun turns ffirun's package-keyed tree into the flat pairing set.
//
// The symbol is read from the SAME fields the two renderers read —
// codegen.wrapperFuncValue and cmd/nomi-stdlibbindings' render both spell
// `pkg.Alias + "." + exp.FuncName` — so this stands in for both of them
// without either being importable (one is a template, the other is package
// main).
func flattenFfirun(packages []ffirun.DiscoveredPackage) []ffirunPairing {
	var out []ffirunPairing
	for _, pkg := range packages {
		for _, typ := range pkg.Types {
			p := ffirunPairing{
				Kind:       "type",
				Key:        typ.Key,
				EntryKey:   typ.EntryKey,
				ImportPath: pkg.ImportPath,
				Symbol:     typ.TypeName,
				SourceFile: typ.SourceFile,
				SourceLine: typ.SourceLine,
				SourceCol:  typ.SourceCol,
			}
			out = append(out, p)
		}
		for _, exp := range pkg.Exports {
			p := ffirunPairing{
				Kind:       "func",
				Key:        exp.Key,
				EntryKey:   exp.EntryKey,
				ImportPath: pkg.ImportPath,
				Symbol:     exp.FuncName,
				SourceFile: exp.SourceFile,
				SourceLine: exp.SourceLine,
				SourceCol:  exp.SourceCol,
			}
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// projectPairing is a hostpair.Pairing rendered into ffirun's shape, using
// BindingKey — the spelling ffirun's externDeclKey produces.
//
// This is the whole content of the claim "hostpair agrees with ffirun": one
// projection of hostpair's decomposed declaration onto ffirun's joined key,
// with the symbol taken from the same two fields.
func projectPairing(p Pairing) ffirunPairing {
	out := ffirunPairing{
		Kind:       p.Kind.String(),
		Key:        p.BindingKey(),
		EntryKey:   p.EntryKey(),
		ImportPath: p.ImportPath,
		Symbol:     p.Symbol,
		SourceFile: p.SourceFile,
		SourceLine: p.SourceLine,
		SourceCol:  p.SourceCol,
	}
	return out
}

func projectAll(pairings []Pairing) []ffirunPairing {
	out := make([]ffirunPairing, 0, len(pairings))
	for _, p := range pairings {
		out = append(out, projectPairing(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// diffPairings is the agreement CHECK, extracted so a planted positive can
// call it with a known-wrong input and prove it reports the wrongness.
//
// A checker that is only ever run against agreeing inputs is indistinguishable
// from `return nil`.
func diffPairings(want, got []ffirunPairing) []string {
	index := func(in []ffirunPairing) map[string]ffirunPairing {
		m := make(map[string]ffirunPairing, len(in))
		for _, p := range in {
			m[p.Kind+"\x00"+p.Key] = p
		}
		return m
	}
	wantByKey, gotByKey := index(want), index(got)
	var diffs []string
	for _, p := range want {
		id := p.Kind + "\x00" + p.Key
		other, ok := gotByKey[id]
		if !ok {
			diffs = append(diffs, "only in ffirun: "+p.String())
			continue
		}
		if other != p {
			diffs = append(diffs, fmt.Sprintf("differs for %s %s:\n  ffirun:   %+v\n  hostpair: %+v", p.Kind, p.Key, p, other))
		}
	}
	for _, p := range got {
		if _, ok := wantByKey[p.Kind+"\x00"+p.Key]; !ok {
			diffs = append(diffs, "only in hostpair: "+p.String())
		}
	}
	sort.Strings(diffs)
	return diffs
}

// The std-facing half of this file is gone, and it is a DELETION rather than a
// repoint because its population emptied.
//
// It derived the co-located adapter facades' pairings — std/calendar,
// std/random, std/regex — and compared them against ffirun's over
// the same synthetic import-only scope. Those facades declare `host fn` now;
// no Nomi source in `std/` names a Go symbol, so hostpair derives ZERO
// pairings there and ffirun discovers nothing. A check over an empty
// population passes for the wrong reason.
//
// Nothing is uncovered by the removal. agree_project_test.go runs the same
// diffPairings comparison over nine fixture projects — including the shapes
// the adapters exercised, an impl-owned binding and two `gopkg` handles in one
// file — and TestProjectAgreementCatchesAnImplKeyRegression is the planted
// positive that says the checker can still report a disagreement.
