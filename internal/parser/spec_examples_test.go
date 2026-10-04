package parser_test

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The language specification, docs/spec.md. Its ```nomi blocks
// are mostly FRAGMENTS — signature tables, variant lists, bare type
// expressions, snippets with `...` standing in for omitted code — so unlike
// the tour (vmhost.TestTourDoctests, which runs every ```nomi-run block end
// to end) a whole-file parse of a spec block fails for legitimate reasons most
// of the time. The two tests below are the spec's instrument, and the whole
// difficulty is separating "fragment" from "malformed". The blocks that are
// complete programs (a `fn main` at column 0, nothing elided) are also
// type-checked and run, by vmhost.TestSpecPrograms.
//
// THE FRAGMENT-VERSUS-MALFORMED RULE
//
// A block is classified by the first of these that accepts it. Every category
// but the last is decided mechanically by re-parsing; nothing is decided by
// reading an error message.
//
//	program    parses as a whole file exactly as written.
//	elided     parses once every `...` — the spec's "and so on" mark, which is
//	           not Nomi syntax — is replaced by the identifier `elided`.
//	signature  parses once every bare `fn`/`host fn` signature line is given
//	           the body ` { elided }`. Reference sections list signatures
//	           without bodies; this is the shape of the stdlib tables in §9, §16 and §19.
//	types      every non-blank, non-comment line parses as a type expression
//	           (each line is wrapped as `fn _p(): <line> { elided }`).
//	variants   the whole block parses as an enum body (wrapped as
//	           `enum _E { <block> }`). Enum variant lines are shown bare.
//	divergent  pinned below in specDivergences, with a written reason. These
//	           are places where the spec describes syntax the parser does not
//	           accept. Each entry is keyed by a substring of the block, never
//	           by line number, and TestSpecExamples_PinnedDivergencesStillFail
//	           fails if a pinned block starts parsing — the list cannot rot.
//	MALFORMED  none of the above. This is a defect in the spec and the test
//	           fails.
//
// The rule is deliberately generous about syntax and strict about structure.
// A block that reaches `program` is then checked for a second defect class
// that no parse error can find, because it parses clean and means the wrong
// thing: TestSpecExamples_NoDetachedStructLiteral, below.

const specPath = "../../docs/spec.md"

// specDivergences pins blocks the parser rejects because the spec describes
// syntax the implementation does not accept. Each key is a substring that
// occurs in exactly one spec block. These are findings, not fragments; the
// value is why the block does not parse.
var specDivergences = map[string]string{
	".Circle{r} or .Square{r}": "an `or` pattern in a case branch; the parser expects `->` after the first pattern. " +
		"A genuine missing feature — no alternative-pattern node exists in `ast/`, `parser/` or `analysis/` — " +
		"and §10 Guards carries the matching `Status: not yet implemented` callout.",
	"typealias Lookup<V> Map<String, V>": "`typealias` IS a keyword the parser knows and `typealias Name Target` (no `=`) works end to end; " +
		"only the GENERIC form is refused, as a hard parse error at the `<` (`expected type name, got LT`). " +
		"`ast.TypeAlias` has no type-parameter field to hold one. §15 Type Aliases carries the callout.",
	"Rectangle (Float, Float)    // payload is a tuple": "a trailing `//` comment on an enum variant line. A parser INCONSISTENCY, not a language rule: " +
		"all four variant kinds reject one (bare -> `expected type name, got COMMENT`; positional, struct-shaped and " +
		"`embeds` -> `enum variants are newline- or semicolon-separated`), while a struct field and an interface " +
		"requirement line both accept one. Two arms are implicated — `parser.go:5330`'s same-line separator check " +
		"excludes RBRACE/NEWLINE/SEMICOLON/COMMA but not COMMENT, and `parseEnumVariant` reads a same-line COMMENT " +
		"after a bare name as the start of a payload type — but the fix is larger than the arms: `ast.EnumVariant` " +
		"has `LeadingComments` and (struct-shaped only) `EndTrivia` and no trailing-comment field, so accepting the " +
		"comment without somewhere to keep it would make `nomi fmt` delete it. Parser arms + AST field + formatter " +
		"emission + a fmt round-trip test.",
}

