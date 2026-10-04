package hostgen

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"

	"github.com/iancoleman/strcase"
	"github.com/nomi-language/nomi/rt"
)

// The conversions in this file are for Go functions written against rt's own
// types: the rt functions the VM's hand-written hosts call (rt.StringToInt,
// rt.JsonDecode, rt.SupervisorNewExact, ...). The marshaller has no rules for
// these, because no FFI binding takes them; what they must agree with is the
// VM's `value`-based hosts in internal/vm/*hosts.go, and the parity suite
// there holds them to it.
//
//   - rt's generic prelude types: rt.Maybe[T], rt.Result[T, E], *rt.List[T]
//     and rt.Map[K, V], each converted element by element to and from the
//     VM's forms (a Maybe or Result record, *rt.List[any], rt.Map[any, any]).
//   - rt's tagged-struct encoding of a std enum (rt.Json, rt.Ordering,
//     rt.Restart, rt.Backoff, rt.GiveUp, rt.Wait, rt.FlushOutcome): a Go
//     struct whose first field is `Tag uint8`, 1-based in declaration order,
//     with one Go field per payload. The Nomi value is an enum record.
//   - the rt types a VM holds directly in the ref bank (rt.Decimal,
//     rt.Context, rt.Supervisor, rt.Dynamic), which pass through unconverted.

var rtPkg = reflect.TypeFor[rt.Frame]().PkgPath()

// rtHeld is every `host type` whose Nomi value IS the rt Go value, by
// qualified Nomi name. The VM values design holds these in the ref bank as
// themselves, so an adapter neither wraps nor unwraps them.
var rtHeld = map[string]string{
	"decimal.Decimal":        TypeKey(ReflectType(reflect.TypeFor[rt.Decimal]())),
	"context.Context":        TypeKey(ReflectType(reflect.TypeFor[rt.Context]())),
	"supervisors.Supervisor": TypeKey(ReflectType(reflect.TypeFor[rt.Supervisor]())),
	"dynamic.Dynamic":        TypeKey(ReflectType(reflect.TypeFor[rt.Dynamic]())),
}

// rtGenericBase is the generic type an rt instantiation instantiates, or "".
func rtGenericBase(t Type) string {
	if t.PkgPath() != rtPkg {
		return ""
	}
	base, _, found := strings.Cut(t.Name(), "[")
	if !found {
		return ""
	}
	return base
}

// isRtGeneric reports whether t is one of the named rt generic types as a
// boundary carries it: *rt.List[T], or rt.Maybe, rt.Result, rt.Map by value.
func isRtGeneric(t Type, bases ...string) bool {
	base := rtGenericBase(t)
	if t.Kind() == reflect.Pointer {
		if rtGenericBase(t.Elem()) != "List" {
			return false
		}
		base = "List"
	} else if base == "List" {
		return false
	}
	for _, b := range bases {
		if b == base {
			return true
		}
	}
	return false
}

// rtGenericArgs answers the type arguments of an rt generic instantiation,
// read off its fields rather than parsed out of reflect's name.
func rtGenericArgs(t Type) (string, []Type, bool) {
	base := rtGenericBase(t)
	field := func(t Type, name string) Type {
		f, ok := t.FieldByName(name)
		if !ok {
			panic(fmt.Sprintf("hostgen: %s has no field %s", t, name))
		}
		return f.Type
	}
	switch base {
	case "Maybe":
		return base, []Type{field(t, "Some")}, true
	case "Result":
		return base, []Type{field(t, "Ok"), field(t, "Err")}, true
	case "List":
		return base, []Type{field(t, "Head")}, true
	case "Map":
		entry := field(field(t, "root").Elem(), "leaf").Elem()
		return base, []Type{field(entry, "Key"), field(entry, "Val")}, true
	}
	return "", nil, false
}

// rtKeyOps is the hash and equality a Go map key type's rt.Map is built
// with: the ones rt's own producers use (rt.JsonDecode builds its objects
// with rt.HashString and rt.Eq[string]).
func rtKeyOps(k Type) (string, string, error) {
	switch {
	case isPredeclared(k, "string"):
		return "rt.HashString", "rt.Eq[string]", nil
	case isPredeclared(k, "int64"):
		return "rt.HashInt", "rt.Eq[int64]", nil
	}
	return "", "", fmt.Errorf("Go map key %s: only string and int64 keys convert", k)
}

