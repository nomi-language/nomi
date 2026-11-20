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
// # Quick fixes
//
// A quick fix answers a diagnostic the client sends back with a code
// action request. The client's copy arrives without its code, so the
// server finds the error it was published for (reported, diagnostics.go:
// the error at that position whose message is the diagnostic's headline)
// and reads what kind of error it is from the error's Code and what it
// names from its Args (the analysis package doc, "Diagnostic codes"). No
// fix reads a message's wording.
//
// An error that carries its repair (TypeError.Fix, ParseError.Fix) is
// offered as it is by buildParseFixActions, under the fix's own title, so
// a fix the checker can write whole needs no code here: calling a bare
// pipe stage, `then` before a piped lambda, a `Display.to_string()` stage,
// `Iter.take` before a terminal that drains an infinite iterator, and the
// rewrites of what another language writes. The fixes written here are the
// ones that need the document: the fill fixes, which read the impl block,
// the literal or the `case` and format the result
// (code_action_fill.go), "Generate function" (code_action_generate.go),
// "Remove the `return`", whose edit depends on what else is on the line
// (code_action_useless_return.go), the import fixes, and a missing
// translation's catalog entry (catalogs.go).
//
// A lowering's error carries its fix the same way (frontend.Diagnostic's
// Fix): a run keeps the errors of the document that have one
// (loweringChecks.fixesOf), and the code action offers them. A call
// nomi.toml's [capabilities] lists do not allow carries the edit to
// nomi.toml that adds what its help says to add (vmhost's allowFix,
// analysis.AllowCapabilities), so its edits are in another file
// (parser.Fix's Path): the action is a WorkspaceEdit of nomi.toml, read as
// the server reads it (contentForURI), and it is never the preferred fix,
// since it widens what the program may reach. A refused call in a
// dependency's file is shown in the document at each place the project's
// code leads there (the diagnostic's Related, vmhost's leadsTo), headed
// by what the place says, with the dependency's call as its
// relatedInformation, the refusal's code and args, and its fix
// (problemDiagnosticsAndFixes): one refusal, so its marks come and go
// together. An error in another file with no such place stays at the top
// of the document.
//
// # nomi.toml as a document
//
// An editor that opens a module's nomi.toml sends it to the server, which
// keeps its text as the module's manifest (analysis.SetEditedManifest):
// every analysis and lowering reads it as `nomi check` reads the file once
// it is saved. An edit rechecks and lowers every open document again
// (manifestChanged) once the edits pause for manifestDelay; closing the
// document returns the module to the disk's text.
//
// The diagnostics in nomi.toml are the errors `nomi check` reports there,
// published as an input's are (inputs.go) under two kinds of source: the
// file's own text, parsed as the front end parses it
// (analysis.ParseManifestIn), with its error at its line
// (analysis.ManifestErrorAt); and each open document's last lowering,
// whose errors about the [capabilities] lists are in the file
// (manifestDiagnostics). The lists' errors therefore follow the programs
// of the open documents, as `nomi check` of those files reports them.
// Completion in nomi.toml offers std module names inside a list of std
// names (capability.StdNames). Inside [capabilities.deps]' `go` and
// `go_in_tests` it offers the dependencies whose code binds Go, and as a
// key under [capabilities.deps.modules] each dependency with its whole
// entry, what its own files reach, in the text the fix of a missing entry
// writes (analysis.ModuleEntryText over vmhost.Reach.Tightest), from what
// the project's programs reach (projectReaches, below): the modules they
// load Nomi files from (vmhost.Reach's Modules), less those the list or
// table already names (manifestCompletion).
// Completion, hover and references find what the cursor is on the same way
// (manifestPlaceAt): the table, the key whose value holds it, and the
// string under it.
//
// # What a project reaches, in nomi.toml
//
// Hover and references on a std name in a capability list answer from
// what the project's programs reach, worked out as `nomi capabilities`
// works it out: the programs `nomi check` checks in the module's root
// (frontend.CheckedFiles), each reached and added (vmhost.ReachedAll), from
// the Nomi files on disk and the editor's text of nomi.toml
// (manifest_reach.go). It is the whole project, not the open documents'
// programs, since a list holds every program and each one's tests. It is
// worked out on the first request after a change and kept
// (projectReaches): a nomi.toml's change (manifestChanged), a saved Nomi
// document and a watched Nomi file's change drop it. A project with a
// program that does not check has no answer.
//
// A hover on a std name shows the calls that list holds it for, as `nomi
// capabilities --why` prints them (vmhost.Reach.Why and WriteWhy), at most
// maxHoverCalls with a count of the rest: the project's own calls under
// [capabilities.self], the dependencies' under [capabilities.deps], and the
// module's own under its entry (neededBy). A name none of them reaches says
// so (nothingReaches). References answer the same calls, at their callees.
//
// Each open nomi.toml has a hint, tagged unnecessary, at each std name its
// lists hold that `nomi capabilities --check` reports unused: the changes
// from its tables to the tightest ones (analysis.CapabilityChanges) that
// --check holds them to (vmhost.HeldChanges), each a removal of a name
// from a list of std names (unusedHints). The message says why: nothing
// the list holds reaches it, only the tests reach a name std holds, or std
// already allows a name std_in_tests holds. Its fix, offered from the
// code actions in nomi.toml (manifestFixes), writes the tables without the
// name with the writer `nomi capabilities --write` uses
// (analysis.CapabilitiesFix), moving a name only the tests reach to
// std_in_tests. The hints are published under their own source
// (unusedSource) after each change (reachChanged), the newest refresh
// winning, and cleared when the file closes. A project with a program
// that does not check has none.
//
// A hover on a dependency's path, in [capabilities.deps]' go and
// go_in_tests and as a key under [capabilities.deps.modules], shows what
// the module's own files reach as `nomi add` prints it
// (vmhost.Reach.ModuleReaches, vmhost.WriteModuleReach), or that no program
// imports it yet. The error entries_required reports for a dependency with
// no entry carries its fix from where vmhost builds it (entryFix): the
// entry with what the module reaches in the lowered program, written as
// `nomi add` writes it. The lowering keeps the fixes of its errors in a
// nomi.toml beside them (manifestDiagnostics, loweringChecks.manifestFixes),
// and the code actions in the file offer them with the hints' fixes.
//
// # What a call reaches
//
// A lowering run keeps, beside the document's diagnostics and fixes and
// under the same key, the outside calls its program reaches
// (analyzedlowering.Found's Reached) with the text it lowered. They come
// from the walk `nomi check` holds to nomi.toml's [capabilities] lists,
// from main, the boot, the exported functions and the tests, which every
// lowering runs once, lists or not (vmhost's loweringProblems): each call
// at its callee, where the diagnostics point (shownSpan), with the line
// hover shows (capability.ReachedLine), refused or allowed as the lists'
// one rule decides (standingsOf), naming the table of nomi.toml that
// allows or refuses it, or saying that no table holds it; a site that is
// no call, a `go` binding's declaration, is a point, which the token
// starting there shows. The same walk says what each function of the
// program and its dependencies can reach (vm.OutsideWalk.Reaches, vmhost's
// reachedFuncs): its name, and each call of it outside std, says the one
// name it can reach in full, through the functions on the way, or of
// several each refused one in full, four at most, and the rest in one
// line by name (capability.FunctionReach); a generic function's
// name holds what any of its instances can reach, and a function that
// reaches nothing, or that the walk never reaches, has none. A call in a
// file whose syntax the lowered program does not hold, as in a lowering
// relinked from kept objects, is placed at its callee in that file's text
// (vmhost's Program.syntaxOf). The memo
// keeps the calls with the problems, and so does the lowering of the entry
// of a program opened from its record, which lowers from the record's kept
// lowering as soon as the program opens, and again after each edit
// (vmhost's checklowering_stored.go). Hover on a callee adds its line
// through hoverdoc.AppendReach, which the tour's editors use too, from the
// calls loweringChecks.reachedIn gives: the document's own run's when it
// lowered the text shown, then another open document's run's in the file,
// whose program reaches it, when the file is as on disk, which that
// lowering read.
// Hover does not wait for a run, as the published diagnostics do not: a
// hover before the run of the text shown has finished has no line, and
// the first hover after it has one. Another document of a program opened
// from its record, whose run the record answers without lowering
// (DocumentManager.StoredLoweredClean), keeps no calls of its own, and its
// calls have the lines of the entry's run.
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
// the text does not show (hoverdoc rules.go): that the `if` is a statement,
// when its branch runs, and how a branch that leaves or ends in an `assert`
// or `dbg` keeps it one; when the `else` runs, and whether it leaves or
// gives a fallback for the pattern's payload, read from the types the
// checker recorded on its paths. Each quotes the condition, value
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
