package vm

// Construction and projection of rt records.
//
// A construction's descriptor is decided once, by makePlan, from the
// construction's own node and the stored types of its destination and
// operands (see values.go). The bytecode compiler turns a plan into `opMake`,
// which writes each operand into its field's slot; the handler below runs the
// same plan over boxed operands for a construction the compiler left to it,
// and builds the collections and ranges that are not records.

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// makePlan is how one construction builds its value: a shared value that
// needs no allocation (a bare variant, a marker, a Bool), or a descriptor, a
// variant tag, and for each operand the index of the field it fills.
type makePlan struct {
	shared any
	desc   *rt.TypeDesc
	tag    int
	fields []int
}

// planMake decides n's plan. typeOf is the stored type of a temporary. It
// answers false for a construction that is not a record (a collection, a
// range, an update) or that widens into an `embeds` variant.
func planMake(n *ir.Make, dst *ir.ValType, typeOf func(ir.Temp) *ir.ValType) (makePlan, bool) {
	operandSlot := func(i int) rt.SlotType { return slotOf(typeOf(n.Operand(i))) }
	seq := func(k int) []int {
		out := make([]int, k)
		for i := range out {
			out[i] = i
		}
		return out
	}
	switch n.Kind() {
	case ir.MakeStruct:
		name := n.Typ().Name()
		names := n.Names()
		if len(names) != n.Arity() || hasDuplicate(names) {
			return makePlan{}, false
		}
		if d := descOfType(dst); d != nil && d.Kind == rt.KindStruct && d.Name == name && len(d.Fields) == len(names) {
			if idx, ok := indicesByName(&d.Layout, names); ok {
				return makePlan{desc: d, fields: idx}, true
			}
		}
		specs := make([]rt.FieldSpec, len(names))
		for i, name := range names {
			specs[i] = rt.FieldSpec{Name: name, Type: operandSlot(i)}
		}
		return makePlan{desc: instStructDesc(name, instOf(dst, name), specs), fields: seq(len(names))}, true
	case ir.MakeRecord:
		names := n.Names()
		if len(names) != n.Arity() || hasDuplicate(names) {
			return makePlan{}, false
		}
		specs := make([]rt.FieldSpec, len(names))
		for i, name := range names {
			specs[i] = rt.FieldSpec{Name: name, Type: operandSlot(i)}
		}
		d := rt.AnonDesc(specs)
		idx, _ := indicesByName(&d.Layout, names)
		return makePlan{desc: d, fields: idx}, true
	case ir.MakeTuple:
		slots := make([]rt.SlotType, n.Arity())
		for i := range slots {
			slots[i] = operandSlot(i)
		}
		return makePlan{desc: rt.TupleDesc(slots...), fields: seq(len(slots))}, true
	case ir.MakeDistinct:
		if n.Arity() != 1 {
			return makePlan{}, false
		}
		name := n.Typ().Name()
		if d := descOfType(dst); d != nil && d.Kind == rt.KindDistinct && d.Name == name && len(d.Fields) == 1 {
			return makePlan{desc: d, fields: []int{0}}, true
		}
		return makePlan{desc: distinctDesc(name, slotPtr(operandSlot(0))), fields: []int{0}}, true
	case ir.MakeVariant:
		if n.Embeds() != nil {
			return makePlan{}, false
		}
		name, variant, names := n.Typ().Name(), n.Variant(), n.Names()
		if n.Arity() == 0 && rt.ShortTypeName(name) == "Bool" && (variant == "True" || variant == "False") {
			return makePlan{shared: variant == "True"}, true
		}
		if len(names) > 0 && (len(names) != n.Arity() || hasDuplicate(names)) {
			return makePlan{}, false
		}
		if len(names) == 0 && n.Arity() > 1 {
			return makePlan{}, false
		}
		if d := descOfType(dst); d != nil && d.Kind == rt.KindEnum && d.Name == name {
			if tag := d.VariantIndex(variant); tag >= 0 {
				vd := &d.Variants[tag]
				switch {
				case n.Arity() == 0 && vd.Shape == rt.VariantBare:
					return makePlan{shared: d.NewVariant(tag)}, true
				case len(names) == 0 && n.Arity() == 1 && vd.Shape == rt.VariantPositional:
					return makePlan{desc: d, tag: tag, fields: []int{0}}, true
				case len(names) > 0 && vd.Shape == rt.VariantFields && len(vd.Fields) == len(names):
					if idx, ok := indicesByName(&vd.Layout, names); ok {
						return makePlan{desc: d, tag: tag, fields: idx}, true
					}
				}
			}
		}
		spec := rt.VariantSpec{Name: variant}
		inst := instOf(dst, name)
		switch {
		case n.Arity() == 0:
			spec.Shape = rt.VariantBare
			return makePlan{shared: instEnumDesc(name, inst, []rt.VariantSpec{spec}).NewVariant(0)}, true
		case len(names) == 0:
			spec.Shape = rt.VariantPositional
			spec.Fields = []rt.FieldSpec{{Type: operandSlot(0)}}
			return makePlan{desc: instEnumDesc(name, inst, []rt.VariantSpec{spec}), fields: []int{0}}, true
		default:
			spec.Shape = rt.VariantFields
			spec.Fields = make([]rt.FieldSpec, len(names))
			for i, fname := range names {
				spec.Fields[i] = rt.FieldSpec{Name: fname, Type: operandSlot(i)}
			}
			return makePlan{desc: instEnumDesc(name, inst, []rt.VariantSpec{spec}), fields: seq(len(names))}, true
		}
	}
	return makePlan{}, false
}

