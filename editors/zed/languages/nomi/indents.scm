; Zed's syntax-based auto-indent: the contents of any node delimited by a
; bracket pair are indented one level, and the closing bracket returns to
; the node's own level. Zed applies it on Enter, on paste, and to a
; completion that does not ask to be inserted as is (a multi-line snippet
; from nomi-lsp asks for adjustIndentation).
(_ "{" "}" @end) @indent
(_ "[" "]" @end) @indent
(_ "(" ")" @end) @indent
