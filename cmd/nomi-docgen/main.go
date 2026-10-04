// Command nomi-docgen generates the standard-library API reference from ///
// doc comments.
//
// For each stdlib module it writes <out>/<module>.md: every pub declaration with
// its signature + doc, rendered by the SAME renderer the LSP and the tour's
// hovers use (hoverdoc.Render), so the reference can't drift
// from the editor. Signatures use ```nomi fences (statically highlighted,
// not turned into runnable editors). Output is generated, not committed.
//
//	go run ./cmd/nomi-docgen <reference-output-dir>
package main

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	nomiformat "github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/std"
)

// There is no hand-maintained exclusion list. A module is excluded exactly
// when it renders no public declarations (see main's `page == ""` arm), which
// is a property of the module rather than a name somebody remembered to add.
//
// What stood here was `skip = {"prelude": true, "structs": true}`, described
// as "internal modules that aren't part of the importable public surface".
// That criterion was redundant for `prelude` and wrong for `structs`.
// `prelude.nomi` is nothing but re-export imports — zero `pub` declarations of
// its own — so it renders empty and the `page == ""` arm drops it either way.
// `structs.nomi` declares `pub interface Struct` with the documented
// `host fn update`, which the tour's own prose reaches seven times as
// `Struct.update(...)`; it had no page, so none of those had a target.
// Being preluded is why nobody writes `import std/structs` — it is not a
// reason for the declaration to be undocumented.

// The declaration kinds that make up a module's public API.
var declKind = map[analysis.SymbolKind]bool{
	analysis.SymbolFunction:  true,
	analysis.SymbolStruct:    true,
	analysis.SymbolEnum:      true,
	analysis.SymbolType:      true,
	analysis.SymbolTypeAlias: true,
	analysis.SymbolInterface: true,
	analysis.SymbolOnce:      true,
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: nomi-docgen <reference-output-dir>")
		os.Exit(1)
	}
	outDir := os.Args[1]
	must(os.MkdirAll(outDir, 0o755))

	lib := std.Load()
	names := make([]string, 0, len(lib.Modules))
	for name := range lib.Modules {
		names = append(names, name)
	}
	sort.Strings(names)

	links := referenceLinksForModules(lib, names)
	var written []string
	for _, name := range names {
		page := renderModuleWithLinks(lib, name, links)
		if page == "" {
			continue // no public declarations of its own — `prelude`
		}
		outPath := filepath.Join(outDir, name+".md")
		must(os.MkdirAll(filepath.Dir(outPath), 0o755))
		must(os.WriteFile(outPath, []byte(page), 0o644))
		written = append(written, name)
	}
	must(os.WriteFile(filepath.Join(outDir, "index.md"), []byte(renderIndex(written)), 0o644))
	fmt.Printf("nomi-docgen: wrote %d module pages to %s\n", len(written), outDir)
}

func renderModuleWithLinks(lib *std.StdLib, name string, globalLinks referenceLinks) string {
	fa := lib.Files[name]
	if fa == nil || fa.ModuleScope == nil {
		return ""
	}
	entries := sortedAPIEntries(fa, lib.Nodes[name])
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: std/%s\n---\n\n", name)
	if intro := moduleDoc(name); intro != "" {
		b.WriteString(intro)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Import with `import std/%s`.\n\n", name)
	if summary := renderExportsSummary(name, entries); summary != "" {
		b.WriteString(summary)
	}
	implLinks := samePageInterfaceMethodLinks(name, entries)
	for ref, href := range globalLinks {
		if _, ok := implLinks[ref]; !ok {
			implLinks[ref] = href
		}
	}
	shared := sharedImplHeadings(name, entries)
	for _, group := range apiGroups(entries) {
		fmt.Fprintf(&b, "## %s\n\n", renderGroupHeading(name, group.apiGroupKey))
		for _, entry := range group.Entries {
			if !entry.RendersAsGroupHeading() {
				renderEntryHeading(&b, name, entry, shared)
			}
			renderAPIEntry(&b, name, fa, entry, implLinks)
		}
	}
	return b.String()
}

func sortedAPIEntries(fa *analysis.FileAnalysis, nodes []ast.Node) []apiEntry {
	if fa == nil || fa.ModuleScope == nil {
		return nil
	}
	entries := publicAPISymbols(fa, nodes)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Sym.Pos.Line != entries[j].Sym.Pos.Line {
			return entries[i].Sym.Pos.Line < entries[j].Sym.Pos.Line
		}
		return entries[i].Sym.Pos.Col < entries[j].Sym.Pos.Col
	})
	return entries
}

