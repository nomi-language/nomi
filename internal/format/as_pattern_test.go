package format

import "testing"

// `P as name` prints with one space either side of `as` in every pattern
// position, and is a fixed point.
func TestFormat_AsPatternRoundTrips(t *testing.T) {
	for _, src := range []string{
		"fn f(r: Result<Tx, String>): Int {\n    case r {\n        Ok(Tx{kind: .Deposit} as t) -> t.end\n        Ok(_) as whole -> 0\n        Err(_) -> 1\n    }\n}\n",
		"fn f(xs: List<Int>): Int {\n    case xs {\n        [Some(n) as first, ..rest] as all -> n\n        _ -> 0\n    }\n}\n",
		"fn f((a, b) as pair: (Int, Int)): Int {\n    a + b\n}\n",
		"fn f(m: Maybe<Int>): Int {\n    Some(n) as found = m else { return 0 }\n    (x, y) as both = (n, n)\n    if Ok(v) as r = load() { v } else { x }\n}\n",
		"fn f(m: Map<String, Int>): Int {\n    case m {\n        {\"k\" => 1 as one} as all -> one\n        _ -> 0\n    }\n}\n",
		"fn f(p: String): String {\n    case p {\n        \"/users/\" + id as path -> path\n        _ -> \"\"\n    }\n}\n",
		"test \"t\" {\n    assert Some(n) as m = find()\n}\n",
	} {
		if got := formatTwice(t, src); got != src {
			t.Errorf("got:\n%s\nwant:\n%s", got, src)
		}
		if err := SameMeaning(src, src); err != nil {
			t.Fatal(err)
		}
	}
}

// Spacing and grouping parentheses go; the name stays on the pattern it
// names.
func TestFormat_AsPatternNormalizesSpacingAndParens(t *testing.T) {
	formatsKeepingMeaning(t,
		"fn f(r: Maybe<Int>): Int {\n    case r {\n        Some(n)   as   s -> n\n        (None) as   s -> 0\n    }\n}\n",
		"fn f(r: Maybe<Int>): Int {\n    case r {\n        Some(n) as s -> n\n        None as s -> 0\n    }\n}\n")
}

// A pattern too wide for the line breaks inside its own brackets, and the
// name follows the closing one. The arms then lay out as any case whose arm
// breaks does.
func TestFormat_AsPatternNameFollowsABrokenPattern(t *testing.T) {
	src := "fn f(r: Result<Transaction, String>): Int {\n    case r {\n        Ok(Transaction{transaction_type: .Deposit, starting_balance, ending_balance, account_number} as t) -> 1\n        _ -> 0\n    }\n}\n"
	want := "fn f(r: Result<Transaction, String>): Int {\n    case r {\n        Ok(Transaction{\n            transaction_type: .Deposit,\n            starting_balance,\n            ending_balance,\n            account_number,\n        } as t) ->\n            1\n\n        _ ->\n            0\n    }\n}\n"
	formatsKeepingMeaning(t, src, want)
}
