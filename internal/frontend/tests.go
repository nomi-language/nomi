package frontend

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// The test case list is the front end's, not the VM's: the VM runs the cases
// CollectTestCases names, in its order, under its names.

// SetupFrame is the enclosing `tests` group's boot line and setup.
type SetupFrame struct {
	Boot ast.Node
	Body ast.Node
}

// TestCase is one case `nomi test` runs.
type TestCase struct {
	// Name is the case's path: enclosing groups and owners, then its own.
	Name           []string
	Setup          []SetupFrame
	ContextPattern ast.Node
	Body           *ast.Block
	Line           int
	// EndLine is the last line `--line` matches, or 0 when only Line does.
	EndLine    int
	GroupLines []int
	// SelfTypeName is the owning type of an attached test, which a bare
	// same-owner call resolves against.
	SelfTypeName string
	// Clock is the innermost `clock` variant in force ("Virtual", "System"),
	// "" when no group declared one, or InvalidClock.
	Clock string
}

// InvalidClock marks a case whose `clock` declaration names no testing.Clock
// variant. It is reported, never run.
const InvalidClock = "\x00invalid"

// ErrInvalidClock is the failure a case with InvalidClock reports.
func ErrInvalidClock() error {
	return fmt.Errorf("`clock` must name a testing.Clock variant — " +
		"write `clock Clock.Virtual` or `clock Clock.System`")
}

// FullName is the case's name as reports print it.
func (tc TestCase) FullName() string { return strings.Join(tc.Name, " / ") }

// LastLine is the declaration's last line as a report states it. It is not
// EndLine: a `test` block's EndLine is zero so that only its first line
// selects it, and its closing brace is read from the body instead.
func (tc TestCase) LastLine() int {
	if tc.EndLine > 0 {
		return tc.EndLine
	}
	if tc.Body != nil && tc.Body.EndLine > 0 {
		return tc.Body.EndLine
	}
	return tc.Line
}

// TestOptions selects cases.
type TestOptions struct {
	Line    int
	LineSet bool
}

// TestResult is one case's outcome.
type TestResult struct {
	Name string
	Err  error
	// Line and EndLine are the declaration's first and last lines, which a
	// JSON report locates the result by.
	Line    int
	EndLine int
}

// DuplicateTestNameError is the failure of a case whose name an earlier case
// in the file already took.
type DuplicateTestNameError struct {
	Name      string
	FirstLine int
	Line      int
}

// SourceLine is the duplicate declaration's line, which a JSON test report
// records as the failure's error_line.
func (e *DuplicateTestNameError) SourceLine() int { return e.Line }

func (e *DuplicateTestNameError) Error() string {
	return fmt.Sprintf("duplicate test name %q at line %d; first declared at line %d", e.Name, e.Line, e.FirstLine)
}

// SelectTests collects nodes' cases and applies opts' line filter. When the
// selection holds duplicate names it answers those as failed results instead,
// and nothing should run.
func SelectTests(nodes []ast.Node, opts TestOptions) (cases []TestCase, duplicates []TestResult) {
	cases = CollectTestCases(nodes)
	if opts.LineSet {
		cases = FilterTestCases(cases, opts.Line)
	}
	if d := DuplicateTestNames(cases); len(d) > 0 {
		return nil, d
	}
	return cases, nil
}

// DuplicateTestNames answers a failed result for every case whose name an
// earlier case already took.
func DuplicateTestNames(cases []TestCase) []TestResult {
	firstLine := make(map[string]int, len(cases))
	var duplicates []TestResult
	for _, tc := range cases {
		name := tc.FullName()
		if line, ok := firstLine[name]; ok {
			duplicates = append(duplicates, TestResult{
				Name:    name,
				Line:    tc.Line,
				EndLine: tc.LastLine(),
				Err:     &DuplicateTestNameError{Name: name, FirstLine: line, Line: tc.Line},
			})
			continue
		}
		firstLine[name] = tc.Line
	}
	return duplicates
}

