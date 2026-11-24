package editors

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nomi-language/nomi/editors/helix"
	grammar "github.com/nomi-language/nomi/tree-sitter-nomi"
)

var helixEditor = editor{
	name:      "helix",
	install:   installHelix,
	uninstall: uninstallHelix,
	status: func(version string) []string {
		cfg, err := host.helixConfig()
		if err != nil {
			return []string{err.Error()}
		}
		return []string{markerStatus(helixPaths(cfg).grammar, version, "helix"), cfg}
	},
}

// helixDirs are the places in Helix's configuration directory an install
// writes.
type helixDirs struct {
	cfg, languages string
	// queries holds a directory per language, nomi and nomi-catalog.
	queries string
	// grammar is the copy of the grammar's C that languages.toml names as
	// its source, and holds the marker.
	grammar string
	// grammars is where `hx --grammar build` writes the parser.
	grammars string
}

func helixPaths(cfg string) helixDirs {
	runtime := filepath.Join(cfg, "runtime")
	return helixDirs{
		cfg:       cfg,
		languages: filepath.Join(cfg, "languages.toml"),
		queries:   filepath.Join(runtime, "queries"),
		grammar:   filepath.Join(runtime, "grammars", "sources", "nomi"),
		grammars:  filepath.Join(runtime, "grammars"),
	}
}

// helixQueryRoot is where the queries are in helix.Files.
const helixQueryRoot = "runtime/queries"

func installHelix(p *plan) error {
	cfg, err := host.helixConfig()
	if err != nil {
		return err
	}
	d := helixPaths(cfg)
	current, err := readIfPresent(d.languages)
	if err != nil {
		return err
	}
	languages, err := withHelixBlock(current, d.grammar)
	if err != nil {
		return fmt.Errorf("%s: %w", d.languages, err)
	}
	for _, lang := range subdirs(helix.Files, helixQueryRoot) {
		p.removeIfPresent(filepath.Join(d.queries, lang))
	}
	p.removeIfPresent(d.grammar)
	if err := p.writeTree(helix.Files, helixQueryRoot, d.queries); err != nil {
		return err
	}
	if err := p.writeTree(grammar.Files, "src", filepath.Join(d.grammar, "src")); err != nil {
		return err
	}
	p.writeMarker(d.grammar)
	p.write(d.languages, []byte(languages))

	hx, err := exec.LookPath("hx")
	if err != nil {
		p.incomplete(errors.New("hx is not on PATH, so Nomi's grammar is not built; the rest is installed.\n" +
			"Install Helix, then run `nomi editor install helix` again."))
		return nil
	}
	p.runFunc("hx --grammar build, building only Nomi's grammar", func(out io.Writer) error {
		return buildHelixGrammar(out, hx, d)
	})
	p.run(nil, hx, "--health", "nomi")
	return nil
}

// buildHelixGrammar builds only Nomi's grammar: `hx --grammar build`
// against the real configuration would build every grammar Helix knows,
// and one unrelated failure fails the run. The temporary configuration is
// the user's languages.toml with `use-grammars` replaced, and its runtime
// links to the real one's grammars and queries, so the parser lands in the
// user's configuration. Windows makes links only with a privilege, and
// Helix there reads no XDG_CONFIG_HOME, so it builds every grammar.
func buildHelixGrammar(out io.Writer, hx string, d helixDirs) error {
	if host.goos == "windows" {
		return command(out, nil, hx, "--grammar", "build")
	}
	tmp, err := os.MkdirTemp("", "nomi-helix-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	runtime := filepath.Join(tmp, "helix", "runtime")
	if err := os.MkdirAll(runtime, 0o755); err != nil {
		return err
	}
	for _, link := range []struct{ to, at string }{{d.grammars, "grammars"}, {d.queries, "queries"}} {
		if err := os.Symlink(link.to, filepath.Join(runtime, link.at)); err != nil {
			return err
		}
	}
	languages, err := readIfPresent(d.languages)
	if err != nil {
		return err
	}
	languages = `use-grammars = { only = ["nomi"] }` + "\n\n" + useGrammars.ReplaceAllString(languages, "")
	if err := os.WriteFile(filepath.Join(tmp, "helix", "languages.toml"), []byte(languages), 0o644); err != nil {
		return err
	}
	return command(out, []string{"XDG_CONFIG_HOME=" + tmp}, hx, "--grammar", "build")
}

// useGrammars matches a languages.toml line that sets use-grammars.
var useGrammars = regexp.MustCompile(`(?m)^[ \t]*use-grammars[ \t]*=.*\n?`)

func uninstallHelix(p *plan) error {
	cfg, err := host.helixConfig()
	if err != nil {
		return err
	}
	d := helixPaths(cfg)
	p.nothing = "Helix has no Nomi support in " + cfg
	for _, lang := range subdirs(helix.Files, helixQueryRoot) {
		p.removeIfPresent(filepath.Join(d.queries, lang))
	}
	p.removeIfPresent(d.grammar)
	parser := "nomi.so"
	if host.goos == "windows" {
		parser = "nomi.dll"
	}
	p.removeIfPresent(filepath.Join(d.grammars, parser))
	current, err := readIfPresent(d.languages)
	if err != nil {
		return err
	}
	without, found, err := withoutHelixBlock(current)
	if err != nil {
		return fmt.Errorf("%s: %w", d.languages, err)
	}
	if found {
		p.write(d.languages, []byte(without))
	}
	return nil
}

// helixEntry is the embedded languages.toml with the grammar's source a
// path: the copy of the grammar an install writes.
func helixEntry(grammar string) (string, error) {
	data, err := fs.ReadFile(helix.Files, "languages.toml")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	replaced := false
	sc := bufio.NewScanner(strings.NewReader(strings.TrimSpace(string(data))))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "source = { ") {
			line = `source = { path = ` + tomlString(filepath.ToSlash(grammar)) + ` }`
			replaced = true
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if !replaced {
		return "", errors.New("editors/helix/languages.toml has no `source = { ` line for the grammar")
	}
	return b.String(), nil
}

// tomlString is s as a TOML basic string.
func tomlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func readIfPresent(file string) (string, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}