// inRtGeneric writes the body of an `in` method reading a Nomi value into an
// rt generic instantiation.
func (g *Generator) inRtGeneric(w *bytes.Buffer, s *Shape, t Type, goT string) error {
	elemOf := t
	if t.Kind() == reflect.Pointer {
		elemOf = t.Elem()
	}
	base, args, _ := rtGenericArgs(elemOf)
	want := map[string]ShapeKind{"Maybe": SMaybe, "Result": SResult, "List": SList, "Map": SMap}[base]
	if s.Kind != want {
		return fmt.Errorf("Go %s projects to %s, not %s", t, base, s)
	}
	fail := "\t\treturn out, err\n"
	switch base {
	case "Maybe", "Result":
		name, tags, fields := maybeName, []string{"Some", "None"}, []string{"Some", ""}
		consts := []string{"rt.TagSome", "rt.TagNone"}
		if base == "Result" {
			name, tags, fields = resultName, []string{"Ok", "Err"}, []string{"Ok", "Err"}
			consts = []string{"rt.TagOk", "rt.TagErr"}
		}
		fmt.Fprintf(w, "\tr, tag, err := hostadapt.Variant(v, %q)\n\tif err != nil {\n%s\t}\n\tswitch tag {\n", name, fail)
		for i, tag := range tags {
			fmt.Fprintf(w, "\tcase %q:\n", tag)
			if fields[i] == "" {
				fmt.Fprintf(w, "\t\treturn %s{Tag: %s}, nil\n", goT, consts[i])
				continue
			}
			conv, err := g.in(s.Elems[i], args[i])
			if err != nil {
				return fmt.Errorf("%s payload: %w", tag, err)
			}
			fmt.Fprintf(w, "\t\tx, err := b.%s(fr, hostadapt.Payload(r))\n\t\tif err != nil {\n\t\t\treturn out, fmt.Errorf(\"%s: %%w\", err)\n\t\t}\n\t\treturn %s{Tag: %s, %s: x}, nil\n",
				conv, tag, goT, consts[i], fields[i])
		}
		fmt.Fprintf(w, "\t}\n\treturn out, fmt.Errorf(\"unmarshal %s: unknown variant %%q\", tag)\n", base)
	case "List":
		conv, err := g.in(s.Elems[0], args[0])
		if err != nil {
			return fmt.Errorf("element: %w", err)
		}
		elemT, err := g.typ(args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "\txs, err := hostadapt.List(v)\n\tif err != nil {\n%s\t}\n", fail)
		fmt.Fprintf(w, "\titems := make([]%s, 0, hostadapt.Len(xs))\n\ti := 0\n", elemT)
		fmt.Fprintf(w, "\tfor c := xs; c != nil; c = c.Tail {\n\t\te, err := b.%s(fr, c.Head)\n\t\tif err != nil {\n\t\t\treturn out, fmt.Errorf(\"index %%d: %%w\", i, err)\n\t\t}\n\t\titems = append(items, e)\n\t\ti++\n\t}\n", conv)
		w.WriteString("\tfor k := len(items) - 1; k >= 0; k-- {\n\t\tout = rt.Cons(items[k], out)\n\t}\n\treturn out, nil\n")
	case "Map":
		hash, eq, err := rtKeyOps(args[0])
		if err != nil {
			return err
		}
		kin, err := g.in(s.Elems[0], args[0])
		if err != nil {
			return fmt.Errorf("key: %w", err)
		}
		vin, err := g.in(s.Elems[1], args[1])
		if err != nil {
			return fmt.Errorf("value: %w", err)
		}
		kT, err := g.typ(args[0])
		if err != nil {
			return err
		}
		vT, err := g.typ(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "\tm, ok := v.(rt.Map[any, any])\n\tif !ok {\n\t\treturn out, hostadapt.Want(%q, v)\n\t}\n", s.String())
		fmt.Fprintf(w, "\tents := rt.MapEntries(m)\n\tconv := make([]rt.MapEntry[%s, %s], len(ents))\n\tfor i, e := range ents {\n", kT, vT)
		fmt.Fprintf(w, "\t\tk, err := b.%s(fr, e.Key)\n\t\tif err != nil {\n\t\t\treturn out, fmt.Errorf(\"key %%d: %%w\", i, err)\n\t\t}\n", kin)
		fmt.Fprintf(w, "\t\tx, err := b.%s(fr, e.Val)\n\t\tif err != nil {\n\t\t\treturn out, fmt.Errorf(\"value %%d: %%w\", i, err)\n\t\t}\n", vin)
		fmt.Fprintf(w, "\t\tconv[i] = rt.MapEntry[%s, %s]{Key: k, Val: x}\n\t}\n\treturn rt.MapOf(%s, %s, conv), nil\n", kT, vT, hash, eq)
	}
	return nil
}

