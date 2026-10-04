package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// implFuncDef builds a minimal impl-method FuncDef fixture for
// detectImplCollisions: `fn <name>(_: <recvType>)`. The collision check reads
// the method name and the receiver base name (Params[0].TypeAnnotation); the
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

func TestDetectImplCollisions_TwoModulesSamePair(t *testing.T) {
	fnA := implFuncDef("to_string", "Display", "Int")
	fnB := implFuncDef("to_string", "Display", "Int")
	index := map[string]map[string][]*ast.FuncDef{
		"Display": {"to_string": {fnA, fnB}},
	}
	files := map[*ast.FuncDef]string{fnA: "app/widgets", fnB: "app/legacy"}

	errs := detectImplCollisions(index, files, nil, nil, nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 collision error, got %d: %v", len(errs), errs)
	}
	msg := errs[0].Message
	for _, want := range []string{"Display", "Int", "to_string", "app/widgets", "app/legacy"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

func TestDetectImplCollisions_MultiReceiverNotACollision(t *testing.T) {
	// impl Speech for Dog { fn speak } + impl Speech for Cat { fn speak } —
	// different receiver base names, the intended polymorphic-dispatch shape.
	fnDog := implFuncDef("speak", "Speech", "Dog")
	fnCat := implFuncDef("speak", "Speech", "Cat")
	index := map[string]map[string][]*ast.FuncDef{
		"Speech": {"speak": {fnDog, fnCat}},
	}
	files := map[*ast.FuncDef]string{fnDog: "animals", fnCat: "animals"}

	if errs := detectImplCollisions(index, files, nil, nil, nil); len(errs) != 0 {
		t.Fatalf("expected no collision for multi-receiver impl, got: %v", errs)
	}
}

func TestDetectImplCollisions_MultiMethodOneImplNotACollision(t *testing.T) {
	// impl Comparable for Foo providing both compare and equals — distinct
	// methods, one impl.
	fnCompare := implFuncDef("compare", "Comparable", "Foo")
	fnEquals := implFuncDef("equals", "Comparable", "Foo")
	index := map[string]map[string][]*ast.FuncDef{
		"Comparable": {"compare": {fnCompare}, "equals": {fnEquals}},
	}
	files := map[*ast.FuncDef]string{fnCompare: "m", fnEquals: "m"}

	if errs := detectImplCollisions(index, files, nil, nil, nil); len(errs) != 0 {
		t.Fatalf("expected no collision for multi-method impl, got: %v", errs)
	}
}

func TestDetectImplCollisions_NoReceiverTypeSkipped(t *testing.T) {
	// A FuncDef with no first-param type annotation can't be keyed by
	// receiver — skip it rather than crash or mis-group.
	fn := implFuncDef("to_string", "Display", "Int")
	fn.Params[0].TypeAnnotation = nil
	index := map[string]map[string][]*ast.FuncDef{"Display": {"to_string": {fn}}}
	files := map[*ast.FuncDef]string{fn: "m"}

	if errs := detectImplCollisions(index, files, nil, nil, nil); len(errs) != 0 {
		t.Fatalf("expected no error for receiver-less FuncDef, got: %v", errs)
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
			IfaceMethodImpls: map[string]map[string][]*ast.FuncDef{
				"Speech": {"speak": {fnA}},
			},
			IfaceMethodImplFiles: map[*ast.FuncDef]string{fnA: "a"},
			ImplManifest: map[string]map[string][]Recording{
				"Speech": {"Dog": {{Pos: Pos{File: "a", Line: 1, Col: 1}, Kind: RecordingKindCallSite}}},
			},
		},
		"b": {
			Impls: map[string]map[string]bool{
				"Cat": {"Speech": true},
			},
			IfaceMethodImpls: map[string]map[string][]*ast.FuncDef{
				"Speech": {"speak": {fnB}},
			},
			IfaceMethodImplFiles: map[*ast.FuncDef]string{fnB: "b"},
			ImplManifest: map[string]map[string][]Recording{
				"Speech": {"Cat": {{Pos: Pos{File: "b", Line: 1, Col: 1}, Kind: RecordingKindCallSite}}},
			},
		},
	}
	idx := buildProjectImplIndex(filesByKey)
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

// TestBuildProjectImplIndex_DedupesSharedPointers pins the dedupe
// behavior that prevents N-fold duplicates in IfaceMethodImpls when
// project_build.go broadcasts the same map pointer to every "all"-side
// FA. Without dedupe, downstream consumers (collision detector, IR
// builder) would see len(fns) == N and emit spurious diagnostics.
func TestBuildProjectImplIndex_DedupesSharedPointers(t *testing.T) {
	fn := &ast.FuncDef{Name: "speak"}
	shared := map[string]map[string][]*ast.FuncDef{
		"Speech": {"speak": {fn}},
	}
	sharedFiles := map[*ast.FuncDef]string{fn: "a"}
	filesByKey := map[string]*FileAnalysis{
		"a": {IfaceMethodImpls: shared, IfaceMethodImplFiles: sharedFiles},
		"b": {IfaceMethodImpls: shared, IfaceMethodImplFiles: sharedFiles},
		"c": {IfaceMethodImpls: shared, IfaceMethodImplFiles: sharedFiles},
	}
	idx := buildProjectImplIndex(filesByKey)
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
		"std/int": {IfaceMethodImpls: shared, IfaceMethodImplFiles: sharedFiles},
	}
	idx := buildProjectImplIndex(filesByKey)
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
	idx := buildProjectImplIndex(filesByKey)
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
	if got := idx.QualifiedReceiver[fn]; got != "calendar.Date" {
		t.Fatalf("qualified receiver = %q, want calendar.Date", got)
	}
}

// The collision check's grouping key must be the runtime's dispatch key,
// or a pair of impls can reach one slot with the guard silent. A
// namespaced receiver keeps its namespace on both sides: the check sees
// `json.Json.DecodeError` and `dynamic.DecodeError` as two types, and
// dispatch derives the same two keys from the runtime type names.
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
	if got := idx.QualifiedReceiver[fn]; got != "json.Json.DecodeError" {
		t.Fatalf("qualified receiver = %q, want json.Json.DecodeError", got)
	}
}

