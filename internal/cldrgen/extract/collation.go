package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/cldrgen"
)

// extractCollation writes data/collation from an unpacked CLDR release
// (the source archive, https://github.com/unicode-org/cldr/archive/refs/tags/release-48-2.zip):
// common/uca/FractionalUCA_SHORT.txt copied unchanged, and rules.txt, the
// rules of every collation type cldrgen compiles from common/collation/*.xml
// gathered into one file, each locale's default type first.
func extractCollation(src, out string) error {
	frac, err := os.ReadFile(filepath.Join(src, "common", "uca", "FractionalUCA_SHORT.txt"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "FractionalUCA_SHORT.txt"), frac, 0o644); err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(src, "common", "collation", "*.xml"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	var b strings.Builder
	fmt.Fprintf(&b, "# CLDR %s common/collation/*.xml: the rules of every collation type cldrgen compiles (%s),\n", cldrgen.Release, strings.Join(cldrgen.CollationTypes, ", "))
	b.WriteString("# with each line's indentation removed. `@default <locale> <type>` is a locale's default\n")
	b.WriteString("# type; `@rules <locale> <type>` starts a type's rules. Written by ./extract.\n")
	for _, f := range files {
		var doc struct {
			Collations struct {
				Default    string `xml:"defaultCollation"`
				Collations []struct {
					Type  string `xml:"type,attr"`
					Alt   string `xml:"alt,attr"`
					Rules string `xml:"cr"`
				} `xml:"collation"`
			} `xml:"collations"`
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if err := xml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		locale := strings.TrimSuffix(filepath.Base(f), ".xml")
		if d := doc.Collations.Default; d != "" {
			fmt.Fprintf(&b, "@default %s %s\n", locale, d)
		}
		for _, c := range doc.Collations.Collations {
			if c.Alt != "" || !cldrgen.CompilesCollationType(c.Type) {
				continue
			}
			fmt.Fprintf(&b, "@rules %s %s\n", locale, c.Type)
			for _, line := range strings.Split(c.Rules, "\n") {
				if line = strings.TrimSpace(line); line != "" {
					if strings.HasPrefix(line, "@") {
						return fmt.Errorf("%s %s: a rule line starts with @", locale, c.Type)
					}
					b.WriteString(line + "\n")
				}
			}
		}
	}
	return os.WriteFile(filepath.Join(out, "rules.txt"), []byte(b.String()), 0o644)
}
