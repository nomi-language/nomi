; Functions
(function_definition
  "fn" @context
  name: (identifier) @name) @item

(function_definition
  "fn" @context
  name: (type_identifier) @name) @item

; Structs
(struct_definition
  "struct" @context
  name: (qualified_type_identifier) @name) @item

; Enums
(enum_definition
  "enum" @context
  name: (qualified_type_identifier) @name) @item

; Enum variants (children of enum_definition)
(enum_variant
  name: (type_identifier) @name) @item

; Interfaces
(interface_definition
  "interface" @context
  name: (type_identifier) @name) @item

; Tests
(test_declaration
  "test" @context
  name: (_) @name) @item
(tests_declaration
  "tests" @context
  name: (_) @name) @item

; Interface methods (children of interfaces)
(interface_method
  name: (identifier) @name) @item

(interface_method
  name: (type_identifier) @name) @item

; Type definitions
(type_definition
  "type" @context
  name: (qualified_type_identifier) @name) @item

; Type aliases
(type_alias
  "typealias" @context
  name: (qualified_type_identifier) @name) @item

; Once bindings (the only "set once, never changes" declaration)
(once_binding
  "once" @context
  name: (identifier) @name) @item

(once_binding
  "once" @context
  name: (type_identifier) @name) @item

; Host functions
(extern_func_definition
  (host_keyword) @context
  name: (identifier) @name) @item

(extern_func_definition
  (host_keyword) @context
  name: (type_identifier) @name) @item

; Host types
(extern_type_definition
  (host_keyword) @context
  name: (qualified_type_identifier) @name) @item

(derive_conformance
  interface: (_) @name) @item

; Inherent impl blocks (`impl Type { ... }`) defining type-owned API.
(impl_block
  !interface
  "impl" @context
  receiver: (_) @name) @item

; Impl blocks (`impl Iface for Type { ... }`) implementing an interface for a
; type; the interface names the block, the implementing type is its context.
(impl_block
  interface: (_) @name
  "for" @context
  receiver: (_) @context) @item