// instOf is the instance key a construction of the type named name stamps on
// its value: its destination type's, when that type is the one constructed.
func instOf(dst *ir.ValType, name string) string {
	if dst == nil || dst.Sym() == nil || dst.Sym().Name() != name {
		return ""
	}
	return dst.InstanceKey()
}

func hasDuplicate(names []string) bool {
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if seen[n] {
			return true
		}
		seen[n] = true
	}
	return false
}

// indicesByName maps each name to its field's index in l.
func indicesByName(l *rt.Layout, names []string) ([]int, bool) {
	out := make([]int, len(names))
	for i, name := range names {
		j := l.FieldIndex(name)
		if j < 0 {
			return nil, false
		}
		out[i] = j
	}
	return out, true
}

// make compiles a construction whose plan is known now: a shared value is a
// constant, and anything else is opMake over its descriptor.
func (cp *compiler) make(n *ir.Make) bool {
	if n.Kind() == ir.MakeDistinct && n.Arity() == 1 {
		// A distinct over a scalar whose destination holds the scalar in a
		// register is the scalar: its identity is the destination's type.
		d, s := cp.loc(n.Dst()), cp.loc(n.Operand(0))
		if (d.bank() == bankW || d.bank() == bankS) && d.bank() == s.bank() {
			cp.move(n.Dst(), n.Operand(0))
			return true
		}
	}
	if !cp.inBank(bankR, n.Dst()) {
		return false
	}
	plan, ok := planMake(n, cp.c.types[n.Dst()], cp.typeOf)
	if !ok {
		return false
	}
	if plan.shared != nil {
		cp.c.kr = append(cp.c.kr, plan.shared)
		cp.emit(cp.op(opLoadR, uint32(cp.loc(n.Dst()).reg())), uint32(len(cp.c.kr)-1))
		return true
	}
	cp.c.descs = append(cp.c.descs, plan.desc)
	cp.emit(cp.op(opMake, uint32(cp.loc(n.Dst()).reg())), uint32(len(cp.c.descs)-1), uint32(plan.tag),
		uint32(len(plan.fields)))
	for i, fi := range plan.fields {
		cp.emit(uint32(fi), uint32(n.Operand(i)))
	}
	return true
}

// typeOf is temporary t's stored type, or nil.
func (cp *compiler) typeOf(t ir.Temp) *ir.ValType {
	if t == ir.NoTemp || int(t) >= len(cp.c.types) {
		return nil
	}
	return cp.c.types[t]
}

