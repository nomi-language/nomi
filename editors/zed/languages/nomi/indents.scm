; Zed's syntax-based auto-indent. A body is written below its header line
; and closed by `end`: the node that owns the header indents the lines
; between its first line and its `end`, and the `end` returns to the
; header's level. An `else` starting a line returns there too. The contents
; of any node delimited by a bracket pair are indented one level, and the
; closing bracket returns to the node's own level. Zed applies it on Enter,
; on paste, and to a completion that does not ask to be inserted as is (a
; multi-line snippet from nomi-lsp asks for adjustIndentation).
(_ (body "end" @end)) @indent
(struct_definition (struct_body "end" @end)) @indent
(enum_definition (enum_body "end" @end)) @indent
(group_declaration (group_body "end" @end)) @indent
(group_subtests (group_body "end" @end)) @indent
(else_binding (else_arms "end" @end)) @indent
(interface_definition "end" @end) @indent
(impl_block "end" @end) @indent
(import_block "end" @end) @indent
(case_expression "end" @end) @indent
(if_expression "end" @end) @indent
(pipe_if_expression "end" @end) @indent
; A case arm whose `->` ends its line indents the arm's body below it.
(case_branch (arm_block)) @indent
(guard_branch (arm_block)) @indent
(else_arm (arm_block)) @indent
"else" @outdent
(_ "[" "]" @end) @indent
(_ "(" ")" @end) @indent
(_ "{" "}" @end) @indent
