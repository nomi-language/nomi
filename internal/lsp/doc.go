// Package lsp is Nomi's language server, the server `nomi-lsp` runs. It
// answers the Language Server Protocol over JSON-RPC from the analyses an
// analysis.DocumentManager keeps: each open document is checked as part of
// a whole program, rechecked incrementally on each edit, and opened from a
// record in the artifact cache when an earlier server checked the same
// files (internal/analysis package doc, "Programs the document manager
// keeps", "Rechecks", "Closed files" and "Opening from a record").
//
// # Requests
//
// The server never applies an edit computed from an analysis older than
// the text it edits. didChange stores the text and returns, and the
// document is analyzed analysisDelay (50 ms) after the last edit
// (docsched.go). Each request has a freshness (requestFreshness, rpc.go).
// Completion and signature help answer at once from the last finished
// analysis, with positions mapped into it, since waiting makes menus
// arrive too late. Hover, definition, implementation, references, symbols,
// semantic tokens and inlay hints wait up to analysisWait (2 s) for the
// current analysis, then answer from the older one. Rename and code
// actions fail with ContentModified rather than answer from an older one.
//
// Checking a std module's bodies (std.StdLib.Bodies, which a process's
// first lowering, an instance of a generic std function and hover's
// examples ask for) writes into the shared stdlib's nodes, analyses and
// scopes, which the document manager's analyses read on other goroutines.
// So the server checks every std module's bodies when it starts
// (StdLib.AllBodies in NewServer, about 0.3 s once per process) and no
// later call writes.
//
// # Annotations a default decides
//
// The "Remove type annotation" and "Add type annotation" actions
// (code_action_default_type.go) ask the default's type alone
// (analysis.FileAnalysis.DefaultTypeAlone): the default checked with no
// expected type, under the inference phase's owner, against a copy of the
// body tables (FileBodies.clone), so the published analysis is untouched.
// Removal is offered when that check reports nothing and leaves no part
// unknown, the type is the written one (analysis.SameDefaultType:
// identical, no `embeds` coercion, printed alike), and the written text is
// that type as the file spells it (analysis.FileAnalysis.SpellType), so an
// alias such as `n: Count = 1` stays.
//
// # MessageFormat 2 in the editor
//
// No editor Nomi supports has an MF2 grammar for the injection
// tree-sitter-nomi/queries/injections.scm asks for, so the server colors
// MF2 itself (phrase_tokens.go). mf2.Tokens runs the MF2 parser recording
// each piece of syntax it consumes as a token, keeping those read before a
// syntax error. phraseBodyTokens places a `phrase` or `noun` body's tokens
// with ast.KeywordDecl.BodyPosition, which accounts for a multi-line body's
// stripped indentation; catalogTokens places a catalog string's through
// catalog.Value.Offset. encodeTokens merges them with semtokens' identifier
// tokens. Token types are mf2TokenType's. The tour's highlighter does not
// use them.
//
// Hover inside MF2 (phrase_hover.go) finds the mf2.Tokens token under the
// cursor, through BodyPosition in a body and catalog.Value.Body in a
// catalog. An mf2Text carries the text, its declaration's signature
// (analysis.PhraseSignature or mf2.NounSignature) and its locale: the
// source locale for a body and a catalog's `source`, the catalog's locale
// for its `text` and a noun's forms. Function and option docs are one
// table, mf2.FunctionDocOf (internal/mf2/docs.go), which
// TestFunctionDocsMatchValidate holds to what Validate accepts. A variant
// key asks mf2.Selectors for its column's selector (plural with Ordinal and
// Integers, gender, weekday, enum); a plural category's numbers come from
// mf2.PluralExamples, which scans 0 to 1000 under the locale's rules and
// writes runs (`2–4, 22–24, 32–34, …`). Pattern text has no hover, so a
// catalog entry's own hover answers there.
//
// "Add plural forms" (code_action_plural.go) is mf2.AddPluralForms: it
// keeps the declarations, adds `.input {$name :integer}` unless one
// declares the variable, adds `.match`, and asks mf2.Selectors which keys
// the locale needs, so the arms are those the checker requires (English
// `one`; Polish `one`, `few`, `many`), each with the old pattern, then `*`.
//
// # Keyword hover
//
// hoverdoc.KeywordAt (internal/hoverdoc/keywords.go) runs first in
// textDocumentHover (hover.go) and in the tour's hoverdoc.At (the wasm
// build's nomiHover and nomiHoverIn). Every keyword's hover is a row of
// keywordDocs: the word, its context ("" for its main use), the construct
// as Nomi code, one to three sentences from the spec, and the page that
// teaches it. KeywordAt lexes only when the word under the cursor is in
// lexer.ReservedWords or lexer.ContextualWords, then reads the context from
// neighbouring tokens and enclosing nodes: `as` in an import, `gopkg` or
// pattern; `case`, `if` and `dbg` after `|>`; a binding's `else`
// (ast.BindingElse); `or` in an ast.OrPattern; `for` and `with` on a
// `derive` line; `type` after `host` or before a `go` binding; `where
// self`.
//
// A contextual word is a keyword only where an AST node records its
// construct at that position (a group's `clock`, `boot` and `setup`,
// ast.Subtests, ast.ConcurrentBlock, ast.KeywordDecl, ...) or its tokens
// are its construct's (`host fn`, `open fn` in an interface, `embeds`
// starting an enum line, `derive Name` starting a line); elsewhere hover
// falls through to the name. Where the analysis already hovers the token,
// the row keeps that hover and adds the link when it explains the keyword
// with types (`if`, `case`, `then`, `assert`, `impl`, a lambda's `fn`, word
// operators), ignores it for `self` in an interface, and otherwise appends
// its text (a declaration's `fn`).
//
// An `if` with no final `else` and a binding's `else` answer with the rule
// the text does not show (hoverdoc rules.go): the `if`'s type and that it
// is a Maybe (`Some(digits)` when `total == 0` holds) or a statement, read
// from analysis.FileAnalysis.ElselessIfs; when the `else` runs, and whether
// it leaves or gives a fallback for the pattern's payload, read from the
// types the checker recorded on its paths. Each quotes the condition, value
// and pattern from the source when they fit on a line.
//
// TestKeywordDocsCoverTheLexer fails when a reserved or contextual word has
// no main-use row, TestContextualWordsAreListed (internal/parser) when the
// parser recognizes a lexeme lexer.ContextualWords lacks, and
// TestKeywordHoverLinksResolve (cmd/nomi-docgen) when a row links a page or
// anchor the generated pages and tour chapters lack (siteAnchors).
//
// # Hovers on punctuation and puns
//
// hoverdoc.SyntaxAt answers for the punctuation of literals and patterns.
// The `[` or `{` of an unnamed collection literal names the collection the
// checker made it (ast.ListLit's Kind) and what expected that type: the
// parameter it fills, the annotated binding, the field, the function's
// result, or the expected type alone (hoverdoc literal_kinds.go).
//
// A punned `name:` is a label and a binding at one position
// (analysis.FileAnalysis.PunnedFieldLabels beside References or
// Definitions). hoverdoc.PunAt finds it in a struct literal, an update, a
// struct pattern or a named argument. Hover shows the field or parameter
// and the binding; go-to-definition (definition.go) offers the field's or
// parameter's declaration and the binding's, except on a pattern's pun,
// which declares the binding and offers the field; rename of either side
// unpuns it (rename.go).
package lsp