// CollectTestCases is every case in nodes, in order: `test` blocks inside
// their groups, and attached `//!` tests under their owner's name.
func CollectTestCases(nodes []ast.Node) []TestCase {
	var out []TestCase
	collectTestCases(nodes, nil, "", nil, nil, "", &out)
	return out
}

func collectTestCases(nodes []ast.Node, namePrefix []string, selfTypeName string, setupPrefix []SetupFrame, groupLines []int, clock string, out *[]TestCase) {
	for _, n := range nodes {
		collectAttachedTestCases(n, namePrefix, selfTypeName, setupPrefix, groupLines, clock, out)
		switch v := n.(type) {
		case *ast.StructDef:
			collectTestCases(v.Items, appendName(namePrefix, v.Name), v.Name, setupPrefix, groupLines, clock, out)
		case *ast.EnumDef:
			collectTestCases(v.Items, appendName(namePrefix, v.Name), v.Name, setupPrefix, groupLines, clock, out)
		case *ast.TypeDef:
			collectTestCases(v.Items, appendName(namePrefix, v.Name), v.Name, setupPrefix, groupLines, clock, out)
		case *ast.ExternType:
			collectTestCases(v.Items, appendName(namePrefix, v.Name), v.Name, setupPrefix, groupLines, clock, out)
		case *ast.ImplBlock:
			names := appendName(namePrefix, attachedTestOwnerName(v))
			receiverName := analysis.TypeExprBaseName(v.Receiver)
			if v.InferInterfaceMethods && v.Interface != nil {
				ifaceName := analysis.TypeExprBaseName(v.Interface)
				for _, item := range v.Items {
					if !testImplItemClaimsInterface(item, ifaceName) {
						continue
					}
					collectTestCases([]ast.Node{item}, names, receiverName, setupPrefix, groupLines, clock, out)
				}
			} else {
				collectTestCases(v.Items, names, receiverName, setupPrefix, groupLines, clock, out)
			}
		case *ast.TestDecl:
			collectTestCasesInBlock(v, namePrefix, setupPrefix, groupLines, clock, out)
		}
	}
}

func testImplItemClaimsInterface(item ast.Node, ifaceName string) bool {
	switch it := item.(type) {
	case *ast.FuncDef:
		return analysis.TypeExprBaseName(it.ImplIface) == ifaceName
	case *ast.ExternFunc:
		return analysis.TypeExprBaseName(it.ImplIface) == ifaceName
	default:
		return false
	}
}

func collectAttachedTestCases(n ast.Node, namePrefix []string, selfTypeName string, setupPrefix []SetupFrame, groupLines []int, clock string, out *[]TestCase) {
	owner := attachedTestOwnerName(n)
	for _, t := range AttachedTestsOf(n) {
		if t.Body == nil {
			continue
		}
		kind := t.Kind
		if kind == "" {
			kind = "test"
		}
		*out = append(*out, TestCase{
			Name:         appendName(namePrefix, fmt.Sprintf("%s //! %s %s", owner, kind, attachedTestLineLabel(t))),
			Setup:        append([]SetupFrame(nil), setupPrefix...),
			Body:         t.Body,
			Line:         t.Line,
			EndLine:      t.EndLine,
			GroupLines:   append([]int(nil), groupLines...),
			SelfTypeName: attachedTestSelfTypeName(selfTypeName),
		})
	}
}

func attachedTestSelfTypeName(owner string) string {
	if owner == "" {
		return ""
	}
	first, _ := utf8.DecodeRuneInString(owner)
	if !unicode.IsUpper(first) {
		return ""
	}
	return owner
}

func attachedTestLineLabel(t ast.AttachedTest) string {
	if t.EndLine > t.Line {
		return fmt.Sprintf("lines %d-%d", t.Line, t.EndLine)
	}
	return fmt.Sprintf("line %d", t.Line)
}