type referenceLinks map[string]string

type apiEntry struct {
	Sym   *analysis.Symbol
	Group apiGroupKey
	// Impl is the `impl Iface for Type` block declaring Sym, nil for
	// anything else. Its header tells apart an owner's several impls of
	// one interface method (Instant's two `subtract`s).
	Impl *ast.ImplBlock
}

type apiGroupKey struct {
	Key   string
	Kind  string
	Name  string
	Label string
}

type apiGroup struct {
	apiGroupKey
	Entries []apiEntry
}

func publicAPISymbols(fa *analysis.FileAnalysis, nodes []ast.Node) []apiEntry {
	seen := make(map[*analysis.Symbol]int)
	var entries []apiEntry
	addEntry := func(s *analysis.Symbol, group apiGroupKey) {
		if idx, ok := seen[s]; ok {
			if entries[idx].Group.Key == "" && group.Key != "" {
				entries[idx].Group = group
			}
			return
		}
		seen[s] = len(entries)
		entries = append(entries, apiEntry{Sym: s, Group: group})
	}
	add := func(s *analysis.Symbol, allowTypeOwned bool, group apiGroupKey) {
		if !isReferenceSymbol(s, allowTypeOwned) {
			return
		}
		addEntry(s, group)
	}
	addInterfaceMember := func(s *analysis.Symbol, group apiGroupKey) {
		if !isReferenceInterfaceMethod(s) {
			return
		}
		addEntry(s, group)
	}

	publicOwners := make(map[string]apiGroupKey)
	for _, s := range fa.ModuleScope.Symbols {
		if s.Public && s.Resolved == nil && canOwnPublicMethods(s.Kind) {
			group := rootGroupFor(s)
			publicOwners[s.Name] = group
			if s.Kind == analysis.SymbolInterface {
				for _, member := range s.Members {
					addInterfaceMember(member, group)
				}
			}
		}
		add(s, false, rootGroupFor(s))
	}

	for _, owner := range publicNamespaces(fa.ModuleScope) {
		if owner.ModuleScope == nil {
			continue
		}
		for _, s := range owner.ModuleScope.Symbols {
			add(s, false, moduleGroup(owner.Name))
		}
	}

	for owner, byName := range fa.TypeMethods {
		ownerGroup, ok := publicOwners[owner]
		if !ok {
			continue
		}
		for _, s := range byName {
			if s.Public || s.IsImplMethod {
				add(s, true, ownerGroup)
			}
		}
	}

	// TypeMethods holds one symbol per (owner, method name), so an owner
	// with several impls of one interface method (Instant's
	// `Subtract<Duration, Instant>` and `Subtract<Instant, Duration>`,
	// Date's four `Add`s) lists only one of them there. The impl blocks
	// are the complete list.
	implOf := make(map[*analysis.Symbol]*ast.ImplBlock)
	for _, node := range nodes {
		block, ok := node.(*ast.ImplBlock)
		if !ok {
			continue
		}
		ownerGroup, ok := publicOwners[analysis.TypeExprBaseName(block.Receiver)]
		if !ok {
			continue
		}
		for _, item := range block.Items {
			s := fa.Definitions[implItemPos(item)]
			if s == nil || !(s.Public || s.IsImplMethod) {
				continue
			}
			if block.Interface != nil {
				implOf[s] = block
			}
			add(s, true, ownerGroup)
		}
	}
	for i := range entries {
		entries[i].Impl = implOf[entries[i].Sym]
	}

	return entries
}

func implItemPos(item ast.Node) analysis.Pos {
	switch it := item.(type) {
	case *ast.FuncDef:
		return analysis.Pos{Line: it.Line, Col: it.Col}
	case *ast.ExternFunc:
		return analysis.Pos{Line: it.Line, Col: it.Col}
	case *ast.OnceBinding:
		return analysis.Pos{Line: it.Line, Col: it.Col}
	}
	return analysis.Pos{}
}

