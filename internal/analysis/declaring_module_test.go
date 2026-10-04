package analysis

import "testing"

// faWith builds a FileAnalysis whose ModuleScope holds the supplied
// symbols — the shape declaringModuleIndex consumes. Tests use this
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

// TestDeclaringModuleIndex_BuildsExpectedMap verifies the helper folds
// (project-files map, stdlib names) into a single name→module map.
func TestDeclaringModuleIndex_BuildsExpectedMap(t *testing.T) {
	// entry declares interface greeter + struct Dog. Sibling "foo"
	// declares struct Tag. Stdlib provides Display + Int.
	entry := faWith(
		&Symbol{Name: "greeter", Kind: SymbolInterface},
		&Symbol{Name: "Dog", Kind: SymbolStruct},
	)
	foo := faWith(&Symbol{Name: "Tag", Kind: SymbolStruct})
	files := map[string]*FileAnalysis{"": entry, "foo": foo}

	stdlibInterfaces := map[string]bool{"Display": true}
	stdlibTypes := map[string]bool{"Int": true}
	entryModuleName := "myapp"
	crossModuleSegments := map[string]bool{"foo": true}

	idx := declaringModuleIndex(files, entryModuleName, stdlibInterfaces, stdlibTypes, crossModuleSegments)

	want := map[string]string{
		"greeter": "myapp",
		"Dog":     "myapp",
		"Tag":     "foo",
		"Display": "std",
		"Int":     "std",
	}
	for name, mod := range want {
		if got := declaringModuleOfForTest(idx, name); got != mod {
			t.Errorf("idx[%q] = %q; want %q", name, got, mod)
		}
	}
	if len(idx) != len(want) {
		t.Errorf("idx has %d entries, want %d: %v", len(idx), len(want), idx)
	}
}

func TestDeclaringModuleIndex_EntryOnly(t *testing.T) {
	// No siblings, no stdlib — all names map to entryModuleName.
	entry := faWith(
		&Symbol{Name: "greeter", Kind: SymbolInterface},
		&Symbol{Name: "Dog", Kind: SymbolStruct},
		&Symbol{Name: "Color", Kind: SymbolEnum},
		&Symbol{Name: "Id", Kind: SymbolType},
		&Symbol{Name: "Alias", Kind: SymbolTypeAlias},
	)
	files := map[string]*FileAnalysis{"": entry}

	idx := declaringModuleIndex(files, "myapp", nil, nil, nil)

	for _, name := range []string{"greeter", "Dog", "Color", "Id", "Alias"} {
		if got := declaringModuleOfForTest(idx, name); got != "myapp" {
			t.Errorf("idx[%q] = %q; want %q", name, got, "myapp")
		}
	}
}

func TestDeclaringModuleIndex_CrossModuleDep(t *testing.T) {
	// Sibling key "stringkit/pad" with stringkit in crossModuleSegments
	// — names map to "stringkit", not entryModuleName.
	dep := faWith(&Symbol{Name: "Padded", Kind: SymbolStruct})
	files := map[string]*FileAnalysis{"stringkit/pad": dep}
	crossModuleSegments := map[string]bool{"stringkit": true}

	idx := declaringModuleIndex(files, "myapp", nil, nil, crossModuleSegments)

	if got := declaringModuleOfForTest(idx, "Padded"); got != "stringkit" {
		t.Errorf("idx[\"Padded\"] = %q; want %q", got, "stringkit")
	}
}

func TestDeclaringModuleIndex_IntraModuleSibling(t *testing.T) {
	// Sibling key "sub/log" — first segment "sub" is NOT in
	// crossModuleSegments, so names map to entryModuleName (sibling of
	// the entry, not a cross-module dep).
	sibling := faWith(&Symbol{Name: "Logger", Kind: SymbolInterface})
	files := map[string]*FileAnalysis{"sub/log": sibling}

	idx := declaringModuleIndex(files, "myapp", nil, nil, nil)

	if got := declaringModuleOfForTest(idx, "Logger"); got != "myapp" {
		t.Errorf("idx[\"Logger\"] = %q; want %q", got, "myapp")
	}
}

func TestDeclaringModuleIndex_EmptyStdlib(t *testing.T) {
	// Nil stdlib maps — must not panic and must not synthesize entries.
	entry := faWith(&Symbol{Name: "X", Kind: SymbolStruct})
	files := map[string]*FileAnalysis{"": entry}

	idx := declaringModuleIndex(files, "myapp", nil, nil, nil)

	if len(idx) != 1 || declaringModuleOfForTest(idx, "X") != "myapp" {
		t.Errorf("idx = %v; want {X:myapp}", idx)
	}
}

func TestDeclaringModuleIndex_ProjectWinsOverStdlib(t *testing.T) {
	// Precedence on collision: project wins. A user's local
	// `pub struct Date` (or any non-prelude stdlib name) shadows the
	// stdlib name in normal lookup; the orphan check must mirror that
	// so `impl Iface for Date` in the user's module doesn't look
	// orphan when the receiver is the user's own type. See helper's
	// doc-comment for the full rationale.
	entry := faWith(&Symbol{Name: "Date", Kind: SymbolStruct})
	files := map[string]*FileAnalysis{"": entry}
	stdlibTypes := map[string]bool{"Date": true}

	idx := declaringModuleIndex(files, "myapp", nil, stdlibTypes, nil)

	if got := declaringModuleOfForTest(idx, "Date"); got != "myapp" {
		t.Errorf("idx[\"Date\"] = %q; want %q (project wins)", got, "myapp")
	}
}

