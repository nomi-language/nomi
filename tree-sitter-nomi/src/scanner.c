#include "tree_sitter/parser.h"

#include <stdbool.h>
#include <stddef.h>
#include <stdlib.h>

// External scanner for Nomi string content.
//
// Why this exists: tree-sitter's pure-grammar approach for string
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
//   ATTACHED_COMMENT_TEST_PROMPT — the line-leading `//!` attached-test opener.
//   ATTACHED_COMMENT_TEST_LINE_PROMPT — the line-leading `//!` attached-test
//                                body prompt, skipped as an extra.
//   ATTACHED_TEST_PROMPT_GROUP_END — whitespace ending a contiguous `//!`
//                                attached-test group.
//
// Each scanner emits at least one character; if the first character
// is already a boundary, the scanner returns false and lets the main
// grammar handle it.

enum TokenType {
    STRING_CONTENT,
    TRIPLE_STRING_CONTENT,
    RAW_STRING_CONTENT,
    // Separator between `case` arms. Emitted on a newline that is immediately
    // followed by `.Uppercase` — a `dot_variant` pattern starting the next
    // arm. Forces the previous arm's body to end there instead of greedily
    // extending into the dot via `type_field_access` (`sort(xs, f).Descending`).
    // Restricted to `.Uppercase` so it never severs a multi-line body that
    // continues with `|>`, an operator, or a lowercase `.field`.
    CASE_ARM_SEP,
    // Opening brace of a *headless* anonymous struct literal (`{a: 1}`) or map
    // literal (`{k => v}`) in value/scrutinee position. The scanner classifies
    // a whitespace-preceded `{` by peeking its content and emits one of these
    // (or nothing → a plain `{`, leaving block / case-block / construction
    // alone). Distinct opening tokens give `anon_struct` / `map_literal` their
    // own un-merged LR state, so a field value extends (`{a: Map.put(x)}`) and
    // a map literal can be a `case` scrutinee. A head-hugging construction
    // brace (`Set{…}`, no leading whitespace) is left to `token.immediate`.
    ANON_STRUCT_OPEN,
    MAP_OPEN,
    ATTACHED_COMMENT_TEST_PROMPT,
    ATTACHED_COMMENT_TEST_LINE_PROMPT,
    ATTACHED_TEST_PROMPT_GROUP_END,
};

typedef struct {
    bool in_attached_test_prompt_group;
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
    return 1;
}

