package stdformat

import (
	"strings"

	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/rt"
)

// listType is CLDR's list pattern type for a style and width, by their
// positions in std/strings' `ListStyle` and `ListWidth` (1-based tags).
func listType(style, width int64) string {
	base := map[int64]string{int64(rt.TagListAnd): "standard", int64(rt.TagListOr): "or", int64(rt.TagListUnit): "unit"}[style]
	if base == "" {
		base = "standard"
	}
	switch width {
	case int64(rt.TagListShort):
		return base + "-short"
	case int64(rt.TagListNarrow):
		return base + "-narrow"
	}
	return base
}

// FFIJoinList is `String.join_list`: items joined with locale's list
// pattern for style and width, composed from the end as CLDR describes
// (UTS #35 Part 6, "List Patterns"): the last two by the end pattern, each
// earlier item by the middle pattern, the first by the start pattern.
func FFIJoinList(fr *rt.Frame, locale string, items []string, style, width int64) string {
	return JoinList(stdlocale.Data(fr), locale, items, listType(style, width))
}

func listPatterns(d *cldrdata.Data, locale, typ string) (start, middle, end, two string) {
	v, _, _ := d.Resolve(stdlocale.Fallbacks(d, locale), "list\t"+typ)
	p := strings.Split(v, "\t")
	if len(p) != 4 {
		p = []string{"{0}, {1}", "{0}, {1}", "{0}, {1}", "{0}, {1}"}
	}
	return p[0], p[1], p[2], p[3]
}

// fill puts a and b in a pattern's {0} and {1}.
func fill(pattern, a, b string) string {
	i, j := strings.Index(pattern, "{0}"), strings.Index(pattern, "{1}")
	if i < 0 || j < 0 {
		return a + b
	}
	if i < j {
		return pattern[:i] + a + pattern[i+3:j] + b + pattern[j+3:]
	}
	return pattern[:j] + b + pattern[j+3:i] + a + pattern[i+3:]
}
