package vm

import "github.com/nomi-language/nomi/rt"

// debugText is the machine's structural Debug of v with no impl hook: rt's
// kernel over the machine's representation.
func debugText(v any) (string, error) {
	return rt.DebugText(v, nil)
}
