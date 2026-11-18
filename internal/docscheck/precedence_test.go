package docscheck

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/token"
)

// The operator levels written outside Go are the precedence table's
// (internal/token/precedence.go), which the parser and the formatter read.
//
// tree-sitter-nomi/grammar.js uses the table's own numbers, so each
// operator's level is compared as written, and each infix rule must read
// left to right (prec.left). The grammar reads `is`, `in` and `as` left to
// right too, where the compiler rejects a chain; that is not a difference
// in what parses without an error.
//
// docs/spec.md numbers its levels differently (it gives prefix `not` a
// level of its own), so its table is compared by order: two operators
// stand on one level, or one above the other, as they do in the Go table.
// Its notes must say which operators do not chain.
func TestPrecedenceTablesMatch(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(rel string) string {
		text, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(text)
	}
	t.Run("grammar.js", func(t *testing.T) { checkGrammarPrecedence(t, read("tree-sitter-nomi/grammar.js")) })
	t.Run("spec.md", func(t *testing.T) { checkSpecPrecedence(t, read("docs/spec.md")) })
}

func checkGrammarPrecedence(t *testing.T, grammar string) {
	seen := map[string]bool{}
	level := func(where, op string, got int) {
		seen[op] = true
		if want := token.Precedence(op); got != want {
			t.Errorf("grammar.js %s: `%s` is at level %d, and the Go table has it at %d", where, op, got, want)
		}
	}

	// The operators of binaryExpression, `['op', level]` pairs under one
	// prec.left.
	_, rest, found := strings.Cut(grammar, "function binaryExpression(operand) {")
	body, _, closed := strings.Cut(rest, "\n}\n")
	if !found || !closed {
		t.Fatal("grammar.js has no binaryExpression function")
	}
	pairs := regexp.MustCompile(`\['([^']+)', (\d+)\]`).FindAllStringSubmatch(body, -1)
	if len(pairs) == 0 || !strings.Contains(body, "prec.left(p, seq(operand, op, operand))") {
		t.Fatal("grammar.js binaryExpression no longer holds `['op', level]` pairs under prec.left")
	}
	for _, m := range pairs {
		n, _ := strconv.Atoi(m[2])
		level("binaryExpression", m[1], n)
	}

	// The infix rules written one by one, each with a lambda-body twin
	// except the pipe.
	for _, rule := range []struct {
		name  string
		ops   []string
		twins int
	}{
		{"is_expression", []string{"is"}, 2},
		{"ascription_expression", []string{"as"}, 2},
		{"range_expression", []string{"..", "..="}, 2},
		{"pipe_expression", []string{"|>"}, 1},
	} {
		ms := regexp.MustCompile(`(?m)^    (?:lambda_body_)?`+rule.name+`: \$ => prec\.(\w+)\((\d+), `).FindAllStringSubmatch(grammar, -1)
		if len(ms) != rule.twins {
			t.Errorf("grammar.js: found %d `%s: $ => prec.left(N, ...)` rules, want %d", len(ms), rule.name, rule.twins)
		}
		for _, m := range ms {
			if m[1] != "left" {
				t.Errorf("grammar.js %s is prec.%s; infix operators read left to right", rule.name, m[1])
			}
			n, _ := strconv.Atoi(m[2])
			for _, op := range rule.ops {
				level(rule.name, op, n)
			}
		}
	}

	// Every infix operator is in the grammar.
	for _, op := range token.InfixOperators() {
		if !seen[op.Text] {
			t.Errorf("grammar.js gives no level to `%s`, which the Go table has at %d", op.Text, op.Prec)
		}
	}

	// The prefix operators: `-` and `~~~` at PrecPrefix, and `not` holding
	// everything down to NotOperandPrecedence, to its right.
	prefix := regexp.MustCompile(`prec(\.right)?\((\d+), seq\('(-|~~~|not)', \$\._(?:lambda_body_)?expression\)\)`).FindAllStringSubmatch(grammar, -1)
	count := map[string]int{}
	for _, m := range prefix {
		n, _ := strconv.Atoi(m[2])
		count[m[3]]++
		want := token.OperandPrecedence(m[3], token.PrefixOperand)
		if n != want {
			t.Errorf("grammar.js: prefix `%s` is at level %d, and the Go table has its operand at %d", m[3], n, want)
		}
		if m[3] == "not" && m[1] != ".right" {
			t.Errorf("grammar.js: prefix `not` must be prec.right, since it holds what follows it")
		}
	}
	for _, op := range []string{"-", "~~~", "not"} {
		if count[op] != 2 {
			t.Errorf("grammar.js: found %d prefix `%s` rules, want one for an expression and one for a lambda body", count[op], op)
		}
	}
}

