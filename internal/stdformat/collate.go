package stdformat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/collation"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/rt"
)

// std/collation's `Collator` is an opaque String: the locale's tag and the
// options it was made with (FFICollatorNew writes it). The collator itself
// is built from the program's CLDR data the first time the data compares
// with it, and kept with the data (cldrdata.Data.Memo), so a program that
// loads a locale file gets collators of the new data.

// The positions of std/collation's `Strength` and `CaseFirst` variants.
const (
	strengthPrimary int64 = iota + 1
	strengthSecondary
	strengthTertiary
	strengthQuaternary
	strengthIdentical
)

const (
	caseFirstDefault int64 = iota + 1
	caseFirstUpper
	caseFirstLower
	caseFirstOff
)

// FFICollatorNew is `Collator.new`: the collator's text. It reads no data,
// so a collator for any locale can be made; one the program carries no
// tailoring for compares in the root order.
func FFICollatorNew(locale string, strength int64, numeric bool, caseFirst int64, ignorePunctuation bool) string {
	return fmt.Sprintf("%s;%d;%t;%d;%t", locale, strength, numeric, caseFirst, ignorePunctuation)
}

// FFICollatorInspect is `Collator`'s Debug: the call that makes it.
func FFICollatorInspect(spec string) string {
	tag, strength, numeric, caseFirst, shifted := parseSpec(spec)
	var b strings.Builder
	fmt.Fprintf(&b, "Collator.new(Locale`%s`", tag)
	if name, ok := map[int64]string{strengthPrimary: "Primary", strengthSecondary: "Secondary",
		strengthQuaternary: "Quaternary", strengthIdentical: "Identical"}[strength]; ok {
		b.WriteString(", strength: Strength." + name)
	}
	if numeric {
		b.WriteString(", numeric: True")
	}
	if name, ok := map[int64]string{caseFirstUpper: "Upper", caseFirstLower: "Lower", caseFirstOff: "Off"}[caseFirst]; ok {
		b.WriteString(", case_first: CaseFirst." + name)
	}
	if shifted {
		b.WriteString(", ignore_punctuation: True")
	}
	b.WriteString(")")
	return b.String()
}

func parseSpec(spec string) (tag string, strength int64, numeric bool, caseFirst int64, shifted bool) {
	f := strings.Split(spec, ";")
	for len(f) < 5 {
		f = append(f, "")
	}
	strength, _ = strconv.ParseInt(f[1], 10, 64)
	caseFirst, _ = strconv.ParseInt(f[3], 10, 64)
	return f[0], strength, f[2] == "true", caseFirst, f[4] == "true"
}

// FFICollatorCompare is `Collator.compare`: -1, 0 or 1.
func FFICollatorCompare(fr *rt.Frame, spec, a, b string) int64 {
	return int64(CollatorFor(stdlocale.Data(fr), spec).Compare(a, b))
}

// FFICollatorSortKey is `Collator.sort_key`.
func FFICollatorSortKey(fr *rt.Frame, spec, text string) rt.Bytes {
	return rt.Bytes(CollatorFor(stdlocale.Data(fr), spec).SortKey(text))
}

type collatorKey struct{ spec string }

type rootKey struct{}

type tailoringKey struct{ tag, typ string }

// CollatorFor is the collator a Collator's text names over d.
func CollatorFor(d *cldrdata.Data, spec string) *collation.Collator {
	return d.Memo(collatorKey{spec}, func() any { return newCollator(d, spec) }).(*collation.Collator)
}

func rootOf(d *cldrdata.Data) *collation.Root {
	return d.Memo(rootKey{}, func() any {
		root, err := collation.DecodeRoot(d.CollationRoot())
		if err != nil {
			// Data with no collation table: every code point takes its
			// implicit weight, code point order.
			return &collation.Root{Table: collation.NewTable(nil)}
		}
		return root
	}).(*collation.Root)
}

// Tailoring is the tailoring of collation type typ (the locale's default
// type when "") that a locale's collator uses over d, nil for the root
// order, and the type found.
func Tailoring(d *cldrdata.Data, tag, typ string) (*collation.Tailoring, string) {
	chain := stdlocale.CollationFallbacks(d, tag)
	def := "standard"
	if v, _, ok := d.Resolve(chain, cldrdata.KeyCollationDefault); ok {
		def = v
	}
	if typ == "" || typ == "default" {
		typ = def
	}
	v, from, ok := d.Resolve(chain, cldrdata.RecordKey(cldrdata.KeyCollation, typ))
	if !ok && typ != def {
		typ = def
		v, from, ok = d.Resolve(chain, cldrdata.RecordKey(cldrdata.KeyCollation, typ))
	}
	if !ok {
		return nil, typ
	}
	t := d.Memo(tailoringKey{from, typ}, func() any {
		t, err := collation.DecodeTailoring([]byte(v))
		if err != nil {
			return (*collation.Tailoring)(nil)
		}
		return t
	}).(*collation.Tailoring)
	return t, typ
}

func newCollator(d *cldrdata.Data, spec string) *collation.Collator {
	tag, strength, numeric, caseFirst, shifted := parseSpec(spec)
	tail, _ := Tailoring(d, tag, collation.TypeOfKeyword(stdlocale.Extension(tag, "co")))
	return collation.New(rootOf(d), tail, func(s *collation.Settings) {
		// The tailoring's settings, then the tag's keywords, then the
		// arguments that differ from their defaults.
		switch stdlocale.Extension(tag, "ks") {
		case "level1":
			s.Strength = collation.Primary
		case "level2":
			s.Strength = collation.Secondary
		case "level3":
			s.Strength = collation.Tertiary
		case "level4":
			s.Strength = collation.Quaternary
		case "identic":
			s.Strength = collation.Identical
		}
		switch stdlocale.Extension(tag, "kn") {
		case "true", "yes":
			s.Numeric = true
		case "false", "no":
			s.Numeric = false
		}
		switch stdlocale.Extension(tag, "kf") {
		case "upper":
			s.CaseFirst = collation.CaseFirstUpper
		case "lower":
			s.CaseFirst = collation.CaseFirstLower
		case "false":
			s.CaseFirst = collation.CaseFirstOff
		}
		switch stdlocale.Extension(tag, "ka") {
		case "shifted":
			s.Shifted = true
		case "noignore":
			s.Shifted = false
		}
		switch strength {
		case strengthPrimary:
			s.Strength = collation.Primary
		case strengthSecondary:
			s.Strength = collation.Secondary
		case strengthQuaternary:
			s.Strength = collation.Quaternary
		case strengthIdentical:
			s.Strength = collation.Identical
		}
		if numeric {
			s.Numeric = true
		}
		switch caseFirst {
		case caseFirstUpper:
			s.CaseFirst = collation.CaseFirstUpper
		case caseFirstLower:
			s.CaseFirst = collation.CaseFirstLower
		case caseFirstOff:
			s.CaseFirst = collation.CaseFirstOff
		}
		if shifted {
			s.Shifted = true
		}
	})
}
