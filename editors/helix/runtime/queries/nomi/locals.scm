;; Scopes

(source_file) @local.scope
(body) @local.scope
(function_definition) @local.scope
(lambda) @local.scope
(test_declaration) @local.scope
(tests_declaration) @local.scope

;; Definitions

(parameter
  name: (identifier) @local.definition.variable.parameter)

(lambda_parameter
  name: (identifier) @local.definition.variable.parameter)

(lambda_parameter
  (identifier) @local.definition.variable.parameter)

(binding
  . (identifier) @local.definition.variable)

(destructuring_binding
  binding: (identifier) @local.definition.variable)

(function_definition
  name: (identifier) @local.definition.function)

(import_selector_path
  (identifier) @local.definition.function)

(import_selector_path
  (type_identifier) @local.definition.type)

(import_entry
  (identifier) @local.definition.function)

(import_entry
  (type_identifier) @local.definition.type)

(type_definition
  name: (qualified_type_identifier
    (type_identifier) @local.definition.type))

(type_alias
  name: (qualified_type_identifier
    (type_identifier) @local.definition.type))

(struct_definition
  name: (qualified_type_identifier
    (type_identifier) @local.definition.type))

(enum_definition
  name: (qualified_type_identifier
    (type_identifier) @local.definition.type))

(interface_definition
  name: (type_identifier) @local.definition.type)

(extern_type_definition
  name: (qualified_type_identifier
    (type_identifier) @local.definition.type))

; BEGIN attached-test dim (Helix only; scripts/sync-nvim-runtime.sh drops
; this block, since the Neovim plugin dims attached tests from Lua.)
;
; Helix gives a reference the highlight of the definition it resolves to,
; over the highlights query, so a type, import or binding named in a `//!`
; attached test would keep its colour inside the dimmed test. The test is
; a scope that does not see outer definitions, and every name in it is a
; definition highlighted @comment, so a reference inside resolves to one of
; those. These patterns come after the other definitions so they win on a
; node both capture (a lambda parameter, a binding). Depth 16 matches the
; highlights.scm dim.
((attached_test_prompt) @local.scope
 (#set! local.scope-inherits false))
(attached_test_prompt [(identifier) (type_identifier)] @local.definition.comment)
(attached_test_prompt (_ [(identifier) (type_identifier)] @local.definition.comment))
(attached_test_prompt (_ (_ [(identifier) (type_identifier)] @local.definition.comment)))
(attached_test_prompt (_ (_ (_ [(identifier) (type_identifier)] @local.definition.comment))))
(attached_test_prompt (_ (_ (_ (_ [(identifier) (type_identifier)] @local.definition.comment)))))
(attached_test_prompt (_ (_ (_ (_ (_ [(identifier) (type_identifier)] @local.definition.comment))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ [(identifier) (type_identifier)] @local.definition.comment)))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ [(identifier) (type_identifier)] @local.definition.comment))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ [(identifier) (type_identifier)] @local.definition.comment)))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ [(identifier) (type_identifier)] @local.definition.comment))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ [(identifier) (type_identifier)] @local.definition.comment)))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ [(identifier) (type_identifier)] @local.definition.comment))))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ [(identifier) (type_identifier)] @local.definition.comment)))))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ [(identifier) (type_identifier)] @local.definition.comment))))))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ [(identifier) (type_identifier)] @local.definition.comment)))))))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ [(identifier) (type_identifier)] @local.definition.comment))))))))))))))))
; END attached-test dim

;; References

(identifier) @local.reference
(type_identifier) @local.reference
