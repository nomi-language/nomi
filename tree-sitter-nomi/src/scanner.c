#include "tree_sitter/parser.h"

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// External scanner for Nomi string content and for the tokens that depend on
// the whitespace before them (scan_layout).
//
// Why the string tokens exist: tree-sitter's pure-grammar approach for string
// interpolation suffers from LR error recovery that treats a bare
// `{` mid-string as a missing-`$` interpolation start, even when the
// grammar rule is `seq('${', expr, '}')`. The Nomi lexer
// (`lexer/lexer.go`) treats bare `{` as plain string text,
// so the grammar must match. Producing `string_content` tokens from
// C side-steps the LR recovery: when we emit a content token that
// consumes the bare `{`, the parser never sees `{` as a valid
// lookahead at that position.
//
// Tokens, in declaration order matching the externals[] array in
// grammar.js:
//
//   STRING_CONTENT             — chars inside a "..." string up to
//                                the next `"`, `\`, or `${` boundary.
//   TRIPLE_STRING_CONTENT      — chars inside a """...""" string up
//                                to the next `"""` or `${` boundary;
//                                `\${` is content.
//   RAW_STRING_CONTENT         — chars inside a backtick raw string. Stops
//                                only at "`". Backslash is literal,
//                                `${` is literal, `\${` keeps its `\`.
//   STATEMENT_END              — zero-width end of a statement or a `case`
//                                arm at a line break.
//   ATTACHED_COMMENT_TEST_PROMPT — the line-leading `//!` attached-test opener.
//   ATTACHED_COMMENT_TEST_LINE_PROMPT — the line-leading `//!` attached-test
//                                body prompt, skipped as an extra.
//   ATTACHED_TEST_PROMPT_GROUP_END — zero-width end of a contiguous `//!`
//                                attached-test group.
//   SAME_LINE                  — zero-width mark before a keyword's operand.
//   SUBTESTS_KEYWORD, GROUP_KEYWORD, HOST_KEYWORD — the contextual keywords
//                                `subtests`, `group` and `host`, each emitted
//                                only where what follows makes it one.
//   NEWLINE_BRACE              — zero-width mark before a `{` that starts
//                                its line.
//
// Each string scanner emits at least one character; if the first character
// is already a boundary, the scanner returns false and lets the main
// grammar handle it.

enum TokenType {
    STRING_CONTENT,
    TRIPLE_STRING_CONTENT,
    RAW_STRING_CONTENT,
    // Zero-width, at the start of a line, where the line break before it ends
    // a statement or a `case` arm (see line_continues).
    STATEMENT_END,
    ATTACHED_COMMENT_TEST_PROMPT,
    ATTACHED_COMMENT_TEST_LINE_PROMPT,
    ATTACHED_TEST_PROMPT_GROUP_END,
    // Zero-width, before the operand of `dbg`, `try`, `return`, `break` or
    // `todo` when the operand starts on the keyword's line.
    SAME_LINE,
    // The contextual keyword `subtests` at the start of a statement, emitted
    // only when an expression and a comma follow it (scan_subtests_keyword).
    SUBTESTS_KEYWORD,
    // The contextual keyword `group` that opens a test group, emitted only
    // when the group's name string or its `{` follows it
    // (scan_group_keyword).
    GROUP_KEYWORD,
    // The contextual keyword `host` that opens a host declaration, emitted
    // only when `fn` or `type` follows it on its line (scan_host_keyword).
    HOST_KEYWORD,
    // Zero-width, before a `{` that starts its line, where that brace may
    // open a block statement or a declaration's body.
    NEWLINE_BRACE,
};

typedef struct {
    bool in_attached_test_prompt_group;
    // The last external token was a `//!` line prompt after a line break,
    // ending at prompt_end_column. The prompt is an extra, so the whitespace
    // before the token after it holds no line break; this carries the break
    // over to that token, which starts where the prompt ends, so a statement
    // on a prompt line ends as it would on a plain line. A later token on
    // the line starts further right.
    bool line_break_before_prompt;
    uint32_t prompt_end_column;
} Scanner;

void *tree_sitter_nomi_external_scanner_create(void) {
    Scanner *scanner = (Scanner *)calloc(1, sizeof(Scanner));
    return scanner;
}