// makeValue builds one composite value out of the operands a construction
// names, for a construction the compiler left to its handler.
func (m *Machine) makeValue(fr *frame, n *ir.Make) (err error) {
	defer recoverKey(&err)
	switch n.Kind() {
	case ir.MakeRange:
		var start any = int64(0)
		var err error
		if n.Operand(0) != ir.NoTemp {
			start, err = fr.read(n.Operand(0))
			if err != nil {
				return err
			}
		}
		end := noneValue
		if n.Operand(1) != ir.NoTemp {
			v, err := fr.read(n.Operand(1))
			if err != nil {
				return err
			}
			end = some(v)
		}
		r := rangeDesc.New()
		r.R[0], r.R[1], r.W[0] = start, end, b2w(n.Inclusive())
		fr.write(n.Dst(), r)
		return nil
	case ir.MakeMap:
		entries := make([]rt.MapEntry[any, any], n.Arity()/2)
		for i := range entries {
			key, err := fr.read(n.Operand(2 * i))
			if err != nil {
				return err
			}
			val, err := fr.read(n.Operand(2*i + 1))
			if err != nil {
				return err
			}
			entries[i] = rt.MapEntry[any, any]{Key: key, Val: val}
		}
		var k *rt.Keys
		if len(entries) > 0 {
			k = m.keysOver(fr, entries[0].Key)
		}
		fr.write(n.Dst(), mapOf(k, entries))
		return nil
	case ir.MakeVector, ir.MakeSet:
		items := make([]any, n.Arity())
		for i := range items {
			v, err := fr.read(n.Operand(i))
			if err != nil {
				return err
			}
			items[i] = v
		}
		if n.Kind() == ir.MakeSet {
			var k *rt.Keys
			if len(items) > 0 {
				k = m.keysOver(fr, items[0])
			}
			fr.write(n.Dst(), newSetKeyed(k, items))
		} else {
			fr.write(n.Dst(), rt.VectorOf(items))
		}
		return nil
	case ir.MakeList:
		var tail *list
		if n.Tail() != ir.NoTemp {
			v, err := fr.read(n.Tail())
			if err != nil {
				return err
			}
			var ok bool
			tail, ok = v.(*list)
			if !ok {
				return fmt.Errorf("vm: %s: list tail is %T, not a List", fr.fn.Name(), v)
			}
		}
		for i := n.Arity() - 1; i >= 0; i-- {
			v, err := fr.read(n.Operand(i))
			if err != nil {
				return err
			}
			tail = cons(v, tail)
		}
		fr.write(n.Dst(), tail)
		return nil
	case ir.MakeUpdate:
		base, err := fr.read(n.Operand(0))
		if err != nil {
			return err
		}
		rec, ok := base.(*rt.Record)
		if !ok || rec == nil || (rec.Desc.Kind != rt.KindStruct && rec.Desc.Kind != rt.KindAnon) {
			return fmt.Errorf("vm: %s: %s updates %T, not a struct", fr.fn.Name(), n, base)
		}
		names := n.Names()
		idx := make([]int, len(names))
		vals := make([]any, len(names))
		for i, name := range names {
			j := rec.Desc.FieldIndex(name)
			if j < 0 {
				return fmt.Errorf("vm: %s: %s names field %s the base does not carry", fr.fn.Name(), n, name)
			}
			v, err := fr.read(n.Operand(i + 1))
			if err != nil {
				return err
			}
			if !fitsSlot(rec.Desc.Fields[j].Type, v) {
				// The base's layout holds a scalar where this update writes a
				// value of another kind: rebuild over a layout that fits.
				return m.rebuildUpdate(fr, n, rec, names, i)
			}
			idx[i], vals[i] = j, v
		}
		fr.write(n.Dst(), rec.With(idx, vals))
		return nil
	case ir.MakeVariant:
		if embedded := n.Embeds(); embedded != nil {
			// An `embeds` widening does nothing to the value, so the enum
			// value is the embedded value; a zero-sized one is its marker.
			if n.Arity() == 0 {
				fr.write(n.Dst(), markerValue(embedded.Name()))
				return nil
			}
			v, err := fr.read(n.Operand(0))
			if err != nil {
				return err
			}
			fr.write(n.Dst(), v)
			return nil
		}
		if len(n.Names()) == 0 && n.Arity() > 1 {
			return fmt.Errorf("vm: %s: this machine constructs only bare or single-payload variants", fr.fn.Name())
		}
	case ir.MakeStruct, ir.MakeRecord:
		if names := n.Names(); len(names) != n.Arity() {
			return fmt.Errorf("vm: %s: %s names %d field(s) for %d operand(s)",
				fr.fn.Name(), n, len(names), n.Arity())
		} else if hasDuplicate(names) {
			return fmt.Errorf("vm: %s: %s assigns a field twice", fr.fn.Name(), n)
		}
	}
	plan, ok := planMake(n, fr.fn.TempType(n.Dst()), fr.fn.TempType)
	if !ok {
		return fmt.Errorf("vm: %s: this machine runs no %s construction", fr.fn.Name(), n.Kind())
	}
	if plan.shared != nil {
		fr.write(n.Dst(), plan.shared)
		return nil
	}
	var rec *rt.Record
	if plan.desc.Kind == rt.KindEnum {
		rec = plan.desc.NewVariant(plan.tag)
	} else {
		rec = plan.desc.New()
	}
	fields := rec.Layout().Fields
	for i, fi := range plan.fields {
		if err := fr.fill(rec, &fields[fi], n.Operand(i)); err != nil {
			return err
		}
	}
	fr.write(n.Dst(), rec)
	return nil
}

