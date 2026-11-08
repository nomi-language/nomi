package hoverdoc

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
)

// The hover on a keyword: the construct it starts, what it does, and where
// the tour or the spec teaches it. A word that means different things in
// different places (`as` in an import and in a pattern, `case` as an
// expression and as a pipe stage) answers for the place it stands in, and
// some answers name what the place holds: the type `self` stands for, the
// function a `return` leaves, the callback a `break` leaves, what a `pub`
// publishes. A contextual word (`group`, `derive`, `host`, ...) answers only
// where its construct stands; used as a name it gets the name's hover.

// site is where the links of keywordDocs point.
const site = "https://nomi-lang.org"

// existingHover says what a keyword's hover does with the hover the
// analysis already gives its token.
type existingHover int

const (
	// appendKeyword keeps the analysis's hover, which is about something
	// else (the function a `fn` declares, the file a `self` imports), and
	// adds the keyword's text and link below it.
	appendKeyword existingHover = iota
	// typedKeyword keeps the analysis's hover, which explains this keyword
	// with the types at hand (`if: Bool -> Int`, a lambda's type and where
	// its parameter types come from), and adds the link.
	typedKeyword
	// ownKeyword ignores the analysis's hover.
	ownKeyword
)

// keywordDoc is the hover of one keyword in one context.
type keywordDoc struct {
	word string
	// context names the place the word stands in, "" for its only or main
	// use. keywordContext answers it.
	context string
	// form is the construct, shown as Nomi code.
	form string
	// text says what the construct does, in one to three sentences.
	text string
	// link is the page that teaches it on site, a tour chapter or a spec
	// chapter, with its anchor.
	link     string
	existing existingHover
}

