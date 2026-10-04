package token

// TokenType represents the type of a lexical token.
type TokenType int

const (
	// Special
	ILLEGAL TokenType = iota
	EOF
	NEWLINE
	SEMICOLON

	// Literals
	INT
	FLOAT
	DECIMAL
	STRING

	// Identifiers
	IDENT      // lowercase start
	TYPE_IDENT // uppercase start

	// Operators
	PLUS      // +
	MINUS     // -
	STAR      // *
	SLASH     // /
	PERCENT   // %
	BANG      // !
	EQ        // = (binding)
	DOT       // .
	COMMA     // ,
	COLON     // :
	ARROW     // ->
	PIPE      // |>
	FAT_ARROW // =>
	BAR       // |
	DOTDOT    // .. (half-open range)
	DOTDOTEQ  // ..= (inclusive range)
	AT        // @ (decorator marker)
	HASH      // # (collection literal sigil)
	CARET     // ^ (reserved; not an expression prefix)

	// Comparison
	EQEQ   // ==
	BANGEQ // !=
	LT     // <
	GT     // >
	LTEQ   // <=
	GTEQ   // >=

	// Keywords
	AND
	OR
	IF
	ELSE
	FN
	RETURN
	CASE
	WHEN
	TYPE
	STRUCT
	ENUM
	TYPEALIAS
	BREAK
	CONTINUE
	IT
	UNDERSCORE
	INTERFACE   // Interface
	IMPL        // impl
	AS          // as
	FOR         // for
	IMPORT      // import
	EXTERN      // extern
	HOST        // host
	EXPORT      // export
	PUB         // pub — inline visibility modifier on declarations
	ONCE        // once — file-level immutable, lazy on first access
	OPAQUE      // opaque — qualifier in export blocks for opaque distinct types
	WHERE       // where — method-level interface-bound clause on interface default methods
	WITH        // with — scoped binding statement introducer
	DEFER       // defer — block-scoped cleanup statement
	TEST        // test — executable test case declaration
	TESTS       // tests — test container declaration
	ASSERT      // assert — assertion statement
	REFUTE      // refute — negated assertion statement
	DBG         // dbg — debug-print expression and return its value
	TODO        // todo — placeholder expression for unwritten code; traps when reached
	SELF        // self — keyword for the implementing type in interface signatures and for import self-markers
	TRY         // try — prefix error-propagation keyword (unwrap Result/Maybe or short-circuit)
	DOC_COMMENT // /// doc comment text
	COMMENT     // // line comment
	TEST_PROMPT // //! attached test line prompt
	BLANK_LINE  // a blank line between statements

	// Delimiters
	LBRACE   // {
	RBRACE   // }
	LPAREN   // (
	RPAREN   // )
	LBRACKET // [
	RBRACKET // ]

	// String interpolation parts (single-line `"..."` form)
	STRING_START   // opening portion of interpolated string
	STRING_PART    // middle portion between interpolations
	STRING_END     // closing portion of interpolated string
	STRING_LITERAL // string with no interpolation

	// Triple-quoted string variants (`"""..."""` form).
	//
	// These are distinct token kinds — not a flag on the single-line
	// kinds — because the source-form distinction (single-line vs
	// triple-quoted) is what the formatter needs to round-trip
	// triple-quoted literals as triple-quoted, rather than collapsing
	// them to `\n`-escaped single-line form. Putting the discriminator in
	// the kind keeps it where token-shape information belongs.
	TRIPLE_STRING_LITERAL // triple-quoted string with no interpolation
	TRIPLE_STRING_START   // opening portion of interpolated triple-quoted
	TRIPLE_STRING_PART    // middle portion between interpolations
	TRIPLE_STRING_END     // closing portion of interpolated triple-quoted

	// Raw string variants (backtick forms).
	//
	// Raw strings disable `${...}` interpolation parsing and the `\${`
	// escape — the body is verbatim text. Same rationale as the
	// triple-quoted family: the discriminator lives in the kind, not as
	// a flag on the shared Token struct, so the formatter can round-trip
	// the source form. Raw strings can never have interpolation slots,
	// so there is no RAW_STRING_START/PART/END family.
	RAW_STRING_LITERAL        // `raw single-line`
	RAW_TRIPLE_STRING_LITERAL // multi-line `raw`

	// Tagged string variants (`<tag>"..."`, `<tag>"""..."""`, and
	// `<tag>`raw`` forms). The tag is a snake_case identifier
	// immediately adjacent to the string opener (no whitespace
	// in-between). The tag name itself is carried on the Token's Tag
	// field; the kind discriminates the *shape* (single-line vs triple
	// vs raw) so the formatter and parser don't need to peek at adjacent
	// tokens to know what they're looking at.
	//
	// For interpolated tagged forms, only the START token carries the
	// tagged kind (and the Tag string); subsequent PART/END tokens reuse
	// the existing STRING_PART/STRING_END (or TRIPLE_STRING_PART/
	// TRIPLE_STRING_END) kinds — they're shape-identical to untagged.
	// Raw-tagged forms can never have interpolation slots, so they only
	// have a literal kind.
	TAGGED_STRING_LITERAL            // sql"SELECT 1" (no interpolation)
	TAGGED_STRING_START              // sql"id = ${id}" (opener of interpolated)
	TAGGED_TRIPLE_STRING_LITERAL     // sql"""..."""   (no interpolation)
	TAGGED_TRIPLE_STRING_START       // sql"""... ${x} ..."""
	RAW_TAGGED_STRING_LITERAL        // Regex`\d+`
	RAW_TAGGED_TRIPLE_STRING_LITERAL // Bash`...`

	// CODEPOINT_LITERAL is an ASCII codepoint literal, `'a'` or `'\n'`. Its
	// Lexeme is the body between the quotes exactly as written, escapes
	// undecoded, so the formatter can reproduce the spelling;
	// strlit.DecodeCodepoint gives the value. The lexer emits it only for a
	// body that decodes to one ASCII codepoint.
	CODEPOINT_LITERAL
)

