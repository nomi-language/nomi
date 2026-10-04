package vm

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

func TestSetHostsValidateOperands(t *testing.T) {
	for key, arity := range map[string]int{"Set.size": 1, "Set.contains?": 2, "Set.insert": 2, "Set.remove": 2} {
		t.Run(key, func(t *testing.T) {
			host := setHost(key)
			if _, err := host(nil, ir.Pos{}, nil); err == nil || !strings.Contains(err.Error(), "expected") {
				t.Fatalf("missing operands: %v", err)
			}
			args := make([]any, arity)
			args[0] = int64(1)
			if _, err := host(nil, ir.Pos{}, args); err == nil || !strings.Contains(err.Error(), "want Set") {
				t.Fatalf("invalid receiver: %v", err)
			}
			args[0] = rtStruct("sets.Set", "items", int64(1))
			if _, err := host(nil, ir.Pos{}, args); err == nil || !strings.Contains(err.Error(), "want Map") {
				t.Fatalf("invalid backing map: %v", err)
			}
		})
	}
}

func TestSetDebugPropagatesUnsupportedElement(t *testing.T) {
	s := newSet([]any{rtStruct("Point", "x", int64(1))})
	if _, err := debugText(s); err == nil || !strings.Contains(err.Error(), "unrepresented") {
		t.Fatalf("unsupported child: %v", err)
	}
}