// keywordDocs is every keyword's hover. TestKeywordDocsCoverTheLexer holds
// it to the lexer's reserved and contextual words, and
// TestKeywordHoverLinksResolve (cmd/nomi-docgen) holds each link to a page
// and anchor the site has.
var keywordDocs = []keywordDoc{
	{word: "and", form: "a and b",
		text: "Logical and of two `Bool`s. The right operand runs only when the left is `True`.",
		link: "/spec/expressions/#logical-operators", existing: typedKeyword},
	{word: "as", context: "import", form: "import path::file as name\nimport path::file.Item as Name",
		text: "Binds the imported file or item under another name. The alias is the import's short name; the original name is not bound.",
		link: "/modules-and-imports/#import-syntax"},
	{word: "as", context: "gopkg", form: `gopkg "path" as alias`,
		text: "Names the Go package handle, which this file's `go alias.Symbol` bindings use.",
		link: "/calling-go/#binding-the-go-standard-library"},
	{word: "as", form: "Pattern as name",
		text: "Matches `Pattern` and binds the whole value it matched to `name`. It never changes what matches, and binds looser than every other pattern construct, `or` included.",
		link: "/pattern-matching/#naming-the-whole-match-with-as"},
	{word: "assert", form: "assert expr\nassert Pattern = value",
		text: "Requires `expr` to hold: `True`, `Ok(_)`, `Some(_)`, or an `Assertable` with no failure. A failure leaves the nearest test or `Result`-returning function with `Err(AssertionFailure)`.",
		link: "/testing/#assertions", existing: typedKeyword},
	{word: "break", form: "break\nbreak value",
		text: "Stops the iteration this callback runs in, with what has accumulated so far; `break value` first takes `value` as this element's result. It works only in a callback of an iteration function that takes it, such as `Iter.loop`, `Iter.map` or `Iter.reduce`.",
		link: "/iteration-and-loops/#break-and-continue-in-iter-callbacks"},
	{word: "case", form: "case value {\n    Pattern -> body\n}",
		text: "Matches the value against each arm's pattern in order and has the value of the first arm that matches. The arms must be exhaustive. With no value, `case { condition -> body }` runs the first arm whose condition is `True`.",
		link: "/pattern-matching/#binding-patterns-and-guards", existing: typedKeyword},
	{word: "case", context: "stage", form: "value |> case {\n    Pattern -> body\n}",
		text: "A pipe stage: matches the piped value against the arms. Written before a call stage (`|> case parse_id() {`), it matches that call's result.",
		link: "/pipes/#other-stages"},
	{word: "continue", form: "continue",
		text: "Skips the rest of this element or round, with the loop state or accumulator unchanged. It takes no value, and works only in a callback of an iteration function that takes it.",
		link: "/iteration-and-loops/#break-and-continue-in-iter-callbacks"},
	{word: "dbg", form: "dbg expr",
		text: "Prints a Debug rendering of `expr` with its source line and text, and has the value of `expr`. `nomi build` refuses a program that holds one.",
		link: "/spec/expressions/#dbg"},
	{word: "dbg", context: "stage", form: "value |> dbg",
		text: "A pipe stage: prints the piped value's Debug rendering with its source line, and passes the value on. `nomi build` refuses a program that holds one.",
		link: "/pipes/#other-stages"},
	{word: "defer", form: "defer call(args)",
		text: "Registers a call to run when the enclosing block ends, however it ends; the most recently registered runs first. The function and its arguments are evaluated where the `defer` stands, and the call returns `Unit`.",
		link: "/app-fields/#defer-runs-block-scoped-cleanup"},
	{word: "else", form: "if condition {\n    ...\n} else {\n    ...\n}",
		text: "The branch that runs when the `if` condition is `False`, or its pattern does not match. `else if` chains another condition.",
		link: "/bindings-and-expressions/#ifelse-and-case-are-expressions"},
	{word: "else", context: "binding", form: "Pattern = value else {\n    ...\n}",
		text: "Runs when the value does not match the binding's pattern; on a match, the pattern's names are bound for the rest of the block. Every path through it leaves, or produces a fallback that stands in for the one variant's payload.",
		link: "/pattern-matching/#binding-with-else"},
	{word: "enum", form: "enum Shape {\n    Circle {r: Float}\n    Point\n}",
		text: "Declares a type whose value is one of its variants. A variant has no payload, one payload type, a tuple or named fields, and `embeds T` makes a struct or distinct type a variant.",
		link: "/structs-enums-distinct/#enums"},
	{word: "export", form: "export fn name(params) -> Result { body }",
		text: "Declares a top-level function that a host calls by name, such as a web page or a WASI runtime. Its arguments and result cross as JSON, through `FromJson` and `ToJson`.",
		link: "/building-and-shipping/#calling-nomi-from-a-page"},
	{word: "extern", form: "host fn name(params) -> Result",
		text: "`extern` is reserved and starts nothing. A declaration implemented outside Nomi is written `host fn` or `host type`, and a Go package handle `gopkg`.",
		link: "/spec/lexical-structure/#keywords"},
	{word: "fn", form: "fn name(params) -> Result { body }",
		text: "Declares a function. Every parameter has a type, the result type is written unless it is `Unit`, and the body's last expression is the result.",
		link: "/functions-and-lambdas/#defining-a-function"},
	{word: "fn", context: "lambda", form: "fn params -> body",
		text: "A lambda, a function with no name. Its parameters are written without parentheses, and their types may come from the function type the position expects.",
		link: "/functions-and-lambdas/#lambdas", existing: typedKeyword},
	{word: "for", form: "impl Interface for Type { ... }",
		text: "Names the type the `impl` implements the interface for.",
		link: "/interfaces-and-dispatch/#defining-and-implementing-an-interface"},
	{word: "for", context: "derive", form: "derive Interface for Type",
		text: "Names the type the `derive` writes the interface's impl for.",
		link: "/interfaces-and-dispatch/#deriving-impls"},
	{word: "if", form: "if condition {\n    ...\n} else {\n    ...\n}",
		text: "An expression that branches on a `Bool`, written without parentheses; `if Pattern = value` branches on a match. Without `else`, a branch with a value makes the `if` a `Maybe`.",
		link: "/bindings-and-expressions/#ifelse-and-case-are-expressions", existing: typedKeyword},
	{word: "if", context: "stage", form: "value |> if f() {\n    ...\n} else {\n    ...\n}",
		text: "A pipe stage: branches on the piped value, a `Bool`. Written before a call stage (`|> if Status.is_ready() {`), it branches on that call's result.",
		link: "/pipes/#other-stages"},
	{word: "impl", form: "impl Interface for Type { ... }\nimpl Type { ... }",
		text: "Implements an interface for a type, its functions written with the type where the interface writes `self`. With no interface it holds functions the type owns, called `Type.f(...)`.",
		link: "/interfaces-and-dispatch/#defining-and-implementing-an-interface", existing: typedKeyword},
	{word: "import", form: "import path::file\nimport path::file.{Item, Other}",
		text: "Brings a file, or items it publishes, into scope. A path-only import binds the file's short name, the last segment of its path.",
		link: "/modules-and-imports/#import-syntax"},
	{word: "in", form: "x in collection",
		text: "Whether the collection holds `x`: `Contains.contains(collection, x)` written as an operator.",
		link: "/spec/expressions/#membership-contains", existing: typedKeyword},
	{word: "interface", form: "interface Name {\n    fn f(value: self) -> Result\n}",
		text: "Declares a contract of functions. `self` in a signature stands for the implementing type, and a function with a body is a default.",
		link: "/interfaces-and-dispatch/#defining-and-implementing-an-interface"},
	{word: "is", form: "value is Pattern",
		text: "A `Bool`: whether the value matches the pattern, as a `case` arm would. It binds nothing.",
		link: "/pattern-matching/#testing-a-match-with-is", existing: typedKeyword},
	{word: "not", form: "not x",
		text: "Logical not of a `Bool`. It binds looser than comparisons, so `not a == b` is `not (a == b)`.",
		link: "/spec/expressions/#logical-operators", existing: typedKeyword},
	{word: "once", form: "once name = expression",
		text: "A value computed the first time it is read and kept for every later read. It stands at file scope or in an inherent `impl` block, and is read like any value, with no call.",
		link: "/bindings-and-expressions/#file-level-values-with-once"},
	{word: "opaque", form: "pub opaque struct Name { ... }",
		text: "Keeps a type's representation private to its file. Other files may name and pass the type, but not construct it, destructure it or read its fields.",
		link: "/modules-and-imports/#opaque-types"},
	{word: "or", form: "a or b",
		text: "Logical or of two `Bool`s. The right operand runs only when the left is `False`.",
		link: "/spec/expressions/#logical-operators", existing: typedKeyword},
	{word: "or", context: "pattern", form: "Pattern or Pattern",
		text: "Matches a value that either alternative matches, tried left to right. Every alternative binds the same names, each at one type.",
		link: "/pattern-matching/#matching-alternatives-with-or"},
	{word: "pub", form: "pub fn name(params) -> Result { body }",
		text: "Makes a top-level declaration visible to other files, which reach it through the file's qualifier or import it by name. Without `pub` it is private to its file.",
		link: "/modules-and-imports/#visibility"},
	{word: "pub", context: "import", form: "pub import path::file.{Item, Other}",
		text: "Re-exports every item the import selects, under its local name. Items can be re-exported, a file cannot.",
		link: "/modules-and-imports/#re-exports"},
	{word: "refute", form: "refute expr",
		text: "Requires `expr` to fail: `False`, `Err(_)`, `None`, or an `Assertable` with a failure. Otherwise it leaves the nearest test or `Result`-returning function with `Err(AssertionFailure)`.",
		link: "/testing/#assertions", existing: typedKeyword},
	{word: "return", form: "return value\nreturn",
		text: "Leaves the nearest function or lambda with `value`; a bare `return` leaves a `Unit` one. One in a nested function, a lambda or a `concurrent` block leaves only that.",
		link: "/spec/functions-and-closures/#returning"},
	{word: "self", form: "fn f(value: self) -> Result",
		text: "The type that implements the interface. It is a type, valid only in an interface's signatures: not a value, not a parameter.",
		link: "/interfaces-and-dispatch/#defining-and-implementing-an-interface", existing: ownKeyword},
	{word: "self", context: "import", form: "import path::file.{self, Item}",
		text: "Binds the file itself beside the listed items, or, after a type (`Bool.{self, True}`), the type.",
		link: "/modules-and-imports/#import-syntax"},
	{word: "struct", form: "struct Name {\n    field: Type\n}",
		text: "Declares a record type with named fields, one per line, each with an optional default. A literal builds one: `Name{field: value}`.",
		link: "/structs-enums-distinct/#structs"},
	{word: "tap", form: "value |> tap fn v -> body",
		text: "A pipe stage that runs the function on the piped value for its effect and passes the value on unchanged.",
		link: "/pipes/#the-tap-stage", existing: typedKeyword},
	{word: "test", form: "test \"name\" { ... }",
		text: "Declares a test, which runs only under `nomi test`. Its body holds at least one `assert`, `refute` or `subtests`.",
		link: "/testing/", existing: typedKeyword},
	{word: "then", form: "value |> then fn v -> body",
		text: "A pipe stage that applies the lambda or capture to the piped value, as `(fn v -> body)(value)` would.",
		link: "/pipes/#the-then-stage", existing: typedKeyword},
	{word: "todo", form: "todo\ntodo \"reason\"",
		text: "A placeholder for code not written yet. It has the type its position expects and traps when reached, and `nomi build` refuses a program that holds one.",
		link: "/functions-and-lambdas/#unwritten-code"},
	{word: "type", form: "type Name Inner",
		text: "Declares a distinct type: a new nominal type over its representation, which is a tuple, a generic or single type, a function type, or nothing for a zero-sized type. `Name(value)` wraps a value and the pattern `Name(x)` unwraps one.",
		link: "/structs-enums-distinct/#distinct-types"},
	{word: "type", context: "host", form: "host type Name",
		text: "Declares a type implemented outside Nomi, with no visible fields or body. Its operations are `host fn` declarations or functions in `impl` blocks.",
		link: "/spec/host-declarations-and-go-bindings/#host-and-foreign-declarations"},
	{word: "type", context: "go", form: "opaque type Name go alias.Type",
		text: "Binds a Go type to a Nomi name, from the package a `gopkg` names `alias`. It is opaque: it can be named and passed, but not constructed, unwrapped or inspected.",
		link: "/calling-go/#binding-the-go-standard-library"},
	{word: "typealias", form: "typealias Name Target",
		text: "Makes `Name` another spelling of `Target` in type positions. The two are one type.",
		link: "/structs-enums-distinct/#type-aliases"},
	{word: "unreachable", form: "unreachable \"reason\"",
		text: "Claims the program never reaches this point; reaching it traps. Unlike `todo` it always leaves its block, and `nomi build` accepts it.",
		link: "/functions-and-lambdas/#code-that-cannot-be-reached"},
	{word: "when", form: "Pattern when condition -> body",
		text: "A guard: the arm runs when its pattern matches and the condition is `True`, and matching goes on to the next arm otherwise. An arm with a guard never counts toward exhaustiveness.",
		link: "/pattern-matching/#binding-patterns-and-guards"},
	{word: "where", form: "fn f<T>(x: T) -> Result where T: Interface",
		text: "Requires type parameters to implement interfaces. It follows a function's result, an `impl` header or a `derive`, and the bound is checked at each call.",
		link: "/interfaces-and-dispatch/#interface-bounded-generics"},
	{word: "where", context: "self", form: "fn f(value: self) -> Result where self: Interface { ... }",
		text: "On an interface's default, names another interface the default needs of a `self` value. An implementor that lacks it writes the function itself.",
		link: "/interfaces-and-dispatch/#defaults-that-need-another-interface"},
	{word: "with", form: "with App.field = value",
		text: "Replaces one application field from this line to the end of the enclosing block. Every function called there reads the replacement, and the previous value comes back however the block exits.",
		link: "/app-fields/#with-replaces-a-field-for-the-rest-of-the-block"},
	{word: "with", context: "derive", form: "derive Interface for Type with Options{...}",
		text: "Passes options to the derive, such as `ToJson.Options{rename_all: Json.Case.Camel}`. It comes before a `where`.",
		link: "/interfaces-and-dispatch/#deriving-impls"},

	// Contextual words.
	{word: "boot", form: "boot server.boot(startup)",
		text: "Calls an entry file's `fn boot` before each test of the group, publishing the application value the tests read.",
		link: "/testing/#groups-and-setup"},
	{word: "clock", form: "clock Clock.Virtual",
		text: "Selects the clock the group's tests run under, `Clock.System` by default. Under `Clock.Virtual`, time moves only when every task in the test is blocked.",
		link: "/testing/#groups-and-setup"},
	{word: "concurrent", form: "concurrent { ... }",
		text: "A block that opens a task scope: every task started in it has finished or been cancelled before the block's value is produced. `return`, `?`, `break` and `continue` leave the block, not the function.",
		link: "/concurrency/#concurrent-taskspawn-taskawait"},
	{word: "derive", form: "derive Interface for Type",
		text: "Asks the compiler to write `Type`'s impl of `Interface` from its fields or variants. It sits in the file of the type's declaration.",
		link: "/interfaces-and-dispatch/#deriving-impls"},
	{word: "embed", form: "embed name `pattern ...`",
		text: "Names files the program carries inside itself, which the compiler finds when it compiles the program. `name` is a `Files` value, read like a `once`.",
		link: "/building-and-shipping/#executables"},
	{word: "embeds", form: "enum Name {\n    embeds Type\n}",
		text: "Makes a struct or distinct type, declared on its own, a variant of this enum. A value of the embedded type is accepted wherever the enum is expected.",
		link: "/structs-enums-distinct/#embedding-existing-types"},
	{word: "go", form: "fn name(params) -> Result go alias.Symbol",
		text: "Binds an exported Go function to this Nomi name and signature, from the package a `gopkg` names `alias`.",
		link: "/calling-go/#binding-the-go-standard-library"},
	{word: "go", context: "type", form: "opaque type Name go alias.Type",
		text: "Binds a Go type to this Nomi name, from the package a `gopkg` names `alias`. It can be named and passed, but not constructed, unwrapped or inspected.",
		link: "/calling-go/#binding-the-go-standard-library"},
	{word: "gopkg", form: `gopkg "path" as alias`,
		text: "Declares a handle for a Go package, resolved by Go's module rules, which this file's `go alias.Symbol` bindings name.",
		link: "/calling-go/#binding-the-go-standard-library"},
	{word: "group", form: "group \"name\" { ... }",
		text: "Holds tests that share a `clock`, a `boot` or a `setup`. Each test still starts fresh.",
		link: "/testing/#groups-and-setup", existing: typedKeyword},
	{word: "host", form: "host fn name(params) -> Result",
		text: "Declares a function with no body, implemented outside Nomi: by the runtime for the standard library, or by the host table of a Go program that embeds Nomi.",
		link: "/spec/host-declarations-and-go-bindings/#host-and-foreign-declarations"},
	{word: "host", context: "type", form: "host type Name",
		text: "Declares a type implemented outside Nomi, with no visible fields or body. Its operations are `host fn` declarations or functions in `impl` blocks.",
		link: "/spec/host-declarations-and-go-bindings/#host-and-foreign-declarations"},
	{word: "noun", form: "noun name `MessageFormat 2 text`",
		text: "Declares a noun that phrases agree with, a `Noun` value. Its body is its form in the source locale, over an implicit `$count: Int`.",
		link: "/translating-text/#nouns"},
	{word: "open", form: "open fn name(value: self) -> Result { body }",
		text: "An open default of an interface: every implementor gets it, and an impl may write its own. A default without `open` is final.",
		link: "/spec/interfaces-implementations-and-derive/#item-kinds"},
	{word: "phrase", form: "phrase name(params) `MessageFormat 2 text`",
		text: "Declares text a person reads, in the source locale, translated in catalogs. It declares a function of the same name that returns a `Phrase`.",
		link: "/translating-text/#declaring-a-phrase"},
	{word: "setup", form: "setup expr",
		text: "Produces the value each test of the group binds with the pattern after its name. It runs before each test, after the group's boot.",
		link: "/testing/#groups-and-setup", existing: typedKeyword},
	{word: "subtests", form: "subtests rows, pattern { body }",
		text: "Runs the body once per row, in order, with `pattern` bound to the row. A failed row does not stop the next one.",
		link: "/testing/#subtests", existing: typedKeyword},
}

