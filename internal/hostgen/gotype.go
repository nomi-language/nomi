package hostgen

import (
	"reflect"
	"strconv"
	"strings"
)

// typeIdentity is t spelled with the import path of every named type in it:
// `[]github.com/nomi-language/nomi/rt.Int`. String spells a named type by
// its package's name, so two packages of one name read alike, and a
// conversion memoized under String would serve one package's type to the
// other's. It also keys the registered host types (Handles) and the rt types
// std's host types are held as (rtHeld).
func typeIdentity(t reflect.Type) string {
	if t == nil {
		return "<nil>"
	}
	if t.Name() != "" {
		if t.PkgPath() == "" {
			return t.Name()
		}
		return t.PkgPath() + "." + t.Name()
	}
	switch t.Kind() {
	case reflect.Pointer:
		return "*" + typeIdentity(t.Elem())
	case reflect.Slice:
		return "[]" + typeIdentity(t.Elem())
	case reflect.Func:
		var b strings.Builder
		b.WriteString("func(")
		for i := range t.NumIn() {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(typeIdentity(t.In(i)))
		}
		b.WriteString(")")
		for i := range t.NumOut() {
			b.WriteString(" " + typeIdentity(t.Out(i)))
		}
		return b.String()
	case reflect.Struct:
		var b strings.Builder
		b.WriteString("struct{")
		for i := range t.NumField() {
			f := t.Field(i)
			b.WriteString(f.Name + " " + typeIdentity(f.Type) + " " + strconv.Quote(string(f.Tag)) + ";")
		}
		b.WriteString("}")
		return b.String()
	}
	// A type no conversion takes (a map, an array, a channel): its spelling
	// keys nothing a generated file holds.
	return t.String()
}

// isNamed reports whether t is the named type pkg.name.
func isNamed(t reflect.Type, pkg, name string) bool {
	return t != nil && t.PkgPath() == pkg && t.Name() == name
}

// isErrorType reports whether t is the predeclared `error`.
func isErrorType(t reflect.Type) bool {
	return t != nil && t.Kind() == reflect.Interface && t.PkgPath() == "" && t.Name() == "error"
}

// isFrame reports whether t is *rt.Frame.
func isFrame(t reflect.Type) bool {
	return t != nil && t.Kind() == reflect.Pointer && isNamed(t.Elem(), rtPkg, "Frame")
}

// isRtBytes reports whether t is rt.Bytes.
func isRtBytes(t reflect.Type) bool { return isNamed(t, rtPkg, "Bytes") }
