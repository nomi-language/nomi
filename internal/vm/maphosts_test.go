package vm

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

func TestMapHostsRejectInvalidReceiversAndArity(t *testing.T) {
	for key, arity := range map[string]int{"Map.get": 2, "Map.put": 3, "Map.size": 1} {
		t.Run(key, func(t *testing.T) {
			host := mapHost(key)
			for _, count := range []int{arity - 1, arity + 1} {
				if _, err := host(nil, ir.Pos{}, make([]any, count)); err == nil || !strings.Contains(err.Error(), "expected") {
					t.Fatalf("arity %d: %v", count, err)
				}
			}
			args := make([]any, arity)
			args[0] = int64(1)
			if _, err := host(nil, ir.Pos{}, args); err == nil || !strings.Contains(err.Error(), "want Map") {
				t.Fatalf("wrong receiver: %v", err)
			}
		})
	}
}