// keywordDocFor is the hover of word in context, falling back to its main
// use.
func keywordDocFor(word, context string) (keywordDoc, bool) {
	var main keywordDoc
	found := false
	for _, d := range keywordDocs {
		if d.word != word {
			continue
		}
		if d.context == context {
			return d, true
		}
		if d.context == "" {
			main, found = d, true
		}
	}
	return main, found
}

// KeywordDocLinks are the pages keyword hovers link to, as paths on the
// site, each once.
func KeywordDocLinks() []string {
	seen := map[string]bool{}
	var links []string
	for _, d := range keywordDocs {
		if !seen[d.link] {
			seen[d.link] = true
			links = append(links, d.link)
		}
	}
	return links
}

// KeywordAt answers the hover on the keyword at pos: its markdown, and the
// keyword token's start and length. ok is false when no keyword stands
// there, a contextual word used as a name included.
func KeywordAt(fa *analysis.FileAnalysis, nodes []ast.Node, content string, pos analysis.Pos) (markdown string, start analysis.Pos, length int, ok bool) {
	if fa == nil || !keywordWord(wordAt(content, pos)) {
		return "", analysis.Pos{}, 0, false
	}
	toks := lexer.Lex(content)
	i := keywordTokenAt(toks, pos)
	if i < 0 {
		return "", analysis.Pos{}, 0, false
	}
	tok := toks[i]
	start = analysis.Pos{Line: tok.Line, Col: tok.Col}
	k := keywordSite{fa: fa, nodes: nodes, toks: toks, i: i, at: start}
	k.path = pathTo(nodes, start)
	context, extra, isKeyword := k.context()
	if !isKeyword {
		return "", analysis.Pos{}, 0, false
	}
	doc, found := keywordDocFor(tok.Lexeme, context)
	if !found {
		return "", analysis.Pos{}, 0, false
	}
	if k.form != "" {
		doc.form = k.form
	}
	return renderKeyword(doc, extra, k.analysisHover()), start, len(tok.Lexeme), true
}