// outRtGeneric writes the body of an `out` method building a Nomi value from
// an rt generic instantiation.
func (g *Generator) outRtGeneric(w *bytes.Buffer, s *Shape, t Type) error {
	elemOf := t
	if t.Kind() == reflect.Pointer {
		elemOf = t.Elem()
	}
	base, args, _ := rtGenericArgs(elemOf)
	want := map[string]ShapeKind{"Maybe": SMaybe, "Result": SResult, "List": SList, "Map": SMap}[base]
	if s.Kind != want {
		return fmt.Errorf("Go %s projects to %s, not %s", t, base, s)
	}
	switch base {
	case "Maybe", "Result":
		d := g.enumDesc(s)
		tags, consts, fields := []string{"Some", "None"}, []string{"rt.TagSome", "rt.TagNone"}, []string{"Some", ""}
		if base == "Result" {
			tags, consts, fields = []string{"Ok", "Err"}, []string{"rt.TagOk", "rt.TagErr"}, []string{"Ok", "Err"}
		}
		w.WriteString("\tswitch v.Tag {\n")
		for i := range tags {
			fmt.Fprintf(w, "\tcase %s:\n", consts[i])
			if fields[i] == "" {
				fmt.Fprintf(w, "\t\treturn b.%s.NewVariant(%d), nil\n", d, i)
				continue
			}
			fmt.Fprintf(w, "\t\tr := b.%s.NewVariant(%d)\n", d, i)
			var body bytes.Buffer
			if err := g.store(&body, "r", 0, s.Elems[i], args[i], "v."+fields[i], false, fmt.Sprintf("fmt.Errorf(\"%s: %%w\", err)", tags[i])); err != nil {
				return fmt.Errorf("%s payload: %w", tags[i], err)
			}
			w.WriteString(indent(body.String()))
			w.WriteString("\t\treturn r, nil\n")
		}
		fmt.Fprintf(w, "\t}\n\treturn nil, fmt.Errorf(\"Go rt.%s carries tag %%d, which names no variant\", v.Tag)\n", base)
	case "List":
		conv, err := g.out(s.Elems[0], args[0])
		if err != nil {
			return fmt.Errorf("element: %w", err)
		}
		w.WriteString("\tn := 0\n\tif v != nil {\n\t\tn = v.Len\n\t}\n\titems := make([]rt.Value, 0, n)\n\ti := 0\n")
		fmt.Fprintf(w, "\tfor c := v; c != nil; c = c.Tail {\n\t\tx, err := b.%s(c.Head)\n\t\tif err != nil {\n\t\t\treturn nil, fmt.Errorf(\"index %%d: %%w\", i, err)\n\t\t}\n\t\titems = append(items, x)\n\t\ti++\n\t}\n", conv)
		w.WriteString("\tvar xs *rt.List[any]\n\tfor k := len(items) - 1; k >= 0; k-- {\n\t\txs = rt.Cons[any](items[k], xs)\n\t}\n\treturn xs, nil\n")
	case "Map":
		if _, _, err := rtKeyOps(args[0]); err != nil {
			return err
		}
		kout, err := g.out(s.Elems[0], args[0])
		if err != nil {
			return fmt.Errorf("key: %w", err)
		}
		vout, err := g.out(s.Elems[1], args[1])
		if err != nil {
			return fmt.Errorf("value: %w", err)
		}
		w.WriteString("\tents := rt.MapEntries(v)\n\tconv := make([]rt.MapEntry[any, any], len(ents))\n\tfor i, e := range ents {\n")
		fmt.Fprintf(w, "\t\tk, err := b.%s(e.Key)\n\t\tif err != nil {\n\t\t\treturn nil, fmt.Errorf(\"key %%d: %%w\", i, err)\n\t\t}\n", kout)
		fmt.Fprintf(w, "\t\tx, err := b.%s(e.Val)\n\t\tif err != nil {\n\t\t\treturn nil, fmt.Errorf(\"value %%d: %%w\", i, err)\n\t\t}\n", vout)
		w.WriteString("\t\tconv[i] = rt.MapEntry[any, any]{Key: k, Val: x}\n\t}\n\treturn rt.MapOf(rt.Hash, rt.Equal, conv), nil\n")
	}
	return nil
}

