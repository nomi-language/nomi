package irbuild

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Unsupported names one construct the builder does not lower, at the Nomi
// source position that construct occupies.
//
// A named rejection is the contract of a builder that lowers part of the
// language: the alternative is a program that runs and behaves differently,
// which is the failure mode this design exists to avoid.
type Unsupported struct {
	// Construct is the human-readable name of what was rejected. It is the
	// tally key in the corpus coverage report, so it must be stable and it
	// must not embed positions or identifiers that vary per program — those
	// go in the message, not the key.
	Construct string
	File      string
	Line      int
	Col       int
	// Detail is optional extra context ("io.print", "|>") appended to the
	// message but excluded from the tally key.
	Detail string
}

func (u Unsupported) Error() string {
	var b strings.Builder
	b.WriteString("unsupported: ")
	b.WriteString(u.Construct)
	if u.Detail != "" {
		b.WriteString(" (")
		b.WriteString(u.Detail)
		b.WriteString(")")
	}
	b.WriteString(" at ")
	b.WriteString(u.pos())
	return b.String()
}

func (u Unsupported) pos() string {
	loc := fmt.Sprintf("%d", u.Line)
	if u.Col > 0 {
		loc = fmt.Sprintf("%d:%d", u.Line, u.Col)
	}
	if u.File != "" {
		return u.File + ":" + loc
	}
	return "line " + loc
}

// UnsupportedError reports every construct one lowering attempt rejected.
//
// All of them, not the first: the tally over a corpus is what tells us which
// construct family to lower next, and a fail-fast generator would report only
// the outermost rejection of every file.
//
// The corollary: a rejection that hides a SUBTREE distorts the tally even when
// every rejection is reported. Refusing a whole declaration at the top level
// without inspecting its body would make every file look blocked by that one
// construct alone, when the body needs many other things too.
//
// So a rejection does not end generation. Every reject site records the
// construct and lowering continues, so the set below names every unsupported
// construct the generator reaches rather than only the first. See gen.reject
// in native.go.
type UnsupportedError struct {
	Items []Unsupported
	// Suppressed is every position at which a blocker was NOT named because
	// the operand being judged had no kind, deduped by position and sorted.
	//
	// It is the set's own error bar. Items names every construct the generator
	// COULD name; this names the places it could not, which is the difference
	// between "these are the blockers" and "these are the blockers we can
	// see". A non-empty Suppressed makes every percentage derived from Items a
	// CEILING. See suppression.go.
	Suppressed []Suppression
}

func newUnsupportedError(items []Unsupported, masked []Suppression) *UnsupportedError {
	// One construct at one position is ONE blocker however many paths reach
	// it, so the dedupe belongs here rather than in a guard at each reject
	// site.
	seen := make(map[Unsupported]bool, len(items))
	kept := make([]Unsupported, 0, len(items))
	for _, it := range items {
		if seen[it] {
			continue
		}
		seen[it] = true
		kept = append(kept, it)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Line != kept[j].Line {
			return kept[i].Line < kept[j].Line
		}
		return kept[i].Col < kept[j].Col
	})
	return &UnsupportedError{Items: kept, Suppressed: dedupeSuppressions(masked)}
}

// dedupeSuppressions collapses suppressions to one per position, for the same
// reason newUnsupportedError collapses a construct to one per position: several
// paths can reach one site, and a chain of kindless
// propagation along one line is one masked line rather than five.
func dedupeSuppressions(masked []Suppression) []Suppression {
	if len(masked) == 0 {
		return nil
	}
	seen := make(map[Suppression]bool, len(masked))
	kept := make([]Suppression, 0, len(masked))
	for _, s := range masked {
		if seen[s] {
			continue
		}
		seen[s] = true
		kept = append(kept, s)
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].File != kept[j].File {
			return kept[i].File < kept[j].File
		}
		return kept[i].Line < kept[j].Line
	})
	return kept
}

// Error names ONLY Items[0], plus a count of the rest.
//
// SO A TEST MUST NEVER ASSERT A CONSTRUCT BY SCANNING THIS STRING — assert over
// Items (`errors.As` then range), which is what the test files in this package
// already do. A `strings.Contains(err.Error(), "type argument")` against a
// refusal whose Items[0] is some other construct reads the key as ABSENT while
// the set CONTAINS it: a false negative that looks like a clean result rather
// than like an error, so nothing announces it: against a fixture whose first
// construct is `generic impl block`, a present `type argument` scans as
// missing.
func (e *UnsupportedError) Error() string {
	if len(e.Items) == 0 {
		return "unsupported: unknown construct"
	}
	msg := e.Items[0].Error()
	if len(e.Items) > 1 {
		msg = fmt.Sprintf("%s (and %d more unsupported construct(s))", msg, len(e.Items)-1)
	}
	return msg
}

// Constructs returns the distinct construct names this error names, sorted.
// The differential harness tallies these.
func (e *UnsupportedError) Constructs() []string {
	seen := make(map[string]bool, len(e.Items))
	out := make([]string, 0, len(e.Items))
	for _, it := range e.Items {
		if seen[it.Construct] {
			continue
		}
		seen[it.Construct] = true
		out = append(out, it.Construct)
	}
	sort.Strings(out)
	return out
}