// renderKeyword is a keyword's hover: its form, text and the context's
// extra sentence, then its link, or the analysis's own hover with what
// the keyword adds to it.
func renderKeyword(d keywordDoc, extra, existing string) string {
	link := "More in the [tour](" + site + d.link + ")."
	if strings.HasPrefix(d.link, "/spec/") {
		link = "More in the [spec](" + site + d.link + ")."
	}
	text := d.text
	if extra != "" {
		text += " " + extra
	}
	if existing != "" && d.existing != ownKeyword {
		if d.existing == typedKeyword {
			return existing + "\n\n" + link
		}
		return existing + "\n\n---\n\n" + text + "\n\n" + link
	}
	return "```nomi\n" + d.form + "\n```\n\n" + text + "\n\n" + link
}

// wordAt is the run of identifier bytes around pos on its line, which
// tells a hover that can be no keyword's apart before the file is lexed.
func wordAt(content string, pos analysis.Pos) string {
	line := content
	for n := 1; n < pos.Line; n++ {
		i := strings.IndexByte(line, '\n')
		if i < 0 {
			return ""
		}
		line = line[i+1:]
	}
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	at := pos.Col - 1
	if at < 0 || at >= len(line) {
		return ""
	}
	isWord := func(b byte) bool {
		return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
	}
	start, end := at, at
	for start > 0 && isWord(line[start-1]) {
		start--
	}
	for end < len(line) && isWord(line[end]) {
		end++
	}
	return line[start:end]
}

