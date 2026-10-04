package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

func fakeMain(line, col int) *ast.FuncDef {
	return &ast.FuncDef{Name: "main", Line: line, Col: col}
}

func fakeFn(name string) *ast.FuncDef {
	return &ast.FuncDef{Name: name, Line: 10, Col: 1}
}

func fakeExtern(name string) *ast.ExternFunc {
	return &ast.ExternFunc{Name: name, Line: 11, Col: 2}
}

func fakeOnce(name string) *ast.OnceBinding {
	return &ast.OnceBinding{Name: name, Line: 12, Col: 3}
}

func fakeStruct(name string) *ast.StructDef {
	return &ast.StructDef{Name: name, Line: 20, Col: 1}
}

// TestEntryEnforcement_NoManifestSkips — single-file `nomi run` mode
// (no nomi.toml) keeps entry placement out of the project-wide manifest
// contract.
func TestEntryEnforcement_NoManifestSkips(t *testing.T) {
	errs := CheckEntryPlacement(
		nil,
		[]ast.Node{fakeMain(3, 1)},
		"main",
		map[string][]ast.Node{
			"helper": {fakeMain(5, 1)}, // would normally be flagged
		},
	)
	if len(errs) != 0 {
		t.Errorf("expected 0 errors with nil manifest, got %d: %v", len(errs), errs)
	}
}

func TestEntryEnforcement_NonEntryWithMainRejected(t *testing.T) {
	manifest := &Manifest{
		Name:        "todo",
		EntryPoints: []string{"main"},
	}
	errs := CheckEntryPlacement(
		manifest,
		[]ast.Node{fakeMain(2, 3)}, // entry has fn main — fine
		"main",
		map[string][]ast.Node{
			"helper": {fakeMain(9, 5)},
		},
	)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Message, "helper") {
		t.Errorf("error should mention %q, got %q", "helper", errs[0].Message)
	}
	if !strings.Contains(errs[0].Message, "entry_points") {
		t.Errorf("error should mention %q, got %q", "entry_points", errs[0].Message)
	}
	if errs[0].Line != 9 || errs[0].Col != 5 {
		t.Errorf("expected position 9,5 (the fn main declaration), got %d,%d", errs[0].Line, errs[0].Col)
	}
}

// TestEntryEnforcement_EntryWithoutMainRejected — a file declared in
// entry_points that does NOT define fn main must produce one TypeError
// naming the file. Because nomi.toml is the only source of position
// info (no .nomi node to point at), Line/Col use the manifest diagnostic
// placeholder.
func TestEntryEnforcement_EntryWithoutMainRejected(t *testing.T) {
	manifest := &Manifest{
		Name:        "todo",
		EntryPoints: []string{"main", "tools/seed"},
	}
	errs := CheckEntryPlacement(
		manifest,
		[]ast.Node{fakeMain(2, 1)}, // entry "main" has fn main — fine
		"main",
		map[string][]ast.Node{
			"tools/seed": {fakeFn("seed")}, // declared entry, no fn main
		},
	)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Message, "tools/seed") {
		t.Errorf("error should mention %q, got %q", "tools/seed", errs[0].Message)
	}
	if !strings.Contains(errs[0].Message, "no entry callback") {
		t.Errorf("error should mention %q, got %q", "no entry callback", errs[0].Message)
	}
	if errs[0].Line != 1 || errs[0].Col != 1 {
		t.Errorf("expected placeholder position (1,1), got (%d,%d)",
			errs[0].Line, errs[0].Col)
	}
}