// implHeader is the header of the impl block an entry was declared in,
// `impl Subtract<Instant, Duration> for Instant`, or "" when it was not
// declared in an interface impl.
func implHeader(entry apiEntry) string {
	if entry.Impl == nil || entry.Impl.Interface == nil || entry.Impl.Receiver == nil {
		return ""
	}
	return "impl " + entry.Impl.Interface.TypeString() + " for " + entry.Impl.Receiver.TypeString()
}

// sharedImplHeadings returns the heading labels that more than one entry on
// a page would carry, such as `Instant.subtract` for Instant's two Subtract
// impls, or `Decimal.divide` for the inherent function and the Divide impl.
// An impl entry under such a heading adds the implemented interface so each
// one says which impl it documents.
func sharedImplHeadings(moduleName string, entries []apiEntry) map[string]bool {
	count := make(map[string]int)
	for _, entry := range entries {
		if entry.RendersAsGroupHeading() {
			continue
		}
		label := implHeadingLabel(moduleName, entry)
		if label == "" {
			if owner := entryHeadingOwner(moduleName, entry); owner != "" {
				label = owner + "." + entry.Sym.Name
			} else {
				label = entry.Sym.Name
			}
		}
		count[label]++
	}
	shared := make(map[string]bool)
	for label, n := range count {
		if n > 1 {
			shared[label] = true
		}
	}
	return shared
}

func renderEntryHeading(b *strings.Builder, moduleName string, entry apiEntry, shared map[string]bool) {
	if heading := renderImplHeading(moduleName, entry); heading != "" {
		header := implHeader(entry)
		if shared[implHeadingLabel(moduleName, entry)] && header != "" {
			heading += fmt.Sprintf(
				" <span class=\"nomi-ref-detail\">%s</span>",
				html.EscapeString(entry.Impl.Interface.TypeString()),
			)
		}
		fmt.Fprintf(b, "### %s\n\n", heading)
		if header != "" {
			fmt.Fprintf(b, "<p class=\"nomi-ref-context\">%s</p>\n\n", html.EscapeString(header))
		}
		return
	}
	title := html.EscapeString(entry.Sym.Name)
	if owner := entryHeadingOwner(moduleName, entry); owner != "" {
		fmt.Fprintf(
			b,
			"### <span class=\"nomi-ref-owner\">%s.</span><span class=\"nomi-ref-name\">%s</span>\n\n",
			html.EscapeString(owner),
			title,
		)
	} else {
		fmt.Fprintf(b, "### %s\n\n", title)
	}
	if entry.Sym.ImplInterface != "" {
		fmt.Fprintf(
			b,
			"<p class=\"nomi-ref-context\">impl %s</p>\n\n",
			html.EscapeString(entry.Sym.ImplInterface),
		)
	}
}

func renderImplHeading(moduleName string, entry apiEntry) string {
	if entry.Sym == nil || !entry.Sym.IsImplMethod || entry.Sym.ImplInterface == "" {
		return ""
	}
	owner := implOwnerName(entry)
	callOwner := owner
	if callOwner == "" {
		callOwner = entry.Sym.ImplInterface
	}
	if owner == "" {
		return fmt.Sprintf(
			"<span class=\"nomi-ref-owner\">%s.</span><span class=\"nomi-ref-name\">%s</span>",
			html.EscapeString(callOwner),
			html.EscapeString(entry.Sym.Name),
		)
	}
	if owner == entry.Group.Name {
		return fmt.Sprintf(
			"<span class=\"nomi-ref-owner\">%s.</span><span class=\"nomi-ref-name\">%s</span>",
			html.EscapeString(callOwner),
			html.EscapeString(entry.Sym.Name),
		)
	}
	return fmt.Sprintf(
		"<span class=\"nomi-ref-owner\">%s.</span><span class=\"nomi-ref-name\">%s</span> <span class=\"nomi-ref-detail\">for %s</span>",
		html.EscapeString(callOwner),
		html.EscapeString(entry.Sym.Name),
		html.EscapeString(owner),
	)
}

