package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A typed literal's record names the type whose handler builds it: the
// type its tag names, and for a literal written with no tag where a type
// with a checked-literal handler is expected, that type, which the file
// never names (TypedLiteral.Tag).
func TestTypedLiteralTag_IsTheTypeWhoseHandlerBuildsIt(t *testing.T) {
	fa, errs := checkEntry(t, `import
    std::literals.{Fragment, Literal}
    std::url.Url
end

struct Word
    text: String
end

impl Literal for Word
    fn from_fragments(_fragments: List<Fragment<String>>) -> Word
        Word{text: "w"}
    end
end

fn host(u: Url) -> Url
    u
end

fn total() -> Int
    _ = Word"x"
    _ = host("https://example.com")
    1
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this source: %v", errs)
	}
	tags := map[string]analysis.Type{}
	for n, rec := range fa.TypedLiterals {
		tags[n.Tag] = rec.Tag
	}
	if st, ok := tags["Word"].(*analysis.StructType); !ok || st.Name != "Word" || st.Origin != fa.Origin {
		t.Errorf(`Word"x": recorded for %v, want this file's Word`, tags["Word"])
	}
	if len(fa.TargetLiterals) != 1 {
		t.Fatalf("%d untagged literals read as typed, want 1", len(fa.TargetLiterals))
	}
	for _, twin := range fa.TargetLiterals {
		rec := fa.TypedLiterals[twin]
		if rec == nil || rec.Tag == nil {
			t.Fatalf(`"https://example.com" as a Url: record %+v, want its type`, rec)
		}
		if rec.Tag.String() != "Url" || nominalOrigin(rec.Tag) != "std/url" {
			t.Errorf(`"https://example.com" as a Url: recorded for %v (%s), want std/url's Url`, rec.Tag, nominalOrigin(rec.Tag))
		}
	}
}

// nominalOrigin is the declaring origin of a struct, enum or distinct type.
func nominalOrigin(t analysis.Type) string {
	switch ty := analysis.ResolveClosed(t).(type) {
	case *analysis.StructType:
		return ty.Origin
	case *analysis.EnumType:
		return ty.Origin
	case *analysis.DistinctType:
		return ty.Origin
	}
	return ""
}
