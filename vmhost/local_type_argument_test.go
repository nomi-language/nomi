package vmhost

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// localTypeArgumentProgram is three modules: fx and shares each declare a
// type and implement std interfaces for std receivers at it, which the
// orphan rule admits through that type (spec §13, *Foreign Types*), and app
// uses both. fx's `Multiply<Rate, Decimal>` and shares' `Multiply<Shares,
// Decimal>` are two impls of one std interface for one std receiver from
// two libraries, beside std's own `Multiply<Decimal, Decimal>`.
const localTypeArgumentProgram = `// FILE: app/main.nomi
import {
    std/io
    fx/rate.Rate
    shares/count.Shares
}

fn scale<T>(x: T, r: Rate) -> T where T: Multiply<Rate, T> {
    x * r
}

fn has<C>(c: C, r: Rate) -> Bool where C: Contains<Rate> {
    C.contains?(c, r)
}

fn main() {
    d = 2.5d
    r = #Rate{per_unit: 4d}
    s = #Shares{count: 3}
    io.print(Decimal.to_string(d * r))
    io.print(Decimal.to_string(d + r))
    io.print(Decimal.to_string(d * s))
    io.print(Decimal.to_string(d * d))
    io.print(Decimal.to_string(Decimal.multiply(d, r)))
    io.print(Decimal.to_string(Multiply.multiply(d, s)))
    io.print(Decimal.to_string(scale(d, r)))
    io.print(Bool.to_string(String.contains?("x4y", r)))
    io.print(Bool.to_string(Contains.contains?("x5y", r)))
    io.print(Bool.to_string(has("x4y", r)))
    io.print(Bool.to_string(String.contains?("x4y", "4")))
    io.print(Int.to_string(300 * Pct(50)))
}

type Pct Int

impl Multiply<Pct, Int> for Int {
    fn multiply(lhs: Int, rhs: Pct) -> Int {
        Pct(p) = rhs
        lhs * p / 100
    }
}
// FILE: app/nomi.toml
[module]
name = "app"
entry_points = ["main"]
// FILE: app/go.mod
module app

go 1.27.0

require (
	fx v0.0.0
	shares v0.0.0
)

replace fx => ../fx

replace shares => ../shares
// FILE: fx/rate.nomi
pub struct Rate {
    per_unit: Decimal
}

impl Multiply<Rate, Decimal> for Decimal {
    fn multiply(lhs: Decimal, rhs: Rate) -> Decimal {
        lhs * rhs.per_unit
    }
}

impl Add<Rate, Decimal> for Decimal {
    fn add(lhs: Decimal, rhs: Rate) -> Decimal {
        lhs + rhs.per_unit
    }
}

impl Contains<Rate> for String {
    fn contains?(c: String, x: Rate) -> Bool {
        String.contains?(c, Decimal.to_string(x.per_unit))
    }
}
// FILE: fx/nomi.toml
[module]
name = "fx"
// FILE: fx/go.mod
module fx

go 1.27.0
// FILE: shares/count.nomi
pub struct Shares {
    count: Int
}

impl Multiply<Shares, Decimal> for Decimal {
    fn multiply(lhs: Decimal, rhs: Shares) -> Decimal {
        lhs * Int.to_decimal(rhs.count)
    }
}
// FILE: shares/nomi.toml
[module]
name = "shares"
// FILE: shares/go.mod
module shares

go 1.27.0
`

// Each impl a library writes for a std receiver at its own type runs, by
// every call form: the operator, the type- and interface-qualified calls,
// a bound, and a std call beside it that still takes std's impl.
func TestLocalTypeArgumentImplsRun(t *testing.T) {
	entry, err := writeInput(t.TempDir(), localTypeArgumentProgram)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Load(entry, WithOutput(io.Discard), WithErrorOutput(io.Discard))
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	if u := p.Unsupported(); u != nil {
		t.Fatalf("the program does not lower: %v", u)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	want := strings.Join([]string{
		"10.0", "6.5", "7.5", "6.25", "10.0", "7.5", "10.0",
		"True", "False", "True", "True", "150",
	}, "\n") + "\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}