void tree_sitter_nomi_external_scanner_deserialize(void *payload, const char *buffer, unsigned length) {
    Scanner *scanner = (Scanner *)payload;
    if (scanner == NULL) return;
    scanner->in_attached_test_prompt_group = length > 0 && buffer[0] == 1;
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

// Skip whitespace and `//` line/doc comments (Nomi has no block comments) at a
// trivia position — between the `{` and the first token, after the leading
// identifier, after the `:`. A `/` here is always a comment start (an
// expression can't begin with division), so it's safe to treat it as one. A
// comment body can hold `=>`/`:`/`=`, none of which must reach the classifier.
// Pure look-ahead — never calls mark_end.
static void skip_ws_comments(TSLexer *lexer) {
    while (true) {
        if (is_ws(lexer->lookahead)) { advance(lexer); continue; }
        if (lexer->lookahead == '/') {
            advance(lexer); // past the first '/'
            if (lexer->lookahead == '/') {
                while (lexer->lookahead != 0 && lexer->lookahead != '\n') advance(lexer);
                continue;
            }
            return; // lone '/' — doesn't occur at a trivia position in valid code
        }
        return;
    }
}

// Classify the content immediately after a headless `{` (lookahead positioned
// just past the brace). Pure look-ahead — advances beyond mark_end, never into
// the emitted token. Returns: 0 = block, 1 = anon struct, 2 = map literal.
//
// Heuristics, matching the Go parser's intent:
//   - A leading `IDENT :` (not `::`) with no top-level binding `=` → anon struct
//     (`{a: 1}`, `{tasks: Map.put(x)}`). A top-level single `=` means a typed
//     binding (`{x: Int = 5}`) → block.
//   - A leading `..` → struct update (`{..base, f: v}`, `{..base}`). Reported as
//     kind 1 because the update literal and the anon-struct literal share the
//     one opening token: the grammar's `struct_update` and `anon_struct` both
//     accept `_anon_struct_open`, and which one applies is decided by whether a
//     `struct_update_spread` follows. No Nomi block can begin with `..` — the
//     open-ended range forms (`..5`, `..`) were removed precisely so a prefix
//     `..` inside a literal is unambiguously a spread — so this steals nothing.
//   - A top-level `=>` (before any decisive binding `=`) → map (`{k => v}`,
//     `{(1,2) => "a"}`). Checked by the scan loop below, so it still wins over a
//     leading `..`: `{..m, "b" => 2}` classifies as a map and fails at
//     `map_entry`, which keeps map spread unsupported as designed.
//   - Anything else (bare statements, `foo()`, guard `n < 0 -> …`) → block.
// Depth-tracked across `()[]{}`; strings skipped; comparison `=` (`==`/`!=`/
// `<=`/`>=`/`=>`) never mistaken for a binding.
static int classify_brace(TSLexer *lexer) {
    skip_ws_comments(lexer);
    if (lexer->lookahead == '}' || lexer->lookahead == 0) return 0; // empty → block

    bool leading_ident_colon = false;
    bool leading_spread = false;
    int32_t c0 = lexer->lookahead;
    if (c0 == '_' || (c0 >= 'a' && c0 <= 'z')) {
        while (is_ident_char(lexer->lookahead)) advance(lexer);
        skip_ws_comments(lexer);
        if (lexer->lookahead == ':') {
            advance(lexer);
            if (lexer->lookahead != ':') leading_ident_colon = true; // not `::`
        }
    } else if (c0 == '.') {
        advance(lexer);
        if (lexer->lookahead == '.') {
            advance(lexer);
            leading_spread = true;
        }
    }

    int depth = 0;
    int32_t prev = 0; // last non-whitespace char seen
    while (true) {
        int32_t ch = lexer->lookahead;
        if (ch == 0) break;
        if (ch == '/') { // comment or division
            advance(lexer);
            if (lexer->lookahead == '/') {
                while (lexer->lookahead != 0 && lexer->lookahead != '\n') advance(lexer);
            } else {
                prev = '/'; // division operator
            }
            continue;
        }
        if (ch == '"') { // skip a string literal
            advance(lexer);
            while (lexer->lookahead != 0 && lexer->lookahead != '"') {
                if (lexer->lookahead == '\\') advance(lexer);
                advance(lexer);
            }
            if (lexer->lookahead == '"') advance(lexer);
            prev = '"';
            continue;
        }
        if (ch == '(' || ch == '[' || ch == '{') { depth++; prev = ch; advance(lexer); continue; }
        if (ch == ')' || ch == ']' || ch == '}') {
            if (depth == 0) break; // our own closing brace
            depth--; prev = ch; advance(lexer); continue;
        }
        if (depth == 0 && ch == '=') {
            int32_t before = prev;
            advance(lexer);
            int32_t next = lexer->lookahead;
            if (next == '>') return 2; // `=>` → map
            if (next == '=' || before == '=' || before == '!' ||
                before == '<' || before == '>') {
                prev = '='; // `==` / comparison half — keep scanning
                continue;
            }
            return 0; // single top-level `=` → binding → block
        }
        if (!is_ws(ch)) prev = ch;
        advance(lexer);
    }
    return leading_ident_colon ? 1 : 0;
}

// Classify and emit the opening brace of a headless anon-struct / map literal.
// Fires only when the `{` is preceded by whitespace, so a head-hugging
// construction brace (`Set{…}`) — which has no leading whitespace and is owned
// by `token.immediate('{')` — is never stolen.
static bool scan_headless_brace(TSLexer *lexer, const bool *valid_symbols) {
    if (!is_ws(lexer->lookahead)) return false;
    while (is_ws(lexer->lookahead)) advance(lexer);
    if (lexer->lookahead != '{') return false;
    advance(lexer);          // consume `{`
    lexer->mark_end(lexer);  // token = leading whitespace + `{`
    int kind = classify_brace(lexer); // look-ahead beyond mark_end
    if (kind == 1 && valid_symbols[ANON_STRUCT_OPEN]) {
        lexer->result_symbol = ANON_STRUCT_OPEN;
        return true;
    }
    if (kind == 2 && valid_symbols[MAP_OPEN]) {
        lexer->result_symbol = MAP_OPEN;
        return true;
    }
    return false; // block (or the wanted kind isn't valid) → rewind, plain `{`
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

// Scan a `case`-arm separator. Fires only on a newline that, after the
// newline+whitespace run, is immediately followed by `.` then an uppercase
// letter — i.e. a `dot_variant` pattern (`.Descending`) starting the next arm.
// The token consumes the newline+whitespace (mark_end is set before the `.`),
// leaving the `.Uppercase` for the next arm's pattern. Returning false (any
// other lookahead) rewinds, so a newline mid-body — before `|>`, an operator,
// a lowercase `.field`, or anything else — is left as plain whitespace for the
// internal lexer to skip and the body continues normally. Emits CASE_ARM_SEP.
static bool scan_case_arm_sep(TSLexer *lexer) {
    if (lexer->lookahead != '\n' && lexer->lookahead != '\r') {
        return false;
    }
    while (lexer->lookahead == '\n' || lexer->lookahead == '\r' ||
           lexer->lookahead == ' ' || lexer->lookahead == '\t') {
        advance(lexer);
    }
    // End the token after the whitespace run, before the next character, so
    // the next arm's pattern is not consumed into the separator.
    lexer->mark_end(lexer);
    int32_t c = lexer->lookahead;
    // `[` — a list pattern starting the next arm (`[head, ..tail]`). A complete
    // body would otherwise extend into it as a `typed_list` (`None[head]`).
    // Safe here only because CASE_ARM_SEP is valid (we're between arms, not
    // after `->` where a `[...]` body is expected).
    if (c == '[') {
        lexer->result_symbol = CASE_ARM_SEP;
        return true;
    }
    // `.Uppercase` — a `dot_variant` starting the next arm (`.Descending`).
    // A complete body would otherwise extend via `expr.Type` type_field_access.
    if (c == '.') {
        advance(lexer); // peek past `.` (beyond mark_end — not part of the token)
        int32_t d = lexer->lookahead;
        if (d >= 'A' && d <= 'Z') {
            lexer->result_symbol = CASE_ARM_SEP;
            return true;
        }
    }
    return false;
}

static bool scan_attached_comment_marker(Scanner *scanner, TSLexer *lexer, const bool *valid_symbols) {
    bool can_scan_prompt = valid_symbols[ATTACHED_COMMENT_TEST_PROMPT] ||
        valid_symbols[ATTACHED_COMMENT_TEST_LINE_PROMPT];
    bool can_end_group = valid_symbols[ATTACHED_TEST_PROMPT_GROUP_END] &&
        scanner != NULL &&
        scanner->in_attached_test_prompt_group;
    if (!can_scan_prompt && !can_end_group) return false;

    unsigned skipped_newlines = 0;
    while (is_ws(lexer->lookahead)) {
        if (lexer->lookahead == '\n' || lexer->lookahead == '\r') {
            skipped_newlines++;
        }
        skip(lexer);
    }
    if (skipped_newlines == 0 && lexer->get_column(lexer) != 0) {
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

    if (next_is_attached_prompt && can_end_group && skipped_newlines > 1) {
        lexer->result_symbol = ATTACHED_TEST_PROMPT_GROUP_END;
        scanner->in_attached_test_prompt_group = false;
        return true;
    }

    if (!next_is_attached_prompt) {
        if (can_end_group && skipped_newlines > 0) {
            lexer->result_symbol = ATTACHED_TEST_PROMPT_GROUP_END;
            scanner->in_attached_test_prompt_group = false;
            return true;
        }
        return false;
    }

    if (lexer->lookahead != '!') return false;
    advance(lexer);

    bool continues_group = scanner != NULL &&
        scanner->in_attached_test_prompt_group &&
        skipped_newlines <= 1 &&
        valid_symbols[ATTACHED_COMMENT_TEST_LINE_PROMPT];

    if (lexer->lookahead != ' ' && lexer->lookahead != '\t') {
        lexer->mark_end(lexer);
        lexer->result_symbol = continues_group
            ? ATTACHED_COMMENT_TEST_LINE_PROMPT
            : ATTACHED_COMMENT_TEST_PROMPT;
        if (scanner != NULL) {
            scanner->in_attached_test_prompt_group = true;
        }
        return true;
    }
    while (lexer->lookahead == ' ' || lexer->lookahead == '\t') {
        advance(lexer);
    }
    lexer->mark_end(lexer);
    lexer->result_symbol = continues_group
        ? ATTACHED_COMMENT_TEST_LINE_PROMPT
        : ATTACHED_COMMENT_TEST_PROMPT;
    if (scanner != NULL) {
        scanner->in_attached_test_prompt_group = true;
    }
    return true;
}

bool tree_sitter_nomi_external_scanner_scan(void *payload, TSLexer *lexer, const bool *valid_symbols) {
    Scanner *scanner = (Scanner *)payload;
    int valid_count =
        (valid_symbols[STRING_CONTENT] ? 1 : 0) +
        (valid_symbols[TRIPLE_STRING_CONTENT] ? 1 : 0) +
        (valid_symbols[RAW_STRING_CONTENT] ? 1 : 0);
    bool in_string_content = valid_count == 1;

    if ((valid_symbols[ATTACHED_COMMENT_TEST_PROMPT] ||
         valid_symbols[ATTACHED_COMMENT_TEST_LINE_PROMPT] ||
         valid_symbols[ATTACHED_TEST_PROMPT_GROUP_END]) &&
        (!in_string_content ||
         (scanner != NULL && scanner->in_attached_test_prompt_group))) {
        if (scan_attached_comment_marker(scanner, lexer, valid_symbols)) {
            return true;
        }
    }

    // `case`-arm separator. Same guard: only in a clean (non-string) context.
    if (valid_symbols[CASE_ARM_SEP] &&
        !in_string_content) {
        if (scan_case_arm_sep(lexer)) {
            return true;
        }
        // Fall through: not a `.Uppercase` boundary, so let the internal lexer
        // drive (the newline is plain whitespace continuing the arm body).
    }

    // Headless anon-struct / map-literal opening brace. Same clean-context guard.
    if ((valid_symbols[ANON_STRUCT_OPEN] || valid_symbols[MAP_OPEN]) &&
        !in_string_content) {
        if (scan_headless_brace(lexer, valid_symbols)) {
            return true;
        }
    }

    // Multiple content externals being valid simultaneously means
    // we're either at the parser's start state or in error
    // recovery — never legitimately inside one specific string
    // variant. Refuse, so the internal lexer (which handles the
    // opening `"` / `"""` / backtick) gets a chance to drive the parser
    // into a real string state.
    if (valid_count > 1) {
        return false;
    }

    if (valid_symbols[STRING_CONTENT]) {
        return scan_string_content(lexer);
    }
    if (valid_symbols[TRIPLE_STRING_CONTENT]) {
        return scan_triple_string_content(scanner, lexer);
    }
    if (valid_symbols[RAW_STRING_CONTENT]) {
        return scan_raw_string_content(scanner, lexer);
    }
    return false;
}
