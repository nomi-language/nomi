package analysis_test

import (
	"strings"
	"testing"
)

// A dot-leading variant or a brace literal that is an operand of a
// comparison takes its type from the other operand, wherever the comparison
// sits, and never from the comparison's own expected type (Bool, or Unit at
// a statement).
func TestComparisonOperand_TakesTheOtherOperandsType(t *testing.T) {
	const decls = `enum Kind
    Deposit
    Withdrawal
end

derive Comparable for Kind

struct Point
    x: Int
    y: Int
end

struct Txn
    kind: Kind
end

fn pick(k: Kind) -> Int
`
	for _, tc := range []struct{ name, body string }{
		{"unannotated binding", "    a = k == .Deposit\n    if a then 1 else 0\n"},
		{"Bool binding", "    b: Bool = k == .Deposit\n    if b then 1 else 0\n"},
		{"if condition", "    if k == .Deposit then 1 else 0\n"},
		{"not equal", "    if k != .Withdrawal then 1 else 0\n"},
		{"reversed operands", "    if .Deposit == k then 1 else 0\n"},
		{"nested in and", "    if k == .Deposit and .Withdrawal != k then 1 else 0\n"},
		{"ordering", "    if k < .Withdrawal and .Deposit <= k then 1 else 0\n"},
		{"grouped operand", "    if k == (.Deposit) then 1 else 0\n"},
		{"inside a constructor", "    if Some(k) == Some(.Deposit) then 1 else 0\n"},
		{"case guard", "    r: Result<Txn, String> = Ok(Txn{kind: k})\n    case r\n        Ok(t) when t.kind == .Deposit -> 1\n        _ -> 0\n    end\n"},
		{"brace literal", "    p = Point{x: 1, y: 2}\n    if p == {x: 1, y: 2} then 1 else 0\n"},
		{"brace literal reversed", "    p = Point{x: 1, y: 2}\n    if {x: 1, y: 2} != p then 1 else 0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + tc.body + "end\n")
			expectNoStdlibErrors(t, errs)
		})
	}
}

// When both operands are dot-leading, neither names the enum: one error at
// the operator asks for one of them to be qualified, in place of an error at
// each variant.
func TestComparisonOperand_BothDotLeading_Error(t *testing.T) {
	src := `enum Kind
    Deposit
    Withdrawal
end

fn same() -> Int
    if .Deposit == .Withdrawal then 1 else 0
end
`
	_, errs := checkSourceWithStdlib(src)
	want := "neither operand of `==` names its enum; qualify one variant with the enum's name (write `E.Deposit` for `.Deposit`)"
	if len(errs) != 1 || !strings.Contains(errs[0].Message, want) {
		t.Fatalf("want exactly the error %q, got %d: %v", want, len(errs), errs)
	}
}

// A variant the other operand's enum lacks is reported against that enum,
// not against Bool.
func TestComparisonOperand_UnknownVariant_NamesTheOperandsEnum(t *testing.T) {
	src := `enum Kind
    Deposit
    Withdrawal
end

fn is_refund(k: Kind) -> Bool
    b: Bool = k == .Refund
    b
end
`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "no variant 'Refund' on enum Kind")
}