// keywordWord reports whether w is a reserved or contextual word.
func keywordWord(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range lexer.ReservedWords() {
		if w == r {
			return true
		}
	}
	return contextualWord(w)
}

// keywordTokenAt is the index of the reserved or contextual word under
// pos, or -1.
func keywordTokenAt(toks []token.Token, pos analysis.Pos) int {
	for i, t := range toks {
		if t.Line != pos.Line || pos.Col < t.Col || pos.Col >= t.Col+len(t.Lexeme) {
			continue
		}
		if isReservedToken(t) || (t.Type == token.IDENT && contextualWord(t.Lexeme)) {
			return i
		}
		return -1
	}
	return -1
}

func isReservedToken(t token.Token) bool {
	if t.Type == token.IDENT || t.Type == token.TYPE_IDENT {
		return false
	}
	for _, w := range lexer.ReservedWords() {
		if t.Lexeme == w {
			return true
		}
	}
	return false
}

func contextualWord(w string) bool {
	for _, c := range lexer.ContextualWords() {
		if w == c {
			return true
		}
	}
	return false
}

// keywordSite is a keyword token and what surrounds it.
type keywordSite struct {
	fa    *analysis.FileAnalysis
	nodes []ast.Node
	toks  []token.Token
	i     int
	at    analysis.Pos
	// path holds the nodes whose extent holds the keyword, outermost first.
	path []ast.Node
	// form, when set, replaces the table's form with the construct as
	// this file writes it.
	form string
}