void tree_sitter_nomi_external_scanner_destroy(void *payload) {
    free(payload);
}

unsigned tree_sitter_nomi_external_scanner_serialize(void *payload, char *buffer) {
    Scanner *scanner = (Scanner *)payload;
    buffer[0] = scanner != NULL && scanner->in_attached_test_prompt_group ? 1 : 0;
    buffer[1] = scanner != NULL && scanner->line_break_before_prompt ? 1 : 0;
    uint32_t column = scanner != NULL ? scanner->prompt_end_column : 0;
    memcpy(&buffer[2], &column, sizeof column);
    return 2 + sizeof column;
}

void tree_sitter_nomi_external_scanner_deserialize(void *payload, const char *buffer, unsigned length) {
    Scanner *scanner = (Scanner *)payload;
    if (scanner == NULL) return;
    scanner->in_attached_test_prompt_group = length > 0 && buffer[0] == 1;
    scanner->line_break_before_prompt = length > 1 && buffer[1] == 1;
    scanner->prompt_end_column = 0;
    if (length >= 2 + sizeof scanner->prompt_end_column) {
        memcpy(&scanner->prompt_end_column, &buffer[2], sizeof scanner->prompt_end_column);
    }
}

static inline void advance(TSLexer *lexer) { lexer->advance(lexer, false); }

static inline void skip(TSLexer *lexer) { lexer->advance(lexer, true); }

static inline bool is_ws(int32_t c) {
    return c == ' ' || c == '\t' || c == '\n' || c == '\r';
}

static inline bool is_ident_char(int32_t c) {
    return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
           (c >= '0' && c <= '9');
}

// Scan content of a single-quoted "..." string. Stops at `"`, `\`,
// or the start of an interpolation (`${`). Emits STRING_CONTENT.
// Returns true if at least one character was consumed.
//
// `$` handling matches the Nomi lexer (`lexer/lexer.go`):
//   - `${` — boundary; do not consume the `$`.
//   - any other `$` — literal content (`$$` is two literal `$`s).
// `#` is ordinary text. `\${` is an escape and is emitted by the
// grammar's `escape_sequence`, so the scanner stops at the `\`.
static bool scan_string_content(TSLexer *lexer) {
    bool has_content = false;
    while (true) {
        // Mark the end before checking the next character so that
        // boundary characters (`"`, `\`, `$`-of-`${`) are not
        // consumed into the token.
        lexer->mark_end(lexer);

        int32_t c = lexer->lookahead;
        if (c == 0) {
            // EOF — let the grammar handle the unterminated string.
            break;
        }
        if (c == '"' || c == '\\') {
            break;
        }
        if (c == '$') {
            // Advance past the `$`, then peek to decide.
            advance(lexer);
            if (lexer->lookahead == '{') {
                // `${` — boundary. mark_end is still at the position
                // before the `$`, so the `$` is not in the token.
                break;
            }
            // Lone `$` — already advanced past it; treat as content.
            has_content = true;
            continue;
        }
        advance(lexer);
        has_content = true;
    }

    if (!has_content) {
        return false;
    }
    lexer->result_symbol = STRING_CONTENT;
    return true;
}