// TestEntryEnforcement_EntryFileChecked — the entry file itself must
// also be enforced. If the entry's module-relative path is NOT in
// entry_points, fn main in it must be flagged; if it IS declared but
// lacks fn main, that's also flagged.
func TestEntryEnforcement_EntryFileChecked(t *testing.T) {
	t.Run("entry with main but not declared", func(t *testing.T) {
		manifest := &Manifest{
			Name:        "todo",
			EntryPoints: []string{"tools/seed"}, // "main" NOT listed
		}
		errs := CheckEntryPlacement(
			manifest,
			[]ast.Node{fakeMain(5, 2)},
			"main",
			map[string][]ast.Node{
				"tools/seed": {fakeMain(3, 1)},
			},
		)
		if len(errs) != 1 {
			t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
		}
		if !strings.Contains(errs[0].Message, "main") {
			t.Errorf("error should mention entry module path %q, got %q", "main", errs[0].Message)
		}
		if errs[0].Line != 5 || errs[0].Col != 2 {
			t.Errorf("expected position 5,2 (the entry fn main declaration), got %d,%d", errs[0].Line, errs[0].Col)
		}
	})

	t.Run("entry declared but missing main", func(t *testing.T) {
		manifest := &Manifest{
			Name:        "todo",
			EntryPoints: []string{"main"},
		}
		errs := CheckEntryPlacement(
			manifest,
			[]ast.Node{fakeFn("helper")}, // no fn main in entry
			"main",
			map[string][]ast.Node{},
		)
		if len(errs) != 1 {
			t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
		}
		if !strings.Contains(errs[0].Message, "main") {
			t.Errorf("error should mention %q, got %q", "main", errs[0].Message)
		}
		if !strings.Contains(errs[0].Message, "no entry callback") {
			t.Errorf("error should mention %q, got %q", "no entry callback", errs[0].Message)
		}
	})
}

// TestEntryEnforcement_HappyPath — a manifest with two declared entry
// points, both files providing fn main, and a sibling library file
// with ordinary top-level declarations, must produce
// no errors.
func TestEntryEnforcement_HappyPath(t *testing.T) {
	manifest := &Manifest{
		Name:        "todo",
		EntryPoints: []string{"main", "tools/seed"},
	}
	errs := CheckEntryPlacement(
		manifest,
		[]ast.Node{fakeMain(2, 1)},
		"main",
		map[string][]ast.Node{
			"tools/seed": {fakeMain(2, 1)},
			"task":       {fakeStruct("Task"), fakeFn("new"), fakeOnce("empty")}, // library API — fine
		},
	)
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(errs), errs)
	}
}

func TestEntryEnforcement_NonEntryTopLevelCallableAllowed(t *testing.T) {
	manifest := &Manifest{
		Name:        "todo",
		EntryPoints: []string{"main"},
	}
	tests := []struct {
		name     string
		node     ast.Node
		wantKind string
		wantLine int
		wantCol  int
	}{
		{"fn", fakeFn("helper"), "fn", 10, 1},
		{"host fn", fakeExtern("sleep"), "host fn", 11, 2},
		{"once", fakeOnce("cache"), "once", 12, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := CheckEntryPlacement(
				manifest,
				[]ast.Node{fakeMain(2, 1)},
				"main",
				map[string][]ast.Node{
					"helper": {tc.node},
				},
			)
			if len(errs) != 0 {
				t.Fatalf("expected 0 errors for library %s, got %d: %v", tc.wantKind, len(errs), errs)
			}
		})
	}
}

func TestEntryEnforcement_EntryTopLevelHelpersAllowed(t *testing.T) {
	manifest := &Manifest{
		Name:        "todo",
		EntryPoints: []string{"main"},
	}
	errs := CheckEntryPlacement(
		manifest,
		[]ast.Node{
			fakeMain(2, 1),
			fakeFn("helper"),
			fakeExtern("host_helper"),
			fakeOnce("cache"),
		},
		"main",
		map[string][]ast.Node{},
	)
	if len(errs) != 0 {
		t.Errorf("expected entry top-level helpers to be allowed, got %d: %v", len(errs), errs)
	}
}

// TestEntryEnforcement_EmptyEntryModRelSkipsEntryCheck — when the
// caller passes an empty entryModRel (the runtime doesn't yet know the
// entry's module-relative path), only sibling files are checked. The
// entry's main placement isn't enforced because we can't determine
// whether it's a declared entry. This matches single-file mode's
// existing behavior.
func TestEntryEnforcement_EmptyEntryModRelSkipsEntryCheck(t *testing.T) {
	manifest := &Manifest{
		Name:        "todo",
		EntryPoints: []string{"main"},
	}
	errs := CheckEntryPlacement(
		manifest,
		[]ast.Node{fakeMain(2, 1)}, // entry with fn main, unknown path
		"",                         // empty entryModRel
		map[string][]ast.Node{
			"helper": {fakeMain(3, 1)}, // sibling check still fires
		},
	)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error (sibling only), got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Message, "helper") {
		t.Errorf("error should mention sibling %q, got %q", "helper", errs[0].Message)
	}
}
