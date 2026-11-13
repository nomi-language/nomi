// Package htmltemplate reads the text of an `Html` literal (spec §18
// *HTML templates*) as an HTML template: it checks the markup, finds the
// context of each `${...}` slot, and reports what it cannot accept.
//
// The template language is HTML with stricter rules. Every non-void element
// has an explicit end tag; a `/>` closes only a void element or an element
// inside `<svg>` or `<math>`; an element name is an HTML, SVG or MathML name
// or a custom element's, with a hyphen. Attribute values may be quoted with
// `"` or `'`, and the two quotes mean the same.
//
// A slot's context decides which std/html function receives its value, and
// so which type the value must have. The contexts follow Go's html/template
// (stateText, stateRCDATA, stateAttr, stateURL, stateCSS), found once, when
// the program is checked, rather than at a template's first execution.
//
// The package knows nothing of Nomi's AST. The front end passes the static
// text around the slots and receives the slots' contexts, the static text
// to emit (an optional attribute's name moves into its slot), and errors at
// positions in that text.
//
// # How the front end uses it
//
// internal/analysis's html_template.go is the caller, and its file comment
// shows the expansion. An `Html` literal expands where its tag resolves,
// not per declaration: the builder's walk of a TaggedString asks whether
// the tag names std/html's `Html` (the tag symbol's declaration node is
// std/html's, or in std/html the file's own). Its errors become the file's
// type errors at their character: a part's first line starts after the
// opening quote or the previous slot's `}`, later lines at the literal's
// BodyCol (the column a triple-quoted body starts at once indentation is
// stripped, the lexer's Indent on the opening token). A slot's error is at
// its expression.
//
// A clean literal gets `html$.assemble([...])`, each slot a call of the
// std/html function its context chose (`slot_text`, `slot_attribute`,
// `slot_url_start`, ...), whose parameter's interface is the slot's type
// rule. The expansion is kept in an unexported field of the TaggedString
// (Template), so a tree walk visits the slot expressions once, under
// Parts, and a file's interface never holds it. The builder walks the
// expansion in the literal's place, the checker checks it, and the IR
// builder lowers it. Resolutions are keyed by position, so added nodes sit
// on characters no other node uses (the quotes and each slot's `${`); those
// positions go in FileAnalysis.StandIns, as a membership test's `in` does,
// so semantic tokens, rename, find-references and document highlights skip
// them (StandInReference). A token there would be as long as the
// function's name and paint over the slot's code.
//
// # Slot choice
//
// Element content takes a `ToHtml` or another `Display`, which one
// parameter cannot say, so the checker checks each `slot_text` argument
// first (chooseHtmlTextSlots). A type that is `Display` and not `ToHtml`,
// and is not std's `Maybe`, `Result`, a collection or a tuple, moves the
// call (and the reference at the callee) to `slot_text_display`. A slot
// whose function takes a `Display` (`slot_attribute`, `slot_rcdata`,
// `slot_url_path`, `slot_url_query`, `slot_url_fragment`) moves to its
// `_maybe` twin when the argument is a `Maybe` of a `Display`, writing
// nothing for `None`; a `Maybe` of a `Maybe` there is an error. `UrlValue`
// and `CssValue` reach the same rule through impls for `Maybe<T>`. The
// argument's type is kept in checkedSlots so the call does not check it
// again. An argument-type error at a slot is rewritten into what the slot
// takes, with help for its context (htmlSlotHelp). A slot value whose type
// is unsolved (a bare `None` in `disabled=${None}`) is solved by the one
// impl of the slot's interface whose receiver it fits
// (unifyInterfaceAgainstConcrete); an impl at an instance of a generic type
// (`impl OptionalAttribute for Maybe<String>`) records its receiver
// (recordImplTypeArgsFromBlock), which also refuses a `Maybe<Int>`.
//
// # Imports
//
// A file importing std/html gains `import std::html as html$`
// (withHtmlImport), so `html$` reaches std/html's private slot functions
// as `phrase$` reaches std/phrase's; another std file gains `import html as
// html$`, in std's spelling (std/http's directory listing is a literal). An
// aliased whole-module import moves the module's key in the builder's
// module map to the alias, so the file's own `import html.Html` finds
// std/html among the scopes the file is built against (stdlibScopes, in
// resolveStdlibImport). Without that, a std file built as a program (a
// reference page's editor) would load std/html again from source, with
// fresh copies of std/io whose scopes the IR builder does not know as
// std's.
//
// # Escaping
//
// std/html is a program std module: its types are the program's. Its
// escapers are host functions in the internal std/html/host, over
// rt/html.go, which FuzzTemplateEscaping also calls: a slot value cannot
// change a rendered template's tags.
package htmltemplate
