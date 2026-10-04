package vm

import (
	"github.com/nomi-language/nomi/internal/ir"

	"github.com/nomi-language/nomi/rt"
)

// DescOf is the descriptor the machine builds records of t with, or nil when
// t is not a record type with a stated layout. An embedding host building an
// argument for a struct parameter uses it, so the record it passes has the
// machine's own descriptor.
func DescOf(t *ir.ValType) *rt.TypeDesc { return descOfType(t) }
