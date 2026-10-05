package lexer

import (
	"github.com/nomi-language/nomi/internal/strlit"
	"github.com/nomi-language/nomi/internal/token"
	"strings"
	"unicode"
)

// Lex tokenizes the source string into a slice of tokens.
//
// A shebang line (see Shebang) yields no token: it is for the operating
// system, not part of the program. The lexer still steps over it, so every
// token after it keeps its source line and column.
func Lex(source string) []token.Token {
	l := &lexer{
		source: source,
		tokens: make([]token.Token, 0, 64),
		line:   1,
		col:    1,
	}
	for range len(Shebang(source)) {
		l.advance()
	}
	l.scan()
	return l.tokens
}

// Shebang returns the source's `#!` interpreter line without its line
// ending, or "" when there is none. Only the first line can be one: `#!`
// must start at byte offset 0. Anywhere else `#!` is a lexical error.
func Shebang(source string) string {
	if !strings.HasPrefix(source, "#!") {
		return ""
	}
	line, _, _ := strings.Cut(source, "\n")
	return strings.TrimSuffix(line, "\r")
}

// IsComplete checks whether the input appears to be a complete expression,
// suitable for the REPL to decide whether to evaluate or continue reading.
// Returns false if there are unclosed delimiters or the last token cannot
// end a statement (e.g., trailing operators, pipes, commas).
func IsComplete(source string) bool {
	if source == "" {
		return true
	}
	tokens := Lex(source)

	// Check for unclosed delimiters
	depth := 0
	for _, tok := range tokens {
		switch tok.Type {
		case token.LPAREN, token.LBRACKET, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACKET, token.RBRACE:
			depth--
		}
	}
	if depth > 0 {
		return false
	}

	// Find last non-EOF, non-NEWLINE, non-trivia token
	var last token.TokenType
	for i := len(tokens) - 1; i >= 0; i-- {
		tt := tokens[i].Type
		if tt != token.EOF && tt != token.NEWLINE && tt != token.COMMENT && tt != token.TEST_PROMPT && tt != token.BLANK_LINE {
			last = tt
			break
		}
	}

	// If no real tokens, it's complete (empty/whitespace)
	if last == 0 {
		return true
	}

	return canEndStatement(last)
}

type lexer struct {
	source             string
	tokens             []token.Token
	pos                int
	line               int
	col                int
	inTestPromptedLine bool
}

func (l *lexer) scan() {
	for !l.atEnd() {
		before := len(l.tokens)
		l.scanToken()
		// A string literal's Lexeme is its value, not its source text, so
		// its end is where the step that read it stopped.
		if n := len(l.tokens); n > before && endsAtCursor(l.tokens[n-1].Type) {
			l.tokens[n-1].EndCol = l.col
			if l.line != l.tokens[n-1].Line {
				l.tokens[n-1].EndLine = l.line
			}
		}
	}
	l.emit(token.EOF, "")
}

func (l *lexer) scanToken() {
	ch := l.peek()

	switch {
	case ch == '\n':
		l.scanNewline()
	case ch == ' ' || ch == '\t' || ch == '\r':
		l.advance()
	case ch == '/' && l.peekAt(1) == '/':
		l.scanLineComment()
	case ch == '"':
		l.scanString("")
	case ch == '`':
		l.scanRawBacktick("")
	case ch == '\'':
		l.scanCodepoint()
	case isDigit(ch):
		l.scanNumber()
	case isIdentStart(ch):
		l.scanIdentifier()
	default:
		l.scanOperatorOrDelimiter()
	}
}

// --- Newlines ---

func (l *lexer) scanNewline() {
	line, col := l.line, l.col
	// Consume all consecutive newlines (and whitespace between them),
	// counting how many `\n` characters we saw so we can decide whether
	// a blank line (2+ newlines) was present.
	nlCount := 0
	for !l.atEnd() {
		ch := l.peek()
		if ch == '\n' {
			nlCount++
			l.advance()
		} else if ch == ' ' || ch == '\t' || ch == '\r' {
			l.advance()
		} else {
			break
		}
	}
	if nlCount > 0 {
		l.inTestPromptedLine = false
	}
	// Find the last non-COMMENT token to decide whether to emit NEWLINE.
	// Trailing COMMENT tokens (from `// ...` on the preceding line) should not
	// suppress a newline that would otherwise be emitted.
	lastIdx := len(l.tokens) - 1
	for lastIdx >= 0 && (l.tokens[lastIdx].Type == token.COMMENT || l.tokens[lastIdx].Type == token.TEST_PROMPT) {
		lastIdx--
	}
	// Only emit NEWLINE if previous token can end a statement
	// and the next token is not |> (pipe continuation).
	// BLANK_LINE emission follows the same gate: blank lines only matter
	// between statements, not inside expressions (multi-line calls,
	// list literals, etc.) where the formatter handles layout itself.
	if lastIdx >= 0 && canEndStatement(l.tokens[lastIdx].Type) {
		if !l.atEnd() {
			nextPos := l.nextTokenStartPosAfterPrompt()
			next := byte(0)
			nextNext := byte(0)
			if nextPos < len(l.source) {
				next = l.source[nextPos]
			}
			if nextPos+1 < len(l.source) {
				nextNext = l.source[nextPos+1]
			}
			// Suppress newline before pipe continuation or closing delimiters
			if (next == '|' && nextNext == '>') || next == ')' || next == ']' {
				return
			}
		}
		l.tokens = append(l.tokens, token.Token{Type: token.NEWLINE, Lexeme: "\n", Line: line, Col: col})
		// A run of 2+ newlines represents a blank line separator between
		// statements. Emit at most one BLANK_LINE token (multi-blank collapse).
		if nlCount >= 2 {
			l.tokens = append(l.tokens, token.Token{Type: token.BLANK_LINE, Lexeme: "", Line: line, Col: col})
		}
	}
}

