; Tree-sitter injection queries for Nomi tagged literals.
;
; These queries drive editor-side embedded-language highlighting. The tag
; on a tagged literal (`Sql"..."`, raw `Regex`...``, etc.) is a PascalCase
; type name, captured as `@injection.language` — tree-sitter treats it as
; the *name* of the language to inject. The body content is captured as
; `@injection.content`, which becomes the source text fed to that
; language's parser/highlighter.
;
; Most injection languages are dynamic — the tag's type name. `Sql"..."`
; injects the `Sql` grammar; a tag whose type name has no installed grammar
; falls back to no-injection. Tags that need a spelling alias can be handled
; by a static rule before the generic dynamic rule; `Toml` injects the
; editor's `toml` grammar. `Regex` likewise injects the lowercase
; `regex` grammar.
;
; No `(#set! injection.combined)`: that flag concatenates ALL matching
; `@injection.content` captures across the whole file into one virtual
; document before parsing, so multiple `Sql"..."` literals get joined
; into one malformed `SELECT ... SELECT ... INSERT ...` statement and
; the injected parser produces inconsistent state across literals
; (visible as the same keyword rendering in different colors). Per-
; literal injection avoids that. The cost in theory is fragmenting a
; keyword that straddles a `${...}` slot, but that doesn't happen in
; practice — SQL/HTML/etc. keywords are single tokens.
;
; For interpolated forms, this means each `string_content` /
; `triple_string_content` chunk between slots is its own injection.
; That's fine: each chunk is a complete fragment of the embedded
; language at the parser's resolution.

; Inline Go bindings and top-level Go preludes.
(go_block
  (go_raw_content) @injection.content
  (#set! injection.language "go"))

(go_inline_body
  (go_raw_content) @injection.content
  (#set! injection.language "go"))

; TOML typed literals use the editor's lowercase `toml` grammar name.
(tagged_type_string
  tag: (type_identifier) @tag
  (string_content) @injection.content
  (#eq? @tag "Toml")
  (#set! injection.language "toml"))

(tagged_type_triple_string
  tag: (type_identifier) @tag
  (triple_string_content) @injection.content
  (#eq? @tag "Toml")
  (#set! injection.language "toml"))

(raw_tagged_string
  tag: (type_identifier) @tag
  (raw_string_content) @injection.content
  (#eq? @tag "Toml")
  (#set! injection.language "toml"))

; Regex typed literals use the editor's lowercase `regex` grammar name.
(tagged_type_string
  tag: (type_identifier) @tag
  (string_content) @injection.content
  (#eq? @tag "Regex")
  (#set! injection.language "regex"))

(tagged_type_triple_string
  tag: (type_identifier) @tag
  (triple_string_content) @injection.content
  (#eq? @tag "Regex")
  (#set! injection.language "regex"))

(raw_tagged_string
  tag: (type_identifier) @tag
  (raw_string_content) @injection.content
  (#eq? @tag "Regex")
  (#set! injection.language "regex"))

; Interpolated single-line tagged literal: `<Tag>"...${expr}..."`.
; Body content is `string_content`; interpolation holes
; (`string_interpolation`) are skipped so the host language continues
; to parse them.
(tagged_type_string
  tag: (type_identifier) @injection.language
  (string_content) @injection.content
  (#not-eq? @injection.language "Toml")
  (#not-eq? @injection.language "Regex"))

; Interpolated triple-quoted tagged literal: `<Tag>"""..."""`.
(tagged_type_triple_string
  tag: (type_identifier) @injection.language
  (triple_string_content) @injection.content
  (#not-eq? @injection.language "Toml")
  (#not-eq? @injection.language "Regex"))

; Raw tagged literal: `<Tag>`...``. No interpolation; the body is one
; `raw_string_content` token.
(raw_tagged_string
  tag: (type_identifier) @injection.language
  (raw_string_content) @injection.content
  (#not-eq? @injection.language "Toml")
  (#not-eq? @injection.language "Regex"))