func implHeadingLabel(moduleName string, entry apiEntry) string {
	if entry.Sym == nil || !entry.Sym.IsImplMethod || entry.Sym.ImplInterface == "" {
		return ""
	}
	owner := implOwnerName(entry)
	callOwner := owner
	if callOwner == "" {
		callOwner = entry.Sym.ImplInterface
	}
	if owner == "" {
		return callOwner + "." + entry.Sym.Name
	}
	if owner == entry.Group.Name {
		return callOwner + "." + entry.Sym.Name
	}
	return callOwner + "." + entry.Sym.Name + " for " + owner
}

func implOwnerName(entry apiEntry) string {
	owner := entry.Sym.OwningType
	if owner == "" {
		owner = entry.Group.Name
	}
	return owner
}

func moduleDisplayName(moduleName string) string {
	if moduleName == "" {
		return ""
	}
	if i := strings.LastIndex(moduleName, "/"); i >= 0 {
		return moduleName[i+1:]
	}
	return moduleName
}

func entryHeadingOwner(moduleName string, entry apiEntry) string {
	if entry.Sym == nil || entry.Sym.Name == entry.Group.Name {
		return ""
	}
	if shouldQualifyHeading(entry) {
		return entry.Group.Name
	}
	return ""
}

func shouldQualifyHeading(entry apiEntry) bool {
	if entry.Sym == nil || entry.Sym.Name == entry.Group.Name {
		return false
	}
	if entry.Sym.OwningType == "" && entry.Sym.OwningInterface == "" {
		return false
	}
	switch entry.Group.Kind {
	case "struct", "enum", "type", "interface":
		return entry.Group.Name != ""
	default:
		return false
	}
}

// renderVariantDocs emits the per-variant doc comments of an enum.
//
// The signature block above already lists the variant *names*; without
// this, that was all a reader got. For `Maybe` that is fine — `Some` and
// `None` explain themselves — but for the enums where the whole meaning
// lives in the distinction between variants, the reference page named
// `Temporary` / `Transient` / `Permanent` and said what none of them
// did. Those paragraphs existed all along, in the source and on hover,
// and simply never reached the page.
//
// Rendered as an indented list so a multi-paragraph variant doc keeps
// its paragraphs, and skipped entirely for opaque enums, whose variants
// are not part of the public surface.
func renderVariantDocs(b *strings.Builder, node ast.Node) {
	def, ok := node.(*ast.EnumDef)
	if !ok || def.Opaque {
		return
	}
	documented := false
	for _, v := range def.Variants {
		if strings.TrimSpace(v.Doc) != "" {
			documented = true
			break
		}
	}
	if !documented {
		return
	}
	b.WriteString("<p class=\"nomi-ref-context\">Variants</p>\n\n")
	for _, v := range def.Variants {
		doc := strings.TrimSpace(v.Doc)
		if doc == "" {
			continue
		}
		fmt.Fprintf(b, "- **`%s`**\n\n", v.Name)
		for _, line := range strings.Split(doc, "\n") {
			if strings.TrimSpace(line) == "" {
				b.WriteString("\n")
				continue
			}
			fmt.Fprintf(b, "  %s\n", line)
		}
		b.WriteString("\n")
	}
}

func renderAPIEntry(b *strings.Builder, moduleName string, fa *analysis.FileAnalysis, entry apiEntry, implLinks referenceLinks) {
	s := entry.Sym
	body := hoverdoc.Render(s)
	if body == "" {
		return
	}
	body = linkImplReference(body, s, implLinks)
	// Reference code is highlighted but not runnable (it's signatures, not
	// programs) — plain nomi fences, which tour-client.mjs highlights statically.
	fmt.Fprintf(b, "%s\n\n", body)
	renderVariantDocs(b, s.Node)
	tests := attachedTestsOf(s.Node)
	testContext := referenceTestContext(moduleName, fa, entry)
	wroteTestHeading := false
	for _, test := range tests {
		if body := nomiformat.RenderAttachedTestBody(test); body != "" {
			if !wroteTestHeading {
				b.WriteString("<p class=\"nomi-ref-test-label\">Interactive Tests</p>\n\n")
				wroteTestHeading = true
			}
			displayBody, bodyContext := splitReferenceTestBodyContext(body)
			context := joinReferenceTestContext(testContext, bodyContext)
			contextAttr := ""
			if context != "" {
				contextAttr = fmt.Sprintf(
					" data-nomi-stdlib-context=\"%s\"",
					html.EscapeString(context),
				)
			}
			fmt.Fprintf(
				b,
				"<pre data-nomi-stdlib-module=%q%s><code class=\"language-nomi-test\">%s</code></pre>\n\n",
				moduleName,
				contextAttr,
				html.EscapeString(displayBody),
			)
		}
	}
}

