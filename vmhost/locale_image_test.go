package vmhost

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmrunner"
)

const localeProgram = `import {
    std/io
    std/locale.Locale
}

fn main() {
    io.print(Int.format(1234567, Locale` + "`de`" + `))
    io.print(Int.format(1234567, Locale` + "`de-CH`" + `))
    io.print(Int.format(1234567, Result.with_default(Locale.parse("fr"), Locale` + "`de`" + `)))
    io.print(String.join_list(["a", "b", "c"], Locale` + "`pl`" + `))
    io.print("${Locale.available()}")
}
`

// loadWithLocales loads src with a nomi.toml that lists locales under
// [i18n], or none when locales is empty.
func loadWithLocales(t *testing.T, src string, locales ...string) *Program {
	t.Helper()
	toml := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[money]\ncustom_currencies = [\"PTS:0\"]\n"
	if len(locales) > 0 {
		toml += "\n[i18n]\nlocales = [\"" + strings.Join(locales, "\", \"") + "\"]\n"
	}
	m, err := analysis.ParseManifestData([]byte(toml), "<virtual nomi.toml>")
	if err != nil {
		t.Fatal(err)
	}
	p, err := LoadSource("main", src, WithVirtualManifest(m))
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	return p
}

// imageLocales is the CLDR data a built image of p carries.
func imageLocales(t *testing.T, p *Program) (raw []byte, image []byte) {
	t.Helper()
	image, _, err := p.BuildImage()
	if err != nil {
		t.Fatal(err)
	}
	im, err := ir.DecodeImage(image)
	if err != nil {
		t.Fatal(err)
	}
	return im.Locales, image
}

// A built image carries the listed locales' data and nothing else, and runs
// as the program does in process.
func TestLocaleImage_CarriesOnlyTheListedLocales(t *testing.T) {
	listed := loadWithLocales(t, localeProgram, "de", "pl")
	data, image := imageLocales(t, listed)
	d, err := cldrdata.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Locales(); !slices.Equal(got, []string{"de", "pl", "und"}) {
		t.Errorf("the image carries the locales %v, want de, pl and root", got)
	}
	if got, _ := d.Available(); !slices.Equal(got, []string{"de", "pl"}) {
		t.Errorf("available = %v", got)
	}
	all := loadWithLocales(t, localeProgram)
	allData, _ := imageLocales(t, all)
	if len(data)*5 > len(allData) {
		t.Errorf("listing two locales carries %d bytes of locale data, more than a fifth of every locale's %d",
			len(data), len(allData))
	}
	t.Logf("locale data: %d bytes for de and pl, %d for every locale", len(data), len(allData))

	var inProcess bytes.Buffer
	if err := listed.Run(context.Background(), &inProcess, nil, false); err != nil {
		t.Fatal(err)
	}
	want := "1.234.567\n1.234.567\n1,234,567\na, b i c\n[de, pl]\n"
	if inProcess.String() != want {
		t.Errorf("in process:\n%s\nwant:\n%s", inProcess.String(), want)
	}
	var built, stderr bytes.Buffer
	if code := vmrunner.Run(image, &built, &stderr, nil, nil, false); code != 0 || built.String() != want {
		t.Errorf("the built image exits %d and prints:\n%s%s\nwant:\n%s", code, built.String(), stderr.String(), want)
	}
	// The same image as a compiled program file.
	path := filepath.Join(t.TempDir(), "main.nomic")
	if err := os.WriteFile(path, vmrunner.EncodeImageFile(image, 0, "test"), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(file, []byte("locale\tde\n")) || bytes.Contains(file, []byte("locale\tfr\n")) {
		t.Error("the .nomic does not carry exactly the listed locales")
	}
}

// Within the data, an image carries only the parts its program reaches.
func TestLocaleImage_CarriesOnlyTheReachedParts(t *testing.T) {
	for _, c := range []struct {
		name, body    string
		want, notWant []string
	}{
		{"numbers", `io.print(Int.format(5, Locale` + "`de`" + `))`,
			[]string{"symbols\tlatn", "nonlikely-script-parent"}, []string{"list\t", "cardinal\t", "available\t", "parent\tes-MX"}},
		{"lists", `io.print(String.join_list(["a"], Locale` + "`de`" + `))`,
			[]string{"list\tstandard", "nonlikely-script-parent"}, []string{"symbols\t", "digits\t", "cardinal\t", "parent\tes-MX"}},
		{"plurals", `io.print("${PluralCategory.cardinal(3, Locale` + "`de`" + `)}")`,
			[]string{"cardinal\t", "nonlikely-script-parent"}, []string{"symbols\t", "list\t", "locale\t", "parent\tes-MX"}},
		{"available", `io.print("${Locale.available()}")`,
			[]string{"available\tde,pl"}, []string{"parent\t", "locale\t", "cardinal\t"}},
		// Locale.fallbacks answers for any tag, so it carries every parent.
		// es-MX is parsed: a literal of a locale the program does not carry
		// is a compile error.
		{"fallbacks", `io.print("${Result.map(Locale.parse("es-MX"), Locale.fallbacks)}")`,
			[]string{"parent\tes-MX\tes-419"}, []string{"locale\t", "cardinal\t", "symbols\t"}},
	} {
		src := "import {\n    std/io\n    std/locale.Locale\n    std/plural.PluralCategory\n}\n\nfn main() {\n    " + c.body + "\n}\n"
		if !strings.Contains(c.body, "Plural") {
			src = strings.Replace(src, "    std/plural.PluralCategory\n", "", 1)
		}
		data, _ := imageLocales(t, loadWithLocales(t, src, "de", "pl"))
		for _, w := range c.want {
			if !bytes.Contains(data, []byte(w)) {
				t.Errorf("%s: the image's locale data has no %q:\n%s", c.name, w, data)
			}
		}
		for _, w := range c.notWant {
			if bytes.Contains(data, []byte(w)) {
				t.Errorf("%s: the image's locale data carries %q, which the program never reads", c.name, w)
			}
		}
	}
	// A program that reads no locale data carries none.
	data, _ := imageLocales(t, loadWithLocales(t, "import std/io\n\nfn main() {\n    io.print(\"hi\")\n}\n", "de"))
	if len(data) != 0 {
		t.Errorf("a program that formats nothing carries %d bytes of locale data", len(data))
	}
}

// Every Funcs row whose Go function reads the machine's CLDR data (it takes
// the frame, in a package that reads locale data) names the parts it reads,
// or a built image would carry none of them.
func TestEveryRowThatReadsLocaleDataNamesItsParts(t *testing.T) {
	parts := stdlibbindings.LocaleParts()
	localePkgs := []string{"internal/stdlocale.", "internal/stdplural.", "internal/stdformat."}
	for _, b := range stdlibbindings.Funcs() {
		name := funcName(b.Fn)
		inLocalePkg := false
		for _, pkg := range localePkgs {
			inLocalePkg = inLocalePkg || strings.Contains(name, pkg)
		}
		if !inLocalePkg || !takesFrame(b.Fn) {
			continue
		}
		if parts[b.Name] == 0 {
			t.Errorf("%s (%s) reads the program's locale data and stdlibbindings.LocaleParts names no part for it", b.Name, name)
		}
	}
	for key := range parts {
		found := false
		for _, b := range stdlibbindings.Funcs() {
			found = found || b.Name == key
		}
		if !found {
			t.Errorf("LocaleParts names %s, which is no Funcs row", key)
		}
	}
}

func funcName(fn any) string {
	if f := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()); f != nil {
		return f.Name()
	}
	return ""
}

func takesFrame(fn any) bool {
	ft := reflect.TypeOf(fn)
	return ft.NumIn() > 0 && ft.In(0) == reflect.TypeFor[*rt.Frame]()
}
