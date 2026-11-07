package hoverdoc

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// phraseFunc is the function a `phrase` declaration expanded into that
// sym names, or nil, as its file's source has it when fa's program read
// the file as its interface (FileAnalysis.DeclarationSource), which keeps
// only the keyword of what the function expanded from.
func phraseFunc(sym *analysis.Symbol, fa *analysis.FileAnalysis) *ast.FuncDef {
	for s := sym; s != nil; s = s.Resolved {
		if fn, ok := s.Node.(*ast.FuncDef); ok && fn.Expansion != nil {
			if fa != nil {
				if src, ok := fa.DeclarationSource(fn, s.SourceFile).(*ast.FuncDef); ok && src.Expansion != nil {
					return src
				}
			}
			return fn
		}
		if s.Resolved == s {
			break
		}
	}
	return nil
}

// PhraseTexts is the section a phrase's hover ends with: its text in the
// source locale and in each locale `[i18n]` lists, as `Phrase.format`
// writes it there. A locale with no current translation of its own says
// which locale's text it shows, and why: no entry, or one translated from
// an older text. "" when sym names no phrase. fa, when not nil, is the
// analysis sym was found in.
func PhraseTexts(sym *analysis.Symbol, fa *analysis.FileAnalysis) string {
	info := analysis.PhraseOf(phraseFunc(sym, fa))
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
		case analysis.PhraseCurrent:
			b.WriteString("\n" + head + "\n")
			block(l.TextValue())
			continue
		case analysis.PhraseStale:
			head += fmt.Sprintf(": stale, translated from an older %s text", analysis.LanguageName(info.SourceLocale))
		case analysis.PhraseUnrecorded:
			head += fmt.Sprintf(": the entry does not record the %s text it was translated from", analysis.LanguageName(info.SourceLocale))
		case analysis.PhraseNoText:
			head += ": the entry has no `text`"
		case analysis.PhraseMissing:
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
