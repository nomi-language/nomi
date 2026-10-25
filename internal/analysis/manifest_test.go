package analysis

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeManifestFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadManifest_FullManifest(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `
[module]
name = "todo"
entry_points = ["main", "tools/seed"]
`)
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "todo" {
		t.Errorf("Name: got %q, want %q", m.Name, "todo")
	}
	if want := []string{"main", "tools/seed"}; !reflect.DeepEqual(m.EntryPoints, want) {
		t.Errorf("EntryPoints: got %v, want %v", m.EntryPoints, want)
	}
}

func TestLoadManifest_LibraryOnly(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module]
name = "stringkit"
`)
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "stringkit" {
		t.Errorf("Name: got %q, want %q", m.Name, "stringkit")
	}
	if len(m.EntryPoints) != 0 {
		t.Errorf("EntryPoints should be empty, got %v", m.EntryPoints)
	}
}

func TestLoadManifest_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadManifest(dir)
	if err == nil || !errors.Is(err, ErrManifestMissing) {
		t.Errorf("expected ErrManifestMissing, got %v", err)
	}
}

func TestLoadManifest_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module
name = "todo"
`)
	_, err := LoadManifest(dir)
	if err == nil {
		t.Fatal("expected error for malformed TOML")
	}
	if !strings.Contains(err.Error(), "nomi.toml") {
		t.Errorf("error message should mention nomi.toml, got: %v", err)
	}
}

func TestLoadManifest_MissingName(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module]
entry_points = ["main"]
`)
	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestLoadManifest_DuplicateEntries(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module]
name = "todo"
entry_points = ["main", "main"]
`)
	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("expected error for duplicate entry_points")
	}
}

func TestLoadManifest_WhitespaceOnlyName(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module]
name = "   "
`)
	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("expected error for whitespace-only name")
	}
}

func TestLoadManifest_RejectsReservedStdName(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module]
name = "std"
entry_points = ["main"]
`)
	_, err := LoadManifest(dir)
	if err == nil {
		t.Fatalf("expected reserved-name error, got nil")
	}
	if !strings.Contains(err.Error(), "std") || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("expected error mentioning 'std' and 'reserved'; got: %q", err.Error())
	}
}

func TestLoadManifest_AllowsStdNameForConfiguredStdlib(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "prelude.nomi", "")
	writeManifestFile(t, dir, "nomi.toml", `[module]
name = "std"
entry_points = []
`)
	t.Setenv("NOMI_STD_PATH", dir)

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("unexpected error for configured stdlib manifest: %v", err)
	}
	if m.Name != "std" {
		t.Fatalf("Name: got %q, want std", m.Name)
	}
}

func TestParseManifestData_RejectsReservedStdName(t *testing.T) {
	_, err := ParseManifestData([]byte(`[module]
name = "std"
entry_points = []
`), "<virtual nomi.toml>")
	if err == nil {
		t.Fatal("expected reserved-name error")
	}
}

func TestParseManifestData_I18nLocales(t *testing.T) {
	m, err := ParseManifestData([]byte("[module]\nname = \"app\"\n\n[i18n]\nlocales = [\"de\", \"PL\", \"pt-br\", \"iw\"]\n"), "nomi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"de", "pl", "pt-BR", "he"}; !reflect.DeepEqual(m.Locales, want) {
		t.Errorf("locales = %v, want %v (canonical, in the manifest's order)", m.Locales, want)
	}
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n"), "nomi.toml")
	if err != nil || m.Locales != nil {
		t.Errorf("a manifest with no [i18n] lists %v (%v); want nil, which carries every locale", m.Locales, err)
	}
	for body, want := range map[string]string{
		`locales = []`:              "[i18n] locales is empty",
		`locales = ["pt_BR"]`:       `"pt_BR" is not a locale tag; write "pt-BR"`,
		`locales = ["en--US"]`:      `"en--US" is not a locale tag`,
		`locales = ["de", "DE"]`:    `lists de twice ("de" and "DE")`,
		`locales = ["und"]`:         "und is CLDR's root",
		`locales = ["qaa"]`:         "has no data for qaa or a parent of it",
		`locales = ["en", "xx-YY"]`: `"xx-YY" is not a locale tag`,
	} {
		_, err := ParseManifestData([]byte("[module]\nname = \"app\"\n\n[i18n]\n"+body+"\n"), "nomi.toml")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want one containing %q", body, err, want)
		}
	}
	// A regional locale CLDR has no section of its own for still has data
	// through its parent.
	if _, err := ParseManifestData([]byte("[module]\nname = \"app\"\n\n[i18n]\nlocales = [\"de-US\"]\n"), "nomi.toml"); err != nil {
		t.Errorf("de-US: %v", err)
	}
}