// fitsSlot reports whether v can be written into a slot of type t.
func fitsSlot(t rt.SlotType, v any) bool {
	switch t {
	case rt.SlotRef:
		return true
	case rt.SlotInt:
		_, ok := v.(int64)
		return ok
	case rt.SlotFloat:
		_, ok := v.(float64)
		return ok
	case rt.SlotBool:
		_, ok := v.(bool)
		return ok
	case rt.SlotByte:
		_, ok := v.(rt.Byte)
		return ok
	case rt.SlotString:
		_, ok := v.(string)
		return ok
	case rt.SlotBytes:
		_, ok := v.(rt.Bytes)
		return ok
	}
	return false
}

// rebuildUpdate is an update whose new values do not fit the base's slots: a
// fresh record of the base's identity whose every field is a reference.
func (m *Machine) rebuildUpdate(fr *frame, n *ir.Make, base *rt.Record, names []string, _ int) error {
	vals := make([]any, len(base.Desc.Fields))
	specs := make([]rt.FieldSpec, len(base.Desc.Fields))
	for i := range base.Desc.Fields {
		vals[i] = base.Field(i)
		specs[i] = rt.FieldSpec{Name: base.Desc.Fields[i].Name, Type: rt.SlotRef}
	}
	for i, name := range names {
		v, err := fr.read(n.Operand(i + 1))
		if err != nil {
			return err
		}
		vals[base.Desc.FieldIndex(name)] = v
	}
	var d *rt.TypeDesc
	if base.Desc.Kind == rt.KindAnon {
		d = rt.AnonDesc(specs)
	} else {
		d = instStructDesc(base.Desc.Name, base.Desc.Inst, specs)
	}
	fr.write(n.Dst(), d.Make(vals...))
	return nil
}

