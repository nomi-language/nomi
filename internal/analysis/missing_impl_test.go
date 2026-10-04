package analysis

import (
	"fmt"
	"strings"
	"testing"
)

// TestDetectMissingImplsFlagsUnresolvedPair: a (Iface, T) pair recorded
// from user code with no impl supplied surfaces as one error, anchored
// at the recording's position.
func TestDetectMissingImplsFlagsUnresolvedPair(t *testing.T) {
	rec := Recording{
		Pos:  Pos{File: "/abs/proj/main.nomi", Line: 7, Col: 12},
		Kind: RecordingKindCallSite,
	}
	idx := &ProjectImplIndex{
		Impls: map[string]map[string]bool{}, // no supply
		ImplManifest: map[string]map[string][]Recording{
			"Display": {"Pad": {rec}},
		},
	}

	errs := DetectMissingImpls(idx, nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 missing-impl error, got %d: %+v", len(errs), errs)
	}
	if errs[0].Line != 7 || errs[0].Col != 12 {
		t.Errorf("expected error at 7:12, got %d:%d", errs[0].Line, errs[0].Col)
	}
	for _, want := range []string{"Display", "Pad", "/abs/proj/main.nomi", "derive Display"} {
		if !strings.Contains(diagText(errs[0]), want) {
			t.Errorf("error message missing %q: %s", want, errs[0].Message)
		}
	}
}

// TestDetectMissingImplsExemptsDebug: universal default Debug — Debug is
// satisfied by every type (auto-synthesized), so DetectMissingImpls must
// NEVER flag a (Debug, T) pair, even when no impl supply is recorded in
// the index and the recording is a user-project site. The same shape for
// any other interface (Display here) still errors — proving the exemption
// is Debug-only.
func TestDetectMissingImplsExemptsDebug(t *testing.T) {
	userRec := Recording{
		Pos:  Pos{File: "/abs/proj/main.nomi", Line: 7, Col: 12},
		Kind: RecordingKindCallSite,
	}
	idx := &ProjectImplIndex{
		Impls: map[string]map[string]bool{}, // no supply for either pair
		ImplManifest: map[string]map[string][]Recording{
			"Debug":   {"P": {userRec}}, // must be exempt (no error)
			"Display": {"P": {userRec}}, // control: must still error
		},
	}

	errs := DetectMissingImpls(idx, nil)
	for _, e := range errs {
		if strings.Contains(e.Message, "Debug` for") {
			t.Errorf("Debug must be exempt from missing-impl, but got: %s", e.Message)
		}
	}
	// The Display control must still produce exactly one error.
	displayErrs := 0
	for _, e := range errs {
		if strings.Contains(e.Message, "Display") {
			displayErrs++
		}
	}
	if displayErrs != 1 {
		t.Fatalf("expected exactly 1 Display missing-impl error (control), got %d:\n  %+v", displayErrs, errs)
	}
}

// TestDetectMissingImplsSuppressesStdlibOnlyPair: when every Recording
// for the pair comes from a stdlib file, the diagnostic is suppressed.
func TestDetectMissingImplsSuppressesStdlibOnlyPair(t *testing.T) {
	rec := Recording{
		Pos:  Pos{File: "/abs/std/io.nomi", Line: 14, Col: 3},
		Kind: RecordingKindCallSite,
	}
	idx := &ProjectImplIndex{
		Impls: map[string]map[string]bool{}, // no supply
		ImplManifest: map[string]map[string][]Recording{
			"Display": {"Date": {rec}},
		},
	}

	errs := DetectMissingImpls(idx, nil)
	if len(errs) != 0 {
		t.Fatalf("expected no errors (stdlib-only recordings suppressed), got %d: %+v", len(errs), errs)
	}
}

// TestDetectMissingImplsAttributesToUserProjectSite: when one Recording
// is stdlib and another is user-project, the primary error site must be
// the user-project one — the user's file is actionable, the stdlib site
// is internal.
func TestDetectMissingImplsAttributesToUserProjectSite(t *testing.T) {
	stdlibRec := Recording{
		Pos:  Pos{File: "/abs/std/io.nomi", Line: 5, Col: 9},
		Kind: RecordingKindCallSite,
	}
	userRec := Recording{
		Pos:  Pos{File: "/abs/proj/main.nomi", Line: 22, Col: 4},
		Kind: RecordingKindCallSite,
	}
	idx := &ProjectImplIndex{
		Impls: map[string]map[string]bool{},
		ImplManifest: map[string]map[string][]Recording{
			"Display": {"Pad": {stdlibRec, userRec}}, // stdlib first
		},
	}

	errs := DetectMissingImpls(idx, nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %+v", len(errs), errs)
	}
	if errs[0].Line != 22 || errs[0].Col != 4 {
		t.Errorf("expected primary site at user-project recording 22:4, got %d:%d", errs[0].Line, errs[0].Col)
	}
	// The stdlib site should still appear, as a related location.
	if len(errs[0].Related) != 1 || errs[0].Related[0].File != "/abs/std/io.nomi" ||
		errs[0].Related[0].Line != 5 || errs[0].Related[0].Col != 9 ||
		!strings.HasPrefix(errs[0].Related[0].Message, "also required here via ") {
		t.Errorf("expected the stdlib site /abs/std/io.nomi:5:9 as the related location; got %+v", errs[0].Related)
	}
}

