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
// nomi.toml's [effects] lists do not allow carries the edit to
// nomi.toml that adds what its help says to add (vmhost's allowFix,
// analysis.AllowEffects), so its edits are in another file
// (parser.Fix's Path): the action is a WorkspaceEdit of nomi.toml, read as
// the server reads it (contentForURI), and it is never the preferred fix,
// since it widens the effects the program may have. A refused call in a
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
// whose errors about the [effects] lists are in the file
// (manifestDiagnostics). The lists' errors therefore follow the programs
// of the open documents, as `nomi check` of those files reports them.
// Completion in nomi.toml offers std module names inside a list of std
// names (effects.StdNames), and as a key in a table of entries
// (analysis.EntryLists: under [effects.self.modules] or
// [effects.deps.modules]) each module of the project or dependency with
// its whole entry, the effects its own
// files have, in the text the fix of a missing entry writes
// (analysis.ModuleEntryText over vmhost.Reach.Entries), from the effects
// the project's programs have (projectReaches, below): the keys they load
// files from (vmhost.Reach.EntryKeys), less those the table already names
// (manifestCompletion). The code for the two tables is one, given which
// table the place is in (manifestPlace.entries).
// Completion, hover and references find what the cursor is on the same way
// (manifestPlaceAt): the table, the key whose value holds it, and the
// string under it.
//
// # The effects a project has, in nomi.toml
//
// Hover and references on a std name in an effects list answer from
// the effects the project's programs have, worked out as `nomi effects`
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
// effects --why` prints them (vmhost.Reach.Why and WriteWhy), at most
// maxHoverCalls with a count of the rest: the project's own calls under
// [effects.self], the dependencies' under [effects.deps], and the
// module's own under its entry (neededBy). A name none of them has says
// so (nothingReaches). References answer the same calls, at their callees.
//
// Each open nomi.toml has a hint, tagged unnecessary, at each std name its
// lists hold that `nomi effects --check` reports unused: the changes
// from its tables to the tightest ones (analysis.EffectChanges) that
// --check holds them to (vmhost.HeldChanges), each a removal of a name
// from a list of std names (unusedHints). The message says why: nothing
// the list holds has the effect, only the tests have a name std holds, or std
// already allows a name std_in_tests holds. Its fix, offered from the
// code actions in nomi.toml (manifestFixes), writes the tables without the
// name with the writer `nomi effects --write` uses
// (analysis.EffectsFix), moving a name only the tests have to
// std_in_tests. The hints are published under their own source
// (unusedSource) after each change (reachChanged), the newest refresh
// winning, and cleared when the file closes. A project with a program
// that does not check has none.
//
// A hover on a key under [effects.self.modules] or
// [effects.deps.modules] shows the effects the module's own files
// have as `nomi add` prints them (vmhost.Reach.EntryReaches, vmhost.WriteModuleReach),
// or that no program imports it yet. The error entries_required reports,
// in either table, for a module with no entry carries its fix from where
// vmhost builds it (entryFix): the
// entry with the effects the module has in the lowered program, written as
// `nomi add` writes it. The lowering keeps the fixes of its errors in a
// nomi.toml beside them (manifestDiagnostics, loweringChecks.manifestFixes),
// and the code actions in the file offer them with the hints' fixes.
//
// # The effects a call has
//
// A lowering run keeps, beside the document's diagnostics and fixes and
// under the same key, the effect calls its program reaches
// (analyzedlowering.Found's Reached) with the text it lowered. They come
// from the walk `nomi check` holds to nomi.toml's [effects] lists,
// from main, the boot, the exported functions and the tests, which every
// lowering runs once, lists or not, and then from the same walk started
// at every function of the program and its dependencies that the first
// does not reach (vmhost's loweringProblems and editorWalks), so a
// function nothing calls yet is marked too. The second walk only adds
// lines where the first left none, its calls are held to the lists
// outside tests, and no diagnostic comes from it. Its lines are marked
// Fills (analyzedlowering.Reached), and a hover shows one only where no
// other lowering's line is, so a dependency's file open on its own reads
// as the program that reaches it says. Each call is
// at its callee, where the diagnostics point (shownSpan), with the line
// hover shows ("Has effects: `std::fs`.", effects.ReachedLine), refused or allowed as the lists'
// one rule decides (standingsOf), naming the table of nomi.toml that
// allows or refuses it, or saying that no table holds it. A read of a std
// `once` whose initializer has effects (`io.stdout`) is such a call, which
// the walk takes to the initializer: a qualified read is shown from its dot
// to the end of the name it reads, and a site that is no call or qualified
// read (a bare once read, a `host fn`'s declaration) is a point, which
// the token starting there shows. The same walks say which effects each
// function of the program and its dependencies can have, a `once`'s
// initializer among them (vm.EffectWalk.Reaches, vmhost's reachedFuncs):
// its name, a once's name, and each call of it, or read of the once,
// outside std, says the one
// effect it can have in full, through the functions on the way, or of
// several each refused one in full, four at most, and the rest in one
// line by name (effects.FunctionReach); a generic function's
// name holds the effects any of its instances can have, and a function
// with no effects has none. So does a generic function no call
// instantiates, which has no instance to walk, and a body the builder
// declines, which is not lowered. At the same
// places, a function that reads or replaces app fields, itself or through
// what it can reach, has a line naming them (vm.EffectWalk.Uses,
// effects.UsesLine), which no list holds and no token marks. A call in a
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
// A call that can act and reaches no effect call, through a handle it is
// passed, says so ("Has effects on what it is passed.", effects.HeldLine),
// and a call through an interface only some of whose implementations can
// act says it may (effects.MayLine).
// Semantic tokens read the same calls: hoverdoc.MarkReaching gives the
// `effect` modifier (modEffect), which each editor draws bold, to the
// token at each place the lowering marked (analyzedlowering.Reached's
// Marked): the last token of the callee of a call the compiler cannot show
// is pure, and the name of a function whose body holds one. The `dbg` of a
// `dbg` is the callee of the call that prints: a hover on it ends with its
// line after the keyword's text, and it is not marked, since the keyword
// shows the effect on its line (vmhost's actingCalls). A call of a function
// that holds a `dbg` is marked, as any call that can act is. A read of a
// once, a once's name and a function named as a value keep their hover
// line and no mark. The tour's highlighter marks its tokens the same way. Hover and the tokens do not wait for a run, as the published
// diagnostics do not: before the run of the text shown has finished a
// hover has no line and no token is marked. A run that finishes with calls
// other than the document's last run found asks the editor to request
// every document's tokens again (workspace/semanticTokens/refresh), when
// the editor says it can (refreshSemanticTokens). Another document of a
// program opened from its record, whose run the record answers without
// lowering (DocumentManager.StoredLoweredClean), keeps no calls of its
// own, and its calls have the lines and marks of the entry's run.
//
// A program with a compile error keeps the lines and marks of what checks.
// Its document's analysis offers no program to lower from, so its run
// lowers the text (vmhost.CheckLowering), and when every error is in a
// function, an impl function, a test or a group, the front end names those
// declarations (frontend.Project.ErrorDecls), the builder leaves them
// unlowered and lowers the rest (irbuild.Program.ErrorDecls), and the same
// walks find the calls in what was lowered, beside the front end's error,
// which the run reports nothing of. A body that checked reads as it would
// in a program with no error. A declaration that did not check has no line
// and no mark, and a call of it says nothing of what it may do, since
// nothing of its body was lowered; a function that calls it says what the
// rest of its body can do. An error anywhere else (a type, an import, a
// `once`) leaves the document with no lines and no marks, as a syntax error
// does. A function that stops compiling loses its marks with the run of
// that text; none are kept from an earlier one.
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
// neighbouring tokens and enclosing nodes: `as` in an import or
// pattern; `case`, `if` and `dbg` after `|>`; a binding's `else`
// (ast.BindingElse); `or` in an ast.OrPattern; `for` and `with` on a
// `derive` line; `type` after `host`; `where
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
