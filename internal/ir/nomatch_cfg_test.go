package ir_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

func TestNoMatch_CFGDropsOnlyTheNormalEdge(t *testing.T) {
	for _, mode := range []string{"trap", "ordinary", "fault handler"} {
		t.Run(mode, func(t *testing.T) {
			at := ir.At("case.nomi", 1, 1)
			f := ir.NewFunc(at, "case")
			entry := f.NewBlock(at, "test")
			arm := f.NewBlock(at, "arm")
			fall := f.NewBlock(at, "fall")
			join := f.NewBlock(at, "join")
			condition, answer := f.NewTemp(), f.NewTemp()
			entry.Append(ir.NewBool(at, condition, true))
			entry.SetTerm(ir.NewBranch(at, condition, arm.ID(), fall.ID()))
			arm.Append(ir.NewInt(at, answer, 42))
			arm.SetTerm(ir.NewJump(at, join.ID()))
			if mode != "ordinary" {
				fall.Append(ir.NewNoMatch(at))
			}
			fall.SetTerm(ir.NewJump(at, join.ID()))
			join.SetTerm(ir.NewReturn(at, answer))
			if mode == "fault handler" {
				handler := f.NewBlock(at, "handler")
				handler.SetTerm(ir.NewReturn(at, answer))
				fall.SetFault(at, handler.ID())
			}
			err := ir.Lint(f)
			if mode == "trap" {
				if err != nil {
					t.Fatalf("diverging trap reached the join: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("a live path read a value defined only on another arm")
				}
				found := false
				for _, violation := range err.(*ir.LintError).Violations {
					found = found || violation.Rule == ir.RuleTempDefinedBeforeUse
				}
				if !found {
					t.Fatalf("missing undefined-path diagnostic: %v", err)
				}
			}
		})
	}
}
