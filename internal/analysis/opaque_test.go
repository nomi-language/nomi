package analysis

import (
	"testing"
)

// Tests for opaque distinct types (spec §15.3).

func TestOpaque_SymbolFlagSet(t *testing.T) {
	src := `pub opaque type PositiveInt Int`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)
	sym := fa.ModuleScope.Lookup("PositiveInt")
	if sym == nil {
		t.Fatal("PositiveInt symbol not found")
	}
	if !sym.Opaque {
		t.Errorf("expected PositiveInt symbol Opaque=true, got false")
	}
}

func TestOpaque_NonOpaqueSymbolFlagFalse(t *testing.T) {
	src := `pub type Foo Int`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)
	sym := fa.ModuleScope.Lookup("Foo")
	if sym == nil {
		t.Fatal("Foo symbol not found")
	}
	if sym.Opaque {
		t.Errorf("expected Foo symbol Opaque=false, got true")
	}
}

// Round 2.6 — visibility consistency relaxation.
// For opaque distinct types, the wrapped representation may be private.
// This test exercises a public opaque type wrapping a module-private
// type — without the relaxation, §3 would reject the export. With the
// relaxation, this is the canonical "public handle, private internals"
// idiom and must be allowed.

func TestOpaque_AllowsPrivateRepresentation(t *testing.T) {
	src := `struct InternalUserData { id: Int; label: String }

pub opaque type UserHandle InternalUserData

pub fn create(id: Int, label: String): UserHandle {
  UserHandle(InternalUserData{id: id, label: label})
}

pub fn name(h: UserHandle): String {
  case h {
    UserHandle(d) -> d.label
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestVisibility_NonOpaquePublicTypeStillRequiresPublicRepresentation(t *testing.T) {
	// Sanity check that the existing §3 rule still fires for non-opaque
	// distinct types: their wrapped representation must be exported.
	src := `struct Internal { x: Int }

pub type Foo Internal`
	_, errs := checkSource(src)
	expectError(t, errs, "private type")
}

func TestForeignTypeBinding_MustBeOpaque(t *testing.T) {
	src := `gopkg "example.com/app/ffi" as ffi

type RawBox go ffi.Box`
	_, errs := checkSource(src)
	expectError(t, errs, "must be declared with `opaque type`")
}

func TestForeignTypeBinding_PublicOpaqueAccepted(t *testing.T) {
	src := `gopkg "example.com/app/ffi" as ffi

pub opaque type RawBox go ffi.Box`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)
	sym := fa.ModuleScope.Lookup("RawBox")
	if sym == nil {
		t.Fatal("RawBox symbol not found")
	}
	if !sym.Public || !sym.Opaque {
		t.Fatalf("expected public opaque RawBox, got public=%v opaque=%v", sym.Public, sym.Opaque)
	}
}

func TestForeignTypeBinding_PrivateOpaqueAccepted(t *testing.T) {
	src := `gopkg "example.com/app/ffi" as ffi

opaque type RawBox go ffi.Box`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)
	sym := fa.ModuleScope.Lookup("RawBox")
	if sym == nil {
		t.Fatal("RawBox symbol not found")
	}
	if sym.Public {
		t.Error("expected RawBox to be private")
	}
	if !sym.Opaque {
		t.Error("expected RawBox to be opaque")
	}
}

// ---------------------------------------------------------------------------
// Inline opaque declarations (`pub opaque type ...` / `opaque type ...`).
// Plan A.9: validation moves from the export-block site to the type
// declaration site so inline-opacity is no longer silently accepted.
// ---------------------------------------------------------------------------

func TestOpaqueInline_AcceptedOnStructBody(t *testing.T) {
	// Inline struct body is a valid representation to make opaque —
	// equivalent to wrapping an unnamed inner struct.
	src := `pub opaque struct Foo { x: Int }`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)
	sym := fa.ModuleScope.Lookup("Foo")
	if sym == nil {
		t.Fatal("Foo symbol not found")
	}
	if !sym.Opaque {
		t.Errorf("expected Foo symbol Opaque=true, got false")
	}
	if st, ok := sym.Type.(*StructType); !ok || !st.Opaque {
		t.Errorf("expected StructType Opaque=true, got %T %#v", sym.Type, sym.Type)
	}
}

func TestOpaqueInline_AcceptedOnEnumBody(t *testing.T) {
	// Inline enum body is a valid representation to make opaque —
	// equivalent to wrapping an unnamed sum type.
	src := `pub opaque enum Color { Red; Blue }`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)
	sym := fa.ModuleScope.Lookup("Color")
	if sym == nil {
		t.Fatal("Color symbol not found")
	}
	if !sym.Opaque {
		t.Errorf("expected Color symbol Opaque=true, got false")
	}
	if et, ok := sym.Type.(*EnumType); !ok || !et.Opaque {
		t.Errorf("expected EnumType Opaque=true, got %T %#v", sym.Type, sym.Type)
	}
}

func TestOpaqueInline_RejectedOnZeroSizedDistinct(t *testing.T) {
	src := `pub opaque type Marker`
	_, errs := checkSource(src)
	expectError(t, errs, "opaque")
}

func TestOpaqueInline_AcceptedOnDistinctType(t *testing.T) {
	src := `pub opaque type UserId Int`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestOpaqueInline_PrivateOpaqueAccepted(t *testing.T) {
	// Opacity is independent of visibility: a private opaque distinct type
	// is well-formed (it just has no callers outside the module to care).
	src := `opaque type UserId Int`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestOpaqueInline_PrivateOpaqueAcceptedOnStructBody(t *testing.T) {
	// Opacity is independent of visibility: a private opaque struct-body
	// type is well-formed (it just has no callers outside the module to
	// care). Sanity check that the inline-struct opaque doesn't require
	// `pub`.
	src := `opaque struct Foo { x: Int }`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestInlineOpaque_SymbolFlagSet(t *testing.T) {
	// The inline `pub opaque type` declaration must set sym.Opaque=true,
	// mirroring TestOpaque_SymbolFlagSet for the export-block path. Without
	// this, downstream consumers (LSP hover opacity note, type_builder
	// opacity branches, visibility-consistency relaxation) silently fall
	// back to the non-opaque path.
	src := `pub opaque type UserId Int`
	fa, errs := checkSource(src)
	expectNoErrors(t, errs)
	sym := fa.ModuleScope.Lookup("UserId")
	if sym == nil {
		t.Fatal("UserId symbol not found")
	}
	if !sym.Opaque {
		t.Errorf("expected UserId symbol Opaque=true, got false")
	}
}

func TestInlineOpaque_AllowsPrivateRepresentation(t *testing.T) {
	// Mirrors TestOpaque_AllowsPrivateRepresentation but with inline
	// `pub opaque type` instead of an export-block `opaque <name>` entry.
	// The visibility-consistency relaxation rule must fire so the public
	// opaque type is allowed to wrap a module-private representation.
	src := `
struct InternalUserData { id: Int; label: String }

pub opaque type UserHandle InternalUserData
`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}