func (k *keywordSite) word() string { return k.toks[k.i].Lexeme }

// prev and next are the tokens before and after the keyword, skipping
// line breaks and comments; a zero token past either end.
func (k *keywordSite) prev() token.Token {
	for j := k.i - 1; j >= 0; j-- {
		if !skippable(k.toks[j]) {
			return k.toks[j]
		}
	}
	return token.Token{}
}

func (k *keywordSite) next(n int) token.Token {
	for j := k.i + 1; j < len(k.toks); j++ {
		if skippable(k.toks[j]) {
			continue
		}
		if n--; n == 0 {
			return k.toks[j]
		}
	}
	return token.Token{}
}

func skippable(t token.Token) bool {
	switch t.Type {
	case token.NEWLINE, token.COMMENT, token.DOC_COMMENT, token.BLANK_LINE, token.TEST_PROMPT:
		return true
	}
	return false
}

// lineStart is the first token of the keyword's line, after a `pub`.
func (k *keywordSite) lineStart() token.Token {
	j := k.i
	for j > 0 && k.toks[j-1].Line == k.toks[k.i].Line && k.toks[j-1].Type != token.SEMICOLON {
		j--
	}
	if k.toks[j].Type == token.PUB && j < k.i {
		j++
	}
	return k.toks[j]
}

// startsLine reports whether the keyword is the first token of its line
// or follows a `;` or a `pub`.
func (k *keywordSite) startsLine() bool {
	return k.lineStart().Col == k.at.Col
}

// innermost is the innermost node of the path that match accepts, or
// nil.
func (k *keywordSite) innermost(match func(ast.Node) bool) ast.Node {
	for j := len(k.path) - 1; j >= 0; j-- {
		if match(k.path[j]) {
			return k.path[j]
		}
	}
	return nil
}

// declaredAt reports whether a node of the file declares its construct
// with the keyword at the keyword's position.
func (k *keywordSite) declaredAt(match func(ast.Node) (line, col int)) bool {
	found := false
	for _, n := range k.nodes {
		ast.Inspect(n, func(c ast.Node) bool {
			if found || c == nil {
				return false
			}
			if l, col := match(c); l == k.at.Line && col == k.at.Col {
				found = true
			}
			return true
		})
	}
	return found
}

// context answers the place the keyword stands in, a sentence that place
// adds, and whether the word is a keyword there at all.
func (k *keywordSite) context() (context, extra string, isKeyword bool) {
	switch w := k.word(); w {
	case "as":
		switch k.innermost(func(n ast.Node) bool {
			return is[*ast.AsPattern](n) || is[*ast.ImportStmt](n) || is[*ast.ImportBlock](n) || is[*ast.ExternPackage](n)
		}).(type) {
		case *ast.ImportStmt, *ast.ImportBlock:
			return "import", "", true
		case *ast.ExternPackage:
			return "gopkg", "", true
		}
		return "", "", true
	case "case", "if", "dbg":
		if k.prev().Type == token.PIPE {
			return "stage", "", true
		}
		return "", "", true
	case "else":
		if k.declaredAt(func(n ast.Node) (int, int) {
			if e, ok := n.(*ast.BindingElse); ok {
				return e.Line, e.Col
			}
			return 0, 0
		}) {
			return "binding", "", true
		}
		return "", "", true
	case "fn":
		for _, n := range k.path {
			if l, ok := n.(*ast.Lambda); ok && l.Span.StartLine == k.at.Line && l.Span.StartCol == k.at.Col {
				return "lambda", "", true
			}
		}
		return "", "", true
	case "for", "with":
		if s := k.lineStart(); s.Type == token.IDENT && s.Lexeme == "derive" {
			return "derive", "", true
		}
		return "", "", true
	case "or":
		if _, ok := k.innermost(func(n ast.Node) bool { return is[*ast.OrPattern](n) || is[*ast.Binary](n) }).(*ast.OrPattern); ok {
			return "pattern", "", true
		}
		return "", "", true
	case "pub":
		return k.pubContext()
	case "return":
		return "", k.returnExtra(), true
	case "break", "continue":
		return "", k.loopExtra(), true
	case "self":
		return k.selfContext()
	case "type":
		if p := k.prev(); p.Type == token.IDENT && p.Lexeme == "host" && p.Line == k.at.Line {
			return "host", "", true
		}
		if k.goBindingOnLine() {
			return "go", "", true
		}
		return "", "", true
	case "where":
		if k.next(1).Type == token.SELF {
			return "self", "", true
		}
		return "", "", true
	case "host":
		switch k.next(1).Type {
		case token.FN:
			return "", "", true
		case token.TYPE:
			return "type", "", true
		}
		return "", "", false
	case "open":
		n := k.next(1)
		inInterface := k.innermost(is[*ast.InterfaceDef]) != nil
		return "", "", inInterface && (n.Type == token.FN || (n.Type == token.IDENT && n.Lexeme == "host"))
	case "embeds":
		return "", "", k.startsLine() && k.innermost(is[*ast.EnumDef]) != nil
	case "derive":
		n := k.next(1)
		return "", "", k.startsLine() && n.Type == token.TYPE_IDENT && n.Line == k.at.Line
	case "phrase", "noun":
		return "", "", k.declaredAt(func(n ast.Node) (int, int) {
			if d, ok := n.(*ast.KeywordDecl); ok && d.Keyword == w {
				return d.Line, d.Col
			}
			return 0, 0
		})
	case "embed":
		return "", "", k.declaredAt(func(n ast.Node) (int, int) {
			if d, ok := n.(*ast.EmbedDecl); ok {
				return d.Line, d.Col
			}
			return 0, 0
		})
	case "gopkg":
		return "", "", k.declaredAt(func(n ast.Node) (int, int) {
			if d, ok := n.(*ast.ExternPackage); ok {
				return d.Line, d.Col
			}
			return 0, 0
		})
	case "go":
		return k.goContext()
	case "concurrent":
		return "", "", k.declaredAt(func(n ast.Node) (int, int) {
			if d, ok := n.(*ast.ConcurrentBlock); ok {
				return d.Line, d.Col
			}
			return 0, 0
		})
	case "subtests":
		return "", "", k.declaredAt(func(n ast.Node) (int, int) {
			if d, ok := n.(*ast.Subtests); ok {
				return d.Line, d.Col
			}
			return 0, 0
		})
	case "group", "clock", "boot", "setup":
		return "", "", k.declaredAt(func(n ast.Node) (int, int) {
			d, ok := n.(*ast.TestDecl)
			if !ok || !d.Group {
				return 0, 0
			}
			switch w {
			case "group":
				return d.Line, d.Col
			case "clock":
				return d.ClockLine, d.ClockCol
			case "boot":
				return d.BootLine, d.BootCol
			}
			return d.SetupLine, d.SetupCol
		})
	}
	return "", "", true
}

