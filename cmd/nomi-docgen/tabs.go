package main

// Tab groups in a generated guide (readGuide: docs/install.md, docs/tooling.md,
// docs/style.md). The source stays Markdown that reads in order on GitHub:
//
//	<!-- tabs: os -->
//
//	**macOS**
//
//	```console
//	$ curl -fsSL https://nomi-lang.org/install.sh | sh
//	```
//
//	**Windows**
//
//	...
//
//	<!-- end tabs -->
//
// A group opens with `<!-- tabs: <key> -->` and closes with `<!-- end tabs
// -->`, each a line of its own. Inside it each tab starts at a line that is
// one bold span and nothing else, `**Label**`, a paragraph of its own, and
// holds the lines up to the next such line or the group's end. A group has
// two tabs or more, each with content and a label of its own, and no heading.
// The key names what the tabs choose between: on the site a reader's choice
// is kept per key, and every group with that key follows it
// (tour-client.mjs, "Tab groups"). The labels of an `os` group are `macOS`,
// `Linux` and `Windows`, which the page picks from the reader's browser.
//
// expandTabs writes each group as the HTML the site styles and the client
// turns into tabs: a div per group and per tab, the tab's label first in
// its div, with blank lines between, so the Markdown inside still renders.
// Without the client every tab shows, each under its label.

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	tabsOpen  = regexp.MustCompile(`^<!-- tabs: ([a-z][a-z0-9-]*) -->$`)
	tabsLabel = regexp.MustCompile(`^\*\*([^*]+)\*\*$`)
)

const tabsClose = "<!-- end tabs -->"

// expandTabs answers lines with each tab group in them written as HTML, or
// an error naming the line of path where a group is malformed.
func expandTabs(path string, lines []specLine) ([]specLine, error) {
	var out []specLine
	// blank ends what came before with one blank line.
	blank := func() {
		if n := len(out); n > 0 && (out[n-1].fence || out[n-1].text != "") {
			out = append(out, specLine{})
		}
	}
	// emit writes a line of HTML with a blank line on each side, so the
	// Markdown around it renders.
	emit := func(format string, args ...any) {
		blank()
		out = append(out, specLine{text: fmt.Sprintf(format, args...)}, specLine{})
	}
	inGroup := false
	var key string
	var opened int      // the open group's line
	var labels []string // its tabs so far
	var content bool    // whether its last tab holds a line yet
	var tabLine int     // the line of its last tab
	closeTab := func(at int) error {
		if len(labels) > 0 && !content {
			return fmt.Errorf("%s:%d: tab %q holds nothing before line %d", path, tabLine, labels[len(labels)-1], at)
		}
		if len(labels) > 0 {
			emit("</div>")
		}
		return nil
	}
	for i, l := range lines {
		n := i + 1
		text := strings.TrimSpace(l.text)
		if l.fence {
			if inGroup && len(labels) == 0 {
				return nil, fmt.Errorf("%s:%d: the tab group opened on line %d holds code before its first `**Label**` line", path, n, opened)
			}
			content = content || inGroup
			out = append(out, l)
			continue
		}
		switch {
		case strings.HasPrefix(text, "<!-- tabs") && !tabsOpen.MatchString(text):
			return nil, fmt.Errorf("%s:%d: %q does not open a tab group; write `<!-- tabs: <key> -->`, the key in lowercase letters, digits and dashes", path, n, text)
		case strings.HasPrefix(text, "<!-- end tabs") && text != tabsClose:
			return nil, fmt.Errorf("%s:%d: %q does not close a tab group; write `%s`", path, n, text, tabsClose)
		case tabsOpen.MatchString(text):
			if inGroup {
				return nil, fmt.Errorf("%s:%d: a tab group opens inside the one opened on line %d; close that one first with `%s`", path, n, opened, tabsClose)
			}
			inGroup, key, opened, labels, content = true, tabsOpen.FindStringSubmatch(text)[1], n, nil, false
			emit(`<div class="nomi-tabgroup" data-tabs="%s">`, key)
		case text == tabsClose:
			if !inGroup {
				return nil, fmt.Errorf("%s:%d: `%s` closes no tab group", path, n, tabsClose)
			}
			if err := closeTab(n); err != nil {
				return nil, err
			}
			if len(labels) < 2 {
				return nil, fmt.Errorf("%s:%d: the tab group opened here holds %d tab; a group holds two or more, each starting at a `**Label**` line", path, opened, len(labels))
			}
			emit("</div>")
			inGroup = false
		case !inGroup && text == "" && i > 0 && strings.TrimSpace(lines[i-1].text) == tabsClose:
			// The group's HTML ends with a blank line already.
		case !inGroup:
			out = append(out, l)
		case tabsLabel.MatchString(text):
			label := strings.TrimSpace(tabsLabel.FindStringSubmatch(text)[1])
			for _, had := range labels {
				if had == label {
					return nil, fmt.Errorf("%s:%d: the tab group opened on line %d has two tabs labeled %q", path, n, opened, label)
				}
			}
			if i > 0 && strings.TrimSpace(lines[i-1].text) != "" || i+1 < len(lines) && strings.TrimSpace(lines[i+1].text) != "" {
				return nil, fmt.Errorf("%s:%d: tab label %s must be a paragraph of its own, with a blank line before and after it", path, n, text)
			}
			if err := closeTab(n); err != nil {
				return nil, err
			}
			labels, content, tabLine = append(labels, label), false, n
			emit(`<div class="nomi-tabpanel" data-tab="%s">`, html.EscapeString(label))
			emit(`<p class="nomi-tablabel">%s</p>`, html.EscapeString(label))
		case specSubheading.MatchString(text):
			return nil, fmt.Errorf("%s:%d: heading %q is inside the tab group opened on line %d; a tab holds no heading", path, n, text, opened)
		case text == "":
			blank()
		case len(labels) == 0:
			return nil, fmt.Errorf("%s:%d: the tab group opened on line %d holds %q before its first `**Label**` line", path, n, opened, text)
		default:
			content = true
			out = append(out, l)
		}
	}
	if inGroup {
		return nil, fmt.Errorf("%s:%d: the tab group opened here (key %s) has no `%s`", path, opened, key, tabsClose)
	}
	return out, nil
}
