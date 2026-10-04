package analysis

import "fmt"

// PartialType represents `Partial<T>` — a *deep partial* of a struct `T`. It is
// the parameter type of `Struct.update(original: T, updates: Partial<T>): T`,
// expressing that the patch may omit fields (they are taken from `original`) and
// may itself patch nested struct fields recursively. `Partial<T>` only appears in
// parameter position; there is no construction or return-position machinery for
// it.
//
// Note: `unifyFull` deliberately has no `PartialType` case — a `Partial<T>`
// parameter never unifies structurally with an argument. The deep-partial check
// is performed by `deepPartialMatches`, reached via `argSatisfiesConcreteParam`
// (generic call sites) and `argMatchesParam` (monomorphic). A consequence is
// that a `Partial<T>` parameter cannot itself *solve* `T`; in `Struct.update`
// the preceding `original: T` parameter binds `T` first.
//
// Because the check is separate from the unifier, every coercion the unifier
// does NOT implement has to be reached explicitly from the deep-partial walk.
// Interface-impl admission is the one such coercion: it lives in
// `ifaceParamAdmits`, and `deepPartialMatches` calls it for an interface-typed
// field. That is a shared call, not a second copy — see the note there.
type PartialType struct{ Inner Type }

func (t *PartialType) String() string {
	if t.Inner == nil {
		return "Partial<?>"
	}
	return "Partial<" + t.Inner.String() + ">"
}
func (t *PartialType) typeTag() {}

// deepPartialMatches reports whether `arg` is a valid argument for `Partial<target>`:
//
//   - any value that unifies with `target` (a whole struct, or a nested field's
//     full struct value) is accepted — full-value replacement; or
//   - a concrete implementer of an interface-typed `target`, via the shared
//     `ifaceParamAdmits`; or
//   - an anon-struct whose every field name exists in `target` (a struct, or
//     anon struct) and whose every field value is itself a deep partial of the
//     corresponding `target` field — so `{name}`, `{address: {city}}`, and
//     `{address: Address}` all match `Partial<Person>` while `{bogus: 5}` and a
//     type-mismatched `{name: 5}` do not.
//
// `reg` resolves a thin `StructType` name reference to its field set when the
// struct does not carry its `Fields` inline; pass nil when fields are inline
// (or for non-struct targets). `recPos` is the patch argument's position; it is
// carried unchanged into the recursion because the whole patch is one argument
// and a nested field has no node of its own here (only types reach this walk).
//
// Bare `Unify` handles structural / embeds / bool coercion. Interface-impl
// coercion is NOT in the unifier — it is `ifaceParamAdmits` — so an
// interface-typed field needs the explicit arm below. Scoped values are
// replaced as whole values, so immutable record updates must accept concrete
// implementations in interface-typed fields just like struct literals do.
func (c *checker) deepPartialMatches(arg, target Type, reg *TypeRegistry, recPos Pos) bool {
	if Unify(target, arg) == nil {
		return true
	}
	if iface, ok := resolveTypeVar(target).(*InterfaceType); ok {
		return c.ifaceParamAdmits(arg, iface, recPos, RecordingKindInterfaceTypedParam)
	}
	anon, ok := resolveTypeVar(arg).(*AnonStructType)
	if !ok {
		return false
	}
	fields := partialTargetFields(target, reg)
	if fields == nil {
		return false
	}
	byName := make(map[string]Type, len(fields))
	for _, f := range fields {
		byName[f.Name] = f.Type
	}
	for _, af := range anon.Fields {
		tf, ok := byName[af.Name]
		if !ok {
			return false
		}
		if !c.deepPartialMatches(af.Type, tf, reg, recPos) {
			return false
		}
	}
	return true
}