func checkSpecPrecedence(t *testing.T, spec string) {
	_, rest, found := strings.Cut(spec, "Precedence, from loosest to tightest:\n")
	section, _, ended := strings.Cut(rest, "\nOther operator tokens:")
	if !found || !ended {
		t.Fatal("docs/spec.md has no precedence table between `Precedence, from loosest to tightest:` and `Other operator tokens:`")
	}

	// The table's rows: `| level | operators |`, a `|` inside an operator
	// written `\|`.
	infix := map[string]int{}
	prefix := map[string]int{}
	cell := regexp.MustCompile("(prefix |postfix )?((?:`[^`]+`(?: and | )?)+)")
	quoted := regexp.MustCompile("`([^`]+)`")
	for _, line := range strings.Split(section, "\n") {
		cols := strings.Split(strings.ReplaceAll(line, `\|`, "\x00"), "|")
		if len(cols) != 4 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(cols[1]))
		if err != nil {
			continue
		}
		for _, m := range cell.FindAllStringSubmatch(strings.ReplaceAll(cols[2], "\x00", "|"), -1) {
			for _, q := range quoted.FindAllStringSubmatch(m[2], -1) {
				switch m[1] {
				case "prefix ":
					prefix[q[1]] = n
				case "":
					infix[q[1]] = n
				}
			}
		}
	}
	if len(infix) == 0 {
		t.Fatal("docs/spec.md: read no operators out of the precedence table")
	}

	ops := token.InfixOperators()
	sign := func(n int) int {
		switch {
		case n < 0:
			return -1
		case n > 0:
			return 1
		}
		return 0
	}
	listed := map[string]bool{}
	for _, a := range ops {
		listed[a.Text] = true
		la, ok := infix[a.Text]
		if !ok {
			t.Errorf("docs/spec.md: the precedence table lacks `%s`", a.Text)
			continue
		}
		for _, b := range ops {
			lb, ok := infix[b.Text]
			if ok && sign(la-lb) != sign(a.Prec-b.Prec) {
				t.Errorf("docs/spec.md orders `%s` (level %d) and `%s` (level %d) differently from the Go table (%d and %d)",
					a.Text, la, b.Text, lb, a.Prec, b.Prec)
			}
		}
		// A prefix `-` or `~~~` binds tighter than every infix operator.
		for _, p := range []string{"-", "~~~"} {
			if lp, ok := prefix[p]; !ok {
				t.Errorf("docs/spec.md: the precedence table lacks prefix `%s`", p)
			} else if sign(lp-la) != sign(token.PrecPrefix-a.Prec) {
				t.Errorf("docs/spec.md puts prefix `%s` (level %d) on the wrong side of `%s` (level %d)", p, lp, a.Text, la)
			}
		}
		// Prefix `not` stands below every operator its operand holds, and
		// above the rest.
		if ln, ok := prefix["not"]; !ok {
			t.Errorf("docs/spec.md: the precedence table lacks prefix `not`")
		} else if held := a.Prec >= token.NotOperandPrecedence; held != (la > ln) {
			t.Errorf("docs/spec.md puts prefix `not` (level %d) on the wrong side of `%s` (level %d): its operand holds `%s`: %v",
				ln, a.Text, la, a.Text, held)
		}
	}
	var unknown []string
	for op := range infix {
		if !listed[op] {
			unknown = append(unknown, op)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		t.Errorf("docs/spec.md: the precedence table lists %q, which the Go table does not", unknown)
	}

	// Associativity, in the notes under the table: one level reads left to
	// right, and a note that starts with an operator and says it does not
	// associate or chain names one that does not.
	if !strings.Contains(section, "Binary operators of one level associate to the left.") {
		t.Errorf("docs/spec.md no longer says `Binary operators of one level associate to the left.`")
	}
	nonAssoc := map[string]bool{}
	lead := regexp.MustCompile("^- ((?:`[^`]+`(?: and )?)+)")
	for _, note := range strings.Split(section, "\n- ") {
		note = "- " + strings.Join(strings.Fields(note), " ")
		m := lead.FindStringSubmatch(note)
		if m == nil || !(strings.Contains(note, "do not associate") || strings.Contains(note, "does not chain")) {
			continue
		}
		for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
			nonAssoc[q[1]] = true
		}
	}
	for _, op := range ops {
		if want := op.Assoc == token.NonAssoc; want != nonAssoc[op.Text] {
			t.Errorf("docs/spec.md's notes say `%s` chains: %v, and the Go table: %v", op.Text, !nonAssoc[op.Text], !want)
		}
	}
}