func (l *lexer) nextTokenStartPosAfterPrompt() int {
	if l.peek() != '/' || l.peekAt(1) != '/' || l.peekAt(2) != '!' || !l.linePrefixIsWhitespace(l.pos) {
		return l.pos
	}
	i := l.pos + 3
	for i < len(l.source) && (l.source[i] == ' ' || l.source[i] == '\t') {
		i++
	}
	return i
}

// --- Comments ---

func (l *lexer) scanLineComment() {
	startLine, startCol := l.line, l.col
	start := l.pos

	if l.peekAt(2) == '!' && l.linePrefixIsWhitespace(start) {
		l.advance()
		l.advance()
		l.advance()
		l.inTestPromptedLine = true
		l.tokens = append(l.tokens, token.Token{
			Type:   token.TEST_PROMPT,
			Lexeme: "//!",
			Line:   startLine,
			Col:    startCol,
		})
		return
	}

	// Skip "//"
	l.advance()
	l.advance()

	// Check for doc comment: "///" but NOT "////" (four slashes is a regular comment)
	if !l.atEnd() && l.peek() == '/' && (l.pos+1 >= len(l.source) || l.source[l.pos+1] != '/') {
		l.advance() // skip third '/'
		textStart := l.pos
		for !l.atEnd() && l.peek() != '\n' {
			l.advance()
		}
		text := l.source[textStart:l.pos]
		l.tokens = append(l.tokens, token.Token{
			Type: token.DOC_COMMENT, Lexeme: text, Line: startLine, Col: startCol,
		})
		return
	}

	// Regular comment — consume to end of line and emit COMMENT token
	for !l.atEnd() && l.peek() != '\n' {
		l.advance()
	}
	l.tokens = append(l.tokens, token.Token{
		Type: token.COMMENT, Lexeme: l.source[start:l.pos], Line: startLine, Col: startCol,
	})
}

func (l *lexer) linePrefixIsWhitespace(pos int) bool {
	for i := pos - 1; i >= 0; i-- {
		switch l.source[i] {
		case '\n':
			return true
		case ' ', '\t', '\r':
			continue
		default:
			return false
		}
	}
	return true
}

// --- Strings ---

// scanRawBacktick scans a raw backtick string. The cursor is at the
// opening backtick; if tag is non-empty it is a PascalCase typed literal
// tag that was consumed immediately before the backtick.
//
// Backtick strings disable escape processing and `${...}` interpolation.
// When the literal spans multiple lines it uses the same
// indentation rules as triple-quoted strings: a leading newline after the
// opener is stripped, the closing delimiter's indentation anchors body
// indentation stripping, and a final newline before the closer is stripped.
func (l *lexer) scanRawBacktick(tag string) {
	startLine, startCol := l.line, l.col
	stripAttachedPrompts := l.inTestPromptedLine
	l.advance() // consume opening backtick

	if !l.atEnd() && l.peek() == '\n' {
		l.advance()
		if stripAttachedPrompts {
			l.skipAttachedTestPromptPrefix()
		}
	}

	buf := make([]byte, 0, 128)
	for !l.atEnd() {
		ch := l.peek()
		if ch == '`' {
			endLine := l.line
			l.advance()

			if endLine > startLine {
				parts := []tripleStringPart{{isText: true, text: string(buf)}}
				indent := tripleStringMinIndent(parts)
				if lastNL := strings.LastIndex(parts[0].text, "\n"); lastNL >= 0 {
					parts[0].text = parts[0].text[:lastNL]
				}
				content := stripLinesIndent(assembleTripleText(parts), indent, true)

				kind := token.RAW_TRIPLE_STRING_LITERAL
				if tag != "" {
					kind = token.RAW_TAGGED_TRIPLE_STRING_LITERAL
				}
				l.tokens = append(l.tokens, token.Token{Type: kind, Lexeme: content, Tag: tag, Line: startLine, Col: startCol, EndLine: endLine})
				return
			}

			kind := token.RAW_STRING_LITERAL
			if tag != "" {
				kind = token.RAW_TAGGED_STRING_LITERAL
			}
			l.tokens = append(l.tokens, token.Token{Type: kind, Lexeme: string(buf), Tag: tag, Line: startLine, Col: startCol})
			return
		}
		buf = append(buf, byte(ch))
		l.advance()
	}
	// Unterminated raw string.
	l.tokens = append(l.tokens, token.Token{Type: token.ILLEGAL, Lexeme: string(buf), Line: startLine, Col: startCol})
}

