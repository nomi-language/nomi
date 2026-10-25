package main

// `nomi build --web` for a program that lists its locales: the locale files
// it writes beside app.wasm, the locales the image embeds, and the program's
// Locale.load under nomi.js. See vmhost.BuildWebImage.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/localefiles"
	"github.com/nomi-language/nomi/vmrunner"
)

// localesProject is a project in English, Polish and Portuguese, with
// German partial, whose main formats before and after loading Polish.
var localesProject = map[string]string{
	"nomi.toml": "[module]\nname = \"shop\"\nentry_points = [\"main\"]\n\n[i18n]\nlocales = [\"en\", \"pl\", \"pt\"]\npartial_locales = [\"de\"]\n",
	"text.nomi": "pub message items(count: Int) `\n    .input {$count :integer}\n    .match $count\n    one {{{$count} item}}\n    * {{{$count} items}}\n    `\n",
	"locales/pl/text.toml": "[items]\nsource = '''\n.input {$count :integer}\n.match $count\none {{{$count} item}}\n* {{{$count} items}}\n'''\n" +
		"text = '''\n.input {$count :integer}\n.match $count\none {{{$count} rzecz}}\nfew {{{$count} rzeczy}}\nmany {{{$count} rzeczy}}\n* {{{$count} rzeczy}}\n'''\n",
	"locales/pt/text.toml": "[items]\nsource = '''\n.input {$count :integer}\n.match $count\none {{{$count} item}}\n* {{{$count} items}}\n'''\n" +
		"text = '''\n.input {$count :integer}\n.match $count\none {{{$count} item}}\nmany {{{$count} de itens}}\n* {{{$count} itens}}\n'''\n",
	"main.nomi": `import {
    std/io
    std/locale.Locale
    std/message.Message
    text
}

fn report(locale: Locale) {
    case Locale.load(locale) {
        Ok(_) -> io.print("${locale} loaded")
        Err(e) -> io.print(Display.to_string(e))
    }
}

fn main() {
    pl = Locale` + "`pl`" + `
    io.print("${Locale.loaded()}")
    io.print(text.items(3) |> Message.format(pl))
    io.print(Int.format(1234567, pl))
    io.print(String.join_list(["a", "b", "c"], pl))
    report(pl)
    io.print(text.items(3) |> Message.format(pl))
    io.print(Int.format(1234567, Locale` + "`pl-PL`" + `))
    io.print("${Locale.loaded()}")
    report(Locale` + "`de`" + `)
    report(Locale` + "`pt-BR`" + `)
}
`,
}

