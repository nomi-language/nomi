package irbuild

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Unsupported names one construct the backend cannot lower, at the Nomi source
// position that construct occupies.
//
// A named rejection is the whole contract of a partial backend: the alternative
// is Go that compiles and then behaves differently, which is the failure mode
// this design exists to avoid.
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

// Masked is how many positions declined to name a blocker. Zero means the
// blocker set below is COMPLETE; anything else means it is a lower bound and no
// percentage computed from it is a forecast. See suppression.go.
func (e *UnsupportedError) Masked() int { return len(e.Suppressed) }

// MaskedByFile attributes the count to the file each suppression happened in,
// because a program is a graph of files and one sibling's refusal masks
// blockers in the file that calls it.
func (e *UnsupportedError) MaskedByFile() map[string]int {
	if len(e.Suppressed) == 0 {
		return nil
	}
	out := make(map[string]int, 4)
	for _, s := range e.Suppressed {
		out[s.File]++
	}
	return out
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

// THE UNIT: A SITE IS ONE REFUSAL ITEM — a distinct
// (construct, operand, file, line, col) tuple, which is exactly what Items
// holds after newUnsupportedError's dedupe.
//
// A file-level count cannot see site-level progress: clearing 8
// `argument type mismatch` sites in one file moves nothing when the file still
// carries two blockers.
//
// WHY THE ITEM AND NOT THE POSITION. Collapsing to (file, line, col) merges two
// DIFFERENT constructs refusing at one node into one reading, and those are two
// obstacles owned by two pieces of work.
//
// The two counts still differ across a POPULATION, because one source position
// is reported once per UNIT that reaches it: entry programs that share sibling
// files each carry those siblings' refusals. So both are reported (siteCensus):
// the ITEM count is the ACCOUNT — how much refusal the population's units
// carry, and the thing a partial fix moves — and the POSITION count is the
// closer proxy for WORK. Their difference is the report's DUPLICATIVE mode.
//
// WHY THE OPERAND IS IN THE TUPLE. Several files can share one blocker key
// while carrying different operands under it, and a fix addresses operands,
// not keys. SitesByConstruct's operand column says "S sites over D distinct
// operands".
//
// SHADOWS, AND WHY THE TOTAL AND THE PER-KEY ROW ARE BOTH REPORTED. A key's row
// can go to zero without any refusal being lowered, because the positions it
// named get RE-KEYED to a neighbouring guard. Sites() over the whole
// population is invariant under re-keying and the per-key row is not, so
//
//	shadows = (sites the key's row lost) - (sites the total lost)
//
// is computable from two readings and is the discriminator. Neither number
// alone is.
//
// WHAT A SITE COUNT DOES NOT CAPTURE, and none of these is a rounding error:
//
//   - MASKED POSITIONS. A blocker the builder declined to name is not an Item,
//     so Sites() is a LOWER bound in exactly the way Constructs() is, and
//     Masked() is its error bar. Un-masking ADDS sites, so a real fix can clear
//     8 sites, reveal 21 that were hidden behind them, and read as +13. Sites
//     and masked must be quoted together or the sign is not readable.
//   - WORK. Two sites are not twice one site's work. One fix clears both halves
//     of a DUPLICATIVE pair, and one operand can carry many positions.
//   - PAYOFF. A site cleared is not a test case run. Only `matched` says that,
//     and it says it about a whole file.
//   - REACHABILITY. An item under an already-refused parent is a site here;
//     whether generation would reach that position once the parent lowers is
//     not decided by this count.
func (e *UnsupportedError) Sites() int { return len(e.Items) }

// SiteCount is one construct's refusal accounting at the two granularities that
// answer different questions: how many places refuse under this name, and how
// many DIFFERENT things they refuse.
type SiteCount struct {
	// Sites is the number of refusal items carrying this construct.
	Sites int
	// Operands is the number of distinct Detail strings among them. A key with
	// many sites and one operand is one gap repeated; a key with many operands
	// is a family, and a fix that names two of them finishes two of them.
	Operands int
}

// SitesByConstruct is the per-key half of the reading. Read it BESIDE Sites():
// see the shadow note above for why neither is sound alone.
func (e *UnsupportedError) SitesByConstruct() map[string]SiteCount {
	if len(e.Items) == 0 {
		return nil
	}
	operands := make(map[string]map[string]bool, 4)
	out := make(map[string]SiteCount, 4)
	for _, it := range e.Items {
		row := out[it.Construct]
		row.Sites++
		seen := operands[it.Construct]
		if seen == nil {
			seen = map[string]bool{}
			operands[it.Construct] = seen
		}
		if !seen[it.Detail] {
			seen[it.Detail] = true
			row.Operands++
		}
		out[it.Construct] = row
	}
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
func nodePos(n ast.Node) (line, col int) {
	if n == nil {
		return 0, 0
	}
	line = n.LineNum()
	v := reflect.Indirect(reflect.ValueOf(n))
	if v.Kind() != reflect.Struct {
		return line, 0
	}
	if f := v.FieldByName("Col"); f.IsValid() && f.Kind() == reflect.Int {
		col = int(f.Int())
	}
	return line, col
}

// isNilNode reports whether n carries nothing — a nil interface, or a typed nil
// pointer, which is what an absent optional child (`fd.Body`, `t.Else`) looks
// like once it is stored in an ast.Node field.
func isNilNode(n ast.Node) bool {
	if n == nil {
		return true
	}
	v := reflect.ValueOf(n)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

// childNodes returns n's child AST nodes in field-declaration order, which is
// source order closely enough for a report.
//
// Reflection for the same reason nodePos uses it, and here the stakes are
// higher: the alternative is a switch over every node type in the language
// whose only failure mode is SILENT. A kind somebody forgets to add would
// return no children, and every blocker inside it would vanish from the tally
// — which is precisely the truncation this walk exists to fix. Cost is
// irrelevant: it runs only for files that are already being skipped.
func childNodes(n ast.Node) []ast.Node {
	if isNilNode(n) {
		return nil
	}
	v := reflect.Indirect(reflect.ValueOf(n))
	if v.Kind() != reflect.Struct {
		return nil
	}
	var out []ast.Node
	collectChildFields(v, &out)
	return out
}

var triviaCarrierType = reflect.TypeOf(ast.TriviaCarrier{})

func collectChildFields(v reflect.Value, out *[]ast.Node) {
	t := v.Type()
	for i := range t.NumField() {
		if !t.Field(i).IsExported() {
			continue
		}
		collectChildValue(v.Field(i), out)
	}
}

// collectChildValue descends one field. Nodes reached through plain structs and
// slices count as children: an ast.Param's default value and an ast.CaseBranch's
// guard and body are expressions in their own right, and the struct they are
// stored in is a container, not a construct.
func collectChildValue(v reflect.Value, out *[]ast.Node) {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		if n, ok := v.Interface().(ast.Node); ok {
			appendChildNode(n, out)
			return
		}
		collectChildValue(v.Elem(), out)
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		if n, ok := v.Interface().(ast.Node); ok {
			appendChildNode(n, out)
			return
		}
		collectChildValue(v.Elem(), out)
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			collectChildValue(v.Index(i), out)
		}
	case reflect.Struct:
		if v.Type() == triviaCarrierType {
			// Comments and blank lines. No behaviour, and every node embeds
			// one, so skipping it by type also keeps this walk off a field
			// that can never contribute.
			return
		}
		// A struct VALUE can still be a node: attached tests are stored as
		// []ast.AttachedTest, and their methods have pointer receivers.
		if v.CanAddr() {
			if n, ok := v.Addr().Interface().(ast.Node); ok {
				appendChildNode(n, out)
				return
			}
		}
		collectChildFields(v, out)
	}
}

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
	"Then":            "then stage",
	"CodepointLit":    "codepoint literal",
	"DecimalLit":      "decimal literal",
	// Decorators reach the tally two ways — funcDecl refuses a decorated
	// function by name, and a decorator hanging off a struct or enum is
	// reached as a child of that rejected declaration — so both must land on
	// one key or the report would double-count one gap.
	"Decorator":           "decorator",
	"Defer":               "defer",
	"DistinctDestructure": "distinct destructuring",
	"DotVariant":          "dot variant",
	"EnumDef":             "enum declaration",
	"ExternFunc":          "host fn declaration",
	"ExternPackage":       "go import",
	"ExternType":          "host type declaration",
	"FieldAccess":         "field access",
	"FieldAccessor":       "field accessor",
	// A FuncDef only reaches this map from a nested position: a top-level one
	// goes to funcDecl, which names its own reasons.
	"FuncDef": "nested function definition",
	"GoBlock": "inline go block",
	// `If` is reached only as a bare pipe stage (`x |> if { … }`): ifInto owns
	// every other position, so an unmapped NodeType would surface only there.
	"If":              "if expression",
	"ImplBlock":       "impl block",
	"ImplConformance": "impl conformance",
	"InterfaceDef":    "interface declaration",
	// An interface's members are reached only as children of a refused
	// InterfaceDef, but they are not internals in the way a pattern kind is: a
	// `field` requirement and a `variant` requirement are separate features
	// with separate lowerings, so they earn separate tally keys rather than
	// falling through to a bare NodeType.
	"InterfaceField":     "interface field requirement",
	"InterfaceMethod":    "interface method requirement",
	"Lambda":             "lambda",
	"ListLit":            "list literal",
	"ListSpreadLit":      "list spread literal",
	"MapDestructure":     "map destructuring",
	"MapLit":             "map literal",
	"NamedArg":           "named argument",
	"OnceBinding":        "once binding",
	"PatternBinding":     "pattern binding",
	"BindingElse":        "binding else",
	"PatternDestructure": "pattern destructuring",
	"Placeholder":        "placeholder",
	"RangeLit":           "range literal",
	"SetLit":             "set literal",
	"StructDef":          "struct declaration",
	"StructDestructure":  "struct destructuring",
	"StructLit":          "struct literal",
	"TaggedString":       "typed literal",
	"TestDecl":           "test declaration",
	"TryOp":              "try",
	"TupleDestructure":   "tuple destructuring",
	"TupleLit":           "tuple literal",
	"TypeAlias":          "type alias",
	"TypeDef":            "type declaration",
	"VectorLit":          "vector literal",
}

func constructName(n ast.Node) string {
	t := n.NodeType()
	if name, ok := constructNames[t]; ok {
		return name
	}
	return t
}