// taggedVariant pairs one enum variant with where rt's tagged struct keeps it.
type taggedVariant struct {
	ShapeVariant
	tag    int       // the Go Tag value, 1-based
	fields []goField // parallel to Fields
	// unrepresented is set when the Go struct has no room for the variant's
	// payload because the payload is not data (a function, say).
	unrepresented bool
}

// taggedLayout reads rt's tagged-struct encoding of enum s off Go type t and
// refuses any drift between the two: a data payload with no Go field, or a Go
// field no variant claims.
func taggedLayout(s *Shape, t Type) ([]taggedVariant, error) {
	if t.Kind() != reflect.Struct || t.PkgPath() != rtPkg || t.NumField() == 0 ||
		t.Field(0).Name != "Tag" || t.Field(0).Type.Kind() != reflect.Uint8 {
		return nil, fmt.Errorf("a user enum does not cross the host boundary unless its Go type is rt's tagged-struct "+
			"encoding of it (a struct in rt whose first field is `Tag uint8`); Go %s is not, and %s is an enum", t, s.Name)
	}
	fields, err := goFields(t)
	if err != nil {
		return nil, err
	}
	claimed := map[string]bool{"tag": true}
	out := make([]taggedVariant, len(s.Variants))
	for i, v := range s.Variants {
		tv := taggedVariant{ShapeVariant: v, tag: i + 1}
		switch {
		case v.Shape == rt.VariantBare:
		case v.Err != nil || (v.Shape == rt.VariantPositional && v.Fields[0].Shape.Kind == SFunc):
			if f, ok := fields[strcase.ToSnake(v.Name)]; ok {
				return nil, fmt.Errorf("Go %s has field %s for %s.%s, whose payload has no boundary form", t, f.path, s.Name, v.Name)
			}
			tv.unrepresented = true
		case v.Shape == rt.VariantPositional:
			key := strcase.ToSnake(v.Name)
			f, ok := fields[key]
			if !ok {
				return nil, fmt.Errorf("Go %s has no field for %s.%s's payload (a field named %s)", t, s.Name, v.Name, strcase.ToCamel(v.Name))
			}
			claimed[key] = true
			tv.fields = []goField{f}
		default:
			for _, sf := range v.Fields {
				f, ok := fields[sf.Name]
				if !ok {
					return nil, fmt.Errorf("Go %s has no field for %s.%s.%s", t, s.Name, v.Name, sf.Name)
				}
				claimed[sf.Name] = true
				tv.fields = append(tv.fields, f)
			}
		}
		out[i] = tv
	}
	for name, f := range fields {
		if !claimed[name] {
			return nil, fmt.Errorf("Go %s has field %s, which no variant of %s claims", t, f.path, s.Name)
		}
	}
	return out, nil
}

