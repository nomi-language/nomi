package catalogcheck

import (
	"testing"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/nomi-language/nomi/internal/cldrfull"
)

// TestLanguageNamesAreCurrent holds langnames.go to what langnames_gen.go
// writes from the compiled-in CLDR data and x/text's display names:
// `go generate ./internal/catalogcheck` rewrites it.
func TestLanguageNamesAreCurrent(t *testing.T) {
	want := map[string]string{}
	for _, tag := range cldrfull.Full().Locales() {
		lt, err := language.Parse(tag)
		if err != nil {
			continue
		}
		base, _ := lt.Base()
		if name := display.English.Languages().Name(base); name != "" && base.String() != "und" {
			want[base.String()] = name
		}
	}
	if len(want) != len(languageNames) {
		t.Errorf("langnames.go has %d names and the data %d: run `go generate ./internal/catalogcheck`", len(languageNames), len(want))
	}
	for k, v := range want {
		if languageNames[k] != v {
			t.Errorf("%s: langnames.go says %q, display %q: run `go generate ./internal/catalogcheck`", k, languageNames[k], v)
		}
	}
	if LanguageName("pl-PL") != "Polish" || LanguageName("pt-PT") != "Portuguese" {
		t.Errorf("pl-PL is %q and pt-PT %q", LanguageName("pl-PL"), LanguageName("pt-PT"))
	}
}