var tokenNames = [...]string{
	ILLEGAL:                          "ILLEGAL",
	EOF:                              "EOF",
	NEWLINE:                          "NEWLINE",
	SEMICOLON:                        "SEMICOLON",
	INT:                              "INT",
	FLOAT:                            "FLOAT",
	DECIMAL:                          "DECIMAL",
	STRING:                           "STRING",
	IDENT:                            "IDENT",
	TYPE_IDENT:                       "TYPE_IDENT",
	PLUS:                             "PLUS",
	MINUS:                            "MINUS",
	STAR:                             "STAR",
	SLASH:                            "SLASH",
	PERCENT:                          "PERCENT",
	BANG:                             "BANG",
	EQ:                               "EQ",
	DOT:                              "DOT",
	COMMA:                            "COMMA",
	COLON:                            "COLON",
	ARROW:                            "ARROW",
	PIPE:                             "PIPE",
	FAT_ARROW:                        "FAT_ARROW",
	BAR:                              "BAR",
	DOTDOT:                           "DOTDOT",
	DOTDOTEQ:                         "DOTDOTEQ",
	AT:                               "AT",
	HASH:                             "HASH",
	CARET:                            "CARET",
	EQEQ:                             "EQEQ",
	BANGEQ:                           "BANGEQ",
	LT:                               "LT",
	GT:                               "GT",
	LTEQ:                             "LTEQ",
	GTEQ:                             "GTEQ",
	AND:                              "AND",
	OR:                               "OR",
	IF:                               "IF",
	ELSE:                             "ELSE",
	FN:                               "FN",
	RETURN:                           "RETURN",
	CASE:                             "CASE",
	WHEN:                             "WHEN",
	TYPE:                             "TYPE",
	STRUCT:                           "STRUCT",
	ENUM:                             "ENUM",
	TYPEALIAS:                        "TYPEALIAS",
	BREAK:                            "BREAK",
	CONTINUE:                         "CONTINUE",
	IT:                               "IT",
	UNDERSCORE:                       "UNDERSCORE",
	INTERFACE:                        "INTERFACE",
	IMPL:                             "IMPL",
	AS:                               "AS",
	FOR:                              "FOR",
	IMPORT:                           "IMPORT",
	EXTERN:                           "EXTERN",
	HOST:                             "HOST",
	EXPORT:                           "EXPORT",
	PUB:                              "PUB",
	ONCE:                             "ONCE",
	OPAQUE:                           "OPAQUE",
	WHERE:                            "WHERE",
	WITH:                             "WITH",
	DEFER:                            "DEFER",
	TEST:                             "TEST",
	TESTS:                            "TESTS",
	ASSERT:                           "ASSERT",
	REFUTE:                           "REFUTE",
	DBG:                              "DBG",
	TODO:                             "TODO",
	SELF:                             "SELF",
	TRY:                              "TRY",
	DOC_COMMENT:                      "DOC_COMMENT",
	COMMENT:                          "COMMENT",
	TEST_PROMPT:                      "TEST_PROMPT",
	BLANK_LINE:                       "BLANK_LINE",
	LBRACE:                           "LBRACE",
	RBRACE:                           "RBRACE",
	LPAREN:                           "LPAREN",
	RPAREN:                           "RPAREN",
	LBRACKET:                         "LBRACKET",
	RBRACKET:                         "RBRACKET",
	STRING_START:                     "STRING_START",
	STRING_PART:                      "STRING_PART",
	STRING_END:                       "STRING_END",
	STRING_LITERAL:                   "STRING_LITERAL",
	TRIPLE_STRING_LITERAL:            "TRIPLE_STRING_LITERAL",
	TRIPLE_STRING_START:              "TRIPLE_STRING_START",
	TRIPLE_STRING_PART:               "TRIPLE_STRING_PART",
	TRIPLE_STRING_END:                "TRIPLE_STRING_END",
	RAW_STRING_LITERAL:               "RAW_STRING_LITERAL",
	RAW_TRIPLE_STRING_LITERAL:        "RAW_TRIPLE_STRING_LITERAL",
	TAGGED_STRING_LITERAL:            "TAGGED_STRING_LITERAL",
	TAGGED_STRING_START:              "TAGGED_STRING_START",
	TAGGED_TRIPLE_STRING_LITERAL:     "TAGGED_TRIPLE_STRING_LITERAL",
	TAGGED_TRIPLE_STRING_START:       "TAGGED_TRIPLE_STRING_START",
	RAW_TAGGED_STRING_LITERAL:        "RAW_TAGGED_STRING_LITERAL",
	RAW_TAGGED_TRIPLE_STRING_LITERAL: "RAW_TAGGED_TRIPLE_STRING_LITERAL",
	CODEPOINT_LITERAL:                "CODEPOINT_LITERAL",
}

