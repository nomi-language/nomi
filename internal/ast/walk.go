package ast

import (
	"reflect"
	"sync"
)

// Children calls fn on each node directly beneath n, in field order: the
// first node met on every path through n's fields, whether the field holds
// it directly (`Body *Block`, `Value Node`), in a slice (`Args []Node`), or
// inside a plain struct one level down (`[]CaseBranch`, `[]StructFieldVal`,
// an interpolated string's `StringExpr`). A node stored by value in an
// addressable place (`[]AttachedTest`) is passed as a pointer to it. Type
// expressions are nodes and are visited like any other. A nil or typed-nil
// n has no children.
//
// The fields are found by reflection, so a node type or field added to the
// AST is walked with no change here. Each Go type's walk is worked out once
// and cached; fields that cannot hold a node (names, positions, trivia) are
// never looked at.
func Children(n Node, fn func(Node)) {
	v := reflect.ValueOf(n)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	if s := fieldsStep(v.Type().Elem()); s != nil {
		s(v.Elem(), fn)
	}
}

// Inspect walks the tree under n in depth-first order. It calls f(n) and,
// when f returns true, inspects each of n's children (Children) the same
// way. f returning false prunes n's subtree. A nil or typed-nil n is not
// visited.
//
// The walk does not remember what it has seen: a node shared by two parents
// is visited once under each. A caller that must see each node once keeps
// its own set and returns false for a node already in it.
func Inspect(n Node, f func(Node) bool) {
	in := &inspector{f: f}
	in.visit = in.inspect
	in.inspect(n)
}

type inspector struct {
	f     func(Node) bool
	visit func(Node)
}

func (in *inspector) inspect(n Node) {
	v := reflect.ValueOf(n)
	if !v.IsValid() || v.Kind() == reflect.Pointer && v.IsNil() {
		return
	}
	if in.f(n) {
		Children(n, in.visit)
	}
}

// step walks one value of a fixed Go type, calling fn on each node it
// holds at the top. A nil step means the type can hold no node.
type step func(v reflect.Value, fn func(Node))

var (
	nodeType = reflect.TypeFor[Node]()

	// valSteps and bodySteps publish finished steps by type, for the walk's
	// lock-free reads.
	valSteps, bodySteps sync.Map
	// buildMu guards the cells steps are built in.
	buildMu             sync.Mutex
	valCells, bodyCells = map[reflect.Type]*stepCell{}, map[reflect.Type]*stepCell{}
)

// stepCell holds a type's step. The cell is registered before the step is
// built, so a type that contains itself refers to its own cell.
type stepCell struct {
	s     step
	built bool
}

func (c *stepCell) call(v reflect.Value, fn func(Node)) {
	if c.s != nil {
		c.s(v, fn)
	}
}

// valueStep is the step for a value of type t met in a field, slice or
// interface: a node there is reported, anything else is descended.
func valueStep(t reflect.Type) step {
	return cachedStep(&valSteps, valCells, t, buildValueStep)
}

// fieldsStep is the step over the fields of struct type t, the walk of a
// node's own body.
func fieldsStep(t reflect.Type) step {
	return cachedStep(&bodySteps, bodyCells, t, buildFieldsStep)
}

func cachedStep(done *sync.Map, cells map[reflect.Type]*stepCell, t reflect.Type, build func(reflect.Type) step) step {
	if s, ok := done.Load(t); ok {
		return s.(step)
	}
	buildMu.Lock()
	defer buildMu.Unlock()
	s := stepLocked(cells, t, build)
	done.Store(t, s)
	return s
}

// stepLocked finds or builds t's step with buildMu held. A cell still being
// built is met only through a type that contains itself, and its step is
// reached through the cell once the build ends.
func stepLocked(cells map[reflect.Type]*stepCell, t reflect.Type, build func(reflect.Type) step) step {
	if c, ok := cells[t]; ok {
		if !c.built {
			return c.call
		}
		return c.s
	}
	c := &stepCell{}
	cells[t] = c
	c.s = build(t)
	c.built = true
	return c.s
}

func buildValueStep(t reflect.Type) step {
	switch t.Kind() {
	case reflect.Pointer:
		if t.Implements(nodeType) {
			return func(v reflect.Value, fn func(Node)) {
				if !v.IsNil() {
					fn(v.Interface().(Node))
				}
			}
		}
		elem := stepLocked(valCells, t.Elem(), buildValueStep)
		if elem == nil {
			return nil
		}
		return func(v reflect.Value, fn func(Node)) {
			if !v.IsNil() {
				elem(v.Elem(), fn)
			}
		}
	case reflect.Interface:
		return interfaceStep
	case reflect.Struct:
		fields := stepLocked(bodyCells, t, buildFieldsStep)
		if fields == nil {
			return nil
		}
		if reflect.PointerTo(t).Implements(nodeType) {
			// A node stored by value. Reported through its address when
			// it has one; otherwise (a copy held in an interface) its
			// fields are walked in its place.
			return func(v reflect.Value, fn func(Node)) {
				if v.CanAddr() {
					fn(v.Addr().Interface().(Node))
					return
				}
				fields(v, fn)
			}
		}
		return fields
	case reflect.Slice, reflect.Array:
		elem := stepLocked(valCells, t.Elem(), buildValueStep)
		if elem == nil {
			return nil
		}
		return func(v reflect.Value, fn func(Node)) {
			for i := range v.Len() {
				elem(v.Index(i), fn)
			}
		}
	}
	return nil
}

// interfaceStep walks an interface field by its dynamic value: a node is
// reported (a typed nil one is absent), anything else is descended by its
// own type's step.
func interfaceStep(v reflect.Value, fn func(Node)) {
	if v.IsNil() {
		return
	}
	e := v.Elem()
	if e.Kind() == reflect.Pointer && e.IsNil() {
		return
	}
	if n, ok := e.Interface().(Node); ok {
		fn(n)
		return
	}
	if s := valueStep(e.Type()); s != nil {
		s(e, fn)
	}
}

func buildFieldsStep(t reflect.Type) step {
	if t.Kind() != reflect.Struct {
		return nil
	}
	type field struct {
		index int
		s     step
	}
	var fields []field
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if s := stepLocked(valCells, f.Type, buildValueStep); s != nil {
			fields = append(fields, field{i, s})
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return func(v reflect.Value, fn func(Node)) {
		for _, f := range fields {
			f.s(v.Field(f.index), fn)
		}
	}
}