// Scan content of a triple-quoted """...""" string. Stops at `"""`
// or at the start of an interpolation (`${`). A single `"` or `""`
// followed by non-`"` is part of the content.
//
// Matches the Nomi lexer's triple-string scan (`lexer/lexer.go`):
//   - `${` — boundary; do not consume the `$`.
//   - `\${` — the one escape (a literal `${`); all three characters
//     are consumed as content, so no interpolation starts.
//   - any other `$`, `\` or `#` — literal content.
static bool scan_triple_string_content(Scanner *scanner, TSLexer *lexer) {
    bool has_content = false;
    while (true) {
        lexer->mark_end(lexer);

        int32_t c = lexer->lookahead;
        if (c == 0) {
            break;
        }
        if ((c == '\n' || c == '\r') &&
            scanner != NULL &&
            scanner->in_attached_test_prompt_group &&
            has_content) {
            break;
        }
        if (c == '"') {
            // Look ahead to see if this is the closing `"""`.
            advance(lexer);
            if (lexer->lookahead == '"') {
                advance(lexer);
                if (lexer->lookahead == '"') {
                    // `"""` — stop here; mark_end is still at the
                    // first `"`.
                    break;
                }
                // `""` followed by non-`"` — both `"`s are content.
                has_content = true;
                continue;
            }
            // Single `"` followed by non-`"` — that `"` is content.
            has_content = true;
            continue;
        }
        if (c == '\\') {
            // `\${` — escaped literal `${`, all content. A `\` followed
            // by anything else is a literal backslash; a following `$`
            // that does not open `{` is content too, and a `$` that
            // does (`\$${`) is checked by the next iteration.
            advance(lexer);
            has_content = true;
            if (lexer->lookahead == '$') {
                advance(lexer);
                if (lexer->lookahead == '{') {
                    advance(lexer);
                }
            }
            continue;
        }
        if (c == '$') {
            // Advance past the `$`, then peek to decide.
            advance(lexer);
            if (lexer->lookahead == '{') {
                // `${` — boundary. mark_end is still at the position
                // before the `$`, so the `$` is not in the token.
                break;
            }
            // Lone `$` — already advanced past it; treat as content.
            has_content = true;
            continue;
        }
        advance(lexer);
        has_content = true;
    }

    if (!has_content) {
        return false;
    }
    lexer->result_symbol = TRIPLE_STRING_CONTENT;
    return true;
}

// Scan content of a raw backtick string. Stops at "`"
// only. No `${` boundary check, no backslash handling — every byte
// other than "`" is part of the content (matches the Nomi lexer's
// raw mode in `scanRawBacktick` / `lexer/lexer.go`).
static bool scan_raw_string_content(Scanner *scanner, TSLexer *lexer) {
    bool has_content = false;
    while (true) {
        lexer->mark_end(lexer);

        int32_t c = lexer->lookahead;
        if (c == 0) {
            // EOF — let the grammar handle the unterminated string.
            break;
        }
        if ((c == '\n' || c == '\r') &&
            scanner != NULL &&
            scanner->in_attached_test_prompt_group &&
            has_content) {
            break;
        }
        if (c == '`') {
            break;
        }
        advance(lexer);
        has_content = true;
    }

    if (!has_content) {
        return false;
    }
    lexer->result_symbol = RAW_STRING_CONTENT;
    return true;
}

// Whether the rest of `word` follows, its first character being the
// lookahead, as a whole word. Advances over what matches.
static bool at_word(TSLexer *lexer, const char *word) {
    for (const char *p = word; *p != '\0'; p++) {
        if (lexer->lookahead != *p) return false;
        advance(lexer);
    }
    return !is_ident_char(lexer->lookahead);
}

// Whether the line that starts at the lookahead continues the statement or
// arm before the line break, as the Go lexer and parser read it. The lexer
// emits no NEWLINE before a line that starts with `|>`, `)` or `]`, or with
// an operator no expression starts with (`==`, `!=`, `<`, `<=`, `>`, `>=`,
// `and`, `or`); the
// parser steps over one before `else` (an `if`'s or a binding's) and, after a
// declaration's signature, before its `{` body and its `where` clause. A
// statement never starts with `}`, `,` or `{` after a complete statement
// that a brace could extend, and the end of input ends everything anyway.
// Every other line starts a new statement: `-1`, `(2)`, `[1]`, `.b`, `!c`.
// May advance past the lookahead, so the caller marks the token's end first.
static bool line_continues(TSLexer *lexer) {
    switch (lexer->lookahead) {
        case 0:
        case ')':
        case ']':
        case '}':
        case ',':
        case '{':
            return true;
        case '|':
            advance(lexer);
            return lexer->lookahead == '>';
        case '<':
        case '>':
            return true;
        case '=':
        case '!':
            advance(lexer);
            return lexer->lookahead == '=';
        case 'a':
            return at_word(lexer, "and") && lexer->lookahead != '?';
        case 'o':
            return at_word(lexer, "or") && lexer->lookahead != '?';
        case 'e':
            return at_word(lexer, "else");
        case 'w':
            return at_word(lexer, "where");
        default:
            return false;
    }
}

