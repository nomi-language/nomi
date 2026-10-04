package analysis

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// TestNoAssertionsInsideLambdas sweeps every Nomi source we ship for an
// `assert` or `refute` written inside a lambda body.
//
// checkAssertionBoundaryAt rejects these, so anything the suites compile is
// already covered and this would be redundant — except that not everything we
// ship is compiled by a suite:
//
// the tour has ~42 ```nomi fences alongside its ```nomi-run ones, and only
// the latter are executed. An invalid sample lived in a plain fence for a
// while before anyone read it closely.
//
// That is the surface this actually guards. It is cheap, so it also covers
// the compiled ones rather than trying to be clever about which is which.
//
// Why the shape matters: an assertion inside a lambda unwinds to the lambda,
// not to the test, so its failure becomes the lambda's return value and the
// caller discards it. The test then passes whatever the assertion said.
func TestNoAssertionsInsideLambdas(t *testing.T) {
	roots := []string{"../../tests", "../../std"}
	tourDocs := "../../tour/src/content/docs"

	var offenders []string
	sources, assertions := 0, 0

	record := func(label string, code string, lineOffset int) {
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(code))
		if len(nodes) == 0 {
			return
		}
		sources++
		for _, n := range nodes {
			walkForLambdaAssertions(reflect.ValueOf(n), false, func(a *ast.Assertion, inLambda bool) {
				assertions++
				if !inLambda {
					return
				}
				offenders = append(offenders, formatOffender(label, a, lineOffset))
			})
		}
	}

	for _, root := range roots {
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".nomi") {
				return nil
			}
			data, readErr := os.ReadFile(p)
			if readErr != nil {
				return nil
			}
			record(p, string(data), 0)
			// Doctests live in `//!` comments, invisible to the parse above.
			for _, blk := range doctestComments(string(data)) {
				record(p+" (doctest)", blk.code, blk.startLine-1)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	// Both fence kinds: `nomi-run` is executed by TestTourDoctests, plain
	// `nomi` is not checked by anything else at all.
	err := filepath.Walk(tourDocs, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(p), "/reference/") {
			return nil // generated from stdlib sources, already swept above
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		for _, lang := range []string{"nomi", "nomi-run"} {
			for _, b := range doctest.ExtractBlocks(string(data), lang) {
				record(p, b.Code, b.Line)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", tourDocs, err)
	}

	// Guards against the sweep silently matching nothing after a layout or
	// parser change, which would leave this passing vacuously — the exact
	// failure mode the rule itself has.
	if sources < 200 {
		t.Errorf("only %d sources parsed; the sweep has probably stopped finding them", sources)
	}
	if assertions < 500 {
		t.Errorf("only %d assertions seen; the walk has probably stopped reaching them", assertions)
	}

	if len(offenders) > 0 {
		t.Errorf("assertions inside a lambda body cannot fail their test — "+
			"the failure becomes the lambda's return value and the caller discards it. "+
			"Move each out of the lambda, or have the lambda return what you want to "+
			"assert about:\n  %s", strings.Join(offenders, "\n  "))
	}
}

func formatOffender(label string, a *ast.Assertion, lineOffset int) string {
	kw := "assert"
	if a.Refute {
		kw = "refute"
	}
	return label + ":" + itoa(a.Line+lineOffset) + "\t" + kw
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

type doctestCommentBlock struct {
	code      string
	startLine int
}

// doctestComments returns each contiguous run of `//!` lines with the marker
// stripped, plus the file line the run starts on.
func doctestComments(src string) []doctestCommentBlock {
	var out []doctestCommentBlock
	var cur []string
	start := 0
	flush := func() {
		if len(cur) > 0 {
			out = append(out, doctestCommentBlock{strings.Join(cur, "\n"), start})
			cur = nil
		}
	}
	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//!") {
			if len(cur) == 0 {
				start = i + 1
			}
			cur = append(cur, strings.TrimPrefix(trimmed, "//!"))
			continue
		}
		flush()
	}
	flush()
	return out
}

// walkForLambdaAssertions visits every *ast.Assertion, reporting whether it
// sits inside a lambda body. Reflection rather than a hand-written switch so
// that a new AST node carrying an expression cannot silently create a blind
// spot.
func walkForLambdaAssertions(v reflect.Value, inLambda bool, visit func(*ast.Assertion, bool)) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			return
		}
		if v.Kind() == reflect.Ptr {
			switch node := v.Interface().(type) {
			case *ast.Lambda:
				walkFieldsForLambdaAssertions(reflect.ValueOf(node).Elem(), true, visit)
				return
			case *ast.Assertion:
				visit(node, inLambda)
			}
		}
		walkForLambdaAssertions(v.Elem(), inLambda, visit)
	case reflect.Struct:
		walkFieldsForLambdaAssertions(v, inLambda, visit)
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			walkForLambdaAssertions(v.Index(i), inLambda, visit)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			walkForLambdaAssertions(v.MapIndex(k), inLambda, visit)
		}
	}
}

func walkFieldsForLambdaAssertions(v reflect.Value, inLambda bool, visit func(*ast.Assertion, bool)) {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		if t.Field(i).PkgPath != "" {
			continue // unexported
		}
		walkForLambdaAssertions(v.Field(i), inLambda, visit)
	}
}