func splitReferenceTestBodyContext(body string) (string, string) {
	lines := strings.Split(body, "\n")
	var context []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			i++
			continue
		}
		if !strings.HasPrefix(trimmed, "import ") {
			break
		}
		context = append(context, trimmed)
		i++
	}
	display := strings.TrimLeft(strings.Join(lines[i:], "\n"), "\n")
	return display, strings.Join(context, "\n")
}

func joinReferenceTestContext(parts ...string) string {
	var out []string
	seen := map[string]bool{}
	for _, part := range parts {
		for _, line := range strings.Split(part, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || seen[line] {
				continue
			}
			seen[line] = true
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func referenceTestContext(moduleName string, fa *analysis.FileAnalysis, entry apiEntry) string {
	if fa == nil || fa.ModuleScope == nil || entry.Group.Kind != "module" {
		return ""
	}
	var imports []string
	for _, ns := range publicNamespaces(fa.ModuleScope) {
		if ns.ModuleScope == nil {
			continue
		}
		selectors := []string{"self"}
		if ns.Name == entry.Group.Name {
			selectors = append(selectors, referenceTypeSelectors(ns.ModuleScope)...)
		}
		if len(selectors) == 1 {
			imports = append(imports, fmt.Sprintf("import std/%s.%s", moduleName, ns.Name))
		} else {
			imports = append(
				imports,
				fmt.Sprintf("import std/%s.%s.{%s}", moduleName, ns.Name, strings.Join(selectors, ", ")),
			)
		}
	}
	return strings.Join(imports, "\n")
}

func referenceTypeSelectors(scope *analysis.Scope) []string {
	var selectors []string
	for _, s := range scope.Symbols {
		if s == nil || !s.Public || s.Resolved != nil {
			continue
		}
		switch s.Kind {
		case analysis.SymbolStruct, analysis.SymbolEnum, analysis.SymbolType, analysis.SymbolTypeAlias, analysis.SymbolInterface:
			selectors = append(selectors, s.Name)
		}
	}
	sort.Strings(selectors)
	return selectors
}

func referenceLinksForModules(lib *std.StdLib, names []string) referenceLinks {
	links := make(referenceLinks)
	for _, moduleName := range names {
		entries := sortedAPIEntries(lib.Files[moduleName], lib.Nodes[moduleName])
		for _, entry := range entries {
			if entry.Sym == nil || entry.Sym.Kind != analysis.SymbolInterfaceMethod || entry.Sym.OwningInterface == "" {
				continue
			}
			ref := entry.Sym.OwningInterface + "." + entry.Sym.Name
			links[ref] = "/reference/" + moduleName + "/#" + entryAnchor(moduleName, entry)
		}
	}
	return links
}

func samePageInterfaceMethodLinks(moduleName string, entries []apiEntry) referenceLinks {
	links := make(referenceLinks)
	for _, entry := range entries {
		if entry.Sym == nil || entry.Sym.Kind != analysis.SymbolInterfaceMethod || entry.Sym.OwningInterface == "" {
			continue
		}
		links[entry.Sym.OwningInterface+"."+entry.Sym.Name] = "#" + entryAnchor(moduleName, entry)
	}
	return links
}

func linkImplReference(body string, s *analysis.Symbol, implLinks referenceLinks) string {
	if s == nil || s.ImplInterface == "" {
		return body
	}
	ref := s.ImplInterface + "." + s.Name
	href, ok := implLinks[ref]
	if !ok {
		return body
	}
	plain := "*impl* `" + ref + "`"
	linked := "*impl* [`" + ref + "`](" + href + ")"
	return strings.ReplaceAll(body, plain, linked)
}

func rootGroupFor(s *analysis.Symbol) apiGroupKey {
	if s == nil {
		return apiGroupKey{}
	}
	switch s.Kind {
	case analysis.SymbolStruct:
		return namedGroup("struct", s.Name, "struct "+s.Name)
	case analysis.SymbolEnum:
		return namedGroup("enum", s.Name, "enum "+s.Name)
	case analysis.SymbolType, analysis.SymbolTypeAlias:
		return namedGroup("type", s.Name, "type "+s.Name)
	case analysis.SymbolInterface:
		return namedGroup("interface", s.Name, "interface "+s.Name)
	case analysis.SymbolOnce:
		return namedGroup("value", s.Name, "value "+s.Name)
	case analysis.SymbolFunction:
		return namedGroup("function", s.Name, "function "+s.Name)
	default:
		return namedGroup("api", s.Name, s.Name)
	}
}

func moduleGroup(name string) apiGroupKey {
	return namedGroup("module", name, "module "+name)
}

func namedGroup(kind, name, label string) apiGroupKey {
	return apiGroupKey{
		Key:   kind + ":" + name,
		Kind:  kind,
		Name:  name,
		Label: label,
	}
}

func groupHeadingLabel(moduleName string, group apiGroupKey) string {
	switch group.Kind {
	case "function", "value":
		if moduleName != "" && group.Name != "" {
			return moduleDisplayName(moduleName) + "." + group.Name
		}
	}
	return group.Label
}

func renderGroupHeading(moduleName string, group apiGroupKey) string {
	switch group.Kind {
	case "function", "value":
		if moduleName != "" && group.Name != "" {
			return fmt.Sprintf(
				"<span class=\"nomi-ref-owner\">%s.</span><span class=\"nomi-ref-name\">%s</span>",
				html.EscapeString(moduleDisplayName(moduleName)),
				html.EscapeString(group.Name),
			)
		}
	case "struct", "enum", "type", "interface", "module":
		kind, name, ok := strings.Cut(group.Label, " ")
		if ok {
			return fmt.Sprintf(
				"<span class=\"nomi-ref-detail\">%s </span><span class=\"nomi-ref-name\">%s</span>",
				html.EscapeString(kind),
				html.EscapeString(name),
			)
		}
	}
	return html.EscapeString(group.Label)
}

func apiGroups(entries []apiEntry) []apiGroup {
	byKey := make(map[string]int)
	var groups []apiGroup
	for _, entry := range entries {
		key := entry.Group
		if key.Key == "" {
			key = rootGroupFor(entry.Sym)
		}
		idx, ok := byKey[key.Key]
		if !ok {
			idx = len(groups)
			byKey[key.Key] = idx
			groups = append(groups, apiGroup{apiGroupKey: key})
		}
		groups[idx].Entries = append(groups[idx].Entries, entry)
	}
	return groups
}

func (e apiEntry) RendersAsGroupHeading() bool {
	if e.Sym == nil {
		return false
	}
	switch e.Group.Kind {
	case "struct", "enum", "type", "interface", "value", "function":
		return e.Sym.Name == e.Group.Name
	default:
		return false
	}
}

func renderExportsSummary(moduleName string, entries []apiEntry) string {
	var modules, types, interfaces, values []exportLink
	seen := map[string]bool{}
	for _, entry := range entries {
		switch entry.Group.Kind {
		case "module":
			addExport(&modules, seen, "module:"+entry.Group.Name, entry.Group.Name, headingSlug(entry.Group.Label))
		}
		switch entry.Sym.Kind {
		case analysis.SymbolStruct, analysis.SymbolEnum, analysis.SymbolType, analysis.SymbolTypeAlias:
			addExport(&types, seen, "type:"+entry.Sym.Name, entry.Sym.Name, entryAnchor(moduleName, entry))
		case analysis.SymbolInterface:
			addExport(&interfaces, seen, "interface:"+entry.Sym.Name, entry.Sym.Name, entryAnchor(moduleName, entry))
		case analysis.SymbolOnce:
			addExport(&values, seen, "value:"+entry.Sym.Name, entry.Sym.Name, entryAnchor(moduleName, entry))
		}
	}
	if len(modules)+len(types)+len(interfaces)+len(values) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Exports\n\n")
	b.WriteString("<div class=\"nomi-ref-exports\">\n")
	renderExportList(&b, "Modules", modules)
	renderExportList(&b, "Types", types)
	renderExportList(&b, "Interfaces", interfaces)
	renderExportList(&b, "Values", values)
	b.WriteString("</div>\n\n")
	return b.String()
}

type exportLink struct {
	Label  string
	Anchor string
}

func addExport(out *[]exportLink, seen map[string]bool, key, label, anchor string) {
	if seen[key] {
		return
	}
	seen[key] = true
	*out = append(*out, exportLink{Label: label, Anchor: anchor})
}

func renderExportList(b *strings.Builder, title string, links []exportLink) {
	if len(links) == 0 {
		return
	}
	fmt.Fprintf(b, "<section><p>%s</p><ul>", html.EscapeString(title))
	for _, link := range links {
		fmt.Fprintf(
			b,
			"<li><a href=\"#%s\"><code>%s</code></a></li>",
			html.EscapeString(link.Anchor),
			html.EscapeString(link.Label),
		)
	}
	b.WriteString("</ul></section>\n")
}

func entryAnchor(moduleName string, entry apiEntry) string {
	if label := implHeadingLabel(moduleName, entry); label != "" {
		return headingSlug(label)
	}
	if entry.RendersAsGroupHeading() {
		return headingSlug(groupHeadingLabel(moduleName, entry.Group))
	}
	if owner := entryHeadingOwner(moduleName, entry); owner != "" {
		return headingSlug(owner + "." + entry.Sym.Name)
	}
	return headingSlug(entry.Sym.Name)
}

func headingSlug(label string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '\t' || r == '\n' || r == '-':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func isReferenceSymbol(s *analysis.Symbol, allowTypeOwned bool) bool {
	return s != nil &&
		s.Public &&
		s.Resolved == nil &&
		declKind[s.Kind] &&
		s.Node != nil &&
		(allowTypeOwned || s.OwningType == "")
}

func isReferenceInterfaceMethod(s *analysis.Symbol) bool {
	return s != nil &&
		s.Kind == analysis.SymbolInterfaceMethod &&
		s.Resolved == nil &&
		s.Node != nil &&
		s.OwningInterface != ""
}

func canOwnPublicMethods(kind analysis.SymbolKind) bool {
	switch kind {
	case analysis.SymbolStruct, analysis.SymbolEnum, analysis.SymbolType, analysis.SymbolInterface:
		return true
	default:
		return false
	}
}

func publicNamespaces(scope *analysis.Scope) []*analysis.Symbol {
	var out []*analysis.Symbol
	for _, s := range scope.Symbols {
		if s == nil || !s.Public || s.Resolved != nil || s.ModuleScope == nil {
			continue
		}
		switch s.Kind {
		case analysis.SymbolModule:
			out = append(out, s)
		}
	}
	return out
}

func attachedTestsOf(n ast.Node) []ast.AttachedTest { return ast.AttachedTestsOf(n) }

// moduleDoc returns a module's page intro from its `//#` comments (file-level
// docs). These are plain `//` comments to the compiler — dropped from
// declaration docs, so they don't pollute the first declaration — and surfaced
// here as the reference page's description.
func moduleDoc(name string) string {
	src, ok := std.ReadFile(name)
	if !ok {
		return ""
	}
	var lines []string
	for _, line := range strings.Split(string(src), "\n") {
		t := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(t, "//#"); ok {
			lines = append(lines, strings.TrimPrefix(rest, " "))
		}
	}
	return strings.Join(lines, "\n")
}

func renderIndex(names []string) string {
	var b strings.Builder
	b.WriteString("---\ntitle: Standard Library\n---\n\n")
	b.WriteString("The Nomi standard library, generated from each module's `///` doc comments.\n\n")
	for _, n := range names {
		fmt.Fprintf(&b, "- [std/%s](/reference/%s/)\n", n, n)
	}
	return b.String()
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "nomi-docgen:", err)
		os.Exit(1)
	}
}
