package irbuild

import (
	"reflect"

	"github.com/nomi-language/nomi/rt"
)

func irByteKind() kind  { return stdHostKindOfGoType(reflect.TypeFor[rt.Byte]()) }
func irBytesKind() kind { return stdHostKindOfGoType(reflect.TypeFor[rt.Bytes]()) }
func irByteValueKind(k kind) bool {
	// kindInvalid: sentinel — an unanchored runtime type must not match a refused operand.
	return k != kindInvalid && (k == irByteKind() || k == irBytesKind())
}