// TestDetectMissingImplsOrderingDeterministic: when a single (Iface, T)
// pair carries multiple user-project Recordings from different files,
// the primary error site and the inlined "also required at" notes must
// be ordered deterministically across runs. buildProjectImplIndex sorts
// the Recording slice in-place (by Pos.File, Line, Col, Kind) so map-
// iteration randomization in upstream folding can't reshuffle the
// diagnostic. This test exercises the post-sort consumer-side invariant:
// given a pre-sorted ProjectImplIndex (skipping the build step), the
// diagnostic picks the lowest-Pos Recording as primary and emits the
// remaining notes in ascending Pos order.
func TestDetectMissingImplsOrderingDeterministic(t *testing.T) {
	// Three user-project recordings in three different files, with
	// Pos.File chosen so file-name ordering disagrees with the order
	// they're listed in the slice — the consumer must see them sorted.
	recA := Recording{
		Pos:  Pos{File: "/abs/proj/a.nomi", Line: 9, Col: 1},
		Kind: RecordingKindCallSite,
	}
	recB := Recording{
		Pos:  Pos{File: "/abs/proj/b.nomi", Line: 3, Col: 5},
		Kind: RecordingKindCallSite,
	}
	recC := Recording{
		Pos:  Pos{File: "/abs/proj/c.nomi", Line: 5, Col: 2},
		Kind: RecordingKindCallSite,
	}
	idx := &ProjectImplIndex{
		Impls: map[string]map[string]bool{},
		ImplManifest: map[string]map[string][]Recording{
			// Pre-sorted as buildProjectImplIndex would leave it.
			"Display": {"Pad": {recA, recB, recC}},
		},
	}

	errs := DetectMissingImpls(idx, nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %+v", len(errs), errs)
	}
	// Primary must be the lowest-Pos recording (recA: a.nomi:9:1).
	if errs[0].Line != 9 || errs[0].Col != 1 {
		t.Errorf("expected primary site at recA (9:1), got %d:%d", errs[0].Line, errs[0].Col)
	}
	if !strings.Contains(errs[0].Message, "/abs/proj/a.nomi:9:1") {
		t.Errorf("expected primary message to mention a.nomi:9:1; got:\n%s", errs[0].Message)
	}
	// Related locations come in sorted Pos order: b.nomi before c.nomi.
	var got []string
	for _, r := range errs[0].Related {
		got = append(got, fmt.Sprintf("%s:%d:%d", r.File, r.Line, r.Col))
	}
	if want := "/abs/proj/b.nomi:3:5 /abs/proj/c.nomi:5:2"; strings.Join(got, " ") != want {
		t.Errorf("related locations = %v, want %s (Pos.File ordering)", got, want)
	}
}

// TestDetectMissingImplsOperatorKindSplit: equality-operator demands
// are weak — a (Equatable, T) group whose recordings are all
// RecordingKindEqualityOperator is skipped (structural `rt.Equal`
// fallback) — while ordering-operator demands are strong: a
// (Comparable, T) group of RecordingKindOrderingOperator recordings
// surfaces an ordinary missing-impl error naming the operator token.
func TestDetectMissingImplsOperatorKindSplit(t *testing.T) {
	eqRec := Recording{
		Pos:  Pos{File: "/abs/proj/main.nomi", Line: 4, Col: 8},
		Kind: RecordingKindEqualityOperator,
		Op:   "==",
	}
	ordRec := Recording{
		Pos:  Pos{File: "/abs/proj/main.nomi", Line: 9, Col: 14},
		Kind: RecordingKindOrderingOperator,
		Op:   "<",
	}
	idx := &ProjectImplIndex{
		Impls: map[string]map[string]bool{}, // no supply for either pair
		ImplManifest: map[string]map[string][]Recording{
			"Equatable":  {"Id": {eqRec}},     // weak: must be skipped
			"Comparable": {"Point": {ordRec}}, // strong: must error
		},
	}

	errs := DetectMissingImpls(idx, nil)
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 error (ordering only), got %d: %+v", len(errs), errs)
	}
	if errs[0].Line != 9 || errs[0].Col != 14 {
		t.Errorf("expected error at ordering site 9:14, got %d:%d", errs[0].Line, errs[0].Col)
	}
	for _, want := range []string{"Comparable", "Point", "ordering operator `<`", "derive Comparable"} {
		if !strings.Contains(diagText(errs[0]), want) {
			t.Errorf("error message missing %q: %s", want, errs[0].Message)
		}
	}
	for _, e := range errs {
		if strings.Contains(e.Message, "Equatable") {
			t.Errorf("equality-operator demand must stay weak (no error), but got: %s", e.Message)
		}
	}
}

// TestDetectMissingImplsDeriveCtxInMessage: a Recording carrying
// DeriveCtx must surface the @derive interface and the offending field
// in the "via" clause of the message.
func TestDetectMissingImplsDeriveCtxInMessage(t *testing.T) {
	derive := &DeriveCtx{
		Iface:     "Display",
		TypeName:  "Container",
		DerivePos: Pos{File: "/abs/proj/main.nomi", Line: 3, Col: 1},
		FieldName: "payload",
		FieldPos:  Pos{File: "/abs/proj/main.nomi", Line: 5, Col: 5},
	}
	rec := Recording{
		Pos:    Pos{File: "/abs/proj/main.nomi", Line: 3, Col: 1},
		Kind:   RecordingKindDeriveSynth,
		Derive: derive,
	}
	idx := &ProjectImplIndex{
		Impls: map[string]map[string]bool{},
		ImplManifest: map[string]map[string][]Recording{
			"Display": {"PayloadType": {rec}},
		},
	}

	errs := DetectMissingImpls(idx, nil)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %+v", len(errs), errs)
	}
	for _, want := range []string{"@derive(Display)", "Container", "payload"} {
		if !strings.Contains(diagText(errs[0]), want) {
			t.Errorf("expected message to mention %q; got:\n%s", want, errs[0].Message)
		}
	}
}
