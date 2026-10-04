package irbuild

// Field access on a value whose STATIC type does not name the storage.
//
// Nomi has no `.method()` postfix dispatch — `x.f` is field access and nothing
// else, and `x.f(...)` means "read the field, then call its value". So both
// cases here are about reading a field whose LOCATION the static type leaves
// open, and they are two cases rather than one:
//
//   - an ENUM. The type names several variants and only the run-time tag says
//     which is here, so the read is a tag switch. The checker types the access
//     from the FIRST variant that declares the name and the read answers from
//     the tag, which is why the two providers of one name cannot be
//     collapsed into one slot read.
//   - an EXISTENTIAL. There is no tag at all: an `rt.Dyn` carries a *rt.TypeID,
//     two implementors are two Go layouts, and the read goes through a table
//     keyed on identity. A BOUNDED TYPE PARAMETER is the same case and not a
//     third one, because dict.go lowers `T where T: H` to `existential(H)`.
//
// They share a guard site in `fieldAccess` and nothing else.
//
// # The rule
//
// A field read on an enum value reads the name out of the variant's payload
// only when that payload is a struct. That single rule covers three storage
// shapes the builder keeps apart:
//
//	Shape.Rect{radius: 1.5, height: 3}   struct-shaped   one slot per field
//	Shape.Square{radius: 9.0}            embeds a struct ONE slot, the struct
//	Shape.Wrap(Circle{radius: 4.0})      positional      ONE slot, the struct
//
// and it answers for all three. The last is the one a reader would not predict:
// a POSITIONAL payload that happens to be a struct is indistinguishable from an
// `embeds` at run time, so the read goes through it, and the checker agrees (`fieldTypeFromObject`'s `v.DataType.(*StructType)` arm).
//
// When the tag is not a provider the read FAULTS, and which fault is a
// per-variant fact:
//
//	variant 'Rect' has no field 'radius'          payload is a struct, name absent
//	cannot access field 'height' on variant 'Num' payload is not a struct
//
// So one trap for the whole switch is the wrong answer, and testdata/
// enum_field_read_trap.nomi is the fixture that says so.
//
// A field read answers both for a `c: Shape` coerced from a bare Circle and
// for the same value built as `Shape.Circle{...}`:
// `07-structs-and-enums/enums_test.nomi` has
// `c = Drawable.Circle2{radius: 5.0}` / `assert c.radius == 5.0`, and
// `s: Shape = Circle{radius: 7.0}` answers 7.0 through the tagged variant this
// builder eagerly coerces it into.

// --- an interface `field` requirement ----------------------------------------

// ifaceField is one `field name: Type` requirement an interface declares.
//
// `ast.InterfaceField` obliges every implementor to declare a field of that
// name and that EXACT type, so the read is total over the implementors and the
// only open question at run time is where the field sits. That makes a getter
// table the right shape and makes it complete: every implementing type binds
// one, exactly as it binds a method.
type ifaceField struct {
	name string
	k    kind
	// table is the generated name of this field's rt.Method variable, empty
	// when the field's TYPE has no kind here — in which case the requirement
	// exists and cannot be read, which is a refusal at the read site rather
	// than on the declaration.
	table bool
}

// resolveIfaceFields resolves an interface's `field` requirements.
//
// Runs beside resolveIface's method loop and after the type table is complete,
// because a requirement may name a declared struct.
func (g *gen) resolveIfaceFields(d *ifaceDef) {
	for i := range d.decl.Fields {
		f := &d.decl.Fields[i]
		fd := &ifaceField{name: f.Name, k: g.typeOf(f.TypeAnnotation)}
		// kindInvalid: reports — the read site refuses, naming the interface and the field, which is a position somebody wrote.
		if fd.k != kindInvalid {
			fd.table = true
		}
		d.fields[f.Name] = fd
		d.fieldOrder = append(d.fieldOrder, fd)
	}
}

// bindIfaceFields emits one impl's field-getter bindings.
//
// Called from bindImpl, inside the same `init`, because the two are the same
// obligation: an interface a type implements has to be reachable at every
// requirement it declares, and a program that binds the methods and forgets the
// fields traps at run time on a program the checker accepted.
func (g *gen) bindIfaceFields(d *implDef) {
	for _, f := range d.iface.fieldOrder {
		if !f.table {
			continue
		}
		fd := fieldOfKind(d.recv, f.name)
		if fd == nil {
			// A FAIL-SAFE with no producible witness. An unbound getter traps
			// at the READ site with no name attached, so skipping here would
			// turn a front-end change into a run-time fault.
			//
			// A receiver that declares no field of this name is a front-end
			// error whatever its KIND (an enum, a distinct, a scalar —
			// `validateImplBlockFieldRequirements`, and a struct in the
			// ordinary way). TestFieldRead_TheCheckerWallsOffThreeBackstops
			// carries the enum row, asserting the front end's rejection and its
			// message, so a test fails if that rejection ever moves.
			g.reject("interface field requirement",
				d.iface.nomi+"."+f.name+" is not a field of "+d.recv.nomi(), d.decl)
			continue
		}
	}
}

// bindsAnyIfaceField reports whether this impl owes any field getter, so
// bindImpl knows an `init` is needed for an impl with no METHOD bindings — which
// is every `impl H for T` whose interface declares only requirements.
func (g *gen) bindsAnyIfaceField(d *implDef) bool {
	if d.iface == nil {
		return false
	}
	for _, f := range d.iface.fieldOrder {
		if f.table {
			return true
		}
	}
	return false
}

// fieldOfKind is the declared field of a receiver kind, or nil.
func fieldOfKind(recv kind, name string) *fieldDef {
	if recv.tag != tagNamed || recv.def == nil {
		return nil
	}
	return recv.def.field(name)
}
