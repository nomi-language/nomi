package hoverdoc

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/std"
)

// stdModuleDoc is what a module name's hover says about std module name:
// the first paragraph of its `//#` comments, the paragraph its reference
// page opens with, and a link to that page. "" for a module std does not
// hold, and for an internal one, which has no page.
func stdModuleDoc(name string) string {
	if _, internal := analysis.InternalStdModule(name); internal {
		return ""
	}
	src, ok := std.ReadFile(name)
	if !ok {
		return ""
	}
	var para []string
	for _, line := range strings.Split(string(src), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "//#")
		if !ok {
			if len(para) > 0 {
				break
			}
			continue
		}
		text := strings.TrimPrefix(rest, " ")
		if strings.TrimSpace(text) == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, "```") {
			if len(para) > 0 {
				break
			}
			continue
		}
		para = append(para, text)
	}
	link := "More in the [reference](https://nomi-lang.org/reference/" + name + "/)"
	if len(para) == 0 {
		return link
	}
	return strings.Join(para, "\n") + "\n\n" + link
}

// stdModuleName is the std module an import or implicit module reference
// names, as `std::io` names `io` and `std::http::testing` names
// `http/testing`; ok is false for a project module.
func stdModuleName(sym *analysis.Symbol, path string) (string, bool) {
	if path == "" {
		if _, ok := std.ReadFile(sym.Name); ok {
			return sym.Name, true
		}
		return "", false
	}
	name, ok := strings.CutPrefix(path, "std::")
	if !ok || strings.Contains(name, ".") {
		return "", false
	}
	name = strings.ReplaceAll(name, "::", "/")
	if strings.Contains(name, ":") {
		return "", false
	}
	return name, true
}
