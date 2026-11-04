; Tree-sitter injection queries for Nomi tagged literals.
;
; These queries drive editor-side embedded-language highlighting. The tag
; on a tagged literal (`Sql"..."`, raw `Regex`...``, etc.) is a PascalCase
; type name, captured as `@injection.language`: tree-sitter treats it as
; the name of the language to inject. A tag whose type name has no
; installed grammar injects nothing. `Toml`, `Regex` and `Html` need a
; lowercase spelling (`toml`, `regex`, `html`), so static rules before the
; generic dynamic rule set it.
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

; TOML typed literals use the editor's lowercase `toml` grammar name.
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

(raw_tagged_string
  tag: (type_identifier) @_tag
  (raw_string_content) @injection.content
  (#eq? @_tag "Toml")
  (#set! injection.language "toml"))

; Regex typed literals use the editor's lowercase `regex` grammar name.
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

(raw_tagged_string
  tag: (type_identifier) @_tag
  (raw_string_content) @injection.content
  (#eq? @_tag "Regex")
  (#set! injection.language "regex"))

; HTML typed literals (std::html's templates) use the editor's lowercase
; `html` grammar.
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

(raw_tagged_string
  tag: (type_identifier) @_tag
  (raw_string_content) @injection.content
  (#eq? @_tag "Html")
  (#set! injection.language "html"))

; Every other tag injects the language its type name names: `Sql"..."`,
; `Json"""..."""`, `Css`...``.
((tagged_type_string
  tag: (type_identifier) @injection.language
  [(string_content) @injection.content (string_interpolation) (escape_sequence)]+) @injection.host
  (#not-eq? @injection.language "Toml")
  (#not-eq? @injection.language "Regex")
  (#not-eq? @injection.language "Html"))

((tagged_type_triple_string
  tag: (type_identifier) @injection.language
  [(triple_string_content) @injection.content (string_interpolation)]+) @injection.host
  (#not-eq? @injection.language "Toml")
  (#not-eq? @injection.language "Regex")
  (#not-eq? @injection.language "Html"))

; A raw literal has no slots: its body is one `raw_string_content` token.
(raw_tagged_string
  tag: (type_identifier) @injection.language
  (raw_string_content) @injection.content
  (#not-eq? @injection.language "Toml")
  (#not-eq? @injection.language "Regex")
  (#not-eq? @injection.language "Html"))

; A `message` or `noun` declaration's body is MessageFormat 2, the editor's
; `mf2` grammar when it has one.
(keyword_declaration
  keyword: (identifier) @_keyword
  body: (raw_string
    (raw_string_content) @injection.content)
  (#match? @_keyword "^(message|noun)$")
  (#set! injection.language "mf2"))
