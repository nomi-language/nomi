package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// `nomi messages accept` records a message's current text as the source of
// a locale's translation, which makes a stale entry current: the project
// that failed to check for it checks after.
func TestMessagesAccept_MakesAStaleTranslationCurrent(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("nomi.toml", "[module]\nname = \"shop\"\n\n[i18n]\nlocales = [\"en\", \"pl\"]\n")
	write("cart.nomi", "pub message greeting(name: String) `Hello, {$name}!`\n")
	write("locales/pl/cart.toml", "[greeting]\nsource = '''Hi, {$name}!'''\ntext = '''Cześć, {$name}!'''\n")
	check := func() error {
		_, err := frontend.New(frontend.Config{}).CheckFile(filepath.Join(dir, "cart.nomi"), frontend.Mode{})
		return err
	}
	if err := check(); err == nil || !strings.Contains(err.Error(), "pl: `greeting` was translated from an older English text") {
		t.Fatalf("the stale entry is not reported: %v", err)
	}
	msg, err := acceptTranslation(filepath.Join(dir, "locales"), "pl", "greeting")
	if err != nil {
		t.Fatal(err)
	}
	if msg != "locales/pl/cart.toml: the pl translation of `greeting` is current" {
		t.Errorf("message %q", msg)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "locales", "pl", "cart.toml"))
	if want := "[greeting]\nsource = '''Hello, {$name}!'''\ntext = '''Cześć, {$name}!'''\n"; string(data) != want {
		t.Errorf("catalog:\n%s\nwant:\n%s", data, want)
	}
	if err := check(); err != nil {
		t.Fatalf("still refused: %v", err)
	}
	if _, err := acceptTranslation(dir, "pl", "farewell"); err == nil || err.Error() != "no file of the module declares `message farewell`" {
		t.Errorf("an unknown message: %v", err)
	}
	if _, err := acceptTranslation(dir, "fr", "greeting"); err == nil || err.Error() != "[i18n] lists no locale fr" {
		t.Errorf("an unlisted locale: %v", err)
	}
}