// Whether `c` can start the operand of `dbg`, `try`, `return`, `break` or
// `todo`. A closing delimiter, a comma or the end of input cannot, and neither
// can `|>`, which pipes the bare keyword on (`value |> try |> f`).
static bool starts_operand(TSLexer *lexer) {
    int32_t c = lexer->lookahead;
    if (c == 0 || c == ')' || c == ']' || c == '}' || c == ',') return false;
    if (c == '|') {
        advance(lexer);
        return lexer->lookahead != '>';
    }
    return true;
}

// Skip a `"..."` or `'.'` literal whose opening quote is the lookahead, so a
// comma inside it is not read as the comma that ends the rows of `subtests`.
static void skip_quoted(TSLexer *lexer) {
    int32_t quote = lexer->lookahead;
    advance(lexer);
    while (lexer->lookahead != 0 && lexer->lookahead != quote && lexer->lookahead != '\n') {
        if (lexer->lookahead == '\\') advance(lexer);
        advance(lexer);
    }
    if (lexer->lookahead == quote) advance(lexer);
}

// The contextual keyword `subtests`, at the lookahead at the start of a
// statement. As in the Go parser, the word is the keyword only when an
// expression follows it on its line and then a comma at the top level
// (`subtests cases, (input, want) { ... }`); `subtests = 3`, `subtests(2)` or
// a bare `subtests` leave it an identifier. The rows may span lines inside
// brackets, or continue on a line starting with `|>`. The token ends after
// the word; everything after is look-ahead.
static bool scan_subtests_keyword(TSLexer *lexer) {
    if (!at_word(lexer, "subtests")) return false;
    lexer->mark_end(lexer);
    if (lexer->lookahead != ' ' && lexer->lookahead != '\t') return false;
    while (lexer->lookahead == ' ' || lexer->lookahead == '\t') advance(lexer);
    switch (lexer->lookahead) {
        case 0: case '\n': case '\r': case '=': case '.': case '-': case ',':
        case ')': case ']': case '}': case ':': case '|':
            return false;
    }
    int depth = 0;
    for (;;) {
        int32_t c = lexer->lookahead;
        switch (c) {
            case 0:
                return false;
            case '"':
            case '\'':
                skip_quoted(lexer);
                continue;
            case '/':
                advance(lexer);
                if (lexer->lookahead == '/') {
                    while (lexer->lookahead != 0 && lexer->lookahead != '\n') advance(lexer);
                }
                continue;
            case '(': case '[': case '{':
                depth++;
                break;
            case ')': case ']': case '}':
                if (depth == 0) return false;
                depth--;
                break;
            case ',':
                if (depth == 0) {
                    lexer->result_symbol = SUBTESTS_KEYWORD;
                    return true;
                }
                break;
            case '\n':
                if (depth == 0) {
                    while (is_ws(lexer->lookahead)) advance(lexer);
                    if (lexer->lookahead != '|') return false;
                    advance(lexer);
                    if (lexer->lookahead != '>') return false;
                }
                break;
        }
        advance(lexer);
    }
}

// The contextual keyword `group`, at the lookahead where a top-level item can
// start. As in the Go parser, the word is the keyword only when a string, the
// group's name, follows it on its line (`group "parsing" { ... }`), or the
// group's `{` with the name missing (`group { ... }`, which the compiler
// reports); `group = 3`, `group(2)` or a bare `group` leave it an
// identifier. The token ends after the word.
static bool scan_group_keyword(TSLexer *lexer) {
    if (!at_word(lexer, "group")) return false;
    lexer->mark_end(lexer);
    while (lexer->lookahead == ' ' || lexer->lookahead == '\t') advance(lexer);
    if (lexer->lookahead != '"' && lexer->lookahead != '{') return false;
    lexer->result_symbol = GROUP_KEYWORD;
    return true;
}

// The contextual keyword `host`, at the lookahead where a declaration can
// start. As in the Go parser (startsHostDecl), the word is the keyword only
// when `fn` or `type` follows it: `host fn now(): Instant`, `pub host type
// Regex`. A `host` binding, parameter or field (`host = "localhost"`, `{host:
// h}`) leaves it an identifier. The token ends after the word.
static bool scan_host_keyword(TSLexer *lexer) {
    if (!at_word(lexer, "host")) return false;
    lexer->mark_end(lexer);
    while (lexer->lookahead == ' ' || lexer->lookahead == '\t') advance(lexer);
    bool declares = lexer->lookahead == 'f' ? at_word(lexer, "fn")
        : lexer->lookahead == 't' ? at_word(lexer, "type")
        : false;
    if (!declares) return false;
    lexer->result_symbol = HOST_KEYWORD;
    return true;
}

