package vm

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

func TestRangeHostsValidateOperands(t *testing.T) {
	for _, contains := range []bool{false, true} {
		host := rangeHost(contains)
		if _, err := host(nil, ir.Pos{}, nil); err == nil {
			t.Fatal("missing operands accepted")
		}
		args := []any{int64(1)}
		if contains {
			args = append(args, int64(0), int64(0))
		}
		if _, err := host(nil, ir.Pos{}, args); err == nil {
			t.Fatal("invalid receiver accepted")
		}
		args[0] = rtStruct("ranges.Range")
		if _, err := host(nil, ir.Pos{}, args); err == nil {
			t.Fatal("missing range fields accepted")
		}
	}
}
