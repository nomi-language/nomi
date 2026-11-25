package analysis

import "testing"

// faWith builds a FileAnalysis whose ModuleScope holds the supplied
// symbols — the shape declaringPackageIndex consumes. Tests use this
// instead of stuffing maps directly into FileAnalysis fields because
// declared names live in ModuleScope.Symbols (no Interfaces / TypeDefs
// fields exist on FileAnalysis).
func faWith(syms ...*Symbol) *FileAnalysis {
	scope := NewScope(nil)
	for _, s := range syms {
		scope.Define(s)
	}
	return &FileAnalysis{ModuleScope: scope}
}

// TestDeclaringPackageIndex_BuildsExpectedMap verifies the helper folds
// the files of a project and the stdlib into a single name→module map.
func TestDeclaringPackageIndex_BuildsExpectedMap(t *testing.T) {
	// entry declares interface greeter + struct Dog. Sibling "foo"
	// declares struct Tag. std/display declares Display.
	entry := faWith(
		&Symbol{Name: "greeter", Kind: SymbolInterface},
		&Symbol{Name: "Dog", Kind: SymbolStruct},
	)
	foo := faWith(&Symbol{Name: "Tag", Kind: SymbolStruct})
	display := faWith(&Symbol{Name: "Display", Kind: SymbolInterface})
	files := map[string]*FileAnalysis{"": entry, "foo": foo, "std/display": display}

	entryPackageName := "myapp"
	crossPackageSegments := map[string]bool{"foo": true}

	idx := declaringPackageIndex(files, entryPackageName, crossPackageSegments)

	want := map[string]string{
		"greeter": "myapp",
		"Dog":     "myapp",
		"Tag":     "foo",
		"Display": "std",
	}
	for name, mod := range want {
		if got := declaringPackageOfForTest(idx, name); got != mod {
			t.Errorf("idx[%q] = %q; want %q", name, got, mod)
		}
	}
	if len(idx) != len(want) {
		t.Errorf("idx has %d entries, want %d: %v", len(idx), len(want), idx)
	}
}

func TestDeclaringPackageIndex_EntryOnly(t *testing.T) {
	// No siblings, no stdlib — all names map to entryPackageName.
	entry := faWith(
		&Symbol{Name: "greeter", Kind: SymbolInterface},
		&Symbol{Name: "Dog", Kind: SymbolStruct},
		&Symbol{Name: "Color", Kind: SymbolEnum},
		&Symbol{Name: "Id", Kind: SymbolType},
		&Symbol{Name: "Alias", Kind: SymbolTypeAlias},
	)
	files := map[string]*FileAnalysis{"": entry}

	idx := declaringPackageIndex(files, "myapp", nil)

	for _, name := range []string{"greeter", "Dog", "Color", "Id", "Alias"} {
		if got := declaringPackageOfForTest(idx, name); got != "myapp" {
			t.Errorf("idx[%q] = %q; want %q", name, got, "myapp")
		}
	}
}

func TestDeclaringPackageIndex_CrossPackageDep(t *testing.T) {
	// Sibling key "stringkit/pad" with stringkit in crossPackageSegments
	// — names map to "stringkit", not entryPackageName.
	dep := faWith(&Symbol{Name: "Padded", Kind: SymbolStruct})
	files := map[string]*FileAnalysis{"stringkit/pad": dep}
	crossPackageSegments := map[string]bool{"stringkit": true}

	idx := declaringPackageIndex(files, "myapp", crossPackageSegments)

	if got := declaringPackageOfForTest(idx, "Padded"); got != "stringkit" {
		t.Errorf("idx[\"Padded\"] = %q; want %q", got, "stringkit")
	}
}

func TestDeclaringPackageIndex_IntraPackageSibling(t *testing.T) {
	// Sibling key "sub/log" — first segment "sub" is NOT in
	// crossPackageSegments, so names map to entryPackageName (sibling of
	// the entry, not a cross-package dep).
	sibling := faWith(&Symbol{Name: "Logger", Kind: SymbolInterface})
	files := map[string]*FileAnalysis{"sub/log": sibling}

	idx := declaringPackageIndex(files, "myapp", nil)

	if got := declaringPackageOfForTest(idx, "Logger"); got != "myapp" {
		t.Errorf("idx[\"Logger\"] = %q; want %q", got, "myapp")
	}
}

func TestDeclaringPackageIndex_EmptyStdlib(t *testing.T) {
	// No stdlib file: no entry is synthesized.
	entry := faWith(&Symbol{Name: "X", Kind: SymbolStruct})
	files := map[string]*FileAnalysis{"": entry}

	idx := declaringPackageIndex(files, "myapp", nil)

	if len(idx) != 1 || declaringPackageOfForTest(idx, "X") != "myapp" {
		t.Errorf("idx = %v; want {X:myapp}", idx)
	}
}

// TestDeclaringPackageIndex_ProjectWinsOverStdlibViaFiles pins DETERMINISTIC
// project-wins when the stdlib declaration arrives through `files` as a
// "std/…"-keyed FA. A single map-iteration pass over `files` would
// let stdlib or project win at RANDOM when both declared a name (e.g. a user
// `struct Task` vs `std/tasks.Task`), surfacing intermittently as a
// spurious orphan error. The loop defeats map-order flakiness — every
// iteration must give the project package.
func TestDeclaringPackageIndex_ProjectWinsOverStdlibViaFiles(t *testing.T) {
	for i := 0; i < 50; i++ {
		files := map[string]*FileAnalysis{
			"std/tasks": faWith(&Symbol{Name: "Task", Kind: SymbolStruct}),
			"":          faWith(&Symbol{Name: "Task", Kind: SymbolStruct}),
		}
		idx := declaringPackageIndex(files, "todo", nil)
		if got := declaringPackageOfForTest(idx, "Task"); got != "todo" {
			t.Fatalf("iter %d: idx[\"Task\"] = %q; want %q (project must win over stdlib deterministically)", i, got, "todo")
		}
	}
}

