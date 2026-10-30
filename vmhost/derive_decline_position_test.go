package vmhost

import (
	"strings"
	"testing"
)

// A body Nomi synthesized has no source line, so a decline inside one is
// reported at the declaration that asked for it: the `derive` line, in
// whichever file it is, or the type's declaration for the universal Debug,
// which no line asks for. No derived body declines today, so each case asks
// for the diagnostic of a body that did, by name, as Unsupported would for
// a body the machine finds unretained.
func TestDeclinePosition_ASynthesizedBodyIsReportedAtItsDerive(t *testing.T) {
	entry, name, files, manifest, err := SplitMultiFile(`// FILE: money.nomi
pub struct Money {
    cents: Int
}

impl Equatable for Money {
    fn equal(a: Money, b: Money) -> Bool {
        a.cents == b.cents
    }
}

pub struct Coin {
    n: Int
}

derive Hashable for Coin
derive Equatable for Coin

// FILE: main.nomi
import {
    std/io
    money.{Coin, Money}
}

struct Rec {
    m: Money
    c: Coin
}

derive Equatable for Rec

fn main() {
    io.inspect(#Rec{m: #Money{cents: 1}, c: #Coin{n: 2}} == #Rec{m: #Money{cents: 1}, c: #Coin{n: 2}})
}
`)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	p, err := LoadSource(name, entry, WithVirtualFiles(files), WithVirtualManifest(manifest))
	if err != nil {
		t.Fatalf("the front end rejects the program, so it no longer tests a derived body's position: %v", err)
	}
	for _, c := range []struct {
		fn, want string
	}{
		// The `derive` line, not `Money`'s written equal? in the other
		// file, which the function's last segment also names.
		{"Rec.equal", "main.nomi:11:1: its body is not supported yet, so `fn Rec.equal` cannot run"},
		{"Coin.hash", "money.nomi:15:1: its body is not supported yet, so `fn Coin.hash` cannot run"},
		// The universal Debug: no line asks for it, so the type does.
		{"Rec.inspect", "main.nomi:6:8: its body is not supported yet, so `fn Rec.inspect` cannot run"},
		// A written impl function keeps its own line.
		{"Money.equal", "money.nomi:6:8: the body of `fn equal` is not supported yet, so `fn Money.equal` cannot run"},
	} {
		t.Run(c.fn, func(t *testing.T) {
			ds := p.declineDiagnostics([]string{c.fn})
			if len(ds) != 1 {
				t.Fatalf("want one diagnostic, got %d", len(ds))
			}
			got := ds[0].String()
			if i := strings.Index(got, c.want); i < 0 || (i > 0 && got[i-1] != '/') {
				t.Fatalf("diagnostic:\n%s\nwant it to start with %q", got, c.want)
			}
		})
	}
}
