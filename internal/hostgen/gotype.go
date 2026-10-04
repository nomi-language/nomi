package hostgen

import (
	"reflect"
)

// Type is the part of a Go type the generator reads. Two front ends supply
// it: ReflectType, over a linked function value's reflect.Type (the stdlib
// tables, whose Go functions are compiled into this binary), and a go/ast
// reading of source, for a project's FFI bindings, which are generated into
// the project's wrapper before the project's Go is compiled
// (internal/ffirun/gotypes.go).
//
// The methods mean what reflect.Type's do. A front end that cannot answer one
// for a type it does not describe returns the zero value; the generator reads
// only what its projection rules need.
type Type interface {
	Kind() reflect.Kind
	// Name is the declared name, "" for an unnamed type.
	Name() string
	// PkgPath is the declaring package's import path, "" for a predeclared
	// or unnamed type.
	PkgPath() string
	// String is reflect's spelling: `int64`, `pkg.Name`, `[]pkg.Name`,
	// `func(int64) error`. It appears in refusals and in conversion error
	// texts, which must read as the marshaller's.
	String() string
	Elem() Type
	Key() Type
	NumField() int
	Field(i int) StructField
	FieldByName(name string) (StructField, bool)
	NumIn() int
	In(i int) Type
	NumOut() int
	Out(i int) Type
	IsVariadic() bool
}

// StructField is one field of a struct Type.
type StructField struct {
	Name      string
	Type      Type
	Tag       reflect.StructTag
	Anonymous bool
	Exported  bool
}

// ReflectType is the Type of a reflect.Type.
func ReflectType(t reflect.Type) Type {
	if t == nil {
		return nil
	}
	return reflectType{t}
}

type reflectType struct{ t reflect.Type }

func (r reflectType) Kind() reflect.Kind { return r.t.Kind() }
func (r reflectType) Name() string       { return r.t.Name() }
func (r reflectType) PkgPath() string    { return r.t.PkgPath() }
func (r reflectType) String() string     { return r.t.String() }
func (r reflectType) Elem() Type         { return ReflectType(r.t.Elem()) }
func (r reflectType) Key() Type          { return ReflectType(r.t.Key()) }
func (r reflectType) NumField() int      { return r.t.NumField() }
func (r reflectType) NumIn() int         { return r.t.NumIn() }
func (r reflectType) In(i int) Type      { return ReflectType(r.t.In(i)) }
func (r reflectType) NumOut() int        { return r.t.NumOut() }
func (r reflectType) Out(i int) Type     { return ReflectType(r.t.Out(i)) }
func (r reflectType) IsVariadic() bool   { return r.t.IsVariadic() }

func (r reflectType) Field(i int) StructField { return reflectField(r.t.Field(i)) }

func (r reflectType) FieldByName(name string) (StructField, bool) {
	f, ok := r.t.FieldByName(name)
	if !ok {
		return StructField{}, false
	}
	return reflectField(f), true
}

func reflectField(f reflect.StructField) StructField {
	return StructField{Name: f.Name, Type: ReflectType(f.Type), Tag: f.Tag, Anonymous: f.Anonymous, Exported: f.IsExported()}
}

// TypeKey is a Type's identity: its package path and name for a named type,
// its spelling otherwise. Two front ends agree on it for one Go type.
func TypeKey(t Type) string {
	if t == nil {
		return ""
	}
	if t.Name() != "" {
		if t.PkgPath() == "" {
			return t.Name()
		}
		return t.PkgPath() + "." + t.Name()
	}
	switch t.Kind() {
	case reflect.Pointer:
		return "*" + TypeKey(t.Elem())
	case reflect.Slice:
		return "[]" + TypeKey(t.Elem())
	case reflect.Map:
		return "map[" + TypeKey(t.Key()) + "]" + TypeKey(t.Elem())
	}
	return t.String()
}

// isNamed reports whether t is the named type pkg.name.
func isNamed(t Type, pkg, name string) bool {
	return t != nil && t.PkgPath() == pkg && t.Name() == name
}

// isPredeclared reports whether t is the predeclared type name (`byte` is
// `uint8`).
func isPredeclared(t Type, name string) bool {
	if t == nil || t.PkgPath() != "" {
		return false
	}
	if name == "byte" {
		name = "uint8"
	}
	got := t.Name()
	if got == "byte" {
		got = "uint8"
	}
	return got == name
}

// isErrorType reports whether t is the predeclared `error`.
func isErrorType(t Type) bool {
	return t != nil && t.Kind() == reflect.Interface && t.PkgPath() == "" && t.Name() == "error"
}

// isFrame reports whether t is *rt.Frame.
func isFrame(t Type) bool {
	return t != nil && t.Kind() == reflect.Pointer && isNamed(t.Elem(), rtPkg, "Frame")
}

// isRtBytes reports whether t is rt.Bytes.
func isRtBytes(t Type) bool { return isNamed(t, rtPkg, "Bytes") }

// isTimeDuration and isTimeTime are the two named types from another package
// the marshaller projects.
func isTimeDuration(t Type) bool { return isNamed(t, "time", "Duration") }
func isTimeTime(t Type) bool     { return isNamed(t, "time", "Time") }