func TestBuildWeb_Locales(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a js/wasm runner; -short")
	}
	env := buildEnv(t.TempDir())
	src := t.TempDir()
	for name, text := range localesProject {
		mustWrite(t, filepath.Join(src, filepath.FromSlash(name)), text)
	}
	entry := filepath.Join(src, "main.nomi")
	folder := filepath.Join(t.TempDir(), "site")
	build := func() {
		t.Helper()
		if b := runProcess(t, env, src, "", nomiBin, "build", "--web", entry, "-o", folder); b.exit != 0 || b.stderr != "" {
			t.Fatalf("nomi build --web: exit %d\n%s", b.exit, b.transcript())
		}
	}
	build()

	// One file per listed locale but the source, which the image embeds.
	files, _ := filepath.Glob(filepath.Join(folder, "locales", "*"))
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
		info, _ := os.Stat(f)
		t.Logf("%s: %d bytes", filepath.Base(f), info.Size())
	}
	if strings.Join(names, " ") != "de.nomil pl.nomil pt.nomil" {
		t.Fatalf("locales/ holds %v, want de, pl and pt", names)
	}
	image := webImage(t, folder)
	d, err := cldrdata.Decode(image.Locales)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Remote(); !slices.Equal(got, []string{"pl", "pt", "de"}) || d.Build() == "" {
		t.Errorf("the image leaves %v in files, build %q", got, d.Build())
	}
	if got, _ := d.Available(); !slices.Equal(got, []string{"en", "pl", "pt", "de"}) {
		t.Errorf("the image lists %v as available", got)
	}
	if strings.Contains(string(image.Locales), "locale\tpl\ndecimal") || !strings.Contains(string(image.Locales), "locale\ten\n") {
		t.Errorf("the image's data:\n%s", image.Locales)
	}
	pl, err := os.ReadFile(filepath.Join(folder, "locales", "pl.nomil"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := localefiles.Decode(pl)
	if err != nil {
		t.Fatal(err)
	}
	if f.Build != d.Build() || !slices.Equal(f.Locales, []string{"pl"}) || !strings.Contains(string(f.CLDR), "symbols\tlatn\t,") ||
		!strings.Contains(f.Texts[0].Messages["text.items"], "rzeczy") {
		t.Errorf("pl.nomil: build %q, locales %v, cldr %q, texts %+v", f.Build, f.Locales, f.CLDR, f.Texts[0])
	}
	if bytes.Contains(image.Locales, []byte("rzeczy")) {
		t.Error("the image carries Polish text")
	}

	// web_embedded keeps Polish in the image, and the rebuild removes the
	// file it no longer writes.
	mustWrite(t, filepath.Join(src, "nomi.toml"), localesProject["nomi.toml"]+"web_embedded = [\"en\", \"pl\"]\n")
	mustWrite(t, filepath.Join(folder, "locales", "mine.nomil"), "not a build's")
	build()
	if _, err := os.Stat(filepath.Join(folder, "locales", "pl.nomil")); err == nil {
		t.Error("the rebuild left pl.nomil")
	}
	if got, _ := os.ReadFile(filepath.Join(folder, "locales", "mine.nomil")); string(got) != "not a build's" {
		t.Error("the rebuild removed a file it did not write")
	}
	os.Remove(filepath.Join(folder, "locales", "mine.nomil"))
	mustWrite(t, filepath.Join(src, "nomi.toml"), localesProject["nomi.toml"])
	build()

	t.Run("node", func(t *testing.T) {
		node, err := exec.LookPath("node")
		if err != nil {
			t.Skip("no node on PATH")
		}
		if major := nodeMajor(t, node); major < 22 {
			t.Skipf("node %d; the harness needs 22 or newer", major)
		}
		// German's file is gone, and Portuguese's is from another build.
		if err := os.Remove(filepath.Join(folder, "locales", "de.nomil")); err != nil {
			t.Fatal(err)
		}
		pt := filepath.Join(folder, "locales", "pt.nomil")
		data, _ := os.ReadFile(pt)
		mustWrite(t, pt, strings.Replace(string(data), d.Build(), "0123456789abcdef", 1))
		spec, _ := json.Marshal(map[string]any{"runs": []any{
			map[string]any{"folder": folder},
			map[string]any{"folder": folder, "worker": false},
		}})
		harness := filepath.Join(repoRoot(t), "cmd", "nomi", "testdata", "web", "harness.mjs")
		got := runNode(t, env, src, "", node, harness, string(spec))
		if got.exit != 0 {
			t.Fatalf("the harness: exit %d\n%s", got.exit, got.transcript())
		}
		var results []struct {
			Stdout, Stderr string
			ExitCode       int
		}
		if err := json.Unmarshal([]byte(got.stdout), &results); err != nil || len(results) != 2 {
			t.Fatalf("the harness printed %q (%v)", got.stdout, err)
		}
		for i, r := range results {
			lines := strings.Split(r.Stdout, "\n")
			if r.ExitCode != 0 || len(lines) != 11 {
				t.Fatalf("run %d: exit %d\n%s%s", i, r.ExitCode, r.Stdout, r.Stderr)
			}
			// Before the load, Polish falls back to the source text and
			// the source locale's numbers and lists; after it, Polish
			// throughout, pl-PL included.
			want := []string{"[en]", "3 items", "1,234,567", "a, b, and c", "pl loaded", "3 rzeczy", "1\u00a0234\u00a0567", "[en, pl]"}
			if !slices.Equal(lines[:8], want) {
				t.Errorf("run %d printed:\n%s\nwant it to start:\n%s", i, r.Stdout, strings.Join(want, "\n"))
			}
			if !strings.HasPrefix(lines[8], "the locale file of de could not be fetched: ") || !strings.Contains(lines[8], "de.nomil") {
				t.Errorf("run %d: a missing file: %q", i, lines[8])
			}
			if lines[9] != "the locale file of pt cannot be read: it was written by another build of the program" {
				t.Errorf("run %d: another build's file: %q", i, lines[9])
			}
		}
		// In process, every listed locale is in the program.
		want := "[en, pl, pt, de]\n3 rzeczy\n1\u00a0234\u00a0567\na, b i c\npl loaded\n3 rzeczy\n1\u00a0234\u00a0567\n[en, pl, pt, de]\nde loaded\npt-BR loaded\n"
		if run := runProcess(t, env, src, "", nomiBin, "run", entry); run.stdout != want {
			t.Errorf("nomi run printed:\n%s\nwant:\n%s", run.stdout, want)
		}
	})
}

// webImage is the image in folder's app.wasm.
func webImage(t *testing.T, folder string) ir.Image {
	t.Helper()
	module, err := os.ReadFile(filepath.Join(folder, "app.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := vmrunner.ReadWasmImage(bytes.NewReader(module), int64(len(module)))
	if err != nil {
		t.Fatal(err)
	}
	im, err := ir.DecodeImage(raw)
	if err != nil {
		t.Fatal(err)
	}
	return im
}

// `nomi run --web` serves the locale files beside app.wasm, and a static
// file may not be one.
func TestRunWeb_LocaleFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a js/wasm runner; -short")
	}
	tmp := t.TempDir()
	env := append(buildEnv(t.TempDir()), "TMPDIR="+tmp)
	src := t.TempDir()
	for name, text := range localesProject {
		mustWrite(t, filepath.Join(src, filepath.FromSlash(name)), text)
	}
	s := startWebServer(t, env, filepath.Join(src, "main.nomi"))
	if state := s.next(); state != (webState{Build: 1}) {
		t.Fatalf("first state %+v, want build 1 and no error", state)
	}
	body, typ := s.get("locales/pl.nomil")
	if typ != "application/json; charset=utf-8" || !bytes.HasPrefix(body, localefiles.Magic) {
		t.Fatalf("/locales/pl.nomil (%s): %.80s", typ, body)
	}
	if code := s.status("locales/en.nomil"); code != 404 {
		t.Fatalf("/locales/en.nomil, the embedded source locale's: HTTP %d", code)
	}
	s.stop()

	mustWrite(t, filepath.Join(src, "web", "locales", "pl.nomil"), "{}")
	got := runProcess(t, env, src, "", nomiBin, "build", "--web", filepath.Join(src, "main.nomi"), "-o", filepath.Join(t.TempDir(), "w"))
	if want := "web/locales/pl.nomil: the build writes the locale files in locales/, so a static file cannot be one"; got.exit == 0 || !strings.Contains(got.stderr, want) {
		t.Fatalf("a static locale file: exit %d, want %q\n%s", got.exit, want, got.transcript())
	}
}
