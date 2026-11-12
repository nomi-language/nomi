package analysis_test

import (
	"strings"
	"testing"
)

const targetTag = `import std::literals.{CheckedLiteral, Fragment, LiteralError}

struct Word
    text: String
end

impl CheckedLiteral<Int, Word> for Word
    fn check(statics: List<String>) -> Result<Unit, LiteralError>
        Ok(Unit)
    end

    fn build(fragments: List<Fragment<Int>>) -> Word
        Word{text: "${Iter.count(fragments)}"}
    end
end

struct Box
    word: Word
end

fn take(w: Word) -> Word
    w
end
`

// typeErrorsOf is the errors of src that are about types or literals, as
// checkTypedLiteralProject's callers filter std's import noise.
func typeErrorsOf(t *testing.T, src string) []string {
	t.Helper()
	var out []string
	for _, e := range checkTypedLiteralProject(t, src, nil) {
		if strings.Contains(e.Message, "typed-literal") || strings.Contains(e.Message, "mismatch") || strings.Contains(e.Message, "expected") || strings.Contains(e.Message, "slot") {
			out = append(out, e.Error())
		}
	}
	return out
}

// An untagged literal is the checked literal of the type its position
// expects: an annotated binding, an argument, a field, a returned value and
// a list element, with or without slots.
func TestTargetLiteral_EachPositionTakesTheType(t *testing.T) {
	for _, body := range []string{
		"    w: Word = \"plain\"\n    w\n",
		"    n = 1\n    w: Word = \"a ${n}\"\n    w\n",
		"    take(\"plain\")\n",
		"    Box{word: \"plain\"}.word\n",
		"    \"plain\"\n",
		"    ws: List<Word> = [\"a\", \"b\"]\n    List.head(ws)!\n",
	} {
		src := targetTag + "\nfn f() -> Word\n" + body + "end\n"
		if errs := typeErrorsOf(t, src); len(errs) > 0 {
			t.Errorf("the front end rejects an untagged Word literal:\n%s\n%v", body, errs)
		}
	}
}

// Only a literal converts: a String value where a Word is expected is the
// ordinary mismatch, and a literal with no expected type is a String. A slot
// is checked against the impl's slot type.
func TestTargetLiteral_OnlyALiteralConverts(t *testing.T) {
	for _, c := range []struct{ body, want string }{
		{"    s = \"plain\"\n    take(s)\n", "argument 1: expected Word, got String"},
		{"    s = \"plain\"\n    w: Word = s\n    w\n", "type mismatch: expected Word, got String"},
		{"    t = \"x\"\n    w: Word = \"a ${t}\"\n    w\n", "slot expression of type 'String' does not match required type 'Int' (required by tag 'Word')"},
	} {
		src := targetTag + "\nfn f() -> Word\n" + c.body + "end\n"
		errs := typeErrorsOf(t, src)
		found := false
		for _, e := range errs {
			if strings.Contains(e, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want an error containing %q, got %v", c.body, c.want, errs)
		}
	}
}
