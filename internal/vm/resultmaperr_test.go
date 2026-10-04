package vm

import (
	"github.com/nomi-language/nomi/internal/ir"
	"testing"
)

func TestResultMapErrRejectsMalformedOperands(t *testing.T) {
	good := rtVariant("results.Result", "Err", int64(1))
	callback := &functionValue{arity: 1}
	cases := [][]any{
		nil,
		{int64(1), callback},
		{rtVariant("other.Result", "Err", int64(1)), callback},
		{rtVariant("results.Result", "Unknown"), callback},
		{rtVariant("results.Result", "Err"), callback},
		{good, int64(1)},
		{good, &functionValue{arity: 2}},
	}
	for i, args := range cases {
		if _, err := resultMapErrHost(nil, ir.Pos{}, args); err == nil {
			t.Fatalf("case %d accepted malformed operands", i)
		}
	}
}