// projValue reads one component out of a composite value.
func projValue(fr *frame, n *ir.Proj, subj any) (any, error) {
	switch n.Kind() {
	case ir.ProjField, ir.ProjRecordField:
		rec, isStruct := subj.(*rt.Record)
		if !isStruct || rec == nil || (rec.Desc.Kind != rt.KindStruct && rec.Desc.Kind != rt.KindAnon) {
			return nil, fmt.Errorf("vm: %s: %s reads a field off %T, not a struct",
				fr.fn.Name(), n, subj)
		}
		v, found := rec.FieldNamed(n.Name())
		if !found {
			return nil, fmt.Errorf("vm: %s: %s names a field %s does not carry",
				fr.fn.Name(), n, rec.Desc.Name)
		}
		return v, nil

	case ir.ProjSlot:
		rec, isTuple := subj.(*rt.Record)
		if !isTuple || rec == nil || rec.Desc.Kind != rt.KindTuple {
			return nil, fmt.Errorf("vm: %s: %s reads a component off %T, not a tuple",
				fr.fn.Name(), n, subj)
		}
		if n.Index() >= rec.NumFields() {
			return nil, fmt.Errorf("vm: %s: %s names component %d of a %d-tuple",
				fr.fn.Name(), n, n.Index(), rec.NumFields())
		}
		return rec.Field(n.Index()), nil

	case ir.ProjElem, ir.ProjSuffix:
		xs, ok := subj.(*list)
		if !ok {
			return nil, fmt.Errorf("vm: %s: %s reads %T, not a list", fr.fn.Name(), n, subj)
		}
		for i := 0; i < n.Index(); i++ {
			if xs == nil {
				return nil, fmt.Errorf("vm: %s: %s projects beyond the list", fr.fn.Name(), n)
			}
			xs = xs.Tail
		}
		if n.Kind() == ir.ProjSuffix {
			return xs, nil
		}
		if xs == nil {
			return nil, fmt.Errorf("vm: %s: %s projects beyond the list", fr.fn.Name(), n)
		}
		return xs.Head, nil

	case ir.ProjEnumField:
		v, err := enumFieldValue(n, subj)
		if err != nil {
			// Every error enumFieldValue answers is rt's text for a variant
			// the read cannot answer, which the program caused.
			return nil, &Fault{err: err}
		}
		return v, nil

	case ir.ProjPayload:
		rec, isVariant := enumRecord(subj)
		if embedded := n.PayloadEmbeds(); embedded != nil && !isVariant {
			if runtimeTypeName(subj) != embedded.Name() {
				return nil, fmt.Errorf("vm: %s: %s reads an embedded %s off %T",
					fr.fn.Name(), n, embedded.Name(), subj)
			}
			return subj, nil
		}
		if !isVariant {
			return nil, fmt.Errorf("vm: %s: %s reads a payload off %T, not a variant",
				fr.fn.Name(), n, subj)
		}
		vd := &rec.Desc.Variants[rec.Tag]
		if field := n.PayloadField(); field != "" {
			if vd.Shape != rt.VariantFields {
				return nil, fmt.Errorf("vm: %s: %s reads field %s off %s, not a struct-shaped payload",
					fr.fn.Name(), n, field, vd.Name)
			}
			v, found := rec.FieldNamed(field)
			if !found {
				return nil, fmt.Errorf("vm: %s: %s names field %s the payload does not carry",
					fr.fn.Name(), n, field)
			}
			return v, nil
		}
		if n.Index() != 0 {
			return nil, fmt.Errorf("vm: %s: this machine reads only payload 0; %s selects "+
				"by position and a struct-shaped variant's payloads are keyed by name in "+
				"this representation", fr.fn.Name(), n)
		}
		switch vd.Shape {
		case rt.VariantBare:
			return nil, fmt.Errorf("vm: %s: %s reads a payload off the bare variant %s",
				fr.fn.Name(), n, vd.Name)
		case rt.VariantFields:
			// The whole payload of a struct-shaped variant is a struct named
			// after the variant.
			specs := make([]rt.FieldSpec, len(vd.Fields))
			vals := make([]any, len(vd.Fields))
			for i := range vd.Fields {
				specs[i] = rt.FieldSpec{Name: vd.Fields[i].Name, Type: vd.Fields[i].Type}
				vals[i] = rec.Field(i)
			}
			return structDesc(vd.Name, specs).Make(vals...), nil
		}
		return rec.Field(0), nil

	case ir.ProjInner:
		rec, isDistinct := subj.(*rt.Record)
		if !isDistinct || rec == nil || rec.Desc.Kind != rt.KindDistinct {
			// A distinct held in a scalar register reads back as its inner
			// value's register; boxed, it is always a record.
			return nil, fmt.Errorf("vm: %s: %s unwraps %T, not a distinct type",
				fr.fn.Name(), n, subj)
		}
		if rec.NumFields() == 0 {
			return nil, fmt.Errorf("vm: %s: %s unwraps the zero-sized type %s, which "+
				"carries nothing", fr.fn.Name(), n, rec.Desc.Name)
		}
		return rec.Field(0), nil
	}
	return nil, fmt.Errorf("vm: %s: this machine runs no %s projection",
		fr.fn.Name(), n.Kind())
}

// enumFieldValue reads a field name off an enum value whose variant is not
// known: a struct-shaped variant's own
// field, or a field of a struct payload. A value widened into an `embeds`
// variant is the embedded value itself here, so its variant is its type. The
// faults are rt's texts, keyed on the variant.
func enumFieldValue(n *ir.Proj, subj any) (any, error) {
	line, field := n.Pos().Line(), n.Name()
	variant := ""
	payload := subj
	if rec, ok := enumRecord(subj); ok {
		vd := &rec.Desc.Variants[rec.Tag]
		variant = vd.Name
		switch vd.Shape {
		case rt.VariantFields:
			if v, found := rec.FieldNamed(field); found {
				return v, nil
			}
			return nil, rt.EnumFieldMissingError(line, variant, field)
		case rt.VariantBare:
			return nil, rt.EnumFieldAccessError(line, variant, field)
		}
		p, populated := payloadOf(rec)
		if !populated {
			return nil, rt.EnumFieldAccessError(line, variant, field)
		}
		payload = p
	} else {
		variant = rt.ShortTypeName(runtimeTypeName(subj))
	}
	if rec, ok := payload.(*rt.Record); ok && rec != nil && rec.Desc.Kind == rt.KindStruct {
		if v, found := rec.FieldNamed(field); found {
			return v, nil
		}
		return nil, rt.EnumFieldMissingError(line, variant, field)
	}
	return nil, rt.EnumFieldAccessError(line, variant, field)
}
