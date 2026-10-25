package localefiles_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/localefiles"
)

// A browser build's data names pl as in a file; loading the file adds pl's
// numbers to the store and its texts to the loader, once, and refuses a
// file of another build or of another locale.
func TestLoad(t *testing.T) {
	image := cldrdata.WithBuild(cldrfull.WebBundle([]string{"en", "pl"}, []string{"en"}, cldrdata.AllParts, nil), "b1")
	d, err := cldrdata.Decode(image)
	if err != nil {
		t.Fatal(err)
	}
	// pl keeps its grammatical features in the image, and its numbers are
	// root's until its file is loaded.
	numbersOf := func(d *cldrdata.Data) string {
		_, from, _ := d.Resolve([]string{"pl"}, "symbols\tlatn")
		return from
	}
	if got := d.Remote(); !slices.Equal(got, []string{"pl"}) || d.Build() != "b1" || numbersOf(d) != "und" {
		t.Fatalf("the image's data: remote %v, build %q, pl's numbers from %s", got, d.Build(), numbersOf(d))
	}
	if genders, _ := d.Grammar([]string{"pl"}); len(genders) != 5 {
		t.Fatalf("pl's genders in the image: %v", genders)
	}
	texts := localefiles.New("pl")
	texts.Messages["cart.items"] = "{{rzeczy}}"
	file := func(build, locale string) []byte {
		f := &localefiles.File{Build: build, Locales: []string{locale},
			CLDR: cldrfull.FileBundle([]string{locale}, cldrdata.AllParts, nil), Texts: []*localefiles.Translations{texts}}
		return f.Encode()
	}
	for _, c := range []struct {
		name  string
		fetch func(string) ([]byte, error)
		want  string
	}{
		{"offline", func(string) ([]byte, error) { return nil, errors.New("HTTP 404") }, "unreachable\tpl\tHTTP 404"},
		{"another build", func(tag string) ([]byte, error) { return file("b0", tag), nil }, "unreadable\tpl\tit was written by another build of the program"},
		{"another locale", func(string) ([]byte, error) { return file("b1", "de"), nil }, "unreadable\tpl\tit is not the file of pl"},
		{"not a file", func(string) ([]byte, error) { return []byte("<html>"), nil }, "unreadable\tpl\tit is not a locale file `nomi build --web` wrote"},
		{"no fetch", nil, "unreachable\tpl\tthis runner loads no locale files"},
	} {
		store := cldrdata.NewStore(d)
		if got := localefiles.Load(store, localefiles.NewLoader(c.fetch), []string{"pl"}); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		if store.Data() != d {
			t.Errorf("%s: a failed load changed the data", c.name)
		}
	}
	fetches := 0
	l := localefiles.NewLoader(func(tag string) ([]byte, error) { fetches++; return file("b1", tag), nil })
	store := cldrdata.NewStore(d)
	if got := localefiles.Loaded(store, l, []string{"en", "pl"}); !slices.Equal(got, []string{"en"}) {
		t.Errorf("before the load: %v", got)
	}
	for range 2 {
		if got := localefiles.Load(store, l, []string{"pl-PL", "pl"}); got != "" {
			t.Fatalf("load: %q", got)
		}
	}
	if fetches != 1 || numbersOf(store.Data()) != "pl" || numbersOf(d) != "und" {
		t.Errorf("%d fetches; pl's numbers from %s in the store's data, %s in the image's", fetches, numbersOf(store.Data()), numbersOf(d))
	}
	if v, _, _ := store.Data().Resolve([]string{"pl"}, "symbols\tlatn"); !strings.HasPrefix(v, ",") {
		t.Errorf("pl's symbols after the load: %q", v)
	}
	if m, ok := l.Installed("pl").Message("cart.items"); !ok || m == nil {
		t.Error("the texts are not installed")
	}
	if got := localefiles.Loaded(store, l, []string{"en", "pl"}); !slices.Equal(got, []string{"en", "pl"}) {
		t.Errorf("after the load: %v", got)
	}
}