// TestDeclaringModuleIndex_ProjectWinsOverStdlibViaFiles pins DETERMINISTIC
// project-wins when the stdlib declaration arrives through `files` as a
// "std/…"-keyed FA (the production path post-stdlib-as-package), not via the
// legacy stdlibTypes param. The bug: a single map-iteration pass over `files`
// let stdlib or project win at RANDOM when both declared a name (e.g. a user
// `struct Task` vs `std/tasks.Task`), surfacing intermittently as a
// spurious orphan error. The loop defeats map-order flakiness — every
// iteration must give the project module.
func TestDeclaringModuleIndex_ProjectWinsOverStdlibViaFiles(t *testing.T) {
	for i := 0; i < 50; i++ {
		files := map[string]*FileAnalysis{
			"std/tasks": faWith(&Symbol{Name: "Task", Kind: SymbolStruct}),
			"":          faWith(&Symbol{Name: "Task", Kind: SymbolStruct}),
		}
		idx := declaringModuleIndex(files, "todo", nil, nil, nil)
		if got := declaringModuleOfForTest(idx, "Task"); got != "todo" {
			t.Fatalf("iter %d: idx[\"Task\"] = %q; want %q (project must win over stdlib deterministically)", i, got, "todo")
		}
	}
}

func TestDeclaringModuleIndex_EnumVariantsExcluded(t *testing.T) {
	// Enum variants land in ModuleScope.Symbols as siblings to the
	// enum itself (SymbolEnumVariant), but they aren't impl receiver
	// types — must not appear in the index.
	entry := faWith(
		&Symbol{Name: "Color", Kind: SymbolEnum},
		&Symbol{Name: "Red", Kind: SymbolEnumVariant},
		&Symbol{Name: "Green", Kind: SymbolEnumVariant},
	)
	files := map[string]*FileAnalysis{"": entry}

	idx := declaringModuleIndex(files, "myapp", nil, nil, nil)

	if _, ok := declaringModuleOf(idx, "Red"); ok {
		t.Errorf("enum variant Red leaked into index: %v", idx)
	}
	if _, ok := declaringModuleOf(idx, "Green"); ok {
		t.Errorf("enum variant Green leaked into index: %v", idx)
	}
	if got := declaringModuleOfForTest(idx, "Color"); got != "myapp" {
		t.Errorf("idx[\"Color\"] = %q; want %q", got, "myapp")
	}
}

func TestDeclaringModuleIndex_ImportsExcluded(t *testing.T) {
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

	idx := declaringModuleIndex(files, "myapp", nil, nil, nil)

	if _, ok := declaringModuleOf(idx, "Display"); ok {
		t.Errorf("imported symbol Display leaked into index: %v", idx)
	}
	if got := declaringModuleOfForTest(idx, "Local"); got != "myapp" {
		t.Errorf("idx[\"Local\"] = %q; want %q", got, "myapp")
	}
}

func TestDeclaringModuleIndex_OpaqueTypesIncluded(t *testing.T) {
	// Opaque is a flag, not a kind — opaque types appear with their
	// underlying SymbolStruct/SymbolEnum/SymbolType kind and must be
	// indexed exactly like their non-opaque counterparts.
	entry := faWith(
		&Symbol{Name: "Handle", Kind: SymbolStruct, Opaque: true},
		&Symbol{Name: "State", Kind: SymbolEnum, Opaque: true},
		&Symbol{Name: "Id", Kind: SymbolType, Opaque: true},
	)
	files := map[string]*FileAnalysis{"": entry}

	idx := declaringModuleIndex(files, "myapp", nil, nil, nil)

	for _, name := range []string{"Handle", "State", "Id"} {
		if got := declaringModuleOfForTest(idx, name); got != "myapp" {
			t.Errorf("idx[%q] = %q; want %q", name, got, "myapp")
		}
	}
}

// TestModuleNameFromKey pins the four branches of moduleNameFromKey
// directly, so a regression in the key→module rule fails here even if
// declaringModuleIndex's higher-level tests still pass. Post-stdlib-as-
// module: "std" is just another crossModuleSegment (the resolver
// injects proj.ModuleIndex["std"] = <bundled-path>), so the dedicated
// stdlib short-circuit the pre-cutover helper carried is gone — stdlib
// keys route through the same crossModuleSegments branch as
// "stringkit", "todo", etc.
func TestModuleNameFromKey(t *testing.T) {
	entryModuleName := "myapp"
	// "std" appears here because BuildProjectWithCache folds every
	// proj.ModuleIndex entry into crossModuleSegments — including the
	// virtually-injected stdlib path.
	crossModuleSegments := map[string]bool{"stringkit": true, "std": true}

	cases := []struct {
		key  string
		want string
	}{
		{"", "myapp"},                  // entry file
		{"std/strings", "std"},         // stdlib via crossModuleSegments
		{"std", "std"},                 // stdlib via crossModuleSegments (no slash)
		{"stringkit/pad", "stringkit"}, // cross-module dep
		{"sub/log", "myapp"},           // intra-module sibling
	}
	for _, tc := range cases {
		if got := moduleNameFromKey(tc.key, entryModuleName, crossModuleSegments); got != tc.want {
			t.Errorf("moduleNameFromKey(%q) = %q; want %q", tc.key, got, tc.want)
		}
	}
}

func TestDeclaringModuleIndex_NonTypeKindsExcluded(t *testing.T) {
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

	idx := declaringModuleIndex(files, "myapp", nil, nil, nil)

	if len(idx) != 1 {
		t.Errorf("idx has %d entries, want 1: %v", len(idx), idx)
	}
	if declaringModuleOfForTest(idx, "Iface") != "myapp" {
		t.Errorf("idx[\"Iface\"] = %q; want %q", idx["Iface"], "myapp")
	}
}
