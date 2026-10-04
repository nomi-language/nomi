package analysis

import (
	"github.com/nomi-language/nomi/internal/ast"
	"strings"
	"testing"
)

func inherentRec(recv, method, module string, line int) InherentMethodRecord {
	return InherentMethodRecord{
		Receiver: recv,
		Method:   method,
		Module:   module,
		Fn:       &ast.FuncDef{Name: method, Line: line, Col: 3},
	}
}

func faDeclaring(origin string, decls ...*Symbol) *FileAnalysis {
	fa := &FileAnalysis{Origin: origin, ModuleScope: NewScope(nil)}
	for _, sym := range decls {
		fa.ModuleScope.Define(sym)
	}
	return fa
}

// The origin must come from the impl's OWN home file. The per-file impl maps
// are broadcast, so a pass that picked any FileAnalysis holding the receiver
// would attribute std/calendar's `Date` to the entry and back again depending on
// map iteration order — PopulateQualifiedReceivers' second recorded trap.
// `Module` is stamped from the build key at index time, so no search happens.
func TestPopulateInherentReceiverOrigins_ResolvesThroughHomeFile(t *testing.T) {
	recs := []InherentMethodRecord{
		inherentRec("Date", "new", "", 8),
		inherentRec("Date", "new", "std/calendar", 40),
	}
	files := map[string]*FileAnalysis{
		"": faDeclaring(OriginEntry, &Symbol{
			Name: "Date", Kind: SymbolStruct,
			Type: &StructType{Origin: OriginEntry, Name: "Date"},
		}),
		"std/calendar": faDeclaring("std/calendar", &Symbol{
			Name: "Date", Kind: SymbolStruct,
			Type: &StructType{Origin: "std/calendar", Name: "Date"},
		}),
	}
	PopulateInherentReceiverOrigins(recs, files)
	if recs[0].ReceiverOrigin != OriginEntry {
		t.Errorf("entry record origin = %q, want %q", recs[0].ReceiverOrigin, OriginEntry)
	}
	if recs[1].ReceiverOrigin != "std/calendar" {
		t.Errorf("stdlib record origin = %q, want std/calendar", recs[1].ReceiverOrigin)
	}
}

// A nested receiver takes its identity from its leading segment, which is the
// name the declaring file's scope actually binds — qualifyReceiver reads the
// same segment for the same reason.
func TestPopulateInherentReceiverOrigins_NamespacedReceiverUsesHead(t *testing.T) {
	recs := []InherentMethodRecord{inherentRec("Json.DecodeError", "message", "std/json", 12)}
	files := map[string]*FileAnalysis{
		"std/json": faDeclaring("std/json", &Symbol{
			Name: "Json", Kind: SymbolEnum,
			Type: &EnumType{Origin: "std/json", Name: "Json"},
		}),
	}
	PopulateInherentReceiverOrigins(recs, files)
	if recs[0].ReceiverOrigin != "std/json" {
		t.Errorf("namespaced receiver origin = %q, want std/json", recs[0].ReceiverOrigin)
	}
}

// THE DEFECT. A local `Date` and std/calendar's `Date` are two types by
// (declaring file, name), so each keeps its own `new`. Before the identity key
// this rejected the user's own file for a collision they could not see.
func TestDetectInherentImplCollisions_SameNameDifferentOriginsIsNotADuplicate(t *testing.T) {
	local := inherentRec("Date", "new", "", 8)
	local.ReceiverOrigin = OriginEntry
	std := inherentRec("Date", "new", "std/calendar", 40)
	std.ReceiverOrigin = "std/calendar"

	if errs := detectInherentImplCollisions([]InherentMethodRecord{local, std}); len(errs) != 0 {
		t.Fatalf("two distinct types collided: %+v", errs)
	}
}

// THE NEGATIVE CONTROL. The diagnostic exists for a real reason: two
// type-owned functions of one name on ONE type still make the dispatch slot
// ambiguous, and must still be rejected.
func TestDetectInherentImplCollisions_SameOriginTwiceIsStillADuplicate(t *testing.T) {
	first := inherentRec("Date", "new", "", 8)
	first.ReceiverOrigin = OriginEntry
	second := inherentRec("Date", "new", "", 14)
	second.ReceiverOrigin = OriginEntry

	errs := detectInherentImplCollisions([]InherentMethodRecord{second, first})
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %+v", len(errs), errs)
	}
	if errs[0].Line != 8 {
		t.Errorf("anchor line = %d, want the earlier declaration at 8", errs[0].Line)
	}
	if !strings.Contains(errs[0].Message, "`Date.new` is defined 2 times") {
		t.Errorf("message = %q, want it to name Date.new twice", errs[0].Message)
	}
}

// Identity must never make the check weaker. A receiver whose origin could not
// be established joins every resolved bucket for its name, so a build that has
// not run the population pass at all — a single-file build, a hand-rolled test
// index — reports exactly what it reported before this key existed.
func TestDetectInherentImplCollisions_UnresolvedOriginFallsBackToBare(t *testing.T) {
	bothUnresolved := []InherentMethodRecord{
		inherentRec("Date", "new", "", 8),
		inherentRec("Date", "new", "", 14),
	}
	if errs := detectInherentImplCollisions(bothUnresolved); len(errs) != 1 {
		t.Fatalf("unpopulated records: got %d errors, want 1: %+v", len(errs), errs)
	}

	known := inherentRec("Date", "new", "", 8)
	known.ReceiverOrigin = OriginEntry
	unknown := inherentRec("Date", "new", "vendor/cookie", 14)
	if errs := detectInherentImplCollisions([]InherentMethodRecord{known, unknown}); len(errs) != 1 {
		t.Fatalf("one side unresolved: got %d errors, want 1: %+v", len(errs), errs)
	}
}

// Two genuine duplicates on two same-named types are two diagnostics, not one
// merged count — the user has two things to fix and each is anchored at its
// own type.
func TestDetectInherentImplCollisions_ReportsEachOriginSeparately(t *testing.T) {
	recs := []InherentMethodRecord{
		{Receiver: "Date", Method: "new", ReceiverOrigin: OriginEntry, Fn: &ast.FuncDef{Line: 8, Col: 3}},
		{Receiver: "Date", Method: "new", ReceiverOrigin: OriginEntry, Fn: &ast.FuncDef{Line: 14, Col: 3}},
		{Receiver: "Date", Method: "new", ReceiverOrigin: "shapes", Fn: &ast.FuncDef{Line: 20, Col: 3}},
		{Receiver: "Date", Method: "new", ReceiverOrigin: "shapes", Fn: &ast.FuncDef{Line: 26, Col: 3}},
	}
	errs := detectInherentImplCollisions(recs)
	if len(errs) != 2 {
		t.Fatalf("got %d errors, want 2: %+v", len(errs), errs)
	}
	if errs[0].Line != 8 || errs[1].Line != 20 {
		t.Errorf("anchors = %d, %d; want 8 then 20", errs[0].Line, errs[1].Line)
	}
}
