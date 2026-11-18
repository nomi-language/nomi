; BEGIN attached-test dim (Zed and Helix only; scripts/sync-nvim-runtime.sh
; drops this block, since the Neovim plugin dims attached tests from Lua.)
;
; `//!` attached tests read as comments: everything inside an
; attached_test_prompt is captured @comment. A capture on an outer node
; cannot do it, because Zed and Helix draw the innermost capture over an
; outer one, and neither supports an ancestor predicate (Zed implements only
; has-parent?/not-has-parent?; Helix rejects unknown predicates). Query
; syntax has no descendant operator either, so one pattern per depth
; captures every node, named or anonymous, at that depth below the prompt.
; Each pattern comes after every other capture, so on any node it captures
; it is the last capture and wins. Depth 16 is the bound: the deepest node in
; any attached test in std, examples and tests is at depth 9. A node deeper
; than 16 keeps its ordinary highlight.
(attached_test_prompt _ @comment)
(attached_test_prompt (_ _ @comment))
(attached_test_prompt (_ (_ _ @comment)))
(attached_test_prompt (_ (_ (_ _ @comment))))
(attached_test_prompt (_ (_ (_ (_ _ @comment)))))
(attached_test_prompt (_ (_ (_ (_ (_ _ @comment))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ _ @comment)))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ _ @comment))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ _ @comment)))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment)))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment))))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment)))))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment))))))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment)))))))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment))))))))))))))))
; END attached-test dim
