package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// implFuncDef builds a minimal impl-method FuncDef fixture for the impl
// index and the orphan rule: `fn <name>(_: <recvType>)`. They read the
// method name and the receiver base name (Params[0].TypeAnnotation); the
// owning interface comes from the index key the caller builds, not the FuncDef.
// The `iface` arg is accepted for call-site readability but isn't stored.
func implFuncDef(name, iface, recvType string) *ast.FuncDef {
	_ = iface
	return &ast.FuncDef{
		Name: name,
		Params: []ast.Param{
			{Name: "_", TypeAnnotation: &ast.SimpleType{Name: recvType}},
		},
	}
}

// TestBuildProjectImplIndex_Union asserts the new helper unions
// per-FA Impls / IfaceMethodImpls / IfaceMethodImplFiles / ImplManifest
// entries into the project-level shape.
func TestBuildProjectImplIndex_Union(t *testing.T) {
	fnA := &ast.FuncDef{Name: "speak"} // dummy; pointer identity is the key
	fnB := &ast.FuncDef{Name: "speak"}
	filesByKey := map[string]*FileAnalysis{
		"a": {
			Impls: map[string]map[string]bool{
				"Dog": {"Speech": true},
			},
			program: &Program{implTables: implTables{
				IfaceMethodImpls: map[string]map[string][]*ast.FuncDef{
					"Speech": {"speak": {fnA}},
				},
				IfaceMethodImplFiles: map[*ast.FuncDef]string{fnA: "a"},
			}},
			ImplManifest: map[string]map[string][]Recording{
				"Speech": {"Dog": {{Pos: Pos{File: "a", Line: 1, Col: 1}, Kind: RecordingKindCallSite}}},
			},
		},
		"b": {
			Impls: map[string]map[string]bool{
				"Cat": {"Speech": true},
			},
			program: &Program{implTables: implTables{
				IfaceMethodImpls: map[string]map[string][]*ast.FuncDef{
					"Speech": {"speak": {fnB}},
				},
				IfaceMethodImplFiles: map[*ast.FuncDef]string{fnB: "b"},
			}},
			ImplManifest: map[string]map[string][]Recording{
				"Speech": {"Cat": {{Pos: Pos{File: "b", Line: 1, Col: 1}, Kind: RecordingKindCallSite}}},
			},
		},
	}
	idx := buildProjectImplIndex(filesByKey, nil, nil)
	if !idx.Impls["Dog"]["Speech"] || !idx.Impls["Cat"]["Speech"] {
		t.Errorf("Impls union missing entries; got %v", idx.Impls)
	}
	if len(idx.ImplManifest["Speech"]["Dog"]) == 0 || len(idx.ImplManifest["Speech"]["Cat"]) == 0 {
		t.Errorf("ImplManifest union missing entries; got %v", idx.ImplManifest)
	}
	if len(idx.IfaceMethodImpls["Speech"]["speak"]) != 2 {
		t.Errorf("IfaceMethodImpls expected 2 fns; got %d", len(idx.IfaceMethodImpls["Speech"]["speak"]))
	}
	if idx.ImplFiles[fnA] != "a" || idx.ImplFiles[fnB] != "b" {
		t.Errorf("ImplFiles map wrong: %v", idx.ImplFiles)
	}
}

// TestBuildProjectImplIndex_DedupesSharedPrograms pins the dedupe
// behavior that prevents N-fold duplicates in IfaceMethodImpls when every
// file of one build holds the same Program. Without dedupe, downstream consumers (collision detector, IR
// builder) would see len(fns) == N and emit spurious diagnostics.
func TestBuildProjectImplIndex_DedupesSharedPrograms(t *testing.T) {
	fn := &ast.FuncDef{Name: "speak"}
	shared := map[string]map[string][]*ast.FuncDef{
		"Speech": {"speak": {fn}},
	}
	sharedFiles := map[*ast.FuncDef]string{fn: "a"}
	program := &Program{implTables: implTables{IfaceMethodImpls: shared, IfaceMethodImplFiles: sharedFiles}}
	filesByKey := map[string]*FileAnalysis{
		"a": {program: program},
		"b": {program: program},
		"c": {program: program},
	}
	idx := buildProjectImplIndex(filesByKey, nil, nil)
	if got := len(idx.IfaceMethodImpls["Speech"]["speak"]); got != 1 {
		t.Errorf("expected 1 FuncDef after dedupe; got %d", got)
	}
}

