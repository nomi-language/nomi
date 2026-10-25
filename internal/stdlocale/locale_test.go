package stdlocale

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/cldrdata"
)

// full is the root data internal/cldrfull compiles in, read from its file:
// cldrfull imports this package, so its test cannot import cldrfull.
func full(t *testing.T) *cldrdata.Data {
	t.Helper()
	blob, err := os.ReadFile("../cldrfull/root.cldr")
	if err != nil {
		t.Fatal(err)
	}
	d, err := cldrdata.Decode(blob)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestParse(t *testing.T) {
	for _, c := range []struct {
		in   string
		want LocaleAttempt
	}{
		{"pt-BR", LocaleAttempt{Value: "pt-BR"}},
		{"EN-us", LocaleAttempt{Value: "en-US"}},
		{"zh-hant-hk", LocaleAttempt{Value: "zh-Hant-HK"}},
		{"iw", LocaleAttempt{Value: "he"}},
		{"in", LocaleAttempt{Value: "id"}},
		{"sh", LocaleAttempt{Value: "sr-Latn"}},
		{"en-US-u-nu-latn", LocaleAttempt{Value: "en-US-u-nu-latn"}},
		{"es-419", LocaleAttempt{Value: "es-419"}},
		{"und", LocaleAttempt{Value: "und"}},
		{"", LocaleAttempt{Kind: KindEmpty}},
		{"pt_BR", LocaleAttempt{Kind: KindUnderscore, Detail: "pt-BR"}},
		{"EN_us", LocaleAttempt{Kind: KindUnderscore, Detail: "en-US"}},
		{"en--US", LocaleAttempt{Kind: KindMalformed, Detail: "en--US"}},
		{"123", LocaleAttempt{Kind: KindMalformed, Detail: "123"}},
		{"C", LocaleAttempt{Kind: KindMalformed, Detail: "C"}},
		{"xx", LocaleAttempt{Kind: KindUnknownSubtag, Detail: "xx"}},
	} {
		if got := FFIParse(c.in); got != c.want {
			t.Errorf("parse(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestFromPosix(t *testing.T) {
	for in, want := range map[string]string{
		"pl_PL.UTF-8":      "pl-PL",
		"en_US@euro":       "en-US",
		"de_DE.ISO-8859-1": "de-DE",
		"sr_RS@latin":      "sr-Latn-RS",
		"ca_ES@valencia":   "ca-ES-valencia",
		"C":                "und",
		"POSIX":            "und",
		"C.UTF-8":          "und",
		"fr":               "fr",
	} {
		if got := FFIFromPosix(in); got.Kind != KindOK || got.Value != want {
			t.Errorf("from_posix(%q) = %+v, want %s", in, got, want)
		}
	}
	if got := FFIFromPosix(""); got.Kind != KindEmpty {
		t.Errorf("from_posix(\"\") = %+v, want Empty", got)
	}
	if got := FFIFromPosix("not a locale"); got.Kind != KindMalformed || got.Detail != "not a locale" {
		t.Errorf("from_posix(\"not a locale\") = %+v, want Malformed", got)
	}
}

func TestFallbacks(t *testing.T) {
	for in, want := range map[string]string{
		"es-MX":           "es-MX es-419 es",
		"es-419":          "es-419 es",
		"pt-PT":           "pt-PT pt",
		"pt-AO":           "pt-AO pt-PT pt",
		"pt-BR":           "pt-BR pt",
		"en-150":          "en-150 en-001 en",
		"en-GB":           "en-GB en-001 en",
		"en-US":           "en-US en",
		"zh-Hant-HK":      "zh-Hant-HK zh-Hant",
		"zh-Hant-MO":      "zh-Hant-MO zh-Hant-HK zh-Hant",
		"zh-TW":           "zh-TW zh-Hant",
		"zh-CN":           "zh-CN zh",
		"sr-Latn-RS":      "sr-Latn-RS sr-Latn",
		"ru-Latn":         "ru-Latn",
		"en-Latn-US":      "en-Latn-US en-Latn en",
		"nb":              "nb no",
		"de-CH-u-nu-latn": "de-CH-u-nu-latn de-CH de",
		"ca-ES-valencia":  "ca-ES-valencia ca-ES ca",
		"und":             "",
	} {
		if got := strings.Join(Fallbacks(full(t), in), " "); got != want {
			t.Errorf("fallbacks(%s) = %q, want %q", in, got, want)
		}
	}
	if got := strings.Join(PluralFallbacks(full(t), "zh-Hant-HK"), " "); got != "zh-Hant-HK zh-Hant zh" {
		t.Errorf("plural fallbacks(zh-Hant-HK) = %q: plural rules follow the plurals component, which truncates", got)
	}
}

func TestNegotiate(t *testing.T) {
	available := []string{"en", "pl", "pt-PT", "es"}
	for _, c := range []struct {
		requested []string
		want      string
	}{
		{[]string{"pl-PL"}, "pl"},
		{[]string{"es-MX"}, "es"},
		{[]string{"de", "es"}, "es"},
		{[]string{"en-GB"}, "en"},
		{[]string{"de"}, "en-US"},
		{nil, "en-US"},
	} {
		if got := FFINegotiate(c.requested, available, "en-US"); got != c.want {
			t.Errorf("negotiate(%v) = %s, want %s", c.requested, got, c.want)
		}
	}
	if got := FFINegotiate([]string{"pl"}, nil, "en"); got != "en" {
		t.Errorf("negotiate with nothing available = %s, want the fallback", got)
	}
}

func TestParts(t *testing.T) {
	if got := FFILanguage("zh-Hant-HK"); got != "zh" {
		t.Errorf("language = %s", got)
	}
	if s := FFIScript("zh-Hant-HK"); s == nil || *s != "Hant" {
		t.Errorf("script = %v", s)
	}
	if s := FFIScript("pt-BR"); s != nil {
		t.Errorf("pt-BR spells no script, got %s", *s)
	}
	if r := FFIRegion("es-419"); r == nil || *r != "419" {
		t.Errorf("region = %v", r)
	}
	if r := FFIRegion("pl"); r != nil {
		t.Errorf("pl spells no region, got %s", *r)
	}
	if !slices.Equal(Fallbacks(full(t), "pl"), []string{"pl"}) {
		t.Errorf("fallbacks(pl) = %v", Fallbacks(full(t), "pl"))
	}
}