// is reports whether n is a T.
func is[T ast.Node](n ast.Node) bool {
	_, ok := n.(T)
	return ok
}

// pubContext names what a `pub` publishes: the declaration after it, or
// the items of a `pub import`.
func (k *keywordSite) pubContext() (string, string, bool) {
	j := 1
	modifiers := ""
	for {
		t := k.next(j)
		if t.Type == token.OPAQUE || (t.Type == token.IDENT && t.Lexeme == "host") {
			modifiers += t.Lexeme + " "
			j++
			continue
		}
		break
	}
	kw := k.next(j)
	if kw.Type == token.IMPORT {
		return "import", "", true
	}
	name := k.next(j + 1)
	if kw.Type != token.IDENT && !isReservedToken(kw) || (name.Type != token.IDENT && name.Type != token.TYPE_IDENT) {
		return "", "", true
	}
	k.form = fmt.Sprintf("pub %s%s %s", modifiers, kw.Lexeme, name.Lexeme)
	return "", fmt.Sprintf("Here it publishes `%s%s %s`.", modifiers, kw.Lexeme, name.Lexeme), true
}

// returnExtra names what a `return` leaves and the type it returns.
func (k *keywordSite) returnExtra() string {
	switch b := k.innermost(isBoundary).(type) {
	case *ast.FuncDef:
		result := "Unit"
		if b.ReturnTypeExpr != nil {
			result = b.ReturnTypeExpr.TypeString()
		}
		return fmt.Sprintf("Here it leaves `fn %s`, which returns `%s`.", b.Name, result)
	case *ast.Lambda:
		if ft, ok := k.fa.ExprTypes[b].(*analysis.FuncType); ok && ft.Return != nil {
			if r := displayValueType(ft.Return); r != "" {
				return fmt.Sprintf("Here it leaves the lambda, which returns `%s`.", r)
			}
		}
		return "Here it leaves the lambda."
	case *ast.ConcurrentBlock:
		return "Here it leaves the `concurrent` block, with the block's value."
	case *ast.TestDecl:
		if b.Group {
			return "Here it ends the group's `setup` with its value."
		}
		return fmt.Sprintf("Here it ends `test %q`, which passes.", b.Name)
	}
	return ""
}

// loopExtra names the callback a `break` or `continue` leaves.
func (k *keywordSite) loopExtra() string {
	for j := len(k.path) - 1; j >= 0; j-- {
		switch b := k.path[j].(type) {
		case *ast.Lambda:
			if callee := k.calleeOf(j); callee != "" {
				return fmt.Sprintf("Here it leaves the callback passed to `%s`.", callee)
			}
			return "Here it leaves this lambda, which must be an iteration callback."
		case *ast.FuncDef:
			return fmt.Sprintf("Here it leaves `fn %s`, which is passed as an iteration callback.", b.Name)
		case *ast.ConcurrentBlock:
			return "Here it stands directly in a `concurrent` block, which no iteration catches it in."
		}
	}
	return ""
}