// partialPatchOK reports whether `arg` is an acceptable `Partial<target>` patch,
// and reports the opacity diagnostic when the patch names a field of an opaque
// struct declared outside the file being checked. It is the single admission
// gate for a `Partial<T>` argument: `argMatchesParam` (monomorphic) and
// `argSatisfiesConcreteParam` (generic, via reportGenericArgMismatch) both go
// through it, so no call shape reaches `deepPartialMatches` without the opacity
// walk. `line`/`col` position the diagnostic at the patch argument.
//
// The return value is the *shape* verdict alone. A patch that matches
// structurally but breaches opacity returns true with the error already
// recorded, so the caller emits no second, redundant `expected Partial<T>`
// message — the same handled-and-reported convention `fieldTypeFromObject`
// uses for field access on an opaque struct.
func (c *checker) partialPatchOK(arg, target Type, line, col int) bool {
	if tyName, fieldName, found := c.opaquePatchField(arg, target); found {
		c.addError(line, col, fmt.Sprintf(
			"cannot update field '%s' of opaque type '%s' outside its defining module — use an exported constructor function",
			fieldName, tyName))
		return true
	}
	return c.deepPartialMatches(arg, target, c.reg, c.recPos(line, col))
}

// opaquePatchField finds the first field in a `Partial<target>` patch that names
// a field of an opaque struct declared outside the file being checked, returning
// the opaque type's name and the offending field name.
//
// Naming a private field is the exposure, wherever the name appears. Field
// access (`c.value`), a pattern (`case c { Counter{value} -> ... }`), a struct
// literal (`Counter{value: 0}`) and the call form (`Counter({value: 0})`) are
// guarded elsewhere. An update patch (`Struct.update(c, {value: -999})`) is the
// fifth spelling, and this walk guards it.
//
// The walk is keyed on the PATCH rather than on `Struct.update`'s receiver,
// because the receiver is not always the opaque type: a patch routed through a
// transparent wrapper (`Struct.update(box, {inner: {value: -999}})`, where
// `Box.inner: Counter`) reaches an opaque field with a non-opaque receiver, and
// a receiver-keyed rule would miss it. Recursion mirrors `deepPartialMatches`
// so the two agree on which positions a patch reaches.
//
// A whole-value replacement names no field and is clean, including replacing an
// opaque field wholesale (`Struct.update(box, {inner: counter.new()})`): the
// non-anon-struct arm below is exactly that case. Deliberately NOT keyed on
// `Unify(target, arg)` succeeding, so the verdict does not depend on whether
// unification happens to admit a narrower anonymous struct against a named one.
func (c *checker) opaquePatchField(arg, target Type) (typeName, fieldName string, found bool) {
	anon, ok := resolveTypeVar(arg).(*AnonStructType)
	if !ok || len(anon.Fields) == 0 {
		return "", "", false
	}
	if st, ok := resolveTypeVar(target).(*StructType); ok {
		st = canonicalStructForFieldAccess(c, st)
		if st != nil && st.Opaque && (c.fa == nil || c.fa.FilePath != st.OwningSourceFile) {
			return st.Name, anon.Fields[0].Name, true
		}
	}
	fields := partialTargetFields(target, c.reg)
	if fields == nil {
		return "", "", false
	}
	byName := make(map[string]Type, len(fields))
	for _, f := range fields {
		byName[f.Name] = f.Type
	}
	for _, af := range anon.Fields {
		tf, ok := byName[af.Name]
		if !ok {
			continue
		}
		if tn, fn, hit := c.opaquePatchField(af.Type, tf); hit {
			return tn, fn, true
		}
	}
	return "", "", false
}

// partialTargetFields returns the field set of a struct-shaped target, resolving
// a thin StructType name reference through `reg` and substituting any generic
// type args into the field types. Returns nil for non-struct targets.
func partialTargetFields(target Type, reg *TypeRegistry) []FieldDef {
	switch t := resolveTypeVar(target).(type) {
	case *AnonStructType:
		return t.Fields
	case *StructType:
		fields := t.Fields
		if len(fields) == 0 && reg != nil {
			if looked, ok := reg.Lookup(t.Name).(*StructType); ok {
				fields = looked.Fields
			}
		}
		if len(t.TypeArgs) > 0 && len(t.TypeParamDefs) == len(t.TypeArgs) {
			subs := make(map[*TypeParam_]Type, len(t.TypeArgs))
			for i, tp := range t.TypeParamDefs {
				subs[tp] = t.TypeArgs[i]
			}
			out := make([]FieldDef, len(fields))
			for i, f := range fields {
				out[i] = FieldDef{Name: f.Name, Type: Substitute(f.Type, subs)}
			}
			return out
		}
		return fields
	}
	return nil
}