// inEnum writes the body of an `in` method reading an enum record into rt's
// tagged struct.
func (g *Generator) inEnum(w *bytes.Buffer, s *Shape, t Type) error {
	tvs, err := taggedLayout(s, t)
	if err != nil {
		return err
	}
	goT, err := g.typ(t)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "\tr, tag, err := hostadapt.Variant(v, %q)\n\tif err != nil {\n\t\treturn out, err\n\t}\n\t_ = r\n\tswitch tag {\n", s.Name)
	for _, tv := range tvs {
		fmt.Fprintf(w, "\tcase %q:\n", tv.Name)
		if tv.unrepresented {
			fmt.Fprintf(w, "\t\treturn out, errors.New(%q)\n", fmt.Sprintf("%s.%s has no representation in Go %s", s.Name, tv.Name, goT))
			continue
		}
		for j, f := range tv.Fields {
			conv, err := g.in(f.Shape, tv.fields[j].typ)
			if err != nil {
				return fmt.Errorf("%s.%s: %w", s.Name, tv.Name, err)
			}
			where := tv.Name
			if tv.Shape == rt.VariantPositional {
				fmt.Fprintf(w, "\t\t{\n\t\t\tp := hostadapt.Payload(r)\n")
			} else {
				where += "." + f.Name
				fmt.Fprintf(w, "\t\t{\n\t\t\tp, err := hostadapt.VariantField(r, %q)\n\t\t\tif err != nil {\n\t\t\t\treturn out, err\n\t\t\t}\n", f.Name)
			}
			fmt.Fprintf(w, "\t\t\tx, err := b.%s(fr, p)\n\t\t\tif err != nil {\n\t\t\t\treturn out, fmt.Errorf(\"%s: %%w\", err)\n\t\t\t}\n\t\t\tout.%s = x\n\t\t}\n",
				conv, where, tv.fields[j].path)
		}
		fmt.Fprintf(w, "\t\tout.Tag = %d\n", tv.tag)
	}
	fmt.Fprintf(w, "\tdefault:\n\t\treturn out, fmt.Errorf(\"%s has no variant %%q\", tag)\n\t}\n\treturn out, nil\n", s.Name)
	return nil
}

// outEnum writes the body of an `out` method building an enum record from
// rt's tagged struct.
func (g *Generator) outEnum(w *bytes.Buffer, s *Shape, t Type) error {
	tvs, err := taggedLayout(s, t)
	if err != nil {
		return err
	}
	goT, err := g.typ(t)
	if err != nil {
		return err
	}
	d := g.enumDesc(s)
	w.WriteString("\tswitch v.Tag {\n")
	for i, tv := range tvs {
		fmt.Fprintf(w, "\tcase %d:\n", tv.tag)
		switch {
		case tv.unrepresented:
			fmt.Fprintf(w, "\t\treturn nil, errors.New(%q)\n", fmt.Sprintf("%s.%s has no representation in Go %s", s.Name, tv.Name, goT))
			continue
		case tv.Shape == rt.VariantBare:
			fmt.Fprintf(w, "\t\treturn b.%s.NewVariant(%d), nil\n", d, i)
			continue
		}
		fmt.Fprintf(w, "\t\tr := b.%s.NewVariant(%d)\n", d, i)
		slots := make([]rt.SlotType, len(tv.Fields))
		for j, f := range tv.Fields {
			slots[j] = f.Shape.Slot()
		}
		offsets := layoutOffsets(slots)
		var body bytes.Buffer
		for j, f := range tv.Fields {
			where := tv.Name
			if f.Name != "" {
				where += "." + f.Name
			}
			if err := g.store(&body, "r", offsets[j], f.Shape, tv.fields[j].typ, "v."+tv.fields[j].path, false,
				fmt.Sprintf("fmt.Errorf(\"%s: %%w\", err)", where)); err != nil {
				return fmt.Errorf("%s.%s: %w", s.Name, tv.Name, err)
			}
		}
		w.WriteString(indent(body.String()))
		w.WriteString("\t\treturn r, nil\n")
	}
	fmt.Fprintf(w, "\t}\n\treturn nil, fmt.Errorf(\"Go %s carries tag %%d, which names no variant of %s\", v.Tag)\n", goT, s.Name)
	return nil
}