var (
	sigLineRe  = regexp.MustCompile(`^\s*(pub\s+)?fn\s+[A-Za-z_][A-Za-z0-9_]*\??\s*(<[^>]*>)?\s*\(`)
	blankOrCmt = regexp.MustCompile(`^\s*(//.*)?$`)
)

// splitLineComment splits a source line into code and a trailing `//` comment,
// ignoring `//` that falls inside a string literal.
func splitLineComment(line string) (code, comment string) {
	inStr := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if inStr {
				i++
			}
		case '"':
			inStr = !inStr
		case '/':
			if !inStr && i+1 < len(line) && line[i+1] == '/' {
				return line[:i], line[i:]
			}
		}
	}
	return line, ""
}

func elide(code string) string {
	return strings.ReplaceAll(code, "...", "elided")
}

// completeSignatures gives every bare `fn` signature line a body, inserting it
// before any trailing comment so the body is not swallowed by the comment.
// `host fn` is deliberately excluded: a host signature is legal with no body,
// so completing it would append a bare `{ elided }` block after a finished
// declaration and manufacture the very defect the structure test looks for.
func completeSignatures(code string) string {
	lines := strings.Split(code, "\n")
	for i, ln := range lines {
		if !sigLineRe.MatchString(ln) {
			continue
		}
		src, cmt := splitLineComment(ln)
		if strings.Contains(src, "{") {
			continue
		}
		lines[i] = strings.TrimRight(src, " \t") + " { elided } " + cmt
	}
	return strings.Join(lines, "\n")
}

func parses(code string) bool {
	_, err := parser.Parse(lexer.Lex(code))
	return err == nil
}

// parsesAsTypeLines reports whether every non-blank, non-comment line of the
// block is a type expression on its own.
func parsesAsTypeLines(code string) bool {
	any := false
	for _, ln := range strings.Split(code, "\n") {
		if blankOrCmt.MatchString(ln) {
			continue
		}
		src, _ := splitLineComment(ln)
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		any = true
		if !parses("fn _p(): " + src + " { elided }") {
			return false
		}
	}
	return any
}

// parsesAsEnumBody reports whether the block is a list of bare enum variants.
func parsesAsEnumBody(code string) bool {
	return parses("enum _E {\n" + code + "\n}")
}

type specBlock struct {
	line  int
	code  string
	class string
	why   string
}

func classifySpecBlocks(t *testing.T) []specBlock {
	t.Helper()
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}
	blocks := doctest.ExtractBlocks(string(data), "nomi")
	if len(blocks) == 0 {
		t.Fatalf("no ```nomi blocks found in %s", specPath)
	}
	out := make([]specBlock, 0, len(blocks))
	for _, b := range blocks {
		sb := specBlock{line: b.Line, code: b.Code}
		switch {
		case parses(b.Code):
			sb.class = "program"
		case parses(elide(b.Code)):
			sb.class = "elided"
		case parses(completeSignatures(elide(b.Code))):
			sb.class = "signature"
		case parsesAsTypeLines(b.Code):
			sb.class = "types"
		case parsesAsEnumBody(b.Code):
			sb.class = "variants"
		default:
			sb.class = "MALFORMED"
			for key, why := range specDivergences {
				if strings.Contains(b.Code, key) {
					sb.class, sb.why = "divergent", why
					break
				}
			}
		}
		out = append(out, sb)
	}
	return out
}

