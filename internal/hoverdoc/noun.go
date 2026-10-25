package hoverdoc

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// nounOnce is the `once` a `noun` declaration expanded into that sym
// names, or nil.
func nounOnce(sym *analysis.Symbol) *ast.OnceBinding {
	for s := sym; s != nil; s = s.Resolved {
		if o, ok := s.Node.(*ast.OnceBinding); ok && o.Expansion != nil {
			return o
		}
		if s.Resolved == s {
			break
		}
	}
	return nil
}

// NounTexts is the section a noun's hover ends with: its form in the
// source locale, and in each locale `[i18n]` lists its gender and its
// forms by case. A locale with no current entry of its own says which
// locale's forms it shows, and why. "" when sym names no noun.
func NounTexts(sym *analysis.Symbol) string {
	info := analysis.NounOf(nounOnce(sym))
	if info == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("**Forms by locale**\n")
	block := func(text string) {
		text = strings.TrimSpace(text)
		fence := "```"
		for strings.Contains(text, fence) {
			fence += "`"
		}
		b.WriteString("\n" + fence + "\n" + text + "\n" + fence + "\n")
	}
	fmt.Fprintf(&b, "\n`%s` %s, the source: nominative\n", info.SourceLocale, analysis.LanguageName(info.SourceLocale))
	block(info.Source)
	for _, l := range info.Locales {
		head := fmt.Sprintf("`%s` %s", l.Locale, analysis.LanguageName(l.Locale))
		if l.Partial {
			head += " (partial)"
		}
		if l.State == analysis.MessageCurrent {
			if l.Gender != "" {
				head += ", " + l.Gender
			}
			b.WriteString("\n" + head + "\n")
			for _, f := range l.Forms {
				b.WriteString("\n" + f.Name + "\n")
				block(f.Value.Text)
			}
			continue
		}
		switch l.State {
		case analysis.MessageStale:
			head += fmt.Sprintf(": stale, translated from an older %s text", analysis.LanguageName(info.SourceLocale))
		case analysis.MessageUnrecorded:
			head += fmt.Sprintf(": the entry does not record the %s text it was translated from", analysis.LanguageName(info.SourceLocale))
		case analysis.MessageMissing:
			if l.Required {
				head += ": no translation"
			} else {
				head += ": no entry"
			}
		}
		b.WriteString("\n" + head + fmt.Sprintf("; shows the `%s` forms\n", l.Shows))
	}
	return strings.TrimRight(b.String(), "\n")
}
