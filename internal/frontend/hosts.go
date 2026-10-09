package frontend

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// ValidateHosts checks that every `host fn` and `host type` a user file
// declares has something to answer it: Config.Provided, or, with
// SourceBoundProvided, a `go` binding the consumer compiles itself. A stdlib
// module's declarations are the language's own and are never reported.
//
// Keys are the ones a host registers under: an entry file's declaration by its
// bare name, a sibling file's as `<module>.<name>` with the module path's last
// segment. An impl block's `host fn` is not reported, because its key is one of
// several spellings and this walk cannot tell which one a host meant.
func (c *Checker) ValidateHosts(modules map[string][]ast.Node) error {
	var unmet []string
	for modulePath, nodes := range modules {
		if analysis.IsStdlibKey(modulePath) {
			continue
		}
		moduleName, fileLabel := "", "entry"
		if modulePath != "" {
			moduleName = path.Base(modulePath)
			fileLabel = modulePath + ".nomi"
		}
		for _, n := range nodes {
			var key string
			var line, col int
			switch d := n.(type) {
			case *ast.ExternFunc:
				if c.cfg.SourceBoundProvided && d.ForeignName != "" {
					continue
				}
				key, line, col = HostKey(moduleName, d.Name), d.Line, d.Col
			case *ast.ExternType:
				if c.cfg.SourceBoundProvided && d.ForeignName != "" {
					continue
				}
				if c.cfg.HostTypesAreHandles && d.ForeignName == "" {
					continue
				}
				key, line, col = HostKey(moduleName, d.Name), d.Line, d.Col
			default:
				continue
			}
			if c.cfg.Provided != nil && c.cfg.Provided(key) {
				continue
			}
			unmet = append(unmet, fmt.Sprintf("  - %s\t(%s:%d:%d)\n", key, fileLabel, line, col))
		}
	}
	if len(unmet) == 0 {
		return nil
	}
	sort.Strings(unmet)
	var sb strings.Builder
	plural := "s"
	if len(unmet) == 1 {
		plural = ""
	}
	fmt.Fprintf(&sb, "nomi: %d extern%s declared but not registered:\n", len(unmet), plural)
	for _, u := range unmet {
		sb.WriteString(u)
	}
	hint := c.cfg.UnmetHint
	if hint == "" {
		hint = "register them in a host table passed to vmhost.Load."
	}
	sb.WriteString(hint)
	return fmt.Errorf("%s", sb.String())
}

// HostKey is the key a user file's top-level `host` declaration registers
// under: the bare name in the entry (moduleName ""), `<module>.<name>` in a
// sibling.
func HostKey(moduleName, name string) string {
	if moduleName == "" {
		return name
	}
	return moduleName + "." + name
}