func attachedTestOwnerName(n ast.Node) string {
	switch v := n.(type) {
	case *ast.FuncDef:
		return v.Name
	case *ast.ExternFunc:
		return v.Name
	case *ast.StructDef:
		return v.Name
	case *ast.EnumDef:
		return v.Name
	case *ast.TypeDef:
		return v.Name
	case *ast.ExternType:
		return v.Name
	case *ast.TypeAlias:
		return v.Name
	case *ast.InterfaceDef:
		return v.Name
	case *ast.ImplBlock, *ast.ImplConformance:
		return "impl"
	case *ast.OnceBinding:
		return v.Name
	default:
		return "declaration"
	}
}

// AttachedTestsOf is the `//!` tests attached to a declaration.
func AttachedTestsOf(n ast.Node) []ast.AttachedTest {
	switch v := n.(type) {
	case *ast.FuncDef:
		return v.AttachedTests
	case *ast.ExternFunc:
		return v.AttachedTests
	case *ast.StructDef:
		return v.AttachedTests
	case *ast.EnumDef:
		return v.AttachedTests
	case *ast.TypeDef:
		return v.AttachedTests
	case *ast.ExternType:
		return v.AttachedTests
	case *ast.TypeAlias:
		return v.AttachedTests
	case *ast.InterfaceDef:
		return v.AttachedTests
	case *ast.ImplBlock:
		return v.AttachedTests
	case *ast.ImplConformance:
		return v.AttachedTests
	case *ast.OnceBinding:
		return v.AttachedTests
	default:
		return nil
	}
}

func collectTestCasesInBlock(decl *ast.TestDecl, namePrefix []string, setupPrefix []SetupFrame, groupLines []int, clock string, out *[]TestCase) {
	names := appendName(namePrefix, decl.Name)
	if !decl.Group {
		*out = append(*out, TestCase{
			Name:           names,
			Setup:          append([]SetupFrame(nil), setupPrefix...),
			ContextPattern: decl.ContextPattern,
			Body:           decl.Body,
			Line:           decl.Line,
			GroupLines:     append([]int(nil), groupLines...),
			Clock:          clock,
		})
		return
	}
	localSetup := append([]SetupFrame(nil), setupPrefix...)
	if decl.Boot != nil || decl.Setup != nil {
		localSetup = append(localSetup, SetupFrame{Boot: decl.Boot, Body: decl.Setup})
	}
	localGroupLines := append(append([]int(nil), groupLines...), decl.Line)
	localClock := clock
	if decl.Clock != nil {
		if name, ok := ast.TestClockVariant(decl.Clock); ok {
			localClock = name
		} else {
			localClock = InvalidClock
		}
	}
	for _, stmt := range decl.Body.Stmts {
		if child, ok := stmt.(*ast.TestDecl); ok {
			collectTestCasesInBlock(child, names, localSetup, localGroupLines, localClock, out)
		}
	}
}

// FilterTestCases is `--line N`: the cases whose declaration spans N (or
// N+1), else the cases of the group declared there.
func FilterTestCases(cases []TestCase, line int) []TestCase {
	for _, candidateLine := range []int{line, line + 1} {
		if candidateLine <= 0 {
			continue
		}
		var exact []TestCase
		for _, tc := range cases {
			if testCaseLineMatches(tc, candidateLine) {
				exact = append(exact, tc)
			}
		}
		if len(exact) > 0 {
			return exact
		}
		var group []TestCase
		for _, tc := range cases {
			for _, groupLine := range tc.GroupLines {
				if groupLine == candidateLine {
					group = append(group, tc)
					break
				}
			}
		}
		if len(group) > 0 {
			return group
		}
	}
	return nil
}

func testCaseLineMatches(tc TestCase, line int) bool {
	endLine := tc.EndLine
	if endLine == 0 {
		endLine = tc.Line
	}
	return line >= tc.Line && line <= endLine
}