// scanString scans a regular (non-raw) single-line string. Cursor sits
// at the opening `"` on entry. If tag is non-empty, the *opener* token
// kind is the tagged variant (TAGGED_STRING_LITERAL or
// TAGGED_STRING_START) carrying Tag; subsequent STRING_PART/STRING_END
// for an interpolated form are kind-shape-identical to the untagged
// case (no Tag on those — only the opener carries it).
func (l *lexer) scanString(tag string) {
	startLine, startCol := l.line, l.col

	// Check for triple-quoted string
	if l.peekAt(1) == '"' && l.peekAt(2) == '"' {
		l.scanTripleStringWithMode(false /* raw */, tag)
		return
	}

	// Skip opening quote
	l.advance()

	buf := make([]byte, 0, 32)
	hasInterpolation := false
	firstPart := true

	for !l.atEnd() {
		ch := l.peek()

		if ch == '"' {
			// End of string
			l.advance()
			if hasInterpolation {
				// Closing token: STRING_END regardless of tagged-ness.
				// Tag was attached only to the opening START.
				l.tokens = append(l.tokens, token.Token{Type: token.STRING_END, Lexeme: string(buf), Line: startLine, Col: startCol})
			} else {
				kind := token.STRING_LITERAL
				if tag != "" {
					kind = token.TAGGED_STRING_LITERAL
				}
				l.tokens = append(l.tokens, token.Token{Type: kind, Lexeme: string(buf), Tag: tag, Line: startLine, Col: startCol})
			}
			return
		}

		if ch == '\\' {
			// Escape sequence
			l.advance()
			if l.atEnd() {
				break
			}
			esc := l.peek()
			l.advance()
			if esc == 'u' {
				if l.peek() != '{' {
					l.illegalString(`\u escape requires braces, e.g. \u{1F600}`, startLine, startCol)
					return
				}
				l.advance() // skip '{'
				hexStart := l.pos
				for !l.atEnd() && l.peek() != '}' {
					l.advance()
				}
				hexStr := l.source[hexStart:l.pos]
				if l.atEnd() || l.peek() != '}' {
					l.illegalString(`unterminated \u{...} escape`, startLine, startCol)
					return
				}
				l.advance() // skip '}'
				// strlit decodes the escape for codepoint literals too: 1-6
				// hex digits naming a scalar value. A surrogate would
				// otherwise be coerced to U+FFFD.
				codepoint, problem := strlit.DecodeUnicodeEscape(hexStr)
				if problem != "" {
					l.illegalString(problem, startLine, startCol)
					return
				}
				buf = append(buf, []byte(string(codepoint))...)
			} else if esc == '$' && l.peek() == '{' {
				// `\${` writes a literal `${`; only that pair needs escaping.
				buf = append(buf, '$')
			} else if r, ok := strlit.DecodeSimple(byte(esc)); ok {
				// Simple escape (\n, \t, \\, \") decoded via the shared strlit
				// table, so the lexer and the formatter's encoder can't drift.
				buf = append(buf, []byte(string(r))...)
			} else {
				// Unknown escape — reject it rather than silently keeping the
				// backslash literal. Valid escapes: \n \t \\ \" and \u{HEX}.
				l.illegalString(`invalid escape \`+string(rune(esc)), startLine, startCol)
				return
			}
			continue
		}

		if ch == '$' && l.peekAt(1) == '{' {
			// Start interpolation
			hasInterpolation = true
			if firstPart {
				kind := token.STRING_START
				if tag != "" {
					kind = token.TAGGED_STRING_START
				}
				l.tokens = append(l.tokens, token.Token{Type: kind, Lexeme: string(buf), Tag: tag, Line: startLine, Col: startCol})
				firstPart = false
			} else {
				l.tokens = append(l.tokens, token.Token{Type: token.STRING_PART, Lexeme: string(buf), Line: startLine, Col: startCol})
			}
			buf = buf[:0]
			l.advance() // skip '$'
			l.advance() // skip '{'

			// Scan tokens inside interpolation until matching '}'
			l.scanInterpolation()
			startLine, startCol = l.line, l.col
			continue
		}

		buf = append(buf, byte(ch))
		l.advance()
	}

	// Unterminated string — emit what we have as ILLEGAL
	l.tokens = append(l.tokens, token.Token{Type: token.ILLEGAL, Lexeme: string(buf), Line: startLine, Col: startCol})
}

// illegalString ends a string literal at a malformed escape: an ILLEGAL token
// whose Problem is the diagnosis, which the parser reports verbatim as it
// does a malformed codepoint literal's.
func (l *lexer) illegalString(problem string, line, col int) {
	l.tokens = append(l.tokens, token.Token{Type: token.ILLEGAL, Lexeme: problem, Problem: problem, Line: line, Col: col})
}

// scanCodepoint scans an ASCII codepoint literal, `'a'` or `'\n'`. The cursor
// sits at the opening quote. The body runs to the next unescaped `'` on the
// same line; strlit.DecodeCodepoint decides whether it is one ASCII
// codepoint. A malformed literal becomes an ILLEGAL token carrying the
// diagnosis as its Problem, spanning the whole literal so scanning resumes
// after it.
func (l *lexer) scanCodepoint() {
	startLine, startCol := l.line, l.col
	l.advance() // opening quote
	start := l.pos
	for !l.atEnd() && l.peek() != '\n' && l.peek() != '\'' {
		if l.peek() == '\\' && l.pos+1 < len(l.source) && l.source[l.pos+1] != '\n' {
			l.advance()
		}
		l.advance()
	}
	if l.atEnd() || l.peek() != '\'' {
		l.tokens = append(l.tokens, token.Token{Type: token.ILLEGAL, Lexeme: "'" + l.source[start:l.pos],
			Problem: "unterminated codepoint literal; close it with '", Line: startLine, Col: startCol})
		return
	}
	body := l.source[start:l.pos]
	l.advance() // closing quote
	if _, problem := strlit.DecodeCodepoint(body); problem != "" {
		l.tokens = append(l.tokens, token.Token{Type: token.ILLEGAL, Lexeme: "'" + body + "'", Problem: problem, Line: startLine, Col: startCol})
		return
	}
	l.tokens = append(l.tokens, token.Token{Type: token.CODEPOINT_LITERAL, Lexeme: body, Line: startLine, Col: startCol})
}

func (l *lexer) scanInterpolation() {
	braceDepth := 1
	for !l.atEnd() && braceDepth > 0 {
		ch := l.peek()
		if ch == '{' {
			braceDepth++
			l.emit(token.LBRACE, "{")
			l.advance()
		} else if ch == '}' {
			braceDepth--
			if braceDepth == 0 {
				l.advance() // consume closing brace, don't emit it
				return
			}
			l.emit(token.RBRACE, "}")
			l.advance()
		} else {
			l.scanToken()
		}
	}
}

// --- Triple-quoted strings ---

// tripleStringPart holds either a text segment or a placeholder for interpolated tokens.
type tripleStringPart struct {
	isText bool
	text   string
	tokens []token.Token // interpolated expression tokens
}

// scanTripleStringWithMode scans a triple-quoted string body. The
// `raw` flag controls how `${` and `\${` are handled inside the body:
//
//   - raw=false (regular triple-quoted): `${...}` opens an interpolation
//     slot; `\${` writes a literal `${`. The emitted token kind
//     is TRIPLE_STRING_LITERAL (no interpolation) or
//     TRIPLE_STRING_START/PART/END (with interpolation).
//   - raw=true: both `${` and `\${` are verbatim text. No
//     interpolation slots can appear, so the emitted token kind is
//     always RAW_TRIPLE_STRING_LITERAL — there is no raw interpolation
//     family.
//
// Indentation rules (leading-newline strip, min-indent baseline strip,
// trailing-newline strip) are identical in both modes — the rules
// govern indentation, not escape processing.
//
// If tag is non-empty the *opening* token kind is the tagged variant —
// TAGGED_TRIPLE_STRING_LITERAL/START in regular mode, or
// RAW_TAGGED_TRIPLE_STRING_LITERAL in raw mode (raw-tagged can't be
// interpolated). Subsequent TRIPLE_STRING_PART/END tokens reuse the
// untagged kinds (only the opener carries Tag).
func (l *lexer) scanTripleStringWithMode(raw bool, tag string) {
	startLine, startCol := l.line, l.col
	stripAttachedPrompts := l.inTestPromptedLine
	// Skip opening """
	l.advance()
	l.advance()
	l.advance()

	// Skip leading newline immediately after opening """
	if !l.atEnd() && l.peek() == '\n' {
		l.advance()
		if stripAttachedPrompts {
			l.skipAttachedTestPromptPrefix()
		}
	}

	// Collect all parts (text segments and interpolation token groups)
	var parts []tripleStringPart
	buf := make([]byte, 0, 128)
	hasInterpolation := false

	for !l.atEnd() {
		ch := l.peek()

		// Check for closing """
		if ch == '"' && l.peekAt(1) == '"' && l.peekAt(2) == '"' {
			parts = append(parts, tripleStringPart{isText: true, text: string(buf)})
			l.advance()
			l.advance()
			l.advance()
			endLine := l.line

			// Determine the strip baseline (Java JEP 378 semantics): the
			// minimum leading whitespace across all non-blank body lines,
			// including the closing-delimiter line.
			indent := tripleStringMinIndent(parts)

			// Remove trailing indent line from last text part
			for i := len(parts) - 1; i >= 0; i-- {
				if parts[i].isText {
					if lastNL := strings.LastIndex(parts[i].text, "\n"); lastNL >= 0 {
						parts[i].text = parts[i].text[:lastNL]
					}
					break
				}
			}

			// Emit tokens with indentation stripped from text parts.
			// The TRIPLE_STRING_* / RAW_TRIPLE_STRING_LITERAL token
			// kinds carry the source-form discriminator (vs single-line
			// STRING_*, RAW_STRING_LITERAL) end-to-end so the parser
			// can flag the resulting AST node as triple-quoted/raw and
			// the formatter can round-trip the source form.
			if raw {
				// Raw triple: a single literal token; no interpolation
				// is possible (the loop never produced interpolation
				// parts because `${` is literal in raw mode).
				full := assembleTripleText(parts)
				content := stripLinesIndent(full, indent, true)
				kind := token.RAW_TRIPLE_STRING_LITERAL
				if tag != "" {
					kind = token.RAW_TAGGED_TRIPLE_STRING_LITERAL
				}
				l.tokens = append(l.tokens, token.Token{Type: kind, Lexeme: content, Tag: tag, Line: startLine, Col: startCol, EndLine: endLine})
				return
			}
			if !hasInterpolation {
				full := assembleTripleText(parts)
				content := stripLinesIndent(full, indent, true)
				kind := token.TRIPLE_STRING_LITERAL
				if tag != "" {
					kind = token.TAGGED_TRIPLE_STRING_LITERAL
				}
				l.tokens = append(l.tokens, token.Token{Type: kind, Lexeme: content, Tag: tag, Line: startLine, Col: startCol, EndLine: endLine})
			} else {
				firstText := true
				// Track whether the *next* text part begins at a line
				// start. Initially true (the body's first text follows
				// the opening `"""\n`). After an interpolation slot,
				// the next text part continues mid-line — its leading
				// bytes are user content, not indent. After a text
				// part whose final char is `\n`, the following part
				// begins at a line start again.
				nextAtLineStart := true
				for _, p := range parts {
					if p.isText {
						stripped := stripLinesIndent(p.text, indent, nextAtLineStart)
						if firstText {
							kind := token.TRIPLE_STRING_START
							tt := tag
							if tag != "" {
								kind = token.TAGGED_TRIPLE_STRING_START
							} else {
								tt = ""
							}
							l.tokens = append(l.tokens, token.Token{Type: kind, Lexeme: stripped, Tag: tt, Line: startLine, Col: startCol, EndLine: endLine})
							firstText = false
						} else {
							l.tokens = append(l.tokens, token.Token{Type: token.TRIPLE_STRING_PART, Lexeme: stripped, Line: startLine, Col: startCol, EndLine: endLine})
						}
						// Update nextAtLineStart for whatever text part
						// follows. Subsequent parts after this text are
						// either interpolation tokens (which don't
						// affect line-start status — see below) or
						// another text part (rare but possible if two
						// adjacent slots produce empty interpolation —
						// not a real shape today).
						if strings.HasSuffix(p.text, "\n") {
							nextAtLineStart = true
						} else {
							nextAtLineStart = false
						}
					} else {
						l.tokens = append(l.tokens, p.tokens...)
						// After an interpolation slot the next text
						// part begins mid-line.
						nextAtLineStart = false
					}
				}
				// Change the last text token from TRIPLE_STRING_START/
				// TAGGED_TRIPLE_STRING_START/TRIPLE_STRING_PART to
				// TRIPLE_STRING_END (the END token is shape-identical
				// regardless of tagged-ness — only the opener carries
				// the tag).
				for i := len(l.tokens) - 1; i >= 0; i-- {
					tt := l.tokens[i].Type
					if tt == token.TRIPLE_STRING_START || tt == token.TAGGED_TRIPLE_STRING_START || tt == token.TRIPLE_STRING_PART {
						l.tokens[i].Type = token.TRIPLE_STRING_END
						break
					}
				}
			}
			return
		}

		if !raw && ch == '\\' && l.peekAt(1) == '$' && l.peekAt(2) == '{' {
			// `\${` writes a literal `${` (regular triple only). It is the
			// one backslash sequence a triple-quoted string reads.
			buf = append(buf, '$', '{')
			l.advance()
			l.advance()
			l.advance()
			continue
		}

		if !raw && ch == '$' && l.peekAt(1) == '{' {
			// Start interpolation (regular triple only — raw treats
			// `${` as verbatim text).
			parts = append(parts, tripleStringPart{isText: true, text: string(buf)})
			buf = buf[:0]
			hasInterpolation = true
			l.advance() // skip '$'
			l.advance() // skip '{'

			// Capture interpolation tokens
			savedLen := len(l.tokens)
			l.scanInterpolation()
			interpTokens := make([]token.Token, len(l.tokens)-savedLen)
			copy(interpTokens, l.tokens[savedLen:])
			l.tokens = l.tokens[:savedLen]
			parts = append(parts, tripleStringPart{isText: false, tokens: interpTokens})
			continue
		}

		// No escape processing — backslashes are literal in both
		// regular and raw triple-quoted strings.
		buf = append(buf, byte(ch))
		l.advance()
		if stripAttachedPrompts && ch == '\n' {
			l.skipAttachedTestPromptPrefix()
		}
	}

	// Unterminated triple-quoted string
	l.tokens = append(l.tokens, token.Token{Type: token.ILLEGAL, Lexeme: string(buf), Line: startLine, Col: startCol})
}

func (l *lexer) skipAttachedTestPromptPrefix() bool {
	pos := l.pos
	for pos < len(l.source) && (l.source[pos] == ' ' || l.source[pos] == '\t' || l.source[pos] == '\r') {
		pos++
	}
	if pos+2 >= len(l.source) || l.source[pos] != '/' || l.source[pos+1] != '/' || l.source[pos+2] != '!' {
		return false
	}
	for l.pos < pos+3 {
		l.advance()
	}
	if !l.atEnd() && (l.peek() == ' ' || l.peek() == '\t') {
		l.advance()
	}
	return true
}

// tripleStringMinIndent computes the strip baseline as the minimum leading
// whitespace across all non-blank body lines, including the closing-delimiter
// line. Blank (whitespace-only) lines are excluded from the calculation but
// their content is stripped per the computed baseline.
func tripleStringMinIndent(parts []tripleStringPart) string {
	// Concatenate all text parts into the body content. Note that
	// interpolation segments split text at the boundary; for indent
	// purposes we treat the concatenated text as the body. The final
	// line of the concatenated text is always the closing-delimiter
	// line (everything after the last '\n' before the closing """).
	var b strings.Builder
	for _, p := range parts {
		if p.isText {
			b.WriteString(p.text)
		}
	}
	lines := strings.Split(b.String(), "\n")
	var minIndent string
	seen := false
	for i, line := range lines {
		isLast := i == len(lines)-1
		ind := extractIndent(line)
		// Blank (empty or whitespace-only) lines are excluded from the
		// min calc, except the closing-delimiter line which is always
		// included (it anchors the baseline even when its indent is
		// shorter than the body's).
		if !isLast && (line == "" || ind == line) {
			continue
		}
		if !seen || len(ind) < len(minIndent) {
			minIndent = ind
			seen = true
		}
	}
	if !seen {
		return ""
	}
	return minIndent
}

// assembleTripleText concatenates all text parts (for non-interpolated case).
func assembleTripleText(parts []tripleStringPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.isText {
			b.WriteString(p.text)
		}
	}
	return b.String()
}

// stripLinesIndent removes up to len(indent) leading whitespace characters
// from each line in s, matching characters from the indent prefix. Lines
// that contributed to the min-indent baseline have at least this prefix and
// are stripped fully; blank lines with less leading whitespace are stripped
// of whatever they have (rather than left untouched, which would drag stray
// spaces into the body content).
//
// `firstAtLineStart` controls whether s's first line is treated as the
// start of a body line (true) or a continuation of the prior line (false).
// Text parts that follow a `${...}` interpolation slot continue mid-line
// in the source; their leading bytes are NOT indent and must not be
// stripped. Subsequent lines (after a `\n` inside s) are always treated
// as line starts and stripped per the baseline regardless of this flag.
func stripLinesIndent(s string, indent string, firstAtLineStart bool) string {
	if indent == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i == 0 && !firstAtLineStart {
			continue
		}
		n := 0
		for n < len(indent) && n < len(line) && line[n] == indent[n] {
			n++
		}
		lines[i] = line[n:]
	}
	return strings.Join(lines, "\n")
}

func extractIndent(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			return line[:i]
		}
	}
	return line // entire line is whitespace
}

// --- Numbers ---

func (l *lexer) scanNumber() {
	startLine, startCol := l.line, l.col
	start := l.pos
	isFloat := false

	if l.peek() == '0' && l.pos+1 < len(l.source) {
		next := l.source[l.pos+1]
		if next == 'x' || next == 'X' {
			l.advance() // '0'
			l.advance() // 'x'
			for !l.atEnd() && (isHexDigit(l.peek()) || l.peek() == '_') {
				l.advance()
			}
			l.tokens = append(l.tokens, token.Token{Type: token.INT, Lexeme: l.source[start:l.pos], Line: startLine, Col: startCol})
			return
		}
		if next == 'b' || next == 'B' {
			l.advance() // '0'
			l.advance() // 'b'
			for !l.atEnd() && (l.peek() == '0' || l.peek() == '1' || l.peek() == '_') {
				l.advance()
			}
			l.tokens = append(l.tokens, token.Token{Type: token.INT, Lexeme: l.source[start:l.pos], Line: startLine, Col: startCol})
			return
		}
		if next == 'o' || next == 'O' {
			l.advance() // '0'
			l.advance() // 'o'
			for !l.atEnd() && ((l.peek() >= '0' && l.peek() <= '7') || l.peek() == '_') {
				l.advance()
			}
			l.tokens = append(l.tokens, token.Token{Type: token.INT, Lexeme: l.source[start:l.pos], Line: startLine, Col: startCol})
			return
		}
	}

	// Decimal integer or float
	for !l.atEnd() && (isDigit(l.peek()) || l.peek() == '_') {
		l.advance()
	}

	// Check for '.' (float)
	if !l.atEnd() && l.peek() == '.' && l.pos+1 < len(l.source) && isDigit(l.source[l.pos+1]) {
		isFloat = true
		l.advance() // '.'
		for !l.atEnd() && (isDigit(l.peek()) || l.peek() == '_') {
			l.advance()
		}
	}

	// Check for 'e'/'E' (scientific notation)
	hasExponent := false
	if !l.atEnd() && (l.peek() == 'e' || l.peek() == 'E') {
		isFloat = true
		hasExponent = true
		l.advance()
		if !l.atEnd() && (l.peek() == '+' || l.peek() == '-') {
			l.advance()
		}
		for !l.atEnd() && (isDigit(l.peek()) || l.peek() == '_') {
			l.advance()
		}
	}

	// Check for a trailing 'd'/'D' decimal suffix (1.50d, 5d, 1_000.00D). The
	// suffix is case-insensitive to match every other numeric letter-marker in
	// this lexer (0x/0X, 0b/0B, 0o/0O, e/E). Only when the number has NO
	// exponent (no `1.5e3d` decimals — by design, decimal literals carry no
	// exponent), and only when the char after the suffix is not an identifier
	// char, so `1.50d` lexes as one DECIMAL token while `1.50days`/`1.50Days`
	// stays FLOAT `1.50` + ident. A suffix after a bare integer mantissa (`5d`)
	// is a valid scale-0 decimal.
	if !hasExponent && !l.atEnd() && (l.peek() == 'd' || l.peek() == 'D') &&
		!(l.pos+1 < len(l.source) && isIdentPart(l.source[l.pos+1])) {
		l.advance() // consume 'd'/'D'
		l.tokens = append(l.tokens, token.Token{Type: token.DECIMAL, Lexeme: l.source[start:l.pos], Line: startLine, Col: startCol})
		return
	}

	typ := token.INT
	if isFloat {
		typ = token.FLOAT
	}
	l.tokens = append(l.tokens, token.Token{Type: typ, Lexeme: l.source[start:l.pos], Line: startLine, Col: startCol})
}

// --- Identifiers and keywords ---

var keywords = map[string]token.TokenType{
	"if":        token.IF,
	"else":      token.ELSE,
	"and":       token.AND,
	"or":        token.OR,
	"fn":        token.FN,
	"return":    token.RETURN,
	"case":      token.CASE,
	"when":      token.WHEN,
	"type":      token.TYPE,
	"struct":    token.STRUCT,
	"enum":      token.ENUM,
	"typealias": token.TYPEALIAS,
	"break":     token.BREAK,
	"continue":  token.CONTINUE,
	"interface": token.INTERFACE,
	"impl":      token.IMPL,
	"as":        token.AS,
	"for":       token.FOR,
	"import":    token.IMPORT,
	"extern":    token.EXTERN,
	"export":    token.EXPORT,
	"pub":       token.PUB,
	"once":      token.ONCE,
	"opaque":    token.OPAQUE,
	"where":     token.WHERE,
	"with":      token.WITH,
	"defer":     token.DEFER,
	"test":      token.TEST,
	"tests":     token.TESTS,
	"assert":    token.ASSERT,
	"refute":    token.REFUTE,
	"dbg":       token.DBG,
	"todo":      token.TODO,
	"self":      token.SELF,
	"try":       token.TRY,
	"then":      token.THEN,
	// `field`, `variant`, and `open` are contextual keywords, recognized
	// by lexeme inside specific bodies only:
	//   - `field` in interface bodies (field requirements);
	//   - `variant` in interface bodies (variant requirements);
	//   - `open` before interface default methods.
	// Outside those contexts they remain regular identifiers — `struct
	// Box { field: T }` (a bare field named `field`), `binding open =
	// ...`, and locals named `variant` keep working. The parser does the
	// lexeme checks in parseInterfaceDef / parseInterfaceMethod /
	// parseStructBody / parseEnumItemBody / parseTypeDeclBody.
}

func (l *lexer) scanIdentifier() {
	startLine, startCol := l.line, l.col
	start := l.pos
	l.advance()
	for !l.atEnd() && isIdentPart(l.peek()) {
		l.advance()
	}
	// A single trailing `?` is part of the identifier (predicate convention,
	// e.g. `empty?`, `starts_with?`). Trailing-only and single — `foo?bar`
	// stays `foo?` + `bar`, `foo??` stays `foo?` + a stray `?`. Deliberately
	// NOT added to isIdentPart, which would let `?` appear mid-identifier.
	if !l.atEnd() && l.peek() == '?' {
		l.advance()
	}
	lexeme := l.source[start:l.pos]

	if tt, ok := keywords[lexeme]; ok {
		l.tokens = append(l.tokens, token.Token{Type: tt, Lexeme: lexeme, Line: startLine, Col: startCol})
		return
	}

	// Bare "_" is UNDERSCORE, but "_foo" etc. remain IDENT
	if lexeme == "_" {
		l.tokens = append(l.tokens, token.Token{Type: token.UNDERSCORE, Lexeme: lexeme, Line: startLine, Col: startCol})
		return
	}

	// Typed literal: a PascalCase identifier (a type name) immediately
	// adjacent (no whitespace) to a `"`/`"""` or raw backtick opener — the tag is the
	// type whose `impl Literal` block backs the literal. The body is
	// scanned by the existing string scanners with the tag threaded
	// through; only the opener token gets the tagged kind + Tag field. A
	// snake_case identifier adjacent to a literal opener is NOT a typed literal (tags
	// are types): it lexes as an ordinary IDENT followed by a separate
	// STRING_LITERAL, which the parser rejects as two adjacent primaries.
	// Whitespace between identifier and the opener likewise produces separate
	// tokens (the adjacency rule is preserved).
	if !l.atEnd() && l.peek() == '`' && unicode.IsUpper(rune(lexeme[0])) {
		l.scanRawBacktick(lexeme)
		return
	}
	if !l.atEnd() && l.peek() == '"' && unicode.IsUpper(rune(lexeme[0])) {
		l.scanString(lexeme)
		return
	}

	typ := token.IDENT
	if unicode.IsUpper(rune(lexeme[0])) {
		typ = token.TYPE_IDENT
	}
	l.tokens = append(l.tokens, token.Token{Type: typ, Lexeme: lexeme, Line: startLine, Col: startCol})
}

// --- Operators and delimiters ---

func (l *lexer) scanOperatorOrDelimiter() {
	ch := l.peek()
	startLine, startCol := l.line, l.col

	switch ch {
	case '+':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.PLUS, Lexeme: "+", Line: startLine, Col: startCol})
	case '-':
		if l.peekAt(1) == '>' {
			l.advance()
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.ARROW, Lexeme: "->", Line: startLine, Col: startCol})
		} else {
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.MINUS, Lexeme: "-", Line: startLine, Col: startCol})
		}
	case '*':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.STAR, Lexeme: "*", Line: startLine, Col: startCol})
	case '/':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.SLASH, Lexeme: "/", Line: startLine, Col: startCol})
	case '%':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.PERCENT, Lexeme: "%", Line: startLine, Col: startCol})
	case '!':
		if l.peekAt(1) == '=' {
			l.advance()
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.BANGEQ, Lexeme: "!=", Line: startLine, Col: startCol})
		} else {
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.BANG, Lexeme: "!", Line: startLine, Col: startCol})
		}
	case '=':
		if l.peekAt(1) == '>' {
			l.advance()
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.FAT_ARROW, Lexeme: "=>", Line: startLine, Col: startCol})
		} else if l.peekAt(1) == '=' {
			l.advance()
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.EQEQ, Lexeme: "==", Line: startLine, Col: startCol})
		} else {
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.EQ, Lexeme: "=", Line: startLine, Col: startCol})
		}
	case '<':
		if l.peekAt(1) == '=' {
			l.advance()
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.LTEQ, Lexeme: "<=", Line: startLine, Col: startCol})
		} else {
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.LT, Lexeme: "<", Line: startLine, Col: startCol})
		}
	case '>':
		if l.peekAt(1) == '=' {
			l.advance()
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.GTEQ, Lexeme: ">=", Line: startLine, Col: startCol})
		} else {
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.GT, Lexeme: ">", Line: startLine, Col: startCol})
		}
	case '|':
		if l.peekAt(1) == '>' {
			l.advance()
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.PIPE, Lexeme: "|>", Line: startLine, Col: startCol})
		} else {
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.BAR, Lexeme: "|", Line: startLine, Col: startCol})
		}
	case '.':
		// `.` followed by `.` starts a range operator (`..` or `..=`); a
		// lone `.` stays as DOT for field access etc.
		if l.peekAt(1) == '.' {
			l.advance() // consume first '.'
			l.advance() // consume second '.'
			if !l.atEnd() && l.peek() == '=' {
				l.advance() // consume '='
				l.tokens = append(l.tokens, token.Token{Type: token.DOTDOTEQ, Lexeme: "..=", Line: startLine, Col: startCol})
			} else {
				l.tokens = append(l.tokens, token.Token{Type: token.DOTDOT, Lexeme: "..", Line: startLine, Col: startCol})
			}
		} else {
			l.advance()
			l.tokens = append(l.tokens, token.Token{Type: token.DOT, Lexeme: ".", Line: startLine, Col: startCol})
		}
	case ',':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.COMMA, Lexeme: ",", Line: startLine, Col: startCol})
	case ':':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.COLON, Lexeme: ":", Line: startLine, Col: startCol})
	case '(':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.LPAREN, Lexeme: "(", Line: startLine, Col: startCol})
	case ')':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.RPAREN, Lexeme: ")", Line: startLine, Col: startCol})
	case '[':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.LBRACKET, Lexeme: "[", Line: startLine, Col: startCol})
	case ']':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.RBRACKET, Lexeme: "]", Line: startLine, Col: startCol})
	case '{':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.LBRACE, Lexeme: "{", Line: startLine, Col: startCol})
	case '}':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.RBRACE, Lexeme: "}", Line: startLine, Col: startCol})
	case ';':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.SEMICOLON, Lexeme: ";", Line: startLine, Col: startCol})
	case '@':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.AT, Lexeme: "@", Line: startLine, Col: startCol})
	case '#':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.HASH, Lexeme: "#", Line: startLine, Col: startCol})
	case '^':
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.CARET, Lexeme: "^", Line: startLine, Col: startCol})
	default:
		l.advance()
		l.tokens = append(l.tokens, token.Token{Type: token.ILLEGAL, Lexeme: string(ch), Line: startLine, Col: startCol})
	}
}

