package irbuild

import (
	"reflect"
	"strings"
)

// mapKindOfGoType is the kind an rt signature's `rt.Map[K, V]` names, or
// kindInvalid.
//
// Identified STRUCTURALLY, for the reasons listKindOfGoType gives: a
// hand-written `reflect.Type` per instantiation would be an unbounded table, and
// a name-only match would accept an unrelated rt type spelled `Map`.
//
// # Why this is a walk and not a field read
//
// `*rt.List[T]` exposes its element on an EXPORTED field, so its projection is
// one hop. `rt.Map[K, V]` exposes neither type argument anywhere: its three
// fields are `root *mapNode[K, V]`, `n int` and `seq int`, all unexported, and
// the key operations are deliberately NOT fields — carrying them would make
// every Map value two words fatter and store per value what the builder knows
// statically. There are no methods on Map either, only free functions, so
// reflect cannot read the instantiation off a signature.
//
// So the arguments are reached where rt actually spells them, through the
// storage: Map -> root -> mapNode -> leaf -> mapEntry -> the embedded
// MapEntry[K, V], whose `Key` and `Val` ARE exported because that struct is the
// boundary type. Every hop is shape-checked, which is what makes the walk safe
// rather than merely deep — a struct that happens to carry a K is rejected
// unless it carries it in exactly this shape. Unexported field TYPES are
// readable through reflect; no value is ever read.
//
// The projection is recursive for free, exactly as the list one is:
// `rt.Map[string, *rt.List[int64]]` answers `Map<String, List<Int>>`.
//
// A drift in any of those five shapes returns kindInvalid, which surfaces as a
// spec/rt mismatch in TestStdStructGoWidthMatchesTheDeclaredField rather than as
// a field projected onto a type nobody declared.
func mapKindOfGoType(t reflect.Type) kind {
	if t == nil || t.Kind() != reflect.Struct || t.PkgPath() != rtModulePath {
		return kindInvalid
	}
	if base, _, generic := strings.Cut(t.Name(), "["); !generic || base != "Map" {
		return kindInvalid
	}
	// An unaccounted field is storage this builder never writes; reading type
	// arguments off a struct whose shape has drifted would project a signature
	// nobody declared. Same check, same reason, as listKindOfGoType.
	if t.NumField() != 3 {
		return kindInvalid
	}
	root, hasRoot := t.FieldByName("root")
	n, hasN := t.FieldByName("n")
	seq, hasSeq := t.FieldByName("seq")
	if !hasRoot || !hasN || !hasSeq ||
		n.Type.Kind() != reflect.Int || seq.Type.Kind() != reflect.Int ||
		root.Type.Kind() != reflect.Pointer {
		return kindInvalid
	}
	entry := mapEntryStructOf(root.Type.Elem())
	if entry == nil {
		return kindInvalid
	}
	keyField, hasKey := entry.FieldByName("Key")
	valField, hasVal := entry.FieldByName("Val")
	if !hasKey || !hasVal {
		return kindInvalid
	}
	key, val := kindOfGoType(keyField.Type), kindOfGoType(valField.Type)
	// A component rt spells but this builder cannot project makes the whole Map
	// unprojectable. There is no position to report at either: this reads a
	// reflect.Type, not a node.
	// kindInvalid: propagates — the component's own decline is the answer.
	if key == kindInvalid || val == kindInvalid {
		return kindInvalid
	}
	// Nil gen: both components are package-neutral by construction here — a
	// non-neutral one could not have projected — so this interns in the
	// process-wide table, which is the same entry a call site's own mapKind
	// produces. See stdMapField.
	return mapKindIn(nil, key, val)
}

// mapEntryStructOf walks a `*mapNode[K, V]`'s target to the `MapEntry[K, V]`
// that carries the two type arguments as EXPORTED fields, or nil.
//
// Two hops with a shape check each. `mapNode` is the trie node — five fields,
// two bitmaps, two slices and the owner token an in-place write checks
// (rt/transient.go) — and `leaf` is `[]mapEntry[K, V]`, where
// `mapEntry` is the STORAGE type: it embeds the boundary `MapEntry[K, V]` and
// adds a cached hash and an insertion sequence. rt's own comment says the
// storage type is not the boundary type and keeps them one declaration apart
// precisely so the two field lists cannot drift; this reads the boundary one.
func mapEntryStructOf(node reflect.Type) reflect.Type {
	if node.Kind() != reflect.Struct || node.PkgPath() != rtModulePath {
		return nil
	}
	if base, _, generic := strings.Cut(node.Name(), "["); !generic || base != "mapNode" {
		return nil
	}
	if node.NumField() != 5 {
		return nil
	}
	leaf, hasLeaf := node.FieldByName("leaf")
	if !hasLeaf || leaf.Type.Kind() != reflect.Slice {
		return nil
	}
	stored := leaf.Type.Elem()
	if stored.Kind() != reflect.Struct || stored.PkgPath() != rtModulePath {
		return nil
	}
	if base, _, generic := strings.Cut(stored.Name(), "["); !generic || base != "mapEntry" {
		return nil
	}
	if stored.NumField() == 0 {
		return nil
	}
	boundary := stored.Field(0)
	if !boundary.Anonymous || boundary.Type.Kind() != reflect.Struct {
		return nil
	}
	if base, _, generic := strings.Cut(boundary.Type.Name(), "["); !generic || base != "MapEntry" {
		return nil
	}
	return boundary.Type
}
