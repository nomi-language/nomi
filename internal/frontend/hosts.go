package frontend

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// ValidateHosts checks that every `host fn` and `host type` a user file
// declares has something to answer it: Config.Provided. A stdlib
// module's declarations are the language's own and are never reported.
//
// Keys are the ones a host registers under (HostKey). An impl block's `host
// fn` is not reported: the builder lowers none in a program's own file.
func (c *Checker) ValidateHosts(modules map[string][]ast.Node) error {
	var unmet []string
	for modulePath, nodes := range modules {
		if analysis.IsStdlibKey(modulePath) {
			continue
		}
		fileLabel := "entry"
		if modulePath != "" {
			fileLabel = modulePath + ".nomi"
		}
		for _, n := range nodes {
			var key string
			var line, col int
			switch d := n.(type) {
			case *ast.ExternFunc:
				key, line, col = HostKey(modulePath, d.Name), d.Line, d.Col
			case *ast.ExternType:
				if c.cfg.HostTypesAreHandles {
					continue
				}
				key, line, col = HostKey(modulePath, d.Name), d.Line, d.Col
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
	fmt.Fprintf(&sb, "nomi: no host table answers %d `host` declaration%s:\n", len(unmet), plural)
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

// HostKey is the key a program's top-level `host` declaration registers
// under, given the module key of the file that declares it: the bare name
// in the entry (moduleKey ""), and otherwise the module key, a `.` and the
// name. The module key is the file's path within its module, `/`-separated
// and without `.nomi` (`helpers/util` for helpers/util.nomi, `util` for a
// file at the module's root), under the dependency's name in the program
// for a dependency's file (`lib/sub/thing`), so two files with one base
// name never share a key. The IR builder keys a crossing by the same
// function (irbuild's irHostBindingKey), from the checker's
// FileAnalysis.Origin.
func HostKey(moduleKey, name string) string {
	if moduleKey == "" {
		return name
	}
	return moduleKey + "." + name
}
