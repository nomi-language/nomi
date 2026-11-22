package vm_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A Nomi function that answers a Some or an Ok of a tuple returns the
// tuple's components and the variant in parts (wrappedReturns), and a
// caller that only takes the Maybe or Result apart keeps it fused with its
// payload in parts: matching one allocates neither the tuple nor the Some.
// Each callee form is here: an early `return None` and a last `Some`, an
// `if` whose branches are a None and a Some, and a `case` whose arms are an
// Err and an Ok; and each caller form: `!` into a destructuring bind,
// `case`, an `if` binding pattern and `?`. The count is per activation over
// a loop doing one call each (allocsPerActivation).
func TestMaybeTuple_AMatchedMaybeOfATupleAllocatesNothing(t *testing.T) {
	for _, tc := range []struct{ name, step string }{
		{"!, early return", "begin\n            (v, next) = early(n)!\n            v + next\n        end"},
		{"case, early return", "case early(n)\n            Some((v, next)) -> v - next\n            None -> 0\n        end"},
		{"if binding, if expression", "if Some((v, next)) = branches(n) then v + next else 0"},
		{"case over None, if expression", "case branches(n + 1)\n            Some((v, next)) -> v + next\n            None -> 7\n        end"},
		{"?, early return", "case sum(n)\n            Some(x) -> x\n            None -> 0\n        end"},
		{"case over a Result", "case result(n)\n            Ok((v, s)) -> v + String.length(s)\n            Err(_) -> 0\n        end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `import std::io

fn early(n: Int) -> Maybe<(Int, Int)>
    if n < 0 then return None

    Some((n * 1000, n + 1000))
end

fn branches(n: Int) -> Maybe<(Int, Int)>
    if n %% 2 == 0 then Some((n * 1000, n + 1000)) else None
end

fn result(n: Int) -> Result<(Int, String), String>
    case n %% 2
        0 -> Ok((n * 1000, "zero"))
        1 -> Ok((n + 1000, "one"))
        _ -> Err("two")
    end
end

fn sum(n: Int) -> Maybe<Int>
    (v, next) = early(n)?
    Some(v - next)
end

fn down(n: Int) -> Int
    if n < 1 then
        0
    else
        x = ` + tc.step + `
        down(n - 1) + x
    end
end

fn main()
    down(%d) |> io.print()
end
`
			// sum's own Some(v - next) is one allocation.
			want := 0.0
			if tc.name == "?, early return" {
				want = 1
			}
			if per := allocsPerActivation(t, src); per > want+0.1 {
				t.Errorf("one matched call allocates %.2f times, want %.0f. The likely causes: the "+
					"callee building its Some (a return wrappedReturns does not accept, or its "+
					"tuple not split), or the caller not fusing the Maybe (a use fuseMaybes "+
					"does not accept), so the payload's tuple is built", per, want)
			}
		})
	}
}

// A Maybe or Result of a tuple returned in parts answers what the record
// did, read whole, matched, compared and shown, from each callee form and
// with Ints past int64 in its parts.
func TestMaybeTuple_AMaybeReturnedInPartsAnswersAsTheRecordDid(t *testing.T) {
	const src = `import std::io

fn early(n: Int) -> Maybe<(Int, String)>
    if n < 0 then return None

    Some((n * 1180591620717411303424, "s${n}"))
end

fn branches(n: Int) -> Maybe<(Int, Int)>
    if n == 0 then None else Some((n, -n))
end

fn result(n: Int) -> Result<(Int, Bool), String>
    case n
        0 -> Err("zero")
        _ -> Ok((n, n > 1))
    end
end

fn kept(n: Int) -> Maybe<(Int, Int)>
    branches(n)
end

fn main()
    io.print("${early(2)} ${early(-1)} ${branches(0)} ${branches(3)} ${result(0)} ${result(2)} ${kept(4)}")
    (big, s) = early(3)!
    io.print("${big} ${s} ${early(1) == Some((1180591620717411303424, "s1"))}")
    m = case early(-5)
        Some((v, _)) -> v
        None -> -1
    end
    r = case result(1)
        Ok((v, flag)) -> if flag then v else -v
        Err(e) -> String.length(e)
    end
    io.print("${m} ${r}")
    if Some((a, b)) = branches(9) then io.print("${a + b}")
end
`
	p, err := vmhost.LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := `Some((2361183241434822606848, s2)) None None Some((3, -3)) Err(zero) Ok((2, True)) Some((4, -4))
3541774862152233910272 s3 True
-1 -1
0
`
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// The callees return in parts and the callers take the parts: no `make`
// builds the Some or the tuple in either, and no `matchv` tests a record.
func TestMaybeTuple_ReturnsAndMatchesCompileToParts(t *testing.T) {
	const src = `import std::io

fn early(n: Int) -> Maybe<(Int, Int)>
    if n < 0 then return None

    Some((n, n + 1))
end

fn branches(n: Int) -> Maybe<(Int, Int)>
    if n == 0 then None else Some((n, -n))
end

fn use(n: Int) -> Int
    (a, b) = early(n)!
    case branches(n)
        Some((c, d)) -> a + b + c + d
        None -> a
    end
end

fn main()
    io.print(use(3))
end
`
	p, err := vmhost.LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"early": "retp", "branches": "retp", "use": "tagis"} {
		listing, err := p.Disassemble(name)
		if err != nil {
			t.Fatal(err)
		}
		ops := opcodesOf(listing)
		if !ops[want] || ops["make"] || ops["matchv"] || ops["projp"] {
			t.Errorf("%s does not compile its Maybe of a tuple to parts (%s):\n%s", name, want, listing)
		}
	}
}
