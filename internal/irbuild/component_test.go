package irbuild

import (
	"testing"
)

// TestReferenceEdgesAreImportEdges checks, over the whole corpus rather than
// as a case per arm, that every reference edge between user files is an edge
// of the Nomi import graph:
//
//	reference graph  ⊆  Nomi import graph
//
// The partition collapses every strongly-connected component of the import
// graph into one unit. closesGoCycle reads the REFERENCE graph while the
// merge reads the IMPORT graph, so a reference edge outside the import graph
// would be an edge the guard can see and the merge cannot.
//
// Why the containment holds by construction, so a failure is a real finding and
// not a tolerance to widen: a user file's name enters another user file's scope
// only through an `import`. A qualified `owner.member` needs the qualifier
// bound, a bare name needs a selective import, and a type reference needs the
// type in scope. closeReferences is a deliberate SUPERSET of what lowering
// reaches — a shadowed qualifier, a reference inside a subtree that will be
// refused — but every one of those still sits behind an import statement.
//
// Direction matters and only one direction is asserted. The import graph may be
// strictly larger: `import b` with nothing referenced adds no reference edge,
// which is a program the builder must still lower. Merging on the larger graph
// is conservative in the safe direction.
func TestReferenceEdgesAreImportEdges(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus; -short")
	}
	_, files := corpusAnalysis(t)
	checked, multi := 0, 0
	for _, f := range files {
		p := f.Prog
		if p == nil || len(p.Modules) < 2 {
			continue
		}
		multi++
		reg := buildTypeRegistry(p)
		x := buildFileIndex(p, reg)
		imports := nomiImportGraph(p)
		for i := range p.Modules {
			for j := range p.Modules {
				if i == j || !x.reaches[i][j] {
					continue
				}
				checked++
				if !imports[i][j] {
					t.Errorf("%s: %s references %s but does not import it. "+
						"The component partition reads the IMPORT graph and the cycle "+
						"guard reads the REFERENCE graph, so an edge in one and not the "+
						"other is a Go import the merge will not collapse",
						f.Rel, p.Modules[i].Name, p.Modules[j].Name)
				}
			}
		}
	}
	if multi == 0 || checked == 0 {
		t.Fatalf("nothing was checked (%d multi-file programs, %d edges), so the "+
			"containment is asserted over an empty set", multi, checked)
	}
	t.Logf("%d reference edges over %d multi-file corpus programs, all import edges",
		checked, multi)
}

// TestComponent_MergesExactlyTheCycles checks that the partition merges
// exactly the import cycles.
//
// A unit alone in its component keeps `nomimod<i>` for its own index, so every
// acyclic program's package assignment is the one-package-per-file mapping: no
// file moves, no identifier is reserved against a predecessor, and no name is
// mangled.
//
// Over-merging changes no observable behaviour, since a bigger package still
// runs the same program, so no output comparison can see it. And a test that
// asked componentOwners which units it merged and checked only the rest would
// pass a partition that merged everything. So the assertion below is the
// defining property, read off a graph rather than
// off the function's own answer: unit i is merged if and only if it is mutually
// import-reachable with some lower-indexed unit. Over-merging and under-merging
// each fail one direction of that iff, and the absolute count beside it fails
// both at once.
func TestComponent_MergesExactlyTheCycles(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus; -short")
	}
	_, files := corpusAnalysis(t)
	// wantCyclic is the number of corpus files with a component to merge.
	// Each file is analysed as its own entry, so an SCC contributes one
	// program per member plus each entry above it: {app, prod, test} under
	// config_pattern/ and {app, dev, prod, test} under effects/ with
	// `main.nomi` above it. `greeter.nomi` imports app.EffectsApp to read its
	// fields, and analysed as its own entry it reaches no boot, so those reads
	// are errors and it is not a program here. Absolute, because a partition
	// that merged more or less than the cycles would still satisfy every other
	// assertion here. A change is new corpus material or a partition that
	// stopped being the cycles; name which in the commit message.
	const wantCyclic = 9
	cyclic, acyclic := 0, 0
	for _, f := range files {
		p := f.Prog
		if p == nil {
			continue
		}
		owner := componentOwners(p)
		reach := nomiImportGraph(p)
		merged := false
		for i, o := range owner {
			mutual := false
			for j := range i {
				if reach[i][j] && reach[j][i] {
					mutual = true
				}
			}
			switch {
			case mutual && o == i:
				t.Errorf("%s: unit %d (%s) is mutually import-reachable with a lower "+
					"unit and was NOT merged, so a real cycle reaches Go",
					f.Rel, i, p.Modules[i].Name)
			case !mutual && o != i:
				t.Errorf("%s: unit %d (%s) is in no cycle and was merged into %s anyway, "+
					"which coarsens the caching unit for nothing",
					f.Rel, i, p.Modules[i].Name, unitPackage(o))
			}
			if o != i {
				merged = true
			}
		}
		if merged {
			cyclic++
			continue
		}
		acyclic++
		for i, pkg := range unitPackagesFrom(owner) {
			if pkg != unitPackage(i) {
				t.Fatalf("%s has no import cycle and unit %d moved from %s to %s",
					f.Rel, i, unitPackage(i), pkg)
			}
		}
	}
	if cyclic != wantCyclic {
		t.Errorf("want exactly %d corpus files with a component to merge, got %d; "+
			"the population is the measurement, so a change here is either new "+
			"corpus material or a partition that stopped being the cycles",
			wantCyclic, cyclic)
	}
	t.Logf("%d acyclic programs keep the per-file mapping; %d merge a component",
		acyclic, cyclic)
}