// Scan a line-leading `//!` prompt, or the end of a prompt group, at the
// position after the leading whitespace run, which held `newlines` line
// breaks. A prompt token ends right after its `!`: the spaces after it are
// whitespace, so a `token.immediate` token never attaches across a prompt
// (`v` then `//!     .None` is not `v.None`).
static bool scan_attached_comment_marker(Scanner *scanner, TSLexer *lexer, const bool *valid_symbols, unsigned newlines) {
    bool can_scan_prompt = valid_symbols[ATTACHED_COMMENT_TEST_PROMPT] ||
        valid_symbols[ATTACHED_COMMENT_TEST_LINE_PROMPT];
    bool can_end_group = valid_symbols[ATTACHED_TEST_PROMPT_GROUP_END] &&
        scanner != NULL &&
        scanner->in_attached_test_prompt_group;
    if (!can_scan_prompt && !can_end_group) return false;
    if (newlines == 0 && lexer->get_column(lexer) != 0) {
        return false;
    }

    lexer->mark_end(lexer);

    bool next_is_attached_prompt = lexer->lookahead == '/';
    if (next_is_attached_prompt) {
        advance(lexer);
        next_is_attached_prompt = lexer->lookahead == '/';
        if (next_is_attached_prompt) {
            advance(lexer);
            next_is_attached_prompt = lexer->lookahead == '!';
        }
    }

    if (next_is_attached_prompt && can_end_group && newlines > 1) {
        lexer->result_symbol = ATTACHED_TEST_PROMPT_GROUP_END;
        scanner->in_attached_test_prompt_group = false;
        return true;
    }

    if (!next_is_attached_prompt) {
        if (can_end_group && newlines > 0) {
            lexer->result_symbol = ATTACHED_TEST_PROMPT_GROUP_END;
            scanner->in_attached_test_prompt_group = false;
            return true;
        }
        return false;
    }
    if (!can_scan_prompt) return false;

    advance(lexer); // past `!`
    lexer->mark_end(lexer);

    bool continues_group = scanner != NULL &&
        scanner->in_attached_test_prompt_group &&
        newlines <= 1 &&
        valid_symbols[ATTACHED_COMMENT_TEST_LINE_PROMPT];
    lexer->result_symbol = continues_group
        ? ATTACHED_COMMENT_TEST_LINE_PROMPT
        : ATTACHED_COMMENT_TEST_PROMPT;
    if (scanner != NULL) {
        scanner->in_attached_test_prompt_group = true;
    }
    return true;
}