// TestBuildProjectImplIndex_PreservesSharedFnAcrossSlots pins the
// slot-local dedupe rule: if two interface/method slots legitimately point at
// the same FuncDef, a naive global "have I seen this FuncDef" dedupe would
// catch it at the first slot and silently suppress the second. The slot-local
// (iface, method, fn) dedupe preserves both while still deduping the genuine
// N-broadcast case above.
func TestBuildProjectImplIndex_PreservesSharedFnAcrossSlots(t *testing.T) {
	fn := &ast.FuncDef{Name: "to_string"}
	shared := map[string]map[string][]*ast.FuncDef{
		"Display": {"to_string": {fn}},
		"Debug":   {"to_string": {fn}},
	}
	sharedFiles := map[*ast.FuncDef]string{fn: "std/int"}
	filesByKey := map[string]*FileAnalysis{
		"std/int": {program: &Program{implTables: implTables{IfaceMethodImpls: shared, IfaceMethodImplFiles: sharedFiles}}},
	}
	idx := buildProjectImplIndex(filesByKey, nil, nil)
	if got := len(idx.IfaceMethodImpls["Display"]["to_string"]); got != 1 {
		t.Errorf("Display.to_string: expected 1 FuncDef; got %d", got)
	}
	if got := len(idx.IfaceMethodImpls["Debug"]["to_string"]); got != 1 {
		t.Errorf("Debug.to_string: expected 1 FuncDef (shared FuncDef must reach both slots); got %d", got)
	}
}

// TestProjectImplIndexUnionsRecordings pins the project-aggregation
// counterpart of TestImplManifestMergePreservesRecordings: two FAs each
// recording a distinct Recording for the same (Iface, Type) pair must
// union into a two-element slice on the project index, not collapse to
// one. DetectMissingImpls reads from this project view, so
// dropping per-FA provenance here would render the diagnostic blind to
// half the demand sites in multi-file programs.
func TestProjectImplIndexUnionsRecordings(t *testing.T) {
	recA := Recording{Pos: Pos{File: "a.nomi", Line: 7, Col: 12}, Kind: RecordingKindCallSite}
	recB := Recording{Pos: Pos{File: "b.nomi", Line: 3, Col: 5}, Kind: RecordingKindTypedLiteralSlot}
	filesByKey := map[string]*FileAnalysis{
		"a": {
			ImplManifest: map[string]map[string][]Recording{
				"Display": {"Int": {recA}},
			},
		},
		"b": {
			ImplManifest: map[string]map[string][]Recording{
				"Display": {"Int": {recB}},
			},
		},
	}
	idx := buildProjectImplIndex(filesByKey, nil, nil)
	recs := idx.ImplManifest["Display"]["Int"]
	if len(recs) != 2 {
		t.Fatalf("expected 2 recordings in project index union, got %d: %+v", len(recs), recs)
	}
	gotA, gotB := false, false
	for _, r := range recs {
		if r == recA {
			gotA = true
		}
		if r == recB {
			gotB = true
		}
	}
	if !gotA || !gotB {
		t.Errorf("missing recordings in project index union: gotA=%v gotB=%v, recs=%+v", gotA, gotB, recs)
	}
}

func TestPopulateQualifiedReceivers_IndexesAllImplMethods(t *testing.T) {
	fn := implFuncDef("to_string", "Display", "Date")
	home := &FileAnalysis{
		ModuleScope: NewScope(nil),
	}
	home.ModuleScope.Define(&Symbol{
		Name: "Date",
		Kind: SymbolStruct,
		Type: &StructType{Origin: "std/calendar", Name: "Date"},
	})
	idx := &ProjectImplIndex{
		IfaceMethodImpls: map[string]map[string][]*ast.FuncDef{
			"Display": {"to_string": {fn}},
		},
		ImplFiles: map[*ast.FuncDef]string{fn: "std/calendar"},
	}
	PopulateQualifiedReceivers(idx, map[string]*FileAnalysis{"std/calendar": home})
	got := idx.QualifiedReceiver[fn]
	if got != "std/calendar"+receiverOriginSep+"Date" || receiverDisplay(got) != "calendar.Date" {
		t.Fatalf("qualified receiver = %q (shown %q), want std/calendar's Date, shown calendar.Date", got, receiverDisplay(got))
	}
}

// A qualified receiver is the receiver's declaring module and its whole
// name. A namespaced receiver keeps its namespace: std/json's
// `Json.DecodeError` and another module's `DecodeError` are two types.
func TestPopulateQualifiedReceivers_KeepsNamespaceSegment(t *testing.T) {
	fn := implFuncDef("to_string", "Display", "Json.DecodeError")
	home := &FileAnalysis{ModuleScope: NewScope(nil)}
	home.ModuleScope.Define(&Symbol{
		Name: "Json",
		Kind: SymbolEnum,
		Type: &EnumType{Origin: "std/json", Name: "Json"},
	})
	idx := &ProjectImplIndex{
		IfaceMethodImpls: map[string]map[string][]*ast.FuncDef{
			"Display": {"to_string": {fn}},
		},
		ImplFiles: map[*ast.FuncDef]string{fn: "std/json"},
	}
	PopulateQualifiedReceivers(idx, map[string]*FileAnalysis{"std/json": home})
	got := idx.QualifiedReceiver[fn]
	if got != "std/json"+receiverOriginSep+"Json.DecodeError" || receiverDisplay(got) != "json.Json.DecodeError" {
		t.Fatalf("qualified receiver = %q (shown %q), want std/json's Json.DecodeError, shown json.Json.DecodeError", got, receiverDisplay(got))
	}
}