// String returns the name of the token type.
func (t TokenType) String() string {
	if int(t) >= 0 && int(t) < len(tokenNames) {
		return tokenNames[t]
	}
	return "UNKNOWN"
}

// Token represents a single lexical token.
//
// The single-line vs triple-quoted vs raw distinction for string tokens
// is carried in the Type — STRING_LITERAL/STRING_START/STRING_PART/
// STRING_END for `"..."`, TRIPLE_STRING_LITERAL/TRIPLE_STRING_START/
// TRIPLE_STRING_PART/TRIPLE_STRING_END for `"""..."""`, and
// RAW_STRING_LITERAL / RAW_TRIPLE_STRING_LITERAL for raw backtick forms
// (which never have interpolation parts, since
// raw bodies are verbatim text). There is no separate flag.
//
// Tag is populated only on the tagged-string token kinds
// (TAGGED_STRING_LITERAL / TAGGED_STRING_START /
// TAGGED_TRIPLE_STRING_LITERAL / TAGGED_TRIPLE_STRING_START /
// RAW_TAGGED_STRING_LITERAL / RAW_TAGGED_TRIPLE_STRING_LITERAL) and
// holds the snake_case tag identifier (e.g. `sql`, `regex`, `bash`).
// Empty on every other token kind. Tag is metadata on the kind, not a
// shape discriminator — the kind already says "this is a tagged string
// of shape X"; Tag just adds "and the tag name is Y".
//
// Problem is set only on an ILLEGAL token whose lexer diagnosis is a
// complete message (a malformed codepoint literal); the parser reports it
// verbatim instead of "unexpected token".
//
// EndCol is one past the source text of the last token a lexer step produced
// (a string literal's closing quote, which its Lexeme leaves out), on line
// EndLine when that is set and Line otherwise. Zero when unknown; then the
// token ends len(Lexeme) bytes after Col.
type Token struct {
	Type    TokenType
	Lexeme  string
	Tag     string
	Problem string
	Line    int
	Col     int
	EndLine int
	EndCol  int
}
