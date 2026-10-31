package vm

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// captureShapeKey is std/strings' `capture_shape<C>(re: Regex<C>, groups:
// List<Maybe<String>>) -> C`, behind `String.captures`: the value of C a
// match's groups are. C is phantom in the regex handle, so the IR builder
// lowers the call with C's shape as a second operand (irbuild's
// captureShapeCall), and the groups and the shape decide the value:
//
//	L               the groups as they are: List<Maybe<String>>
//	U               Unit: a pattern with no group
//	S, M            the one group: its text, or its Maybe
//	T<parts>        a tuple, one S or M per group: `TSM` is #(String, Maybe<String>)
//	R<name:part,…>  an anonymous struct: `Rmonth:M,year:S`, a field per
//	                group, named by the group
//
// The third operand is the regex handle: a struct's fields are in its
// type's order, not the pattern's, so a field's group is the one the
// regex names so (groupNamer). An S group took part in every match, which
// the checker decided from the pattern (internal/regexgroups), so its entry
// is always Some.
const captureShapeKey = "strings.capture_shape"

// groupNamer is the regex behind a `Regex` handle (internal/stdregex's
// Regex), as far as capture_shape reads it.
type groupNamer interface {
	// GroupNames is the name of each group from group 1, "" for an
	// unnamed one.
	GroupNames() []string
}

func captureShapeHost(_ *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("vm: %s: expected 3 operands, got %d", captureShapeKey, len(args))
	}
	xs, isList := args[0].(*list)
	shape, isText := args[1].(string)
	if (!isList && args[0] != nil) || !isText || shape == "" {
		return nil, fmt.Errorf("vm: %s: operands %T, %T", captureShapeKey, args[0], args[1])
	}
	if shape == "L" {
		return args[0], nil
	}
	groups := listSlice(xs)
	part := func(i int, p byte) (any, rt.SlotType, error) {
		if i >= len(groups) {
			return nil, 0, fmt.Errorf("vm: %s: shape %q names group %d of %d", captureShapeKey, shape, i+1, len(groups))
		}
		if p == 'M' {
			return groups[i], rt.SlotRef, nil
		}
		r, isEnum := enumRecord(groups[i])
		if !isEnum || variantName(r) != "Some" {
			return nil, 0, fmt.Errorf("vm: %s: group %d took no part in the match, and its type says it always does", captureShapeKey, i+1)
		}
		return r.Field(0), rt.SlotString, nil
	}
	switch shape[0] {
	case 'U':
		return rt.Unit{}, nil
	case 'S', 'M':
		v, _, err := part(0, shape[0])
		return v, err
	case 'T':
		parts := shape[1:]
		vals := make([]any, len(parts))
		slots := make([]rt.SlotType, len(parts))
		for i := range parts {
			v, slot, err := part(i, parts[i])
			if err != nil {
				return nil, err
			}
			vals[i], slots[i] = v, slot
		}
		return rt.TupleDesc(slots...).Make(vals...), nil
	case 'R':
		h, isHandle := args[2].(rt.HostHandle)
		namer, named := h.Value.(groupNamer)
		if !isHandle || !named {
			return nil, fmt.Errorf("vm: %s: operand 3 is %T, not a regex", captureShapeKey, args[2])
		}
		group := map[string]int{}
		for i, name := range namer.GroupNames() {
			if name != "" {
				group[name] = i
			}
		}
		fields := strings.Split(shape[1:], ",")
		specs := make([]rt.FieldSpec, len(fields))
		vals := map[string]any{}
		for i, f := range fields {
			name, p, found := strings.Cut(f, ":")
			at, isGroup := group[name]
			if !found || len(p) != 1 || !isGroup {
				return nil, fmt.Errorf("vm: %s: shape %q does not fit the regex's groups", captureShapeKey, shape)
			}
			v, slot, err := part(at, p[0])
			if err != nil {
				return nil, err
			}
			specs[i] = rt.FieldSpec{Name: name, Type: slot}
			vals[name] = v
		}
		d := rt.AnonDesc(specs)
		ordered := make([]any, len(d.Fields))
		for i := range d.Fields {
			ordered[i] = vals[d.Fields[i].Name]
		}
		return d.Make(ordered...), nil
	}
	return nil, fmt.Errorf("vm: %s: malformed shape %q", captureShapeKey, shape)
}