// calleeOf is the function a call in the path passes the lambda at path
// index j to, as written, or "".
func (k *keywordSite) calleeOf(j int) string {
	lam := k.path[j]
	for c := j - 1; c >= 0; c-- {
		call, ok := k.path[c].(*ast.Call)
		if !ok {
			continue
		}
		for _, a := range call.Args {
			if na, ok := a.(*ast.NamedArg); ok {
				a = na.Value
			}
			if a == lam {
				return calleeName(call.Func)
			}
		}
		return ""
	}
	return ""
}

func calleeName(n ast.Node) string {
	switch f := n.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.TypeIdent:
		return f.Name
	case *ast.FieldAccess:
		if obj := calleeName(f.Object); obj != "" && f.Field != nil {
			return obj + "." + f.Field.Name
		}
	}
	return ""
}

func isBoundary(n ast.Node) bool {
	switch n.(type) {
	case *ast.FuncDef, *ast.Lambda, *ast.ConcurrentBlock, *ast.TestDecl:
		return true
	}
	return false
}

// selfContext names the type `self` stands for: the implementing type in
// an interface, the receiver in an impl, the file or type in an import.
func (k *keywordSite) selfContext() (string, string, bool) {
	switch n := k.innermost(func(n ast.Node) bool {
		return is[*ast.ImportStmt](n) || is[*ast.ImportBlock](n) || is[*ast.InterfaceDef](n) || is[*ast.ImplBlock](n)
	}).(type) {
	case *ast.ImportStmt, *ast.ImportBlock:
		return "import", "", true
	case *ast.InterfaceDef:
		return "", fmt.Sprintf("In `interface %s`, it stands for each type that implements `%s`, and an impl writes that type in its place.", n.Name, n.Name), true
	case *ast.ImplBlock:
		if n.Receiver != nil {
			return "", fmt.Sprintf("In this `impl`, it is `%s`.", n.Receiver.TypeString()), true
		}
	}
	return "", "", true
}

// goContext reports whether a `go` binds a Go function or type: the
// alias of a binding follows it.
func (k *keywordSite) goContext() (string, string, bool) {
	alias, dot := k.next(1), k.next(2)
	if alias.Type != token.IDENT || dot.Type != token.DOT {
		return "", "", false
	}
	context, found := "", false
	for _, n := range k.nodes {
		ast.Inspect(n, func(c ast.Node) bool {
			switch d := c.(type) {
			case *ast.ExternFunc:
				if d.ForeignAliasLine == alias.Line && d.ForeignAliasCol == alias.Col {
					context, found = "", true
				}
			case *ast.ExternType:
				if d.ForeignAliasLine == alias.Line && d.ForeignAliasCol == alias.Col {
					context, found = "type", true
				}
			}
			return !found
		})
	}
	return context, "", found
}

// goBindingOnLine reports whether a `go alias.Type` binding follows the
// keyword on its line.
func (k *keywordSite) goBindingOnLine() bool {
	for j := k.i + 1; j+2 < len(k.toks) && k.toks[j].Line == k.at.Line; j++ {
		t := k.toks[j]
		if t.Type == token.IDENT && t.Lexeme == "go" && k.toks[j+1].Type == token.IDENT && k.toks[j+2].Type == token.DOT {
			return true
		}
	}
	return false
}

// analysisHover is the hover the analysis gives the keyword's token: an
// operator's or a stage's (SyntaxAt), or a symbol's, or "".
func (k *keywordSite) analysisHover() string {
	if content, at, _, ok := SyntaxAt(k.fa, k.nodes, k.at); ok && at == k.at {
		return content
	}
	sym, at, _, ok := k.fa.TokenAt(k.at)
	if !ok || at != k.at {
		return ""
	}
	return RenderForEditor(sym, k.fa)
}

// pathTo is the nodes whose extent holds pos, outermost first. A node the
// parser recorded no extent for is on it when a node below it is.
func pathTo(nodes []ast.Node, pos analysis.Pos) []ast.Node {
	for _, n := range nodes {
		if p := pathFrom(n, pos); p != nil {
			return p
		}
	}
	return nil
}

func pathFrom(n ast.Node, pos analysis.Pos) []ast.Node {
	if n == nil {
		return nil
	}
	sp := spanOf(n)
	if !sp.IsZero() && !spanHolds(sp, pos) {
		return nil
	}
	var below []ast.Node
	ast.Children(n, func(c ast.Node) {
		if below == nil {
			below = pathFrom(c, pos)
		}
	})
	if sp.IsZero() && below == nil {
		return nil
	}
	return append([]ast.Node{n}, below...)
}

func spanHolds(sp ast.Span, pos analysis.Pos) bool {
	if pos.Line < sp.StartLine || pos.Line > sp.EndLine {
		return false
	}
	if pos.Line == sp.StartLine && pos.Col < sp.StartCol {
		return false
	}
	if pos.Line == sp.EndLine && pos.Col > sp.EndCol {
		return false
	}
	return true
}