func TestDetectImplCollisions_NamespacedAndBareReceiverAreDistinct(t *testing.T) {
	// impl Display for Json.DecodeError (std/json) alongside
	// impl Display for DecodeError (std/dynamic). Two types, two slots.
	fnJSON := implFuncDef("to_string", "Display", "Json.DecodeError")
	fnDynamic := implFuncDef("to_string", "Display", "DecodeError")
	index := map[string]map[string][]*ast.FuncDef{
		"Display": {"to_string": {fnJSON, fnDynamic}},
	}
	files := map[*ast.FuncDef]string{fnJSON: "std/json", fnDynamic: "std/dynamic"}
	receivers := map[*ast.FuncDef]string{
		fnJSON:    "json.Json.DecodeError",
		fnDynamic: "dynamic.DecodeError",
	}

	if errs := detectImplCollisions(index, files, receivers, nil, nil); len(errs) != 0 {
		t.Fatalf("expected no collision between Json.DecodeError and DecodeError, got: %v", errs)
	}
}

func TestDetectImplCollisions_TwoImplsForOneNamespacedReceiver(t *testing.T) {
	fnA := implFuncDef("to_string", "Display", "Json.DecodeError")
	fnB := implFuncDef("to_string", "Display", "Json.DecodeError")
	index := map[string]map[string][]*ast.FuncDef{
		"Display": {"to_string": {fnA, fnB}},
	}
	files := map[*ast.FuncDef]string{fnA: "std/json", fnB: "app/patches"}
	receivers := map[*ast.FuncDef]string{
		fnA: "json.Json.DecodeError",
		fnB: "json.Json.DecodeError",
	}

	errs := detectImplCollisions(index, files, receivers, nil, nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 collision error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Message, "json.Json.DecodeError") {
		t.Errorf("error message missing the namespaced receiver: %s", errs[0].Message)
	}
}
