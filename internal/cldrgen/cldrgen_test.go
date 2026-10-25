package cldrgen

import (
	"bytes"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/internal/stdplural"
)

func TestGeneratedDataIsCurrent(t *testing.T) {
	blobs, err := GenerateAll("data")
	if err != nil {
		t.Fatalf("generation fails: %v", err)
	}
	for name, want := range blobs {
		got, err := os.ReadFile(filepath.Join("..", "cldrfull", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("internal/cldrfull/%s is stale (%d bytes checked in, %d generated); run "+
				"`go generate ./internal/cldrfull` from the repository root", name, len(got), len(want))
		}
	}
}

// Every `@integer` and `@decimal` sample CLDR gives for a category selects
// that category under the embedded rules, in every locale, cardinal and
// ordinal.
func TestEverySampleSelectsItsCategory(t *testing.T) {
	src, err := Read("data")
	if err != nil {
		t.Fatal(err)
	}
	root := cldrfull.Full()
	if root.Version != Release {
		t.Fatalf("the embedded blob is CLDR %s, the pinned release is %s", root.Version, Release)
	}
	checked := 0
	for _, kind := range []struct {
		name   string
		rules  map[string][]Rule
		lookup func(string) (*cldrdata.RuleSet, bool)
	}{{"cardinal", src.Cardinal, root.Cardinal}, {"ordinal", src.Ordinal, root.Ordinal}} {
		for locale, rules := range kind.rules {
			set, ok := kind.lookup(locale)
			if !ok {
				t.Errorf("%s %s: no rule set in the blob", kind.name, locale)
				continue
			}
			for _, r := range rules {
				for _, sample := range append(append([]string(nil), r.Integer...), r.Decimal...) {
					for _, value := range expandSample(t, sample) {
						ops, err := cldrdata.ParseOperands(value)
						if err != nil {
							t.Fatalf("%s %s: sample %q: %v", kind.name, locale, value, err)
						}
						if got := set.Select(ops); got != r.Category {
							t.Errorf("%s %s: %s selects %s, CLDR's sample says %s", kind.name, locale, value, got, r.Category)
						}
						checked++
					}
				}
			}
		}
	}
	if checked < 10000 {
		t.Fatalf("checked only %d samples; the sample reader is not reading the data", checked)
	}
	t.Logf("checked %d samples", checked)
}

// expandSample lists the values one sample entry stands for: itself, every
// value of a range `a~b` at a's precision, or nothing for `…`.
func expandSample(t *testing.T, s string) []string {
	if s == "…" {
		return nil
	}
	lo, hi, isRange := strings.Cut(s, "~")
	if !isRange {
		return []string{s}
	}
	if strings.ContainsAny(s, "ce") {
		return []string{lo, hi}
	}
	_, frac, _ := strings.Cut(lo, ".")
	scale := len(frac)
	toInt := func(v string) *big.Int {
		n, ok := new(big.Int).SetString(strings.Replace(v, ".", "", 1), 10)
		if !ok {
			t.Fatalf("bad sample range %q", s)
		}
		return n
	}
	a, b := toInt(lo), toInt(hi)
	if _, hiFrac, _ := strings.Cut(hi, "."); len(hiFrac) != scale {
		t.Fatalf("sample range %q mixes precisions", s)
	}
	var out []string
	for n := a; n.Cmp(b) <= 0; n = new(big.Int).Add(n, big.NewInt(1)) {
		digits := n.String()
		if scale > 0 {
			digits = strings.Repeat("0", max(0, scale+1-len(digits))) + digits
			digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
		}
		out = append(out, digits)
	}
	return out
}

func TestOperands(t *testing.T) {
	for _, c := range []struct {
		in         string
		i, f, t    string
		v, w, e    int
		nIsInteger bool
	}{
		{"1", "1", "0", "0", 0, 0, 0, true},
		{"1.0", "1", "0", "0", 1, 0, 0, true},
		{"1.50", "1", "50", "5", 2, 1, 0, false},
		{"-12.034", "12", "34", "34", 3, 3, 0, false},
		{"0.010", "0", "10", "1", 3, 2, 0, false},
		{"123456789012345678901234567890.25", "123456789012345678901234567890", "25", "25", 2, 2, 0, false},
		{"1.1c6", "1100000", "0", "0", 0, 0, 6, true},
		{"1.0000001c6", "1000000", "1", "1", 1, 1, 6, false},
	} {
		o, err := cldrdata.ParseOperands(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if o.I != c.i || o.FDigits() != c.f || o.TDigits() != c.t || o.V() != c.v || o.W() != c.w || o.E != c.e {
			t.Errorf("%s: got i=%s f=%s t=%s v=%d w=%d e=%d", c.in, o.I, o.FDigits(), o.TDigits(), o.V(), o.W(), o.E)
		}
	}
	for _, bad := range []string{"", "1.2.3", "abc", ".5", "1c", "1e-3"} {
		if _, err := cldrdata.ParseOperands(bad); err == nil {
			t.Errorf("%q: parsed, want an error", bad)
		}
	}
}

// Every record of every locale, looked up along its chain in the generated
// data (where a section leaves out what its parents give it), is the value
// the vendored cldr-json gives that locale.
func TestEveryLocaleResolvesToItsSourceRecords(t *testing.T) {
	src, err := ReadLocales("data")
	if err != nil {
		t.Fatal(err)
	}
	d := cldrfull.Full()
	checked := 0
	for tag, recs := range src.Records {
		chain := stdlocale.Fallbacks(d, tag)
		if tag == "und" {
			chain = nil
		}
		for key, want := range recs {
			got, from, ok := d.Resolve(chain, key)
			if !ok || got != want {
				t.Errorf("%s %q: resolves to %q (from %s), cldr-json says %q", tag, key, got, from, want)
			}
			checked++
		}
	}
	if len(src.Records) < 700 || checked < 700*15 {
		t.Fatalf("checked %d records of %d locales; the reader is not reading the data", checked, len(src.Records))
	}
}

// A program's bundle holds each listed locale resolved, so it answers every
// record as the full data does, with no ancestor beside it.
func TestABundleAnswersAsTheFullData(t *testing.T) {
	full := cldrfull.Full()
	listed := []string{"de-CH", "pl", "ar-EG", "hi", "en-GB"}
	d, err := cldrdata.Decode(cldrfull.Bundle(listed, cldrdata.AllParts, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range listed {
		chain := stdlocale.Fallbacks(full, tag)
		if got := stdlocale.Fallbacks(d, tag); strings.Join(got, " ") != strings.Join(chain, " ") {
			t.Errorf("%s: the bundle's chain is %v, the full data's %v", tag, got, chain)
		}
		for _, key := range []string{cldrdata.KeyNumbering, cldrdata.KeyMinGrouping, "symbols\tlatn", "decimal\tlatn",
			"percent\tlatn", "list\tstandard", "list\tunit-narrow"} {
			want, _, _ := full.Resolve(chain, key)
			if got, _, _ := d.Resolve(chain, key); got != want {
				t.Errorf("%s %q: the bundle gives %q, the full data %q", tag, key, got, want)
			}
		}
	}
	if got, _ := d.Available(); strings.Join(got, ",") != strings.Join(listed, ",") {
		t.Errorf("available: %v", got)
	}
	if d.HasLocale("de") || d.HasLocale("ar") {
		t.Errorf("the bundle carries an ancestor of a listed locale: %v", d.Locales())
	}
	// A bundle with only the list part carries no number symbols, and one
	// with no part carries nothing.
	lists := string(cldrfull.Bundle(listed, cldrdata.PartLists, nil))
	if strings.Contains(lists, "symbols\t") || strings.Contains(lists, "digits\t") || strings.Contains(lists, "cardinal\t") {
		t.Errorf("a lists-only bundle carries other parts:\n%s", lists)
	}
	if b := cldrfull.Bundle(listed, 0, nil); len(b) != 0 {
		t.Errorf("a bundle with no parts holds %d bytes", len(b))
	}
}

// A bundle that keeps only the parent locales of its languages resolves
// every tag, listed or not, as one with every parent does.
func TestListedParentsResolveAsEveryParent(t *testing.T) {
	listed := []string{"no", "es-419", "en-001", "zh-Hant"}
	decode := func(parts cldrdata.Parts) *cldrdata.Data {
		d, err := cldrdata.Decode(cldrfull.Bundle(listed, parts, nil))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	sections := cldrdata.PartNumbers | cldrdata.PartLists | cldrdata.PartPlurals
	pruned, whole := decode(cldrdata.PartListedParents|sections), decode(cldrdata.PartParents|sections)
	for _, tag := range []string{"nb", "nn", "no", "es-MX", "es-AR", "es", "en-GB", "en-AU", "en-150", "en",
		"zh-Hant-HK", "zh-TW", "zh", "fr", "de-CH", "pt-AO", "az-Arab", "sr-Latn-RS"} {
		for _, key := range []string{"symbols\tlatn", "decimal\tlatn", "list\tstandard", cldrdata.KeyMinGrouping} {
			want, wantFrom, _ := whole.Resolve(stdlocale.Fallbacks(whole, tag), key)
			got, from, _ := pruned.Resolve(stdlocale.Fallbacks(pruned, tag), key)
			if got != want || from != wantFrom {
				t.Errorf("%s %q: with the listed languages' parents %q (from %s); with every parent %q (from %s)",
					tag, key, got, from, want, wantFrom)
			}
		}
		if a, b := stdplural.Rules(pruned, tag, false), stdplural.Rules(whole, tag, false); fmt.Sprint(a.Categories()) != fmt.Sprint(b.Categories()) {
			t.Errorf("%s: plural categories %v with the listed parents, %v with every parent", tag, a.Categories(), b.Categories())
		}
	}
	// English and Spanish have most of CLDR's explicit parents; German and
	// Polish have none.
	few := []string{"de", "pl"}
	if p, w := len(cldrfull.Bundle(few, cldrdata.PartListedParents, nil)), len(cldrfull.Bundle(few, cldrdata.PartParents, nil)); p*10 > w {
		t.Errorf("de's and pl's parents take %d bytes, every parent %d", p, w)
	}
}