// --- Helpers ---

func (l *lexer) atEnd() bool {
	return l.pos >= len(l.source)
}

func (l *lexer) peek() byte {
	return l.source[l.pos]
}

func (l *lexer) peekAt(offset int) byte {
	idx := l.pos + offset
	if idx >= len(l.source) {
		return 0
	}
	return l.source[idx]
}

func (l *lexer) advance() {
	if l.pos < len(l.source) {
		if l.source[l.pos] == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		l.pos++
	}
}

func (l *lexer) emit(typ token.TokenType, lexeme string) {
	l.tokens = append(l.tokens, token.Token{Type: typ, Lexeme: lexeme, Line: l.line, Col: l.col})
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func isHexDigit(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentPart(ch byte) bool {
	return isIdentStart(ch) || isDigit(ch)
}

// canEndStatement returns true for tokens that can appear at the end of a statement.
// Newlines after other tokens (operators, opening delimiters, keywords that start
// constructs) are suppressed, allowing expressions to continue on the next line.
func canEndStatement(tt token.TokenType) bool {
	switch tt {
	case token.IDENT, token.TYPE_IDENT,
		token.INT, token.FLOAT, token.DECIMAL, token.CODEPOINT_LITERAL,
		token.STRING_LITERAL, token.STRING_END,
		token.TRIPLE_STRING_LITERAL, token.TRIPLE_STRING_END,
		token.RAW_STRING_LITERAL, token.RAW_TRIPLE_STRING_LITERAL,
		token.TAGGED_STRING_LITERAL, token.TAGGED_TRIPLE_STRING_LITERAL,
		token.RAW_TAGGED_STRING_LITERAL, token.RAW_TAGGED_TRIPLE_STRING_LITERAL,
		token.RPAREN, token.RBRACKET, token.RBRACE, token.GT,
		token.RETURN, token.BREAK, token.CONTINUE,
		token.ASSERT, token.REFUTE, token.TRY, token.DBG, token.TODO,
		token.EXPORT,
		token.UNDERSCORE:
		return true
	default:
		return false
	}
}

// endsAtCursor reports whether a token of type t ends where the lexer step
// that produced it stopped: the literal kinds whose Lexeme is a value rather
// than source text.
func endsAtCursor(t token.TokenType) bool {
	switch t {
	case token.STRING_LITERAL, token.TRIPLE_STRING_LITERAL,
		token.RAW_STRING_LITERAL, token.RAW_TRIPLE_STRING_LITERAL,
		token.TAGGED_STRING_LITERAL, token.TAGGED_TRIPLE_STRING_LITERAL,
		token.RAW_TAGGED_STRING_LITERAL, token.RAW_TAGGED_TRIPLE_STRING_LITERAL,
		token.STRING_END, token.TRIPLE_STRING_END, token.CODEPOINT_LITERAL:
		return true
	}
	return false
}