// nodePos returns a node's 1-based source position.
//
// Reflection rather than a 70-case type switch: every ast.Node carries bare
// Line/Col int fields with no shared accessor beyond LineNum(), so a switch
// here would be a list of every node type in the language that silently loses
// column information for each one somebody forgets to add.
//
// It is a LOWERING path and a hot one, not only a rejection path: `irNodePos`
// asks it for a short-circuit operand's own position (`ir.BeginShortCircuit`
// takes one for each operand where `ir.NewArith` takes only the operator's),
// and the `const` and `ref` classes ask it once per LITERAL and per NAMED
// READ (TestIRConstRef_EveryRoutedSiteHasAPosition). The reflection here is
// two `reflect` calls and a field lookup per call; if this ever shows up in a
// profile, the fix is a generated accessor and not a 70-case switch.
func nodePos(n ast.Node) (line, col int) { return ast.Position(n) }

// isNilNode reports whether n carries nothing — a nil interface, or a typed nil
// pointer, which is what an absent optional child (`fd.Body`, `t.Else`) looks
// like once it is stored in an ast.Node field.
func isNilNode(n ast.Node) bool { return ast.IsNilNode(n) }

// childNodes returns n's child AST nodes (ast.Children) in field-declaration
// order, which is source order closely enough for a report. Nodes reached
// through plain structs and slices count as children: an ast.Param's default
// value and an ast.CaseBranch's guard and body are expressions in their own
// right, and the struct they are stored in is a container, not a construct.
func childNodes(n ast.Node) []ast.Node {
	var out []ast.Node
	ast.Children(n, func(c ast.Node) { appendChildNode(c, &out) })
	return out
}

var triviaCarrierType = reflect.TypeOf(ast.TriviaCarrier{})

// appendChildNode records one child, minus the two kinds of node that are not
// blockers in their own right.
func appendChildNode(n ast.Node, out *[]ast.Node) {
	if _, isType := n.(ast.TypeExpr); isType {
		// Whether a DECLARED type is in the subset is typeOf's answer, and
		// it is already reported as "non-scalar parameter type" and friends.
		// Walking an annotation as if it were behaviour would additionally
		// file `List<Int>` under "GenericType" and count one gap twice under
		// two names, which is the opposite of a predictive blocker set.
		return
	}
	if strings.HasSuffix(n.NodeType(), "Pattern") {
		// The implementable construct is the one that MATCHES — "case
		// expression", "pattern destructuring", "pattern if" — and pattern
		// kinds are that construct's internals, not separate slices. Patterns
		// also contain nothing but literals and sub-patterns, so no behaviour
		// is left unexamined by stopping here.
		return
	}
	*out = append(*out, n)
}

// constructNames maps an ast NodeType onto the name a Nomi programmer would
// use. Anything absent falls back to its NodeType, which is still a usable
// tally key — an unmapped entry degrades the report's prose, not its accuracy.
var constructNames = map[string]string{
	"AnonStructType":  "anonymous struct type",
	"Assertion":       "assertion",
	"Break":           "break",
	"Case":            "case expression",
	"ConcurrentBlock": "concurrent block",
	"Continue":        "continue",
	"Dbg":             "dbg",
	"Todo":            "todo",
	"Unreachable":     "unreachable",
	"Then":            "then stage",
	"Tap":             "tap stage",
	"CodepointLit":    "codepoint literal",
	"DecimalLit":      "decimal literal",
	// Decorators reach the tally two ways — funcDecl refuses a decorated
	// function by name, and a decorator hanging off a struct or enum is
	// reached as a child of that rejected declaration — so both must land on
	// one key or the report would double-count one gap.
	"Decorator":     "decorator",
	"Defer":         "defer",
	"DotVariant":    "dot variant",
	"EnumDef":       "enum declaration",
	"ExternFunc":    "host fn declaration",
	"ExternType":    "host type declaration",
	"FieldAccess":   "field access",
	"FieldAccessor": "field accessor",
	// A FuncDef only reaches this map from a nested position: a top-level one
	// goes to funcDecl, which names its own reasons.
	"FuncDef": "nested function definition",
	// `If` is reached only as a bare pipe stage (`x |> if { … }`): every other
	// position is lowered, so an unmapped NodeType would surface only there.
	"If":              "if expression",
	"ImplBlock":       "impl block",
	"ImplConformance": "impl conformance",
	"InterfaceDef":    "interface declaration",
	// An interface's members are reached only as children of a refused
	// InterfaceDef, but they are not internals in the way a pattern kind is,
	// so they earn their own tally keys rather than falling through to a bare
	// NodeType.
	"InterfaceMethod":    "interface method requirement",
	"Lambda":             "lambda",
	"ListLit":            "bracket literal",
	"MapLit":             "map literal",
	"NamedArg":           "named argument",
	"OnceBinding":        "once binding",
	"PatternBinding":     "pattern binding",
	"BindingElse":        "binding else",
	"PatternDestructure": "pattern destructuring",
	"Placeholder":        "capture parameter",
	"Capture":            "capture",
	"RangeLit":           "range literal",
	"StructDef":          "struct declaration",
	"StructLit":          "struct literal",
	"TaggedString":       "typed literal",
	"TestDecl":           "test declaration",
	"TryOp":              "`?`",
	"Unwrap":             "`!`",
	"TupleLit":           "tuple literal",
	"TypeAlias":          "type alias",
	"TypeDef":            "type declaration",
}

func constructName(n ast.Node) string {
	t := n.NodeType()
	if name, ok := constructNames[t]; ok {
		return name
	}
	return t
}
