package vm

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

func TestVectorHostsValidateOperands(t *testing.T) {
	for key, arity := range map[string]int{"Vector.length": 1, "Vector.at": 2, "Vector.push": 2, "Vector.concat": 2} {
		t.Run(key, func(t *testing.T) {
			host := vectorHost(key)
			if _, err := host(nil, ir.Pos{}, nil); err == nil || !strings.Contains(err.Error(), "expected") {
				t.Fatalf("missing operands: %v", err)
			}
			args := make([]any, arity)
			args[0] = int64(1)
			if _, err := host(nil, ir.Pos{}, args); err == nil || !strings.Contains(err.Error(), "want Vector") {
				t.Fatalf("invalid receiver: %v", err)
			}
			if key == "Vector.at" || key == "Vector.concat" {
				args[0], args[1] = rt.VectorOf[any](nil), "bad"
				if _, err := host(nil, ir.Pos{}, args); err == nil || !strings.Contains(err.Error(), "operand 2") {
					t.Fatalf("invalid second operand: %v", err)
				}
			}
		})
	}
}

func TestVectorDebugPropagatesUnsupportedElement(t *testing.T) {
	v := rt.VectorOf([]any{"a\nb\t\"\\z"})
	got, err := debugText(v)
	if err != nil || got != "#[\"a\nb\t\\\"\\\\z\"]" {
		t.Fatalf("Debug: %q, %v", got, err)
	}
	v = rt.VectorOf([]any{rtStruct("Point", "x", int64(1))})
	if _, err := debugText(v); err == nil || !strings.Contains(err.Error(), "unrepresented") {
		t.Fatalf("unsupported child: %v", err)
	}
}

// TestVectorSet: Vector.set over the machine's vectors, leaving the receiver
// unchanged and answering None out of range.
func TestVectorSet(t *testing.T) {
	v := rt.VectorOf([]any{int64(1), int64(2), int64(3)})
	set, err := vectorHost("Vector.set")(nil, ir.Pos{}, []any{v, int64(1), int64(20)})
	if err != nil || rt.RowText(set) != "Some(#[1, 20, 3])" || rt.RowText(v) != "#[1, 2, 3]" {
		t.Fatalf("set(v, 1, 20) = %v, v = %v, %v", rt.RowText(set), rt.RowText(v), err)
	}
	if out, err := vectorHost("Vector.set")(nil, ir.Pos{}, []any{v, int64(9), int64(99)}); err != nil || rt.RowText(out) != "None" {
		t.Fatalf("set out of range = %v, %v", rt.RowText(out), err)
	}
}