// The tokens that depend on the whitespace before them: the `//!` prompts and
// a group's end, the statement end and the same-line operand marker. One pass
// skips the leading whitespace for all of them, counting line breaks. Each
// check runs from the first character after it; a check that advances
// further owns the rest of the call, since the lexer cannot step back.
// `recovering` (every string-content token valid at once, which happens only
// at the start state or in error recovery) keeps the zero-width tokens out
// of error recovery.
static bool scan_layout(Scanner *scanner, TSLexer *lexer, const bool *valid_symbols, bool recovering) {
    // The token right after a `//!` line prompt starts the prompt's line.
    bool line_break = scanner != NULL && scanner->line_break_before_prompt &&
        lexer->get_column(lexer) == scanner->prompt_end_column;
    if (scanner != NULL) scanner->line_break_before_prompt = false;
    unsigned newlines = 0;
    while (is_ws(lexer->lookahead)) {
        if (lexer->lookahead == '\n' || lexer->lookahead == '\r') newlines++;
        skip(lexer);
    }
    if (newlines > 0) line_break = true;

    if (lexer->lookahead == '/') {
        // A `//!` prompt, or a comment. Nothing else here starts with `/`.
        if (!scan_attached_comment_marker(scanner, lexer, valid_symbols, newlines)) return false;
        if (scanner != NULL && newlines > 0 &&
            lexer->result_symbol == ATTACHED_COMMENT_TEST_LINE_PROMPT) {
            scanner->line_break_before_prompt = true;
            scanner->prompt_end_column = lexer->get_column(lexer);
        }
        return true;
    }

    if (valid_symbols[ATTACHED_TEST_PROMPT_GROUP_END] &&
        scanner != NULL && scanner->in_attached_test_prompt_group &&
        newlines > 0) {
        lexer->mark_end(lexer);
        lexer->result_symbol = ATTACHED_TEST_PROMPT_GROUP_END;
        scanner->in_attached_test_prompt_group = false;
        return true;
    }
    if (recovering) return false;

    // A line break ends the statement or arm in progress, as the Go lexer
    // ends one, unless the next line continues it. A brace on the next line
    // is NEWLINE_BRACE's.
    if (valid_symbols[STATEMENT_END] && line_break && lexer->lookahead != '{') {
        lexer->mark_end(lexer);
        if (line_continues(lexer)) return false;
        lexer->result_symbol = STATEMENT_END;
        return true;
    }

    // A `{` that starts its line, after a statement that may end there: a
    // block statement, or, after a declaration's signature, its body. Only
    // here may a block follow a statement without a separator, so a name
    // followed by a brace on its line (`p = Point{}`) is not a name and a
    // block.
    if (valid_symbols[NEWLINE_BRACE] && line_break && lexer->lookahead == '{') {
        lexer->mark_end(lexer);
        lexer->result_symbol = NEWLINE_BRACE;
        return true;
    }

    if (valid_symbols[SUBTESTS_KEYWORD] && !valid_symbols[SAME_LINE] && lexer->lookahead == 's') {
        return scan_subtests_keyword(lexer);
    }

    if (valid_symbols[GROUP_KEYWORD] && !valid_symbols[SAME_LINE] && lexer->lookahead == 'g') {
        return scan_group_keyword(lexer);
    }

    if (valid_symbols[HOST_KEYWORD] && !valid_symbols[SAME_LINE] && lexer->lookahead == 'h') {
        return scan_host_keyword(lexer);
    }

    // The operand of `dbg`, `try`, `return`, `break` and `todo` must start on
    // the keyword's line, as in the Go parser: `n |> dbg` then a line
    // `same = dbg n` is two statements.
    if (valid_symbols[SAME_LINE] && !line_break) {
        lexer->mark_end(lexer);
        if (!starts_operand(lexer)) return false;
        lexer->result_symbol = SAME_LINE;
        return true;
    }
    return false;
}

bool tree_sitter_nomi_external_scanner_scan(void *payload, TSLexer *lexer, const bool *valid_symbols) {
    Scanner *scanner = (Scanner *)payload;
    int valid_count =
        (valid_symbols[STRING_CONTENT] ? 1 : 0) +
        (valid_symbols[TRIPLE_STRING_CONTENT] ? 1 : 0) +
        (valid_symbols[RAW_STRING_CONTENT] ? 1 : 0);
    bool in_string_content = valid_count == 1;

    if (!in_string_content) {
        // Multiple content externals being valid simultaneously means
        // we're either at the parser's start state or in error
        // recovery, never legitimately inside one specific string
        // variant. No string content then, so the internal lexer (which
        // handles the opening `"` / `"""` / backtick) gets a chance to
        // drive the parser into a real string state.
        return scan_layout(scanner, lexer, valid_symbols, valid_count > 1);
    }

    // Inside a string that spans the lines of a `//!` group, a line break
    // may lead to the next line's prompt.
    if ((valid_symbols[ATTACHED_COMMENT_TEST_PROMPT] ||
         valid_symbols[ATTACHED_COMMENT_TEST_LINE_PROMPT] ||
         valid_symbols[ATTACHED_TEST_PROMPT_GROUP_END]) &&
        scanner != NULL && scanner->in_attached_test_prompt_group) {
        unsigned newlines = 0;
        while (is_ws(lexer->lookahead)) {
            if (lexer->lookahead == '\n' || lexer->lookahead == '\r') newlines++;
            skip(lexer);
        }
        if (scan_attached_comment_marker(scanner, lexer, valid_symbols, newlines)) {
            return true;
        }
    }

    if (valid_symbols[STRING_CONTENT]) {
        return scan_string_content(lexer);
    }
    if (valid_symbols[TRIPLE_STRING_CONTENT]) {
        return scan_triple_string_content(scanner, lexer);
    }
    return scan_raw_string_content(scanner, lexer);
}