func TestDeclaringPackageIndex_EnumVariantsExcluded(t *testing.T) {
	// Enum variants land in ModuleScope.Symbols as siblings to the
	// enum itself (SymbolEnumVariant), but they aren't impl receiver
	// types — must not appear in the index.
	entry := faWith(
		&Symbol{Name: "Color", Kind: SymbolEnum},
		&Symbol{Name: "Red", Kind: SymbolEnumVariant},
		&Symbol{Name: "Green", Kind: SymbolEnumVariant},
	)
	files := map[string]*FileAnalysis{"": entry}

	idx := declaringPackageIndex(files, "myapp", nil)

	if _, ok := declaringPackageOf(idx, "Red"); ok {
		t.Errorf("enum variant Red leaked into index: %v", idx)
	}
	if _, ok := declaringPackageOf(idx, "Green"); ok {
		t.Errorf("enum variant Green leaked into index: %v", idx)
	}
	if got := declaringPackageOfForTest(idx, "Color"); got != "myapp" {
		t.Errorf("idx[\"Color\"] = %q; want %q", got, "myapp")
	}
}

func TestDeclaringPackageIndex_ImportsExcluded(t *testing.T) {
	// Imported symbols land in ModuleScope.Symbols with Resolved set
	// to the source-module symbol. They aren't declarations of THIS
	// module — skip them.
	real := &Symbol{Name: "Display", Kind: SymbolInterface}
	imported := &Symbol{Name: "Display", Kind: SymbolInterface, Resolved: real}
	entry := faWith(
		imported,
		&Symbol{Name: "Local", Kind: SymbolStruct},
	)
	files := map[string]*FileAnalysis{"": entry}

	idx := declaringPackageIndex(files, "myapp", nil)

	if _, ok := declaringPackageOf(idx, "Display"); ok {
		t.Errorf("imported symbol Display leaked into index: %v", idx)
	}
	if got := declaringPackageOfForTest(idx, "Local"); got != "myapp" {
		t.Errorf("idx[\"Local\"] = %q; want %q", got, "myapp")
	}
}

func TestDeclaringPackageIndex_OpaqueTypesIncluded(t *testing.T) {
	// Opaque is a flag, not a kind — opaque types appear with their
	// underlying SymbolStruct/SymbolEnum/SymbolType kind and must be
	// indexed exactly like their non-opaque counterparts.
	entry := faWith(
		&Symbol{Name: "Handle", Kind: SymbolStruct, Opaque: true},
		&Symbol{Name: "State", Kind: SymbolEnum, Opaque: true},
		&Symbol{Name: "Id", Kind: SymbolType, Opaque: true},
	)
	files := map[string]*FileAnalysis{"": entry}

	idx := declaringPackageIndex(files, "myapp", nil)

	for _, name := range []string{"Handle", "State", "Id"} {
		if got := declaringPackageOfForTest(idx, name); got != "myapp" {
			t.Errorf("idx[%q] = %q; want %q", name, got, "myapp")
		}
	}
}

// TestPackageNameFromKey pins the four branches of packageNameFromKey
// directly, so a regression in the key→package rule fails here even if
// declaringPackageIndex's higher-level tests still pass. Post-stdlib-as-
// module: "std" is just another crossPackageSegment (the resolver
// injects proj.PackageIndex["std"] = <bundled-path>), so the dedicated
// stdlib short-circuit the pre-cutover helper carried is gone — stdlib
// keys route through the same crossPackageSegments branch as
// "stringkit", "todo", etc.
func TestPackageNameFromKey(t *testing.T) {
	entryPackageName := "myapp"
	// "std" appears here because BuildProjectWithCache folds every
	// proj.PackageIndex entry into crossPackageSegments — including the
	// virtually-injected stdlib path.
	crossPackageSegments := map[string]bool{"stringkit": true, "std": true}

	cases := []struct {
		key  string
		want string
	}{
		{"", "myapp"},                  // entry file
		{"std/strings", "std"},         // stdlib via crossPackageSegments
		{"std", "std"},                 // stdlib via crossPackageSegments (no slash)
		{"stringkit/pad", "stringkit"}, // cross-package dep
		{"sub/log", "myapp"},           // intra-package sibling
	}
	for _, tc := range cases {
		if got := packageNameFromKey(tc.key, entryPackageName, crossPackageSegments); got != tc.want {
			t.Errorf("packageNameFromKey(%q) = %q; want %q", tc.key, got, tc.want)
		}
	}
}

func TestDeclaringPackageIndex_NonTypeKindsExcluded(t *testing.T) {
	// Functions, bindings, once, params — not types or interfaces, so
	// they aren't impl receivers and must not appear in the index.
	entry := faWith(
		&Symbol{Name: "Iface", Kind: SymbolInterface},
		&Symbol{Name: "helper", Kind: SymbolFunction},
		&Symbol{Name: "cached", Kind: SymbolOnce},
		&Symbol{Name: "p", Kind: SymbolParam},
		&Symbol{Name: "b", Kind: SymbolBinding},
	)
	files := map[string]*FileAnalysis{"": entry}

	idx := declaringPackageIndex(files, "myapp", nil)

	if len(idx) != 1 {
		t.Errorf("idx has %d entries, want 1: %v", len(idx), idx)
	}
	if declaringPackageOfForTest(idx, "Iface") != "myapp" {
		t.Errorf("idx[\"Iface\"] = %q; want %q", idx["Iface"], "myapp")
	}
}