// TestSpecExamples_NoMalformedBlock is the syntax half of the instrument.
// Every ```nomi block in the spec must land in one of the accounted-for
// categories described at the top of this file.
func TestSpecExamples_NoMalformedBlock(t *testing.T) {
	blocks := classifySpecBlocks(t)
	counts := map[string]int{}
	for _, b := range blocks {
		counts[b.class]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var tally []string
	for _, k := range keys {
		tally = append(tally, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	t.Logf("%s: %d ```nomi blocks — %s", specPath, len(blocks), strings.Join(tally, " "))

	for _, b := range blocks {
		if b.class != "MALFORMED" {
			continue
		}
		_, err := parser.Parse(lexer.Lex(completeSignatures(elide(b.code))))
		t.Errorf("%s:%d: malformed ```nomi block — it is not a program, not an\n"+
			"elided snippet, not a signature list, not a type or variant list, and not a\n"+
			"pinned divergence. Fix the block, or pin it in specDivergences with a reason.\n\n%s\n\nparse error after normalization: %v",
			specPath, b.line, b.code, err)
	}
}

// TestSpecExamples_PinnedDivergencesStillFail keeps specDivergences honest: a
// pinned block that has started parsing must be unpinned, not left behind.
func TestSpecExamples_PinnedDivergencesStillFail(t *testing.T) {
	blocks := classifySpecBlocks(t)
	seen := map[string]int{}
	for _, b := range blocks {
		for key := range specDivergences {
			if strings.Contains(b.code, key) {
				seen[key]++
				if b.class == "program" || b.class == "elided" || b.class == "signature" {
					t.Errorf("%s:%d: pinned divergence %q now classifies as %q — remove the pin",
						specPath, b.line, key, b.class)
				}
			}
		}
	}
	for key := range specDivergences {
		switch seen[key] {
		case 1:
		case 0:
			t.Errorf("pinned divergence %q matches no spec block — the spec moved; update or drop the pin", key)
		default:
			t.Errorf("pinned divergence %q matches %d spec blocks; a pin must select exactly one", key, seen[key])
		}
	}
}

// TestSpecExamples_NoDetachedStructLiteral is the structure half of the
// instrument, and it finds a defect no parse error can: a struct literal
// separated from the type name it was meant to attach to.
//
//	user = users.User
//
//	{name: "Alice", age: 30}
//
// parses clean as two statements — a binding of the bare name, then an
// anonymous struct built and thrown away. At file scope there is no tail-value
// position, so a discarded anonymous struct literal is never what the author
// meant. Named literals are not flagged: `User{a: 1}` alone is a legitimate
// "here is the expression" demonstration.
//
// The braces are caught in both shapes they can take. `{status: 404}` parses
// as an anonymous *ast.StructLit; `{}` — the detached form of `Response{}`,
// a struct built entirely from field defaults — is ambiguous with a block and
// the parser resolves it to an empty *ast.Block. A bare block at file scope is
// no more meaningful than a discarded literal, so both are reported.
func TestSpecExamples_NoDetachedStructLiteral(t *testing.T) {
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}
	found := 0
	for _, b := range doctest.ExtractBlocks(string(data), "nomi") {
		nodes, perr := parser.Parse(lexer.Lex(completeSignatures(elide(b.Code))))
		if perr != nil {
			continue
		}
		for _, n := range nodes {
			es, ok := n.(*ast.ExprStmt)
			if !ok {
				continue
			}
			var line int
			switch e := es.Expr.(type) {
			case *ast.StructLit:
				if e.TypeName != nil {
					continue
				}
				line = e.Line
			case *ast.Block:
				line = e.Line
			default:
				continue
			}
			found++
			// b.Line is the opening fence; block line 1 is the line after it.
			t.Errorf("%s:%d: detached struct literal — braces at file scope that\n"+
				"build a value and discard it. Almost always a literal that lost the type\n"+
				"name it should attach to.\n\n%s",
				specPath, b.Line+line, b.Code)
		}
	}
	t.Logf("detached struct literals: %d", found)
}
