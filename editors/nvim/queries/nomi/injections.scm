; Tree-sitter injection queries for Nomi tagged literals.
;
; These queries drive editor-side embedded-language highlighting. A tagged
; literal's tag (`Sql"..."`, raw `Regex`...``) is a PascalCase type name,
; not a language name, so each std tag with an embedded language gets static
; rules that set the editor's lowercase grammar name: `Sql` is `sql`, `Html`
; `html`, `Toml` `toml` and `Regex` `regex`. Every other tag (`Url`,
; `Route`, `Cron`, `Date`, `Locale`, ...) injects nothing.
;
; There is no rule that injects the tag itself as the language. Zed matches
; a language name case-insensitively and Neovim lowercases it, but Helix
; compares it to its language ids exactly and then searches its
; `injection-regex`es inside it, case-sensitively: `Sql` found no language
; there, while `Locale` found C, `Url` and `Route` R, `Cron` RON and `Bits`
; TypeScript.
;
; One literal is one injected document, with its `${...}` slots left out.
; Each pattern matches the literal's children as one repetition of body
; chunks, slots and escapes, so a single match captures every body chunk
; (`string_content`, `triple_string_content`) as `@injection.content`.
; Zed, Helix and Neovim each parse the content nodes of one match as one
; document over several ranges, so `<img src="${avatar}" alt="">` and
; `${initial}</span>` parse as HTML would with the slots filled in. The
; slots, and escapes such as `\"`, are not in the injected text; they stay
; Nomi. Zed also groups by `@injection.host`, the literal, so a body split
; over several matches would still be one document there.
;
; Rejected alternatives. A pattern per chunk (`(string_content)
; @injection.content`) parses each chunk as its own document, so HTML after
; the first slot starts mid-attribute. `(#set! injection.combined)` joins
; every literal of that language in the whole file into one document.
; Capturing the whole literal without `injection.include-children`, with the
; body tokens hidden, leaves the slots out only in Helix and tree-sitter's
; own highlighter: Zed ignores that rule and injects the whole node, tag,
; quotes and slots included, and Neovim cuts out only named children, so the
; quotes would stay in.
;
; tree-sitter's own highlighter (`tree-sitter highlight`) injects only the
; last content node of a match, so it highlights only a literal's last
; chunk. No editor uses it.

; SQL typed literals (std::sql's statements) use the editor's `sql` grammar.
((tagged_type_string
  tag: (type_identifier) @_tag
  [(string_content) @injection.content (string_interpolation) (escape_sequence)]+) @injection.host
  (#eq? @_tag "Sql")
  (#set! injection.language "sql"))

((tagged_type_triple_string
  tag: (type_identifier) @_tag
  [(triple_string_content) @injection.content (string_interpolation)]+) @injection.host
  (#eq? @_tag "Sql")
  (#set! injection.language "sql"))

; A raw literal has no slots: its body is one `raw_string_content` token.
(raw_tagged_string
  tag: (type_identifier) @_tag
  (raw_string_content) @injection.content
  (#eq? @_tag "Sql")
  (#set! injection.language "sql"))

; HTML typed literals (std::html's templates) use the editor's `html` grammar.
((tagged_type_string
  tag: (type_identifier) @_tag
  [(string_content) @injection.content (string_interpolation) (escape_sequence)]+) @injection.host
  (#eq? @_tag "Html")
  (#set! injection.language "html"))

((tagged_type_triple_string
  tag: (type_identifier) @_tag
  [(triple_string_content) @injection.content (string_interpolation)]+) @injection.host
  (#eq? @_tag "Html")
  (#set! injection.language "html"))

; A raw literal has no slots: its body is one `raw_string_content` token.
(raw_tagged_string
  tag: (type_identifier) @_tag
  (raw_string_content) @injection.content
  (#eq? @_tag "Html")
  (#set! injection.language "html"))

; TOML typed literals use the editor's `toml` grammar.
((tagged_type_string
  tag: (type_identifier) @_tag
  [(string_content) @injection.content (string_interpolation) (escape_sequence)]+) @injection.host
  (#eq? @_tag "Toml")
  (#set! injection.language "toml"))

((tagged_type_triple_string
  tag: (type_identifier) @_tag
  [(triple_string_content) @injection.content (string_interpolation)]+) @injection.host
  (#eq? @_tag "Toml")
  (#set! injection.language "toml"))

; A raw literal has no slots: its body is one `raw_string_content` token.
(raw_tagged_string
  tag: (type_identifier) @_tag
  (raw_string_content) @injection.content
  (#eq? @_tag "Toml")
  (#set! injection.language "toml"))

; Regex typed literals use the editor's `regex` grammar.
((tagged_type_string
  tag: (type_identifier) @_tag
  [(string_content) @injection.content (string_interpolation) (escape_sequence)]+) @injection.host
  (#eq? @_tag "Regex")
  (#set! injection.language "regex"))

((tagged_type_triple_string
  tag: (type_identifier) @_tag
  [(triple_string_content) @injection.content (string_interpolation)]+) @injection.host
  (#eq? @_tag "Regex")
  (#set! injection.language "regex"))

; A raw literal has no slots: its body is one `raw_string_content` token.
(raw_tagged_string
  tag: (type_identifier) @_tag
  (raw_string_content) @injection.content
  (#eq? @_tag "Regex")
  (#set! injection.language "regex"))

; A `phrase` or `noun` declaration's body is MessageFormat 2, the editor's
; `mf2` grammar when it has one.
(keyword_declaration
  keyword: (identifier) @_keyword
  body: (raw_string
    (raw_string_content) @injection.content)
  (#match? @_keyword "^(phrase|noun)$")
  (#set! injection.language "mf2"))
