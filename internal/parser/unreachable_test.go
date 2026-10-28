package parser

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

func TestUnreachable_TakesAReasonInEachLiteralForm(t *testing.T) {
	for src, want := range map[string]struct {
		value       string
		triple, raw bool
	}{
		`unreachable "every student is listed"`: {"every student is listed", false, false},
		"unreachable `raw ${not} interpolated`": {"raw ${not} interpolated", false, true},
		"unreachable \"\"\"\n    multi\n    \"\"\"": {"multi", true, false},
	} {
		u, ok := parseExpr(t, src).(*ast.Unreachable)
		if !ok || u.Reason == nil || u.Line != 1 || u.Col != 1 {
			t.Fatalf("%q: %#v", src, u)
		}
		if u.Reason.Value != want.value || u.Reason.Triple != want.triple || u.Reason.Raw != want.raw {
			t.Errorf("%q: reason %#v, want %+v", src, u.Reason, want)
		}
	}
	pipe := parseExpr(t, `3 |> unreachable "never"`).(*ast.Binary)
	if _, ok := pipe.Right.(*ast.Unreachable); !ok || pipe.Op != "|>" {
		t.Fatalf("pipe stage: %#v", pipe)
	}
}

// The reason is required, on the keyword's line: a bare `unreachable`, or one
// whose string is on the next line, is the error that asks for one.
func TestUnreachable_WithoutAReasonIsAnError(t *testing.T) {
	for _, src := range []string{"unreachable", "unreachable\n\"next\"\n", "unreachable 3"} {
		err := parseError(t, src)
		var pe ParseError
		if !errors.As(err, &pe) || pe.Message != "`unreachable` needs a reason" || pe.Line != 1 || pe.Col != 1 ||
			len(pe.Hints) != 1 || !strings.Contains(pe.Hints[0], "write the invariant it claims") {
			t.Errorf("%q: %#v", src, err)
		}
	}
}

func TestUnreachable_ReasonCannotInterpolateOrCarryATag(t *testing.T) {
	for _, src := range []string{`unreachable "step ${n}"`, `unreachable Sql"select 1"`} {
		err := parseError(t, src)
		if !strings.Contains(err.Error(), "an `unreachable` reason is a plain string literal: it cannot interpolate or carry a tag") {
			t.Errorf("%q: %v", src, err)
		}
	}
}