func appendName(prefix []string, name string) []string {
	next := make([]string, 0, len(prefix)+1)
	next = append(next, prefix...)
	return append(next, name)
}

// StripAttachedTests drops every `//!` test from nodes, as `nomi run` does.
func StripAttachedTests(nodes []ast.Node) {
	for _, n := range nodes {
		switch v := n.(type) {
		case *ast.FuncDef:
			v.AttachedTests = nil
		case *ast.ExternFunc:
			v.AttachedTests = nil
		case *ast.StructDef:
			v.AttachedTests = nil
			StripAttachedTests(v.Items)
		case *ast.EnumDef:
			v.AttachedTests = nil
			StripAttachedTests(v.Items)
		case *ast.TypeDef:
			v.AttachedTests = nil
			StripAttachedTests(v.Items)
		case *ast.ExternType:
			v.AttachedTests = nil
			StripAttachedTests(v.Items)
		case *ast.TypeAlias:
			v.AttachedTests = nil
		case *ast.InterfaceDef:
			v.AttachedTests = nil
		case *ast.ImplBlock:
			v.AttachedTests = nil
			StripAttachedTests(v.Items)
		case *ast.ImplConformance:
			v.AttachedTests = nil
		case *ast.OnceBinding:
			v.AttachedTests = nil
		}
	}
}

// NodesContainTests reports whether nodes hold a `test` declaration or an
// attached test, at any depth a case can be declared.
func NodesContainTests(nodes []ast.Node) bool {
	for _, n := range nodes {
		if len(AttachedTestsOf(n)) > 0 {
			return true
		}
		switch v := n.(type) {
		case *ast.TestDecl:
			return true
		case *ast.StructDef:
			if NodesContainTests(v.Items) {
				return true
			}
		case *ast.EnumDef:
			if NodesContainTests(v.Items) {
				return true
			}
		case *ast.TypeDef:
			if NodesContainTests(v.Items) {
				return true
			}
		case *ast.ExternType:
			if NodesContainTests(v.Items) {
				return true
			}
		case *ast.ImplBlock:
			if NodesContainTests(v.Items) {
				return true
			}
		}
	}
	return false
}

// SourceContainsTests reports whether src holds any test, parse-only.
func SourceContainsTests(src string) (bool, error) {
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		return false, err
	}
	return NodesContainTests(nodes), nil
}

// FileDeclaresTests reports whether the file at path declares a test case,
// which decides whether it is checked in test mode.
func FileDeclaresTests(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return SourceDeclaresTests(string(data))
}

// SourceDeclaresTests is FileDeclaresTests for source.
func SourceDeclaresTests(src string) (bool, error) {
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		return false, err
	}
	return DeclaresTests(nodes), nil
}

// DeclaresTests reports whether v, walked structurally, holds a `test`
// declaration or an attached test with a body.
func DeclaresTests(v any) bool {
	if v == nil {
		return false
	}
	if _, ok := v.(*ast.TestDecl); ok {
		return true
	}
	if t, ok := v.(*ast.AttachedTest); ok {
		return t.Body != nil
	}
	if t, ok := v.(ast.AttachedTest); ok {
		return t.Body != nil
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return false
	}
	switch rv.Kind() {
	case reflect.Interface, reflect.Pointer:
		if rv.IsNil() {
			return false
		}
		return DeclaresTests(rv.Elem().Interface())
	case reflect.Slice, reflect.Array:
		for i := range rv.Len() {
			if DeclaresTests(rv.Index(i).Interface()) {
				return true
			}
		}
	case reflect.Struct:
		for i := range rv.NumField() {
			field := rv.Type().Field(i)
			if field.PkgPath != "" {
				continue
			}
			if DeclaresTests(rv.Field(i).Interface()) {
				return true
			}
		}
	}
	return false
}