// [i18n]'s message settings: their defaults, canonical tags, the carried
// locales, and each refusal.
func TestParseManifestData_I18nMessageSettings(t *testing.T) {
	m, err := ParseManifestData([]byte("[module]\nname = \"app\"\n\n[i18n]\nlocales = [\"pl\"]\n"), "nomi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if m.Source() != "en" || m.CatalogDir() != "locales" || m.PartialLocales != nil {
		t.Errorf("defaults: source %q, catalogs %q, partial %v", m.Source(), m.CatalogDir(), m.PartialLocales)
	}
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n\n[i18n]\nsource_locale = \"PL\"\nlocales = [\"pl\", \"en\"]\npartial_locales = [\"DE\"]\ncatalogs = \"i18n/./catalogs\"\n"), "nomi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if m.Source() != "pl" || m.CatalogDir() != "i18n/catalogs" || !reflect.DeepEqual(m.PartialLocales, []string{"de"}) {
		t.Errorf("source %q, catalogs %q, partial %v", m.Source(), m.CatalogDir(), m.PartialLocales)
	}
	if want := []string{"pl", "en", "de"}; !reflect.DeepEqual(m.CarriedLocales(), want) {
		t.Errorf("carried %v, want %v", m.CarriedLocales(), want)
	}
	// A browser build embeds the source locale unless web_embedded says
	// otherwise, which may name a partial locale and may be empty.
	if got := m.WebLocales(); !reflect.DeepEqual(got, []string{"pl"}) {
		t.Errorf("web locales %v, want the source locale", got)
	}
	m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n\n[i18n]\nlocales = [\"en\", \"pl\"]\npartial_locales = [\"de\"]\nweb_embedded = [\"PL\", \"de\"]\n"), "nomi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.WebLocales(); !reflect.DeepEqual(got, []string{"pl", "de"}) {
		t.Errorf("web locales %v, want pl and de", got)
	}
	if m, err = ParseManifestData([]byte("[module]\nname = \"app\"\n\n[i18n]\nlocales = [\"en\"]\nweb_embedded = []\n"), "nomi.toml"); err != nil || m.WebLocales() == nil || len(m.WebLocales()) != 0 {
		t.Errorf("an empty web_embedded: %v, %v", m.WebLocales(), err)
	}
	if m, _ = ParseManifestData([]byte("[module]\nname = \"app\"\n"), "nomi.toml"); m.WebLocales() != nil {
		t.Errorf("no [i18n]: web locales %v, want none to split", m.WebLocales())
	}
	for body, want := range map[string]string{
		`source_locale = "en_US"`:                          `[i18n] source_locale: "en_US" is not a locale tag; write "en-US"`,
		`partial_locales = ["de"]`:                         "[i18n] partial_locales needs locales",
		"locales = [\"de\"]\npartial_locales = [\"de\"]":   "lists de in both locales and partial_locales",
		"locales = [\"de\"]\npartial_locales = [\"x_y\"]":  `[i18n] partial_locales: "x_y" is not a locale tag`,
		"locales = [\"de\"]\ncatalogs = \"../outside\"":    `[i18n] catalogs "../outside" is not a directory under the module root`,
		"locales = [\"de\"]\ncatalogs = \"/abs/catalogs\"": `[i18n] catalogs "/abs/catalogs" is not a directory under the module root`,
		`web_embedded = ["en"]`:                            "[i18n] web_embedded needs locales",
		"locales = [\"de\"]\nweb_embedded = [\"fr\"]":      "[i18n] web_embedded lists fr, which neither locales nor partial_locales lists",
		"locales = [\"de\"]\nweb_embedded = [\"d_e\"]":     `[i18n] web_embedded: "d_e" is not a locale tag`,
	} {
		_, err := ParseManifestData([]byte("[module]\nname = \"app\"\n\n[i18n]\n"+body+"\n"), "nomi.toml")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want one containing %q", body, err, want)
		}
	}
}

func TestLoadManifest_EmptyEntryPointString(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, dir, "nomi.toml", `[module]
name = "todo"
entry_points = ["main", ""]
`)
	_, err := LoadManifest(dir)
	if err == nil {
		t.Fatal("expected error for empty-string entry_points element")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention 'empty', got: %v", err)
	}
}
