package hoverdoc

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// messageFunc is the function a `message` declaration expanded into that
// sym names, or nil.
func messageFunc(sym *analysis.Symbol) *ast.FuncDef {
	for s := sym; s != nil; s = s.Resolved {
		if fn, ok := s.Node.(*ast.FuncDef); ok && fn.Expansion != nil {
			return fn
		}
		if s.Resolved == s {
			break
		}
	}
	return nil
}

// MessageTexts is the section a message's hover ends with: its text in the
// source locale and in each locale `[i18n]` lists, as `Message.format`
// writes it there. A locale with no current translation of its own says
// which locale's text it shows, and why: no entry, or one translated from
// an older text. "" when sym names no message.
func MessageTexts(sym *analysis.Symbol) string {
	info := analysis.MessageOf(messageFunc(sym))
	if info == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("**Text by locale**\n")
	block := func(text string) {
		text = strings.TrimSpace(text)
		fence := "```"
		for strings.Contains(text, fence) {
			fence += "`"
		}
		b.WriteString("\n" + fence + "\n" + text + "\n" + fence + "\n")
	}
	fmt.Fprintf(&b, "\n`%s` %s, the source\n", info.SourceLocale, analysis.LanguageName(info.SourceLocale))
	block(info.Source)
	for _, l := range info.Locales {
		head := fmt.Sprintf("`%s` %s", l.Locale, analysis.LanguageName(l.Locale))
		if l.Partial {
			head += " (partial)"
		}
		shows := fmt.Sprintf("shows the `%s` text", l.Shows)
		switch l.State {
		case analysis.MessageCurrent:
			b.WriteString("\n" + head + "\n")
			block(l.TextValue())
			continue
		case analysis.MessageStale:
			head += fmt.Sprintf(": stale, translated from an older %s text", analysis.LanguageName(info.SourceLocale))
		case analysis.MessageUnrecorded:
			head += fmt.Sprintf(": the entry does not record the %s text it was translated from", analysis.LanguageName(info.SourceLocale))
		case analysis.MessageNoText:
			head += ": the entry has no `text`"
		case analysis.MessageMissing:
			if l.Required {
				head += ": no translation"
			} else {
				head += ": no entry"
			}
		}
		b.WriteString("\n" + head + "; " + shows + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
