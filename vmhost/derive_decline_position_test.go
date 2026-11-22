package vmhost

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// A body Nomi synthesized has no source line, so a decline inside one is
// reported at the declaration that asked for it: the `derive` line, in
// whichever file it is, or the type's declaration for an opaque type's
// name-only Debug, which no line asks for. No derived body declines today, so each case asks
// for the diagnostic of a body that did, by its name and its impl block, as
// Unsupported would for a body the machine finds unretained.
func TestDeclinePosition_ASynthesizedBodyIsReportedAtItsDerive(t *testing.T) {
	entry, name, files, manifest, err := SplitMultiFile(`// FILE: money.nomi
pub struct Money
    cents: Int
end

impl Equatable for Money
    fn equal(a: Money, b: Money) -> Bool
        a.cents == b.cents
    end
end

pub struct Coin
    n: Int
end

derive Hashable for Coin
derive Equatable for Coin

// FILE: main.nomi
import
    std::io
    money.{Coin, Money}
end

struct Rec
    m: Money
    c: Coin
end

derive Equatable for Rec

fn main()
    io.inspect(Rec{m: Money{cents: 1}, c: Coin{n: 2}} == Rec{m: Money{cents: 1}, c: Coin{n: 2}})
    io.inspect(Secret{n: 3})
end

opaque struct Secret
    n: Int
end
`)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	p, err := LoadSource(name, entry, WithVirtualFiles(files), WithVirtualManifest(manifest))
	if err != nil {
		t.Fatalf("the front end rejects the program, so it no longer tests a derived body's position: %v", err)
	}
	for _, c := range []struct {
		impl ir.ImplFunc
		want string
	}{
		// The `derive` line, not `Money`'s written equal in the other
		// file, which the function's own name also names.
		{ir.ImplFunc{Type: "Rec", Interface: "Equatable", Func: "equal"},
			"main.nomi:11:1: its body is not supported yet, so `fn Equatable.equal for Rec` cannot run"},
		{ir.ImplFunc{Type: "Coin", Interface: "Hashable", Func: "hash"},
			"money.nomi:15:1: its body is not supported yet, so `fn Hashable.hash for Coin` cannot run"},
		// An opaque type's name-only Debug: no line asks for it, so the
		// type does.
		{ir.ImplFunc{Type: "Secret", Interface: "Debug", Func: "inspect"},
			"main.nomi:18:15: its body is not supported yet, so `fn Debug.inspect for Secret` cannot run"},
		// A written impl function keeps its own line.
		{ir.ImplFunc{Type: "Money", Interface: "Equatable", Func: "equal"},
			"money.nomi:6:8: the body of `fn equal` is not supported yet, so `fn Equatable.equal for Money` cannot run"},
	} {
		name := c.impl.Name()
		t.Run(name, func(t *testing.T) {
			u := vm.Unretained{Kind: vm.NotRetained, Name: name, Impl: c.impl}
			ds := p.declineDiagnosticsVia([]string{name}, map[string]vm.Unretained{name: u})
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
